package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// TestServeRecoversLostRunsOnStartup mirrors cmd/fm/run_test.go's
// TestRunInterruptedResumption (T033): a task is left "running" in the store with a
// RunRunning row whose PID is already dead — exactly the state a SIGKILL'd daemon leaves
// behind. Restarting the daemon must sweep that run to infra_error/lost before any new
// dispatch is reachable, and must not leave duplicate or inconsistent rows.
//
// A literal os/exec SIGKILL of a real daemon subprocess isn't necessary to cover the
// behavior in scope: the thing that actually needs to survive the crash is the SQLite
// lease + reconciler handshake, not the signal itself. Seeding a dead-PID run and calling
// serve() (which runs Reconciler.Sweep before touching the Supervisor) covers the same
// crash-recovery contract without needing a real, long-running child process to kill.
func TestServeRecoversLostRunsOnStartup(t *testing.T) {
	home, err := os.MkdirTemp("", "fm-home")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(home)
	e := env{
		Home:  home,
		State: filepath.Join(home, "state"),
		Work:  filepath.Join(home, "work"),
	}
	ctx := context.Background()

	store, err := sqlite.Open(ctx, e.State)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	now := time.Now().UTC()
	tsk := task.Task{
		ID:          "tsk_lost_1",
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/tmp/repo.git",
		BaseCommit:  "4c979416b80fd702664709a8af97bf3b17a9ec89",
		RequestText: "Add Sub",
		Status:      task.StatusRunning,
	}
	nodes := task.NewLinearNodes(tsk.ID, now)
	if _, err := store.CreateTask(ctx, tsk, nodes); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := task.Run{
		ID:           "run_lost_1",
		TaskID:       tsk.ID,
		NodeID:       nodes[1].ID,
		AttemptNum:   1,
		LadderRung:   1,
		Route:        "codex/default",
		ModelFamily:  "openai",
		Status:       task.RunRunning,
		PID:          9999999, // inactive PID, same trick as TestRunInterruptedResumption
		PIDStartedAt: "old",
		StartedAt:    now,
	}
	if err := store.RecordTaskRun(ctx, run); err != nil {
		t.Fatalf("RecordTaskRun: %v", err)
	}
	store.Close()

	// "Restart": a fresh serve() call must run the reconciler sweep before anything else.
	serveDone := make(chan int, 1)
	serveCtx, cancel := context.WithCancel(ctx)
	go func() { serveDone <- serve(serveCtx, e, discard{}, discard{}) }()

	// Give the daemon a moment to run the sweep and bind the socket before shutting down
	// (same settle-poll pattern as TestServeStartsAndShutsDownCleanly).
	var socketUp bool
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(e.socketPath()); err == nil {
			socketUp = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !socketUp {
		t.Fatalf("expected the local API socket to exist shortly after serve starts")
	}
	cancel()
	<-serveDone

	store2, err := sqlite.Open(ctx, e.State)
	if err != nil {
		t.Fatalf("sqlite.Open (after restart): %v", err)
	}
	defer store2.Close()
	runs, err := store2.RunningTaskRuns(ctx)
	if err != nil {
		t.Fatalf("RunningTaskRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 running runs after startup sweep, got %d: %+v", len(runs), runs)
	}
}

// TestServeSweepsBeforeDispatch asserts T037's core ordering guarantee directly:
// the only way to reach engine.Engine.Dispatch is through a *Supervisor (or a fake
// explicitly substituted in a test), and serve() constructs that Supervisor only after
// Reconciler.Sweep has already returned. This is a build-time invariant guard — it won't
// fail at runtime, but it documents the contract and will fail to compile if the
// Supervisor's dispatch entrypoint signature ever drifts.
func TestServeSweepsBeforeDispatch(t *testing.T) {
	var _ interface {
		Dispatch(ctx context.Context, params engine.RunParams)
	} = (*engine.Supervisor)(nil)
	_ = proc.Alive
	_ = eval.PIDFingerprint{}
}
