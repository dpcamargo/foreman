package engine

import (
	"testing"
)

func TestCrossFamilyEnforcement(t *testing.T) {
	// Same family must be rejected
	err := ValidateReviewerFamily("openai", "openai")
	if err == nil {
		t.Fatal("expected error for same implementer and reviewer family, got nil")
	}

	// Cross family must pass
	if err := ValidateReviewerFamily("openai", "anthropic"); err != nil {
		t.Fatalf("unexpected error for cross-family review: %v", err)
	}
	if err := ValidateReviewerFamily("anthropic", "openai"); err != nil {
		t.Fatalf("unexpected error for cross-family review: %v", err)
	}
}

func TestEvidenceGatingDowngrade(t *testing.T) {
	in := ReviewResult{
		Approved: false,
		Summary:  "Found issues",
		Findings: []ReviewFinding{
			{
				Severity: "critical",
				File:     "src/add.go",
				Line:     10,
				Message:  "Memory leak potential",
				Evidence: "", // no evidence!
				Repro:    "", // no repro!
			},
			{
				Severity: "major",
				File:     "src/sub.go",
				Line:     15,
				Message:  "Off by one",
				Evidence: "Sub(0, 0) panicked with index out of bounds",
				Repro:    "call Sub(0, 0)",
			},
		},
	}

	gated := GateReviewFindings(in)
	// First finding should have been downgraded to advisory
	if gated.Findings[0].Severity != "advisory" {
		t.Errorf("expected first finding to be downgraded to advisory, got %s", gated.Findings[0].Severity)
	}
	// Second finding had evidence and repro, so it remains major
	if gated.Findings[1].Severity != "major" {
		t.Errorf("expected second finding to stay major, got %s", gated.Findings[1].Severity)
	}
	// Has valid blocking finding, so Approved remains false
	if gated.Approved {
		t.Errorf("expected Approved to be false when valid major finding exists")
	}

	// If all major/critical findings lacked evidence, review should not block
	in2 := ReviewResult{
		Approved: false,
		Summary:  "Unsubstantiated critique",
		Findings: []ReviewFinding{
			{
				Severity: "major",
				File:     "src/add.go",
				Line:     5,
				Message:  "I feel this is inefficient",
				Evidence: "",
				Repro:    "",
			},
		},
	}
	gated2 := GateReviewFindings(in2)
	if gated2.Findings[0].Severity != "advisory" {
		t.Errorf("expected downgrade to advisory, got %s", gated2.Findings[0].Severity)
	}
	if !gated2.Approved {
		t.Errorf("expected Approved to be true when no substantiated blocking findings remain")
	}
}

// Reviewers may label findings "blocking"; that must block the review like
// critical/major do, not silently auto-approve it.
func TestBlockingSeverityBlocksReview(t *testing.T) {
	in := ReviewResult{
		Approved: true,
		Summary:  "approved with notes",
		Findings: []ReviewFinding{
			{
				Severity: "blocking",
				Message:  "existing test deleted, not relocated",
				Evidence: "patch hunks show zero new files",
				Repro:    "git diff --stat",
			},
		},
	}
	gated := GateReviewFindings(in)
	if gated.Approved {
		t.Errorf("a blocking finding with evidence must not be auto-approved")
	}
	if gated.Findings[0].Severity != "blocking" {
		t.Errorf("blocking finding must retain severity, got %s", gated.Findings[0].Severity)
	}
}
