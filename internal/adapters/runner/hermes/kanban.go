package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/runner/gomod"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// KanbanGoal is arm A2: Hermes's goal loop, run headless as a Kanban card with --goal
// (`/goal` itself is interactive-only; research R7).
type KanbanGoal struct{ cfg Config }

// NewKanbanGoal returns arm A2.
func NewKanbanGoal(cfg Config) *KanbanGoal { return &KanbanGoal{cfg: cfg.withDefaults()} }

// Name implements eval.Arm.
func (a *KanbanGoal) Name() string { return "a2" }

// Version implements eval.Arm.
func (a *KanbanGoal) Version(ctx context.Context) (string, error) { return a.cfg.version(ctx) }

func (a *KanbanGoal) kanban(ctx context.Context, args ...string) ([]byte, error) {
	return a.cfg.run(ctx, append([]string{"kanban", "--board", a.cfg.Board}, args...)...)
}

// Attempt implements eval.Arm.
func (a *KanbanGoal) Attempt(ctx context.Context, in eval.AttemptInput) (eval.AttemptResult, error) {
	ws := in.Workspace
	prompt := filepath.Join(in.RunDir, "prompt.md")
	if err := os.WriteFile(prompt, []byte(in.Prompt), 0o644); err != nil {
		return eval.AttemptResult{}, fmt.Errorf("write prompt: %w", err)
	}
	if err := gomod.Warm(ctx, ws.Dir, a.cfg.ModCache); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "warm module cache: " + err.Error()}, nil
	}
	start := time.Now()
	out, err := a.kanban(ctx, "create", ws.ID, "--body-file", prompt, "--assignee", a.cfg.Assignee,
		"--workspace", "dir:"+ws.Dir, "--goal", "--goal-max-turns", fmt.Sprint(a.cfg.GoalMaxTurns),
		"--max-runtime", seconds(in.WallCap), "--max-retries", "1", "--json")
	if err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: err.Error()}, nil
	}
	var card struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &card) != nil || card.ID == "" {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "kanban create returned no id: " + string(out)}, nil
	}
	// The worker inherits this dispatch's environment, so it gets the run's mounts. A
	// card spawned by another dispatcher (e.g. a gateway) would not, so that's refused.
	out, err = a.cfg.runWith(ctx, a.cfg.runEnv(ws.Dir), "kanban", "--board", a.cfg.Board, "dispatch", "--max", "1", "--json")
	if err != nil {
		a.stop(card.ID)
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: err.Error()}, nil
	}
	var disp struct {
		Spawned []struct {
			TaskID string `json:"task_id"`
		} `json:"spawned"`
	}
	json.Unmarshal(out, &disp)
	spawned := false
	for _, s := range disp.Spawned {
		spawned = spawned || s.TaskID == card.ID
	}
	if !spawned {
		a.stop(card.ID)
		return eval.AttemptResult{Status: eval.StatusInfraError,
			Error: "card " + card.ID + " was not spawned by this dispatch (another dispatcher may have taken it), so its workspace mount is not guaranteed"}, nil
	}

	usageFilter := []string{"--source", "kanban", "--title", ws.ID}
	status, summary, pollErr := a.wait(ctx, card.ID, usageFilter, start, in)
	if log, err := a.kanban(context.WithoutCancel(ctx), "log", card.ID); err == nil {
		io.WriteString(in.Transcript, string(log))
	}
	usage, _ := a.cfg.sessionUsage(context.WithoutCancel(ctx), usageFilter, start)
	res := eval.AttemptResult{Status: status, Usage: usage}
	switch {
	case pollErr != nil:
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage, Error: pollErr.Error()}, nil
	case status == eval.StatusSucceeded && cardBlocked(summary):
		res.Claimed = eval.ClaimNone // turn budget ran out: the card blocked for review
	case status == eval.StatusSucceeded:
		res.Claimed, res.Question = claimOrFallback(context.WithoutCancel(ctx), summary.text, ws)
	}
	if res.Status == eval.StatusSucceeded && in.TokenCap > 0 && usage.Billable() > in.TokenCap {
		res.Status = eval.StatusBudgetExceeded
	}
	return res, nil
}

type cardState struct {
	status string
	text   string
}

func cardBlocked(s cardState) bool { return s.status == "blocked" }

// wait polls the card until it is done or blocked, enforcing the wall and token caps by
// reclaiming and archiving the card.
func (a *KanbanGoal) wait(ctx context.Context, id string, usageFilter []string, start time.Time, in eval.AttemptInput) (eval.RunStatus, cardState, error) {
	deadline := start.Add(in.WallCap)
	t := time.NewTicker(a.cfg.PollInterval)
	defer t.Stop()
	for {
		state, err := a.show(ctx, id)
		if err != nil {
			a.stop(id)
			return "", cardState{}, err
		}
		switch state.status {
		case "done", "blocked", "review", "archived":
			return eval.StatusSucceeded, state, nil
		}
		if in.TokenCap > 0 {
			if u, err := a.cfg.sessionUsage(ctx, usageFilter, start); err == nil && u.Billable() > in.TokenCap {
				a.stop(id)
				return eval.StatusBudgetExceeded, state, nil
			}
		}
		if time.Now().After(deadline) {
			a.stop(id)
			return eval.StatusTimedOut, state, nil
		}
		select {
		case <-ctx.Done():
			a.stop(id)
			return "", cardState{}, fmt.Errorf("cancelled: %w", ctx.Err())
		case <-t.C:
		}
	}
}

func (a *KanbanGoal) show(ctx context.Context, id string) (cardState, error) {
	out, err := a.kanban(ctx, "show", id, "--json")
	if err != nil {
		return cardState{}, err
	}
	var v struct {
		Task struct {
			Status string `json:"status"`
		} `json:"task"`
		LatestSummary *string `json:"latest_summary"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return cardState{}, fmt.Errorf("decode kanban show: %w", err)
	}
	s := cardState{status: v.Task.Status}
	if v.LatestSummary != nil {
		s.text = *v.LatestSummary
	}
	return s, nil
}

// stop reclaims and archives a card, best effort.
func (a *KanbanGoal) stop(id string) {
	ctx := context.Background()
	a.kanban(ctx, "reclaim", id)
	a.kanban(ctx, "archive", id)
}
