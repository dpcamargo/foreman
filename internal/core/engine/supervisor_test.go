package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// blockingImplementer signals startedCh every time Implement is entered and then waits for
// either releaseCh or ctx cancellation, so tests can observe and control exactly how many
// attempts are in flight at once.
type blockingImplementer struct {
	family, route string
	startedCh     chan struct{}
	releaseCh     chan struct{}
	inFlight      int32
	maxInFlight   int32
}

func (b *blockingImplementer) Family() string { return b.family }
func (b *blockingImplementer) Route() string  { return b.route }
func (b *blockingImplementer) Implement(ctx context.Context, in engine.ImplementInput) (engine.ImplementResult, error) {
	n := atomic.AddInt32(&b.inFlight, 1)
	for {
		old := atomic.LoadInt32(&b.maxInFlight)
		if n <= old || atomic.CompareAndSwapInt32(&b.maxInFlight, old, n) {
			break
		}
	}
	select {
	case b.startedCh <- struct{}{}:
	case <-ctx.Done():
		atomic.AddInt32(&b.inFlight, -1)
		return engine.ImplementResult{}, ctx.Err()
	}
	defer atomic.AddInt32(&b.inFlight, -1)
	select {
	case <-b.releaseCh:
		return engine.ImplementResult{Status: task.RunSucceeded, Patch: []byte("diff"), DiffHash: "h", TokensBillable: 1}, nil
	case <-ctx.Done():
		return engine.ImplementResult{}, ctx.Err()
	}
}

func newTestTask(id string) task.Task {
	now := time.Now().UTC()
	return task.Task{
		ID:          id,
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repo.git",
		BaseCommit:  "7fb3e561ee1cee9f9f02a6dccaacbee1212cb336",
		RequestText: "Add Sub",
		Status:      task.StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestSupervisorBoundedConcurrency(t *testing.T) {
	impl := &blockingImplementer{family: "openai", route: "codex/default", startedCh: make(chan struct{}), releaseCh: make(chan struct{})}
	store := newFakeStore()
	sup := engine.NewSupervisor(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{"openai": impl},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
	}, 2)

	ctx := context.Background()
	for _, id := range []string{"tsk_1", "tsk_2", "tsk_3"} {
		sup.Dispatch(ctx, engine.RunParams{Task: newTestTask(id), Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})
	}

	// Two slots: exactly two attempts should start.
	for i := 0; i < 2; i++ {
		select {
		case <-impl.startedCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected attempt %d to start within the concurrency limit", i+1)
		}
	}
	select {
	case <-impl.startedCh:
		t.Fatal("a 3rd attempt started before a slot was released")
	case <-time.After(150 * time.Millisecond):
	}

	// Release one; the 3rd should now start.
	impl.releaseCh <- struct{}{}
	select {
	case <-impl.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("3rd attempt never started after a slot freed up")
	}
	impl.releaseCh <- struct{}{}
	impl.releaseCh <- struct{}{}

	sup.Wait()
	if got := atomic.LoadInt32(&impl.maxInFlight); got > 2 {
		t.Errorf("expected at most 2 concurrent attempts, saw %d", got)
	}
}

func TestSupervisorCancelAbortsRunningDispatch(t *testing.T) {
	impl := &blockingImplementer{family: "openai", route: "codex/default", startedCh: make(chan struct{}), releaseCh: make(chan struct{})}
	store := newFakeStore()
	sup := engine.NewSupervisor(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{"openai": impl},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
	}, 2)

	tsk := newTestTask("tsk_cancel")
	sup.Dispatch(context.Background(), engine.RunParams{Task: tsk, Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})

	select {
	case <-impl.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt never started")
	}

	sup.Cancel(tsk.ID)
	sup.Wait() // should return promptly: the implementer honors ctx.Done(), never releaseCh
}

func TestSupervisorPauseGracefulStopsBeforeNextAttempt(t *testing.T) {
	store := newFakeStore()
	var sup *engine.Supervisor
	var mu sync.Mutex
	calls := 0

	impl := &selfPausingImplementer{
		onCall: func() {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				sup.PauseGraceful("tsk_pause")
			}
		},
	}
	sup = engine.NewSupervisor(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: false}, // always fails verification -> ladder would retry
		Implementers: map[string]engine.Implementer{"openai": impl},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
	}, 2)

	tsk := newTestTask("tsk_pause")
	sup.Dispatch(context.Background(), engine.RunParams{Task: tsk, Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})
	sup.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("expected the ladder to stop after 1 attempt once paused, got %d calls", calls)
	}
	got := store.tasks["tsk_pause"]
	if got.Status != task.StatusPaused {
		t.Errorf("expected task status paused, got %s", got.Status)
	}
}

