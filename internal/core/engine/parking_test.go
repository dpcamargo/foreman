package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// stubRouteSelector returns a fixed family or parks on demand.
type stubRouteSelector struct {
	family string
	park   bool
}

func (s *stubRouteSelector) Select(ctx context.Context, req engine.RouteSelectRequest) (string, error) {
	if s.park {
		return "", engine.ErrNoEligibleRoute
	}
	return s.family, nil
}

func parkedTestTask() task.Task {
	return task.Task{
		ID:          "tsk_park",
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repo.git",
		BaseCommit:  "7fb3e561ee1cee9f9f02a6dccaacbee1212cb336",
		RequestText: "Add Sub",
		Status:      task.StatusPending,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func TestNodeParksWhenNoEligibleRoute(t *testing.T) {
	store := newFakeStore()
	eng := engine.New(engine.Config{
		Store:         store,
		Specifier:     &fakeSpecifier{},
		Verifier:      &fakeVerifier{shouldPass: true},
		Implementers:  map[string]engine.Implementer{"openai": &fakeImplementer{family: "openai", route: "codex/default"}},
		Reviewers:     map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:        &fakeCloner{},
		FileExists:    func(path string) bool { return true },
		RouteSelector: &stubRouteSelector{park: true},
	})

	report, err := eng.RunTask(context.Background(), engine.RunParams{
		Task:    parkedTestTask(),
		Visible: nil,
		Hidden:  nil,
	})
	if err != nil {
		t.Fatalf("unexpected RunTask error: %v", err)
	}

	// The implement node should be pending (parked), not failed, with not_before set.
	// Find it by name — fakeStore.NodesForTask iterates a map (random order).
	nodes, err := store.NodesForTask(context.Background(), report.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", len(nodes))
	}
	var implNode task.Node
	found := false
	for _, n := range nodes {
		if n.Name == task.NodeImplement {
			implNode = n
			found = true
			break
		}
	}
	if !found {
		t.Fatal("implement node not found")
	}
	if implNode.Status != task.NodeStatusPending {
		t.Errorf("implement node status = %q, want pending (parked, not failed)", implNode.Status)
	}
	if implNode.NotBefore == nil {
		t.Error("implement node NotBefore not set")
	}
	// No attempt should have been recorded (FR-005: "no attempt counted").
	if len(store.runs) != 0 {
		t.Errorf("expected 0 runs (no attempt counted), got %d", len(store.runs))
	}
	// Report should show running (task is alive, just parked).
	if report.Status != task.StatusRunning {
		t.Errorf("report status = %q, want running", report.Status)
	}
	if report.Attempts != 0 {
		t.Errorf("report attempts = %d, want 0", report.Attempts)
	}
}

func TestRouteSelectorOverridesDefaultFamily(t *testing.T) {
	store := newFakeStore()
	eng := engine.New(engine.Config{
		Store:     store,
		Specifier: &fakeSpecifier{},
		Verifier:  &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{
			"openai":    &fakeImplementer{family: "openai", route: "codex/default"},
			"anthropic": &fakeImplementer{family: "anthropic", route: "hermes/claude-sonnet-5"},
		},
		Reviewers:     map[string]engine.Reviewer{"openai": &fakeReviewer{family: "openai", route: "codex/default", approved: true}},
		Cloner:        &fakeCloner{},
		FileExists:    func(path string) bool { return true },
		RouteSelector: &stubRouteSelector{family: "anthropic"},
	})

	report, err := eng.RunTask(context.Background(), engine.RunParams{
		Task:    parkedTestTask(),
		Visible: nil,
		Hidden:  nil,
	})
	if err != nil {
		t.Fatalf("unexpected RunTask error: %v", err)
	}
	if report.Status != task.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", report.Status)
	}
	// The implementer should have been the anthropic one selected by the router.
	if len(store.runs) == 0 {
		t.Fatal("no runs recorded")
	}
	got := store.runs[0].ModelFamily
	if got != "anthropic" {
		t.Errorf("run model family = %q, want anthropic (router override)", got)
	}
}
