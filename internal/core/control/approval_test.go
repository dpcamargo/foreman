package control_test

import (
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
)

func newApproval() control.Approval {
	return control.Approval{
		ID:            "apv_1",
		TaskID:        "tsk_1",
		Kind:          "spec",
		Question:      "approve this spec?",
		Options:       []string{"approve", "reject"},
		SubjectSHA256: "deadbeef",
		Nonce:         "noncevalue",
		Status:        "pending",
		CreatedAt:     time.Now().UTC(),
	}
}

func TestValidateApprovalSuccess(t *testing.T) {
	a := newApproval()
	if err := control.ValidateApproval(a, "noncevalue", "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateApprovalRejectsNonPending(t *testing.T) {
	for _, status := range []string{"approved", "rejected", "expired"} {
		a := newApproval()
		a.Status = status
		if err := control.ValidateApproval(a, "noncevalue", "deadbeef"); err == nil {
			t.Errorf("status %q: expected error, got nil", status)
		}
	}
}

func TestValidateApprovalRejectsNonceMismatch(t *testing.T) {
	a := newApproval()
	if err := control.ValidateApproval(a, "wrong-nonce", "deadbeef"); err == nil {
		t.Error("expected error for nonce mismatch, got nil")
	}
}

func TestValidateApprovalRejectsStaleSubject(t *testing.T) {
	a := newApproval()
	// The artifact changed since the approval was requested (e.g. a replan produced a new
	// spec) — the current hash no longer matches what was approved (SC-002).
	if err := control.ValidateApproval(a, "noncevalue", "changed-artifact-hash"); err == nil {
		t.Error("expected error for a stale/changed subject, got nil")
	}
}

func TestValidateApprovalRejectsEmptyNonce(t *testing.T) {
	a := newApproval()
	a.Nonce = ""
	if err := control.ValidateApproval(a, "", "deadbeef"); err == nil {
		t.Error("expected error when nonce is empty, got nil")
	}
}
