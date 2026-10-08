package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// fakeTaskStore is a minimal in-memory taskStore for tests that don't need real SQLite.
type fakeTaskStore struct {
	tasks     []task.Task
	nodes     map[string][]task.Node
	approvals map[string]control.Approval
	allowed   []int64
}

func newFakeTaskStore() *fakeTaskStore {
	return &fakeTaskStore{nodes: make(map[string][]task.Node)}
}

func (s *fakeTaskStore) CreateTask(ctx context.Context, t task.Task, nodes []task.Node) (task.Task, error) {
	s.tasks = append(s.tasks, t)
	s.nodes[t.ID] = nodes
	return t, nil
}
func (s *fakeTaskStore) Task(ctx context.Context, id string) (task.Task, error) {
	for _, t := range s.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return task.Task{}, task.ErrNotFound
}
func (s *fakeTaskStore) UpdateTaskStatus(ctx context.Context, id string, status task.TaskStatus, currentNodeID string) error {
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].Status = status
			s.tasks[i].CurrentNodeID = currentNodeID
			return nil
		}
	}
	return task.ErrNotFound
}
func (s *fakeTaskStore) Node(ctx context.Context, id string) (task.Node, error) {
	return task.Node{}, nil
}
func (s *fakeTaskStore) NodesForTask(ctx context.Context, taskID string) ([]task.Node, error) {
	return s.nodes[taskID], nil
}
func (s *fakeTaskStore) UpdateNodeStatus(ctx context.Context, id string, status task.NodeStatus) error {
	return nil
}
func (s *fakeTaskStore) RecordTaskRun(ctx context.Context, r task.Run) error { return nil }
func (s *fakeTaskStore) FinishTaskRun(ctx context.Context, runID string, status task.RunStatus, failureClass task.FailureClass, failureSig string) error {
	return nil
}
func (s *fakeTaskStore) RecentTasks(ctx context.Context, limit int) ([]task.Task, error) {
	out := make([]task.Task, len(s.tasks))
	for i := range s.tasks {
		out[i] = s.tasks[len(s.tasks)-1-i] // newest first, matching the real adapter
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeTaskStore) CreateApproval(ctx context.Context, a control.Approval) (control.Approval, error) {
	if a.ID == "" {
		a.ID = "apv_" + task.NewID("")
	}
	if s.approvals == nil {
		s.approvals = make(map[string]control.Approval)
	}
	s.approvals[a.ID] = a
	return a, nil
}
func (s *fakeTaskStore) Approval(ctx context.Context, id string) (control.Approval, error) {
	a, ok := s.approvals[id]
	if !ok {
		return control.Approval{}, fmt.Errorf("approval not found: %s", id)
	}
	return a, nil
}
func (s *fakeTaskStore) ResolveApproval(ctx context.Context, id, status, response, reason string) error {
	a, ok := s.approvals[id]
	if !ok {
		return fmt.Errorf("approval not found: %s", id)
	}
	a.Status = status
	a.Response = response
	a.Reason = reason
	s.approvals[id] = a
	return nil
}
func (s *fakeTaskStore) AllowedUserIDs(ctx context.Context) ([]int64, error) { return s.allowed, nil }
func (s *fakeTaskStore) PendingApprovalForTask(ctx context.Context, taskID string) (control.Approval, error) {
	for _, a := range s.approvals {
		if a.TaskID == taskID && a.Status == "pending" {
			return a, nil
		}
	}
	return control.Approval{}, fmt.Errorf("no pending approval for task %s", taskID)
}

type fakeDispatcher struct {
	dispatched     []engine.RunParams
	waits          map[string]chan bool
	cancelled      []string
	pausedGraceful []string
	resumed        []string
}

func (d *fakeDispatcher) Dispatch(ctx context.Context, params engine.RunParams) {
	d.dispatched = append(d.dispatched, params)
}
func (d *fakeDispatcher) Cancel(taskID string) { d.cancelled = append(d.cancelled, taskID) }
func (d *fakeDispatcher) PauseGraceful(taskID string) {
	d.pausedGraceful = append(d.pausedGraceful, taskID)
}
func (d *fakeDispatcher) ResumeGraceful(taskID string) { d.resumed = append(d.resumed, taskID) }
func (d *fakeDispatcher) WaitForApproval(ctx context.Context, approvalID string) (bool, error) {
	if d.waits == nil {
		d.waits = make(map[string]chan bool)
	}
	ch, ok := d.waits[approvalID]
	if !ok {
		ch = make(chan bool, 1)
		d.waits[approvalID] = ch
	}
	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
func (d *fakeDispatcher) ResolveApproval(approvalID string, approved bool) {
	if d.waits == nil {
		d.waits = make(map[string]chan bool)
	}
	ch, ok := d.waits[approvalID]
	if !ok {
		ch = make(chan bool, 1)
		d.waits[approvalID] = ch
	}
	ch <- approved
}

func TestCreateTaskDispatchesAndReturnsID(t *testing.T) {
	origResolve := resolveBaseCommit
	resolveBaseCommit = func(ctx context.Context, repoURL string) (string, error) {
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	defer func() { resolveBaseCommit = origResolve }()

	store := newFakeTaskStore()
	disp := &fakeDispatcher{}

	tsk, err := createTask(context.Background(), store, disp, "https://example.com/x/y.git", "Add a Sub function")
	if err != nil {
		t.Fatalf("createTask: %v", err)
	}
	if tsk.ID == "" {
		t.Fatal("expected a non-empty task id")
	}
	if tsk.Status != task.StatusPending {
		t.Errorf("expected status pending, got %s", tsk.Status)
	}
	if len(disp.dispatched) != 1 || disp.dispatched[0].Task.ID != tsk.ID {
		t.Fatalf("expected the task to be dispatched exactly once, got %+v", disp.dispatched)
	}
	if len(store.nodes[tsk.ID]) != 4 {
		t.Errorf("expected 4 linear nodes created, got %d", len(store.nodes[tsk.ID]))
	}
}

func TestCreateTaskRejectsEmptyFields(t *testing.T) {
	store := newFakeTaskStore()
	disp := &fakeDispatcher{}
	if _, err := createTask(context.Background(), store, disp, "", "something"); err == nil {
		t.Error("expected an error for an empty repo")
	}
	if _, err := createTask(context.Background(), store, disp, "repo", "  "); err == nil {
		t.Error("expected an error for empty request text")
	}
	if len(disp.dispatched) != 0 {
		t.Error("expected no dispatch for rejected input")
	}
}

func TestFormatTaskStatus(t *testing.T) {
	tsk := task.Task{ID: "tsk_1", Status: task.StatusRunning, RepoURL: "/r.git"}
	nodes := []task.Node{
		{Name: task.NodeSpec, Status: task.NodeStatusSucceeded},
		{Name: task.NodeImplement, Status: task.NodeStatusRunning},
	}
	got := formatTaskStatus(tsk, nodes)
	if !strings.Contains(got, "tsk_1: running") || !strings.Contains(got, "spec: succeeded") {
		t.Errorf("unexpected status format: %q", got)
	}
}

func TestFormatTaskList(t *testing.T) {
	if got := formatTaskList(nil); got != "no tasks yet" {
		t.Errorf("expected the empty-list message, got %q", got)
	}
	tasks := []task.Task{{ID: "tsk_1", Status: task.StatusSucceeded, RequestText: "Add Sub"}}
	got := formatTaskList(tasks)
	if !strings.Contains(got, "tsk_1") || !strings.Contains(got, "Add Sub") {
		t.Errorf("unexpected task list format: %q", got)
	}
}

func TestGitResolveHEADLocalRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-q", "-m", "init")

	sha, err := gitResolveHEAD(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitResolveHEAD: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("expected a 40-char SHA, got %q", sha)
	}
}
