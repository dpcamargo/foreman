package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
	"github.com/dpcamargo/foreman/internal/core/verify"
)

// taskStore is the persistence subset tasks.go needs — the parts of *sqlite.Store this
// file calls, declared locally since cmd/ wiring code has no business importing a store
// interface from core (it already has the concrete adapter).
type taskStore interface {
	task.TaskStore
	RecentTasks(ctx context.Context, limit int) ([]task.Task, error)
}

// dispatcher is the subset of *engine.Supervisor createTask needs, declared locally for the
// same reason, and so tests can use a fake instead of a real Supervisor.
type dispatcher interface {
	Dispatch(ctx context.Context, params engine.RunParams)
	DispatchWithReport(ctx context.Context, params engine.RunParams, onDone func(engine.TaskReport, error))
}

// supervisorControl is the pause/resume/cancel subset of *engine.Supervisor
// (spec FR-008/FR-009).
type supervisorControl interface {
	Cancel(taskID string)
	PauseGraceful(taskID string)
	ResumeGraceful(taskID string)
}

// resolveBaseCommit resolves repoURL's current commit so a Telegram-created task has a
// concrete, pinned base_commit (FR-004 applies the same way to daemon-dispatched tasks as
// to fm run's task.yaml). Overridable in tests.
var resolveBaseCommit = func(ctx context.Context, repoURL string) (string, error) {
	return gitResolveHEAD(ctx, repoURL)
}

// resolveProjectConfig resolves the base commit's .foreman/project.yaml for a
// daemon-dispatched task (FR-016). Unlike cmd/fm/run.go, there is no task.yaml's directory
// to search beside — the daemon only knows the repo URL and base commit — so the repo is
// the single source of truth. A missing or invalid config refuses the task outright
// (FR-016: "MUST refuse to run against a repo whose file is missing or invalid"), never
// silently guessing a default verifier/verify command.
var resolveProjectConfig = func(ctx context.Context, repoURL, baseCommit string) ([]byte, error) {
	data, err := gitCatFile(ctx, repoURL, baseCommit, ".foreman/project.yaml")
	if err != nil {
		return nil, fmt.Errorf("read .foreman/project.yaml at %s: %w", baseCommit, err)
	}
	if _, err := verify.ParseProjectConfig(data); err != nil {
		return nil, fmt.Errorf("%s: %w", repoURL, err)
	}
	return data, nil
}

// engineRunParamsFor builds a fresh RunParams for re-dispatching tsk (used by resumeTask).
// Visible/Hidden test filesystems aren't persisted anywhere for daemon-dispatched tasks
// (there's no task.yaml to re-read them from), so a resumed task re-verifies with none —
// the same gap buildEngineConfig's FileExists already documents for this feature.
// ProjectYAML is re-resolved from the repo's base commit rather than persisted, since it
// lives in git (FR-016) and is cheap to re-read.
func engineRunParamsFor(ctx context.Context, tsk task.Task) engine.RunParams {
	params := engine.RunParams{Task: tsk}
	if data, err := resolveProjectConfig(ctx, tsk.RepoURL, tsk.BaseCommit); err == nil {
		params.ProjectYAML = data
	}
	return params
}

// dispatchOnDone is the post-RunTask callback createTask/resumeTask pass to
// DispatchWithReport (T041's delivery gate). Wired in serve.go once the store, Supervisor,
// sender, and integrator all exist — nil in tests that don't exercise delivery.
var dispatchOnDone func(ctx context.Context, report engine.TaskReport, err error)

// createTask creates a pending task.Task for repoURL/requestText and hands it to sup for
// dispatch, returning immediately with the new task id (spec US1: "/task" acknowledges
// within seconds, the loop itself runs in the background).
func createTask(ctx context.Context, store taskStore, sup dispatcher, repoURL, requestText string) (task.Task, error) {
	repoURL = strings.TrimSpace(repoURL)
	requestText = strings.TrimSpace(requestText)
	if repoURL == "" {
		return task.Task{}, fmt.Errorf("repo is required")
	}
	if requestText == "" {
		return task.Task{}, fmt.Errorf("request text is required")
	}

	baseCommit, err := resolveBaseCommit(ctx, repoURL)
	if err != nil {
		return task.Task{}, fmt.Errorf("resolve base commit for %s: %w", repoURL, err)
	}

	// FR-016: refuse to run against a repo whose .foreman/project.yaml is missing or
	// invalid, before the task row is even created — same refusal the `fm run` path makes
	// (cmd/fm/run.go step 3), just sourced from the repo itself since there's no task.yaml
	// directory to search beside for a daemon-dispatched task.
	projectYAML, err := resolveProjectConfig(ctx, repoURL, baseCommit)
	if err != nil {
		return task.Task{}, err
	}

	now := time.Now().UTC()
	tsk := task.Task{
		ID:          task.NewID("tsk"),
		TaskType:    task.TaskTypeCode,
		RepoURL:     repoURL,
		BaseCommit:  baseCommit,
		RequestText: requestText,
		Status:      task.StatusPending,
	}
	nodes := task.NewLinearNodes(tsk.ID, now)
	created, err := store.CreateTask(ctx, tsk, nodes)
	if err != nil {
		return task.Task{}, fmt.Errorf("create task: %w", err)
	}

	sup.DispatchWithReport(context.WithoutCancel(ctx), engine.RunParams{Task: created, ProjectYAML: projectYAML}, func(report engine.TaskReport, err error) {
		if dispatchOnDone != nil {
			dispatchOnDone(context.WithoutCancel(ctx), report, err)
		}
	})
	return created, nil
}

// formatTaskStatus renders a /status reply: current phase, attempts so far, overall status.
func formatTaskStatus(tsk task.Task, nodes []task.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", tsk.ID, tsk.Status)
	fmt.Fprintf(&b, "repo: %s\n", tsk.RepoURL)
	for _, n := range nodes {
		fmt.Fprintf(&b, "  %s: %s\n", n.Name, n.Status)
	}
	return strings.TrimRight(b.String(), "\n")
}

// nonTerminalTasks filters tasks down to those still in play — pending, running,
// blocked_human, or paused — matching /tasks and GET /v1/tasks's documented behavior
// (contracts/telegram-commands.md, contracts/local-api.md: "Lists non-terminal tasks").
func nonTerminalTasks(tasks []task.Task) []task.Task {
	out := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		switch t.Status {
		case task.StatusSucceeded, task.StatusFailed, task.StatusCancelled:
			continue
		}
		out = append(out, t)
	}
	return out
}

// formatTaskList renders a /tasks reply: one line per task, newest first (as RecentTasks
// already orders them).
func formatTaskList(tasks []task.Task) string {
	if len(tasks) == 0 {
		return "no tasks yet"
	}
	var b strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&b, "%s  %-13s  %s\n", t.ID, t.Status, truncate(t.RequestText, 60))
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
