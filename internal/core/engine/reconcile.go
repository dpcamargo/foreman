package engine

import (
	"context"
	"fmt"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// LostRunStore is the persistence subset needed by the startup reconciler.
type LostRunStore interface {
	RunningTaskRuns(ctx context.Context) ([]task.Run, error)
	FinishTaskRun(ctx context.Context, runID string, status task.RunStatus, failureClass task.FailureClass, failureSig string) error
}

// Reconciler marks running attempts whose PID fingerprint is no longer alive as lost.
type Reconciler struct {
	Store LostRunStore
	Alive func(pid int, startedAt string) bool
}

func (r Reconciler) Sweep(ctx context.Context) ([]string, error) {
	if r.Store == nil || r.Alive == nil {
		return nil, fmt.Errorf("reconcile lost runs: store and process checker are required")
	}
	runs, err := r.Store.RunningTaskRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running task runs: %w", err)
	}
	var lost []string
	for _, run := range runs {
		if r.Alive(run.PID, run.PIDStartedAt) {
			continue
		}
		if err := r.Store.FinishTaskRun(ctx, run.ID, task.RunInfraError, task.FailureInfra, "lost"); err != nil {
			return lost, fmt.Errorf("mark task run %s lost: %w", run.ID, err)
		}
		lost = append(lost, run.ID)
	}
	return lost, nil
}
