package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

type Config struct {
	Store         task.TaskStore
	Specifier     Specifier
	Verifier      Verifier
	Implementers  map[string]Implementer // keyed by family ("openai", "anthropic")
	Reviewers     map[string]Reviewer    // keyed by family ("openai", "anthropic")
	Cloner        WorkspaceCloner
	FileExists    func(path string) bool
	MaxRung       int
	DefaultFamily string
	PID           int
	PIDStartedAt  string
	// ApprovalGate, if set, is called once after the spec passes ValidateTaskSpec and
	// before the implement ladder starts. It blocks until a human approves or rejects the
	// spec (Feature 003's Telegram/local-API control plane). nil means no gate at all —
	// fm run (Feature 002) never sets this field, so its behavior is unchanged
	// (specs/003-daemon-telegram-control/research.md R7).
	ApprovalGate func(ctx context.Context, taskID string, spec TaskSpec) (approved bool, err error)
	// PauseCheck, if set, is checked once at the top of every ladder iteration (before the
	// next implement attempt starts). A true result stops the ladder without killing the
	// attempt in flight, leaving the task running for a later resume. nil is a no-op, for
	// the same reason as ApprovalGate (research.md R7 addendum).
	PauseCheck func(taskID string) bool
	// RouteSelector, if set, picks the model family before each implement attempt
	// (FR-002/003). When Select returns ErrNoEligibleRoute the engine parks the node
	// (sets not_before, leaves it pending, counts no attempt) instead of failing (FR-005).
	// nil preserves the pre-004 behavior: e.cfg.DefaultFamily with family switches
	// from the ladder (specs/004-routing-test-first/research.md R1/R9).
	RouteSelector RouteSelector
	// OnProviderError, if set, records a provider-health failure signal (429/plan-limit/
	// 5xx/expired-auth) so the router can put the provider in cooldown (FR-004). nil is
	// a no-op, for the same reason as ApprovalGate.
	OnProviderError func(family string, err error)
	// ParkedNotify, if set, is called when a node is parked. nil is a no-op.
	ParkedNotify func(taskID, nodeID string, until time.Time)
}

type Engine struct {
	cfg Config
}

func New(cfg Config) *Engine {
	if cfg.MaxRung <= 0 {
		cfg.MaxRung = 3
	}
	if cfg.DefaultFamily == "" {
		cfg.DefaultFamily = "openai"
	}
	return &Engine{cfg: cfg}
}

type RunParams struct {
	Task        task.Task
	Visible     fs.FS
	Hidden      fs.FS
	ProjectYAML []byte
	Timeout     time.Duration
}

