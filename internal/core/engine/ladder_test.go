package engine

import (
	"testing"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestClassifyFailure(t *testing.T) {
	tests := []struct {
		name    string
		report  VerifyReport
		errStr  string
		wantCls task.FailureClass
	}{
		{
			name: "policy violation is policy failure",
			report: VerifyReport{
				PolicyPassed:     false,
				PolicyViolations: []string{"test file deleted: src/add_test.go"},
			},
			wantCls: task.FailurePolicy,
		},
		{
			name: "test failure is attempt failure",
			report: VerifyReport{
				PolicyPassed: true,
				BuildPassed:  true,
				HiddenPassed: false,
				FailingTests: []string{"TestSub"},
			},
			wantCls: task.FailureAttempt,
		},
		{
			name:    "docker daemon error is infra failure",
			errStr:  "docker: Cannot connect to the Docker daemon",
			wantCls: task.FailureInfra,
		},
		{
			name:    "rate limit 429 is provider failure",
			errStr:  "rate limit exceeded (429) backoff",
			wantCls: task.FailureProvider,
		},
		{
			name:    "context cancelled by operator pause/cancel is infra failure, not attempt",
			errStr:  "context canceled",
			wantCls: task.FailureInfra,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFailure(tc.report, tc.errStr)
			if got != tc.wantCls {
				t.Errorf("got failure class %s, want %s", got, tc.wantCls)
			}
		})
	}
}

func TestLoopGuards(t *testing.T) {
	guard := NewLoopGuard()

	// First attempt: record signature and diff hash
	sig1 := ComputeFailureSignature([]string{"TestSubNegative"}, "assertion failed")
	diff1 := "diff_hash_1"
	if triggered, reason := guard.Check(sig1, diff1, 2); triggered {
		t.Fatalf("unexpected loop guard trigger on first attempt: %s", reason)
	}
	guard.Record(sig1, diff1, 2)

	// Second attempt with exact same signature
	sig2 := ComputeFailureSignature([]string{"TestSubNegative"}, "assertion failed")
	diff2 := "diff_hash_2"
	if triggered, reason := guard.Check(sig2, diff2, 2); !triggered || reason != "repeated_failure_signature" {
		t.Fatalf("expected repeated_failure_signature, got triggered=%v reason=%s", triggered, reason)
	}

	// Reset and test oscillating diff hash
	guard2 := NewLoopGuard()
	guard2.Record("sig_a", "patch_1", 1)
	guard2.Record("sig_b", "patch_2", 2)
	if triggered, reason := guard2.Check("sig_c", "patch_1", 3); !triggered || reason != "diff_oscillation" {
		t.Fatalf("expected diff_oscillation, got triggered=%v reason=%s", triggered, reason)
	}

	// Test pass count flatline
	guard3 := NewLoopGuard()
	guard3.Record("sig_x", "patch_x", 2)
	if triggered, reason := guard3.Check("sig_y", "patch_y", 2); !triggered || reason != "pass_count_flatline" {
		t.Fatalf("expected pass_count_flatline, got triggered=%v reason=%s", triggered, reason)
	}
}

func TestLadderTransitions(t *testing.T) {
	// Policy failure immediately escalates to Rung 3 (blocked_human)
	decision := DecideLadderTransition(1, task.FailurePolicy, false, "openai", "codex/default")
	if decision.NextRung != 3 || !decision.BlockedHuman {
		t.Errorf("expected policy failure to jump to Rung 3 blocked_human, got %+v", decision)
	}

	// Infra failure retries same rung cleanly
	decision = DecideLadderTransition(1, task.FailureInfra, false, "openai", "codex/default")
	if decision.NextRung != 1 || decision.BlockedHuman || decision.SwitchFamily {
		t.Errorf("expected infra to retry same rung, got %+v", decision)
	}

	// Attempt failure on Rung 1 without loop guard retries Rung 1 with evidence
	decision = DecideLadderTransition(1, task.FailureAttempt, false, "openai", "codex/default")
	if decision.NextRung != 1 || decision.BlockedHuman || decision.SwitchFamily {
		t.Errorf("expected attempt failure on rung 1 to retry rung 1, got %+v", decision)
	}

	// Attempt failure on Rung 1 WITH loop guard escalates to Rung 2 (different family)
	decision = DecideLadderTransition(1, task.FailureAttempt, true, "openai", "codex/default")
	if decision.NextRung != 2 || !decision.SwitchFamily || decision.TargetFamily != "anthropic" {
		t.Errorf("expected loop guard to escalate to Rung 2 anthropic, got %+v", decision)
	}

	// Failure on Rung 2 escalates to Rung 3 (blocked_human)
	decision = DecideLadderTransition(2, task.FailureAttempt, false, "anthropic", "hermes/claude")
	if decision.NextRung != 3 || !decision.BlockedHuman {
		t.Errorf("expected Rung 2 failure to escalate to Rung 3 blocked_human, got %+v", decision)
	}
}
