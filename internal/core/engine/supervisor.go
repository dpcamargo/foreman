package engine

import (
	"context"
	"sync"
)

// Supervisor dispatches engine.RunTask calls under bounded concurrency and gives Feature
// 003's control plane (Telegram, local API) a way to pause, resume, or cancel a specific
// task without touching the ladder logic itself (research.md R7, R7 addendum).
type Supervisor struct {
	eng *Engine

	sem chan struct{} // bounded concurrency: one slot per in-flight Dispatch
	wg  sync.WaitGroup

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	paused  map[string]bool
	waits   map[string]chan bool // keyed by approval id (research.md R7)
}

// NewSupervisor builds the Engine cfg wires PauseCheck into (so a single shared Engine
// instance correctly answers "is this specific task paused" for every concurrent dispatch),
// then returns a Supervisor that dispatches work to it.
func NewSupervisor(cfg Config, maxConcurrent int) *Supervisor {
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	s := &Supervisor{
		sem:     make(chan struct{}, maxConcurrent),
		cancels: make(map[string]context.CancelFunc),
		paused:  make(map[string]bool),
		waits:   make(map[string]chan bool),
	}
	cfg.PauseCheck = s.isPaused
	s.eng = New(cfg)
	return s
}

// Dispatch runs RunTask for params.Task in a new goroutine, blocking for a concurrency slot
// if the Supervisor is already at capacity. It returns immediately; call Wait in tests, or
// rely on the Store's task status in production.
func (s *Supervisor) Dispatch(ctx context.Context, params RunParams) {
	taskID := params.Task.ID
	runCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.cancels[taskID] = cancel
	s.paused[taskID] = false
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.cancels, taskID)
			delete(s.paused, taskID)
			s.mu.Unlock()
			cancel()
		}()

		select {
		case s.sem <- struct{}{}:
		case <-runCtx.Done():
			return
		}
		defer func() { <-s.sem }()

		_, _ = s.eng.RunTask(runCtx, params)
	}()
}

// Cancel aborts taskID's in-flight dispatch immediately (FR-009, "/pause <id> now"): the
// cancelled context propagates to the implementer/verifier through the same
// exec.CommandContext threading Feature 002's runners already use, killing the running
// process rather than waiting for it to finish.
func (s *Supervisor) Cancel(taskID string) {
	s.mu.Lock()
	cancel, ok := s.cancels[taskID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

// PauseGraceful marks taskID paused: the ladder stops before its next attempt but the one
// already running is left to finish (FR-008, "/pause <id>" without "now").
func (s *Supervisor) PauseGraceful(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused[taskID] = true
}

// ResumeGraceful clears a graceful pause so a future dispatch of the same task may proceed.
func (s *Supervisor) ResumeGraceful(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.paused, taskID)
}

func (s *Supervisor) isPaused(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused[taskID]
}

// Wait blocks until every dispatched goroutine has returned. Test-only convenience; the
// daemon never calls this (dispatch is meant to run indefinitely).
func (s *Supervisor) Wait() {
	s.wg.Wait()
}

// WaitForApproval blocks until ResolveApproval is called for approvalID, or ctx is
// cancelled (e.g. the whole dispatch was cancelled while waiting on a human). It is the
// Supervisor-side half of research.md R7's ApprovalGate hook: the daemon's ApprovalGate
// closure creates the Approval row and sends the Telegram/local-API request, then calls
// this to block; a Telegram callback or a local API respond call elsewhere calls
// ResolveApproval to unblock it. Only one waiter may be registered per approvalID at a time.
func (s *Supervisor) WaitForApproval(ctx context.Context, approvalID string) (bool, error) {
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.waits[approvalID] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.waits, approvalID)
		s.mu.Unlock()
	}()

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// ResolveApproval wakes a pending WaitForApproval(ctx, approvalID) call with the given
// decision. It is a no-op if no waiter is registered (e.g. a stale or duplicate callback
// after the gate already timed out) — callers that need to tell the difference should check
// the Approval row's status first (contracts/approval-nonce.md: only a pending approval may
// be resolved).
func (s *Supervisor) ResolveApproval(approvalID string, approved bool) {
	s.mu.Lock()
	ch, ok := s.waits[approvalID]
	s.mu.Unlock()
	if ok {
		ch <- approved
	}
}
