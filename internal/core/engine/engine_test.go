package engine_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

type fakeStore struct {
	tasks map[string]task.Task
	nodes map[string]task.Node
	runs  []task.Run
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tasks: make(map[string]task.Task),
		nodes: make(map[string]task.Node),
	}
}

func (s *fakeStore) CreateTask(ctx context.Context, t task.Task, nodes []task.Node) (task.Task, error) {
	s.tasks[t.ID] = t
	for _, n := range nodes {
		s.nodes[n.ID] = n
	}
	return t, nil
}

func (s *fakeStore) Task(ctx context.Context, id string) (task.Task, error) {
	t, ok := s.tasks[id]
	if !ok {
		return task.Task{}, task.ErrNotFound
	}
	return t, nil
}

func (s *fakeStore) UpdateTaskStatus(ctx context.Context, id string, status task.TaskStatus, currentNodeID string) error {
	t := s.tasks[id]
	t.Status = status
	t.CurrentNodeID = currentNodeID
	s.tasks[id] = t
	return nil
}

func (s *fakeStore) Node(ctx context.Context, id string) (task.Node, error) {
	n, ok := s.nodes[id]
	if !ok {
		return task.Node{}, task.ErrNotFound
	}
	return n, nil
}

func (s *fakeStore) NodesForTask(ctx context.Context, taskID string) ([]task.Node, error) {
	var res []task.Node
	for _, n := range s.nodes {
		if n.TaskID == taskID {
			res = append(res, n)
		}
	}
	return res, nil
}

func (s *fakeStore) UpdateNodeStatus(ctx context.Context, id string, status task.NodeStatus) error {
	n := s.nodes[id]
	n.Status = status
	s.nodes[id] = n
	return nil
}

func (s *fakeStore) RecordTaskRun(ctx context.Context, r task.Run) error {
	s.runs = append(s.runs, r)
	return nil
}

func (s *fakeStore) FinishTaskRun(ctx context.Context, runID string, status task.RunStatus, failureClass task.FailureClass, failureSig string) error {
	for i, r := range s.runs {
		if r.ID == runID {
			s.runs[i].Status = status
			s.runs[i].FailureClass = failureClass
			s.runs[i].FailureSig = failureSig
			return nil
		}
	}
	return nil
}

type fakeSpecifier struct{}

func (s *fakeSpecifier) Specify(ctx context.Context, in engine.SpecInput) (engine.TaskSpec, error) {
	return engine.TaskSpec{
		Summary:            "Add Sub function",
		AcceptanceCriteria: []string{"Sub(2, 1) returns 1"},
		VerifyCommands:     [][]string{{"go", "test", "./..."}},
		CitedFiles:         []string{"src/add.go"},
	}, nil
}

type fakeImplementer struct {
	family   string
	route    string
	attempts int
	failures int
}

func (i *fakeImplementer) Family() string { return i.family }
func (i *fakeImplementer) Route() string  { return i.route }
func (i *fakeImplementer) Implement(ctx context.Context, in engine.ImplementInput) (engine.ImplementResult, error) {
	i.attempts++
	return engine.ImplementResult{
		Status:         task.RunSucceeded,
		Patch:          []byte("diff --git a/src/add.go b/src/add.go\n"),
		DiffHash:       "fake_diff_hash",
		TokensBillable: 1500,
	}, nil
}

type fakeVerifier struct {
	shouldPass bool
}

func (v *fakeVerifier) Verify(ctx context.Context, in engine.VerifyInput) (engine.VerifyReport, error) {
	if v.shouldPass {
		return engine.VerifyReport{
			Passed:       true,
			PolicyPassed: true,
			BuildPassed:  true,
			HiddenPassed: true,
		}, nil
	}
	return engine.VerifyReport{
		Passed:       false,
		PolicyPassed: true,
		BuildPassed:  true,
		HiddenPassed: false,
		FailingTests: []string{"TestSub"},
		ErrorSummary: "Sub(2, 1) != 1",
	}, nil
}

type fakeReviewer struct {
	family   string
	route    string
	approved bool
}

func (r *fakeReviewer) Family() string { return r.family }
func (r *fakeReviewer) Route() string  { return r.route }
func (r *fakeReviewer) Review(ctx context.Context, in engine.ReviewInput) (engine.ReviewResult, error) {
	return engine.ReviewResult{
		Approved: r.approved,
		Summary:  "LGTM",
	}, nil
}

type fakeCloner struct{}

func (c *fakeCloner) PrepareWorkspace(ctx context.Context, taskID, runID, repoURL, baseCommit string) (string, error) {
	return "/tmp/ws", nil
}
func (c *fakeCloner) DestroyWorkspace(ctx context.Context, dir string) error {
	return nil
}

func TestEngineRunTaskEndToEnd(t *testing.T) {
	store := newFakeStore()
	eng := engine.New(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{"openai": &fakeImplementer{family: "openai", route: "codex/default"}},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
	})

	tsk := task.Task{
		ID:          "tsk_123",
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repo.git",
		BaseCommit:  "7fb3e561ee1cee9f9f02a6dccaacbee1212cb336",
		RequestText: "Add Sub",
		Status:      task.StatusPending,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	report, err := eng.RunTask(context.Background(), engine.RunParams{
		Task:    tsk,
		Visible: fstest.MapFS{},
		Hidden:  fstest.MapFS{},
	})
	if err != nil {
		t.Fatalf("unexpected RunTask error: %v", err)
	}

	if report.Status != task.StatusSucceeded {
		t.Errorf("expected status succeeded, got %s", report.Status)
	}
	if report.ReviewStatus != "approved" {
		t.Errorf("expected review approved, got %s", report.ReviewStatus)
	}
	if report.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", report.Attempts)
	}
}
