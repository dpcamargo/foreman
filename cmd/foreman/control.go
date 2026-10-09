package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// pauseTaskGraceful implements "/pause <id>" (no "now"): the Supervisor stops the ladder
// before its next attempt; the one already running finishes (FR-008). The engine itself
// writes the task's status to paused once the ladder notices (PauseCheck, research.md R7
// addendum) — this function only needs to set the flag.
func pauseTaskGraceful(sup supervisorControl, taskID string) {
	sup.PauseGraceful(taskID)
}

// pauseTaskNow implements "/pause <id> now": the running attempt is killed immediately via
// context cancellation (FR-008). Because engine.RunTask now returns as soon as it observes
// the cancelled context, without writing any status itself (to avoid racing a /cancel or
// /pause now that already knows the final state), this function is responsible for marking
// the task paused.
func pauseTaskNow(ctx context.Context, store taskStore, sup supervisorControl, taskID string) error {
	sup.Cancel(taskID)
	return transitionTask(ctx, store, taskID, (*task.Task).Pause)
}

// resumeTask implements "/resume <id>": clears the pause flag and re-dispatches the task
// (the ladder restarts at rung 1 — Feature 002 has no mid-ladder resume state to restore
// across a RunTask call boundary, the same limitation `fm run --resume` already has after a
// reconciler sweep).
func resumeTask(ctx context.Context, store taskStore, sup dispatcherApprovals, taskID string) error {
	sup.ResumeGraceful(taskID)
	tsk, err := store.Task(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task %s: %w", taskID, err)
	}
	if err := tsk.Resume(time.Now().UTC()); err != nil {
		return err
	}
	if err := store.UpdateTaskStatus(ctx, taskID, tsk.Status, tsk.CurrentNodeID); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	sup.DispatchWithReport(context.WithoutCancel(ctx), engineRunParamsFor(ctx, tsk), func(report engine.TaskReport, err error) {
		if dispatchOnDone != nil {
			dispatchOnDone(context.WithoutCancel(ctx), report, err)
		}
	})
	return nil
}

// cancelTask implements "/cancel <id>": kills the running attempt and marks the task
// cancelled, keeping its workspace for later inspection (the 7-day sweep is Polish-phase
// work, FR-009).
func cancelTask(ctx context.Context, store taskStore, sup supervisorControl, taskID string) error {
	sup.Cancel(taskID)
	return transitionTask(ctx, store, taskID, (*task.Task).Cancel)
}

// transitionTask loads taskID, applies a domain transition (Task.Pause or Task.Cancel), and
// persists the result — shared by pauseTaskNow and cancelTask since both follow the same
// load/validate/persist shape around a Supervisor.Cancel call.
func transitionTask(ctx context.Context, store taskStore, taskID string, transition func(*task.Task, time.Time) error) error {
	tsk, err := store.Task(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task %s: %w", taskID, err)
	}
	if err := transition(&tsk, time.Now().UTC()); err != nil {
		return err
	}
	if err := store.UpdateTaskStatus(ctx, taskID, tsk.Status, tsk.CurrentNodeID); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}
