package engine

import (
	"context"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/task"
)

type reconcileStore struct {
	runs     []task.Run
	finished []string
}

func (s *reconcileStore) RunningTaskRuns(context.Context) ([]task.Run, error) {
	return append([]task.Run(nil), s.runs...), nil
}

func (s *reconcileStore) FinishTaskRun(_ context.Context, id string, status task.RunStatus, class task.FailureClass, sig string) error {
	if status != task.RunInfraError || class != task.FailureInfra || sig != "lost" {
		panic("unexpected lost-run finish values")
	}
	s.finished = append(s.finished, id)
	return nil
}

func TestReconcileLostRuns(t *testing.T) {
	store := &reconcileStore{runs: []task.Run{
		{ID: "dead", PID: 10, PIDStartedAt: "old"},
		{ID: "active", PID: 20, PIDStartedAt: "current"},
		{ID: "missing-fingerprint", PID: 0},
	}}
	reconciler := Reconciler{
		Store: store,
		Alive: func(pid int, startedAt string) bool { return pid == 20 && startedAt == "current" },
	}

	lost, err := reconciler.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 2 || lost[0] != "dead" || lost[1] != "missing-fingerprint" {
		t.Fatalf("lost = %v", lost)
	}
	if len(store.finished) != 2 || store.finished[0] != "dead" || store.finished[1] != "missing-fingerprint" {
		t.Fatalf("finished = %v", store.finished)
	}
}