func TestSupervisorApprovalGateBlocksUntilResolved(t *testing.T) {
	store := newFakeStore()
	impl := &fakeImplementer{family: "openai", route: "codex/default"}
	gateCalledCh := make(chan struct{})

	var sup *engine.Supervisor
	sup = engine.NewSupervisor(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{"openai": impl},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
		ApprovalGate: func(ctx context.Context, taskID string, spec engine.TaskSpec) (bool, error) {
			close(gateCalledCh)
			return sup.WaitForApproval(ctx, "apv_"+taskID)
		},
	}, 2)

	tsk := newTestTask("tsk_reject")
	sup.Dispatch(context.Background(), engine.RunParams{Task: tsk, Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})

	select {
	case <-gateCalledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("approval gate was never called")
	}

	// The ladder must not have started yet: the gate is still blocking.
	if impl.attempts != 0 {
		t.Errorf("expected 0 implement attempts before the approval resolves, got %d", impl.attempts)
	}

	sup.ResolveApproval("apv_tsk_reject", false) // rejected
	sup.Wait()

	if impl.attempts != 0 {
		t.Errorf("expected the ladder to never run after a rejected approval, got %d attempts", impl.attempts)
	}
	got := store.tasks["tsk_reject"]
	if got.Status != task.StatusFailed {
		t.Errorf("expected task status failed after a rejected approval, got %s", got.Status)
	}
}

// selfPausingImplementer calls onCall() synchronously before returning a failing result, so
// the test can inject a PauseGraceful call deterministically between attempt 1 and the
// ladder's next-iteration pause check, without relying on real concurrency timing.
type selfPausingImplementer struct {
	onCall func()
}

func (s *selfPausingImplementer) Family() string { return "openai" }
func (s *selfPausingImplementer) Route() string  { return "codex/default" }
func (s *selfPausingImplementer) Implement(ctx context.Context, in engine.ImplementInput) (engine.ImplementResult, error) {
	s.onCall()
	return engine.ImplementResult{Status: task.RunSucceeded, Patch: []byte("diff"), DiffHash: "h", TokensBillable: 1}, nil
}

func TestSupervisorResumesAfterApprovalGateReturns(t *testing.T) {
	store := newFakeStore()
	gateReleaseCh := make(chan struct{})
	gateCalledCh := make(chan struct{})
	var gateCalledOnce sync.Once

	sup := engine.NewSupervisor(engine.Config{
		Store:        store,
		Specifier:    &fakeSpecifier{},
		Verifier:     &fakeVerifier{shouldPass: true},
		Implementers: map[string]engine.Implementer{"openai": &fakeImplementer{family: "openai", route: "codex/default"}},
		Reviewers:    map[string]engine.Reviewer{"anthropic": &fakeReviewer{family: "anthropic", route: "hermes/claude", approved: true}},
		Cloner:       &fakeCloner{},
		FileExists:   func(path string) bool { return true },
		ApprovalGate: func(ctx context.Context, taskID string, spec engine.TaskSpec) (bool, error) {
			gateCalledOnce.Do(func() { close(gateCalledCh) })
			<-gateReleaseCh
			return true, nil
		},
	}, 2)

	tsk := newTestTask("tsk_gate")
	sup.Dispatch(context.Background(), engine.RunParams{Task: tsk, Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})

	select {
	case <-gateCalledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("approval gate was never called")
	}

	// Dispatching a second, independent task must not be blocked by the first one's gate.
	sup.Dispatch(context.Background(), engine.RunParams{Task: newTestTask("tsk_other"), Visible: fstest.MapFS{}, Hidden: fstest.MapFS{}})

	close(gateReleaseCh)
	sup.Wait()

	got := store.tasks["tsk_gate"]
	if got.Status != task.StatusSucceeded {
		t.Errorf("expected tsk_gate to succeed once the gate returned, got %s", got.Status)
	}
	other := store.tasks["tsk_other"]
	if other.Status != task.StatusSucceeded {
		t.Errorf("expected tsk_other to succeed independently, got %s", other.Status)
	}
}