func (e *Engine) RunTask(ctx context.Context, params RunParams) (TaskReport, error) {
	now := time.Now().UTC()
	tsk := params.Task
	if err := tsk.Validate(); err != nil {
		return TaskReport{}, err
	}

	var nodes []task.Node
	existing, err := e.cfg.Store.Task(ctx, tsk.ID)
	if err == nil && existing.ID != "" {
		tsk = existing
		nodes, err = e.cfg.Store.NodesForTask(ctx, tsk.ID)
		if err != nil {
			return TaskReport{}, fmt.Errorf("load nodes for task: %w", err)
		}
	} else {
		// Initialize linear nodes in database
		nodes = task.NewLinearNodes(tsk.ID, now)
		createdTask, err := e.cfg.Store.CreateTask(ctx, tsk, nodes)
		if err != nil {
			return TaskReport{}, fmt.Errorf("create task in store: %w", err)
		}
		tsk = createdTask
	}

	if tsk.Status != task.StatusRunning {
		if err := tsk.Start(now); err != nil {
			return TaskReport{}, fmt.Errorf("start task: %w", err)
		}
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, tsk.Status, nodes[0].ID)
	}

	// 1. SPEC NODE
	specNode := nodes[0]
	_ = e.cfg.Store.UpdateNodeStatus(ctx, specNode.ID, task.NodeStatusRunning)
	specWorkspace, err := e.cfg.Cloner.PrepareWorkspace(ctx, tsk.ID, task.NewID("spec"), tsk.RepoURL, tsk.BaseCommit)
	if err != nil {
		_ = e.cfg.Store.UpdateNodeStatus(ctx, specNode.ID, task.NodeStatusFailed)
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusFailed, specNode.ID)
		return TaskReport{}, fmt.Errorf("prepare spec workspace: %w", err)
	}
	spec, err := e.cfg.Specifier.Specify(ctx, SpecInput{
		RequestText: tsk.RequestText,
		RepoURL:     tsk.RepoURL,
		BaseCommit:  tsk.BaseCommit,
		Workspace:   specWorkspace,
	})
	_ = e.cfg.Cloner.DestroyWorkspace(context.WithoutCancel(ctx), specWorkspace)
	if err != nil {
		_ = e.cfg.Store.UpdateNodeStatus(ctx, specNode.ID, task.NodeStatusFailed)
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusFailed, specNode.ID)
		return TaskReport{}, fmt.Errorf("specifier failed: %w", err)
	}
	if err := ValidateTaskSpec(spec, e.cfg.FileExists); err != nil {
		_ = e.cfg.Store.UpdateNodeStatus(ctx, specNode.ID, task.NodeStatusFailed)
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusFailed, specNode.ID)
		return TaskReport{}, fmt.Errorf("spec validation failed: %w", err)
	}
	_ = e.cfg.Store.UpdateNodeStatus(ctx, specNode.ID, task.NodeStatusSucceeded)

	// Spec-approval gate (research.md R7): nil is a no-op, preserving Feature 002's
	// behavior byte-for-byte for callers (fm run) that never set it.
	if e.cfg.ApprovalGate != nil {
		approved, err := e.cfg.ApprovalGate(ctx, tsk.ID, spec)
		if err != nil {
			return TaskReport{}, fmt.Errorf("approval gate: %w", err)
		}
		if !approved {
			_ = tsk.Fail(time.Now().UTC())
			_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusFailed, specNode.ID)
			return TaskReport{
				TaskID:      tsk.ID,
				Status:      task.StatusFailed,
				RepoURL:     tsk.RepoURL,
				BaseCommit:  tsk.BaseCommit,
				RequestText: tsk.RequestText,
				SpecSummary: spec.Summary,
				CreatedAt:   now,
			}, nil
		}
	}

	// 2. IMPLEMENT & VERIFY LOOP
	implNode := nodes[1]
	verifyNode := nodes[2]
	_ = e.cfg.Store.UpdateNodeStatus(ctx, implNode.ID, task.NodeStatusRunning)

	currentRung := 1
	currentFamily := e.cfg.DefaultFamily
	attemptNum := 1
	totalTokens := 0
	startTime := time.Now()
	guard := NewLoopGuard()
	var lastPatch []byte
	var lastDiffHash string
	var lastEvidence *FailureEvidence
	var verified bool

	for attemptNum <= 10 {
		// A cancelled context (Supervisor.Cancel, "/cancel" or "/pause ... now") means the
		// caller is already recording its own authoritative final status (e.g. Cancel ->
		// StatusCancelled) — returning here without any further Store write avoids a race
		// where this goroutine's eventual blocked_human write clobbers that decision a few
		// failed-retry iterations later.
		if ctx.Err() != nil {
			return TaskReport{}, ctx.Err()
		}
		if e.cfg.PauseCheck != nil && e.cfg.PauseCheck(tsk.ID) {
			_ = tsk.Pause(time.Now().UTC())
			_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, tsk.Status, implNode.ID)
			return TaskReport{
				TaskID:      tsk.ID,
				Status:      task.StatusPaused,
				RepoURL:     tsk.RepoURL,
				BaseCommit:  tsk.BaseCommit,
				RequestText: tsk.RequestText,
				Attempts:    attemptNum - 1,
				LadderRung:  currentRung,
				TokensTotal: totalTokens,
				WallTotal:   time.Since(startTime),
				SpecSummary: spec.Summary,
				CreatedAt:   now,
			}, nil
		}
		// Route selection (FR-002/003). nil RouteSelector preserves pre-004 behavior.
		if e.cfg.RouteSelector != nil {
			family, selErr := e.cfg.RouteSelector.Select(ctx, RouteSelectRequest{
				Role:             "implementer",
				ExcludedFamilies: nil,
			})
			if selErr != nil {
				if errors.Is(selErr, ErrNoEligibleRoute) {
					// Park the node rather than fail it (FR-005): no attempt counted,
					// status stays pending, retried after not_before elapses.
					until := time.Now().UTC().Add(1 * time.Minute)
					_ = e.cfg.Store.UpdateNodeStatus(ctx, implNode.ID, task.NodeStatusPending)
					_ = e.cfg.Store.ParkNode(ctx, implNode.ID, until)
					if e.cfg.ParkedNotify != nil {
						e.cfg.ParkedNotify(tsk.ID, implNode.ID, until)
					}
					return TaskReport{
						TaskID:      tsk.ID,
						Status:      task.StatusRunning,
						RepoURL:     tsk.RepoURL,
						BaseCommit:  tsk.BaseCommit,
						RequestText: tsk.RequestText,
						Attempts:    attemptNum - 1,
						LadderRung:  currentRung,
						TokensTotal: totalTokens,
						WallTotal:   time.Since(startTime),
						SpecSummary: spec.Summary,
						CreatedAt:   now,
					}, nil
				}
				return TaskReport{}, fmt.Errorf("route selection: %w", selErr)
			}
			currentFamily = family
		}
		impl, ok := e.cfg.Implementers[currentFamily]
		if !ok {
			return TaskReport{}, fmt.Errorf("no implementer configured for family %s", currentFamily)
		}

		runID := task.NewID("run")
		run := task.Run{
			ID:           runID,
			TaskID:       tsk.ID,
			NodeID:       implNode.ID,
			AttemptNum:   attemptNum,
			LadderRung:   currentRung,
			Route:        impl.Route(),
			ModelFamily:  impl.Family(),
			Status:       task.RunRunning,
			PID:          e.cfg.PID,
			PIDStartedAt: e.cfg.PIDStartedAt,
			StartedAt:    time.Now().UTC(),
		}
		_ = e.cfg.Store.RecordTaskRun(ctx, run)

		ws, err := e.cfg.Cloner.PrepareWorkspace(ctx, tsk.ID, runID, tsk.RepoURL, tsk.BaseCommit)
		if err != nil {
			_ = e.cfg.Store.FinishTaskRun(ctx, runID, task.RunInfraError, task.FailureInfra, "workspace_prep_failed")
			return TaskReport{}, fmt.Errorf("prepare workspace: %w", err)
		}

		implRes, err := impl.Implement(ctx, ImplementInput{
			TaskID:     tsk.ID,
			NodeID:     implNode.ID,
			Attempt:    attemptNum,
			LadderRung: currentRung,
			Spec:       spec,
			Workspace:  ws,
			Evidence:   lastEvidence,
		})
		_ = e.cfg.Cloner.DestroyWorkspace(ctx, ws)

		totalTokens += implRes.TokensBillable

		if err != nil || implRes.Status != task.RunSucceeded {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			} else {
				errStr = implRes.Error
			}
			cls := ClassifyFailure(VerifyReport{}, errStr)
			_ = e.cfg.Store.FinishTaskRun(ctx, runID, task.RunFailed, cls, "")
			decision := DecideLadderTransition(currentRung, cls, false, currentFamily, impl.Route())
			if decision.BlockedHuman || decision.NextRung > e.cfg.MaxRung {
				_ = tsk.BlockHuman(time.Now().UTC())
				_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusBlockedHuman, implNode.ID)
				_ = e.cfg.Store.UpdateNodeStatus(ctx, implNode.ID, task.NodeStatusFailed)
				return TaskReport{
					TaskID:      tsk.ID,
					Status:      task.StatusBlockedHuman,
					RepoURL:     tsk.RepoURL,
					BaseCommit:  tsk.BaseCommit,
					RequestText: tsk.RequestText,
					Attempts:    attemptNum,
					LadderRung:  currentRung,
					TokensTotal: totalTokens,
					WallTotal:   time.Since(startTime),
					SpecSummary: spec.Summary,
					CreatedAt:   now,
				}, nil
			}
			currentRung = decision.NextRung
			attemptNum++
			continue
		}

		// Verification
		_ = e.cfg.Store.UpdateNodeStatus(ctx, verifyNode.ID, task.NodeStatusRunning)
		vReport, err := e.cfg.Verifier.Verify(ctx, VerifyInput{
			TaskID:      tsk.ID,
			RepoURL:     tsk.RepoURL,
			BaseCommit:  tsk.BaseCommit,
			Patch:       implRes.Patch,
			Visible:     params.Visible,
			Hidden:      params.Hidden,
			ProjectYAML: params.ProjectYAML,
		})
		if err != nil {
			_ = e.cfg.Store.FinishTaskRun(ctx, runID, task.RunInfraError, task.FailureInfra, err.Error())
			return TaskReport{}, fmt.Errorf("verifier infrastructure error: %w", err)
		}

		if vReport.Passed {
			_ = e.cfg.Store.FinishTaskRun(ctx, runID, task.RunSucceeded, "", "")
			_ = e.cfg.Store.UpdateNodeStatus(ctx, implNode.ID, task.NodeStatusSucceeded)
			_ = e.cfg.Store.UpdateNodeStatus(ctx, verifyNode.ID, task.NodeStatusSucceeded)
			lastPatch = implRes.Patch
			lastDiffHash = implRes.DiffHash
			verified = true
			break
		}

		// Verification failed
		cls := ClassifyFailure(vReport, "")
		sig := ComputeFailureSignature(vReport.FailingTests, vReport.ErrorSummary)
		guardTriggered, _ := guard.Check(sig, implRes.DiffHash, len(vReport.FailingTests))
		guard.Record(sig, implRes.DiffHash, len(vReport.FailingTests))
		_ = e.cfg.Store.FinishTaskRun(ctx, runID, task.RunFailed, cls, sig)

		decision := DecideLadderTransition(currentRung, cls, guardTriggered, currentFamily, impl.Route())
		if decision.BlockedHuman || decision.NextRung > e.cfg.MaxRung {
			_ = tsk.BlockHuman(time.Now().UTC())
			_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusBlockedHuman, verifyNode.ID)
			_ = e.cfg.Store.UpdateNodeStatus(ctx, verifyNode.ID, task.NodeStatusFailed)
			return TaskReport{
				TaskID:      tsk.ID,
				Status:      task.StatusBlockedHuman,
				RepoURL:     tsk.RepoURL,
				BaseCommit:  tsk.BaseCommit,
				RequestText: tsk.RequestText,
				Attempts:    attemptNum,
				LadderRung:  currentRung,
				TokensTotal: totalTokens,
				WallTotal:   time.Since(startTime),
				SpecSummary: spec.Summary,
				CreatedAt:   now,
			}, nil
		}

		currentRung = decision.NextRung
		if decision.SwitchFamily {
			currentFamily = decision.TargetFamily
			lastEvidence = nil
		} else {
			lastEvidence = &FailureEvidence{
				FailingTests: vReport.FailingTests,
				ErrorSummary: vReport.ErrorSummary,
				FullOutput:   vReport.FullOutput,
			}
		}
		attemptNum++
	}

	if !verified {
		_ = tsk.BlockHuman(time.Now().UTC())
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusBlockedHuman, implNode.ID)
		return TaskReport{TaskID: tsk.ID, Status: task.StatusBlockedHuman}, nil
	}

	// 3. REVIEW NODE
	reviewNode := nodes[3]
	_ = e.cfg.Store.UpdateNodeStatus(ctx, reviewNode.ID, task.NodeStatusRunning)
	reviewerFamily := otherFamily(currentFamily)
	reviewer, ok := e.cfg.Reviewers[reviewerFamily]
	if !ok {
		return TaskReport{}, fmt.Errorf("no reviewer configured for family %s", reviewerFamily)
	}
	if err := ValidateReviewerFamily(currentFamily, reviewerFamily); err != nil {
		return TaskReport{}, err
	}

	revRes, err := reviewer.Review(ctx, ReviewInput{
		TaskID: tsk.ID,
		Spec:   spec,
		Patch:  lastPatch,
	})
	if err != nil {
		return TaskReport{}, fmt.Errorf("review execution failed: %w", err)
	}
	gatedReview := GateReviewFindings(revRes)
	if !gatedReview.Approved {
		_ = e.cfg.Store.UpdateNodeStatus(ctx, reviewNode.ID, task.NodeStatusFailed)
		_ = tsk.BlockHuman(time.Now().UTC())
		_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusBlockedHuman, reviewNode.ID)
		return TaskReport{
			TaskID:       tsk.ID,
			Status:       task.StatusBlockedHuman,
			RepoURL:      tsk.RepoURL,
			BaseCommit:   tsk.BaseCommit,
			RequestText:  tsk.RequestText,
			Attempts:     attemptNum,
			LadderRung:   currentRung,
			TokensTotal:  totalTokens,
			WallTotal:    time.Since(startTime),
			SpecSummary:  spec.Summary,
			ReviewStatus: "changes_requested",
			Findings:     gatedReview.Findings,
			CreatedAt:    now,
		}, nil
	}

	_ = e.cfg.Store.UpdateNodeStatus(ctx, reviewNode.ID, task.NodeStatusSucceeded)
	_ = tsk.Complete(time.Now().UTC())
	_ = e.cfg.Store.UpdateTaskStatus(ctx, tsk.ID, task.StatusSucceeded, reviewNode.ID)

	return TaskReport{
		TaskID:       tsk.ID,
		Status:       task.StatusSucceeded,
		RepoURL:      tsk.RepoURL,
		BaseCommit:   tsk.BaseCommit,
		RequestText:  tsk.RequestText,
		Attempts:     attemptNum,
		LadderRung:   currentRung,
		TokensTotal:  totalTokens,
		WallTotal:    time.Since(startTime),
		SpecSummary:  spec.Summary,
		ReviewStatus: "approved",
		Patch:        lastPatch,
		DiffHash:     lastDiffHash,
		Findings:     gatedReview.Findings,
		CreatedAt:    now,
	}, nil
}
