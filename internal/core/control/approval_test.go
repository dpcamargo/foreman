package control_test

import (
	"crypto/sha256"
	"encoding/hex"
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
	if err := control.ValidateApproval(a, "noncevalue", "cafebabe"); err == nil {
		t.Error("expected error for a stale (changed) subject, got nil")
	}
}

// A merge approval's subject is the delivered diff's hash (deliveries.diff_sha256), not the
// task id — so any change to the delivered diff since the approval was requested invalidates
// it the same way a since-changed spec invalidates a spec approval (SC-002, exercised here
// against the diff rather than the spec).
func TestValidateApprovalMergeSubjectIsDiffHashNotTaskID(t *testing.T) {
	originalDiff := "diff --git a/x.go b/x.go\n+hello\n"
	changedDiff := "diff --git a/x.go b/x.go\n+hello\n+world\n"

	originalDiffHash := sha256Hex(originalDiff)
	changedDiffHash := sha256Hex(changedDiff)

	// Sanity check the premise of this test: the subject really is a hash of the diff's
	// contents, not of the task id (tsk_1) or any other stable identifier.
	if originalDiffHash == sha256Hex("tsk_1") {
		t.Fatal("test premise broken: diff hash coincidentally equals task id hash")
	}
	if originalDiffHash == changedDiffHash {
		t.Fatal("test premise broken: two different diffs hashed to the same value")
	}

	a := newApproval()
	a.Kind = "merge"
	a.SubjectSHA256 = originalDiffHash

	// Unchanged diff: the approval validates fine.
	if err := control.ValidateApproval(a, "noncevalue", originalDiffHash); err != nil {
		t.Fatalf("expected a valid merge approval against an unchanged diff, got: %v", err)
	}

	// The diff changed after this merge approval was requested (e.g. a re-verify/re-apply
	// produced a new patch) — the approval must be invalidated, exactly like a
	// since-changed spec invalidates a spec approval.
	if err := control.ValidateApproval(a, "noncevalue", changedDiffHash); err == nil {
		t.Error("expected a since-changed diff to invalidate its merge approval, got nil error")
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestValidateApprovalRejectsEmptyNonce(t *testing.T) {
	a := newApproval()
	a.Nonce = ""
	if err := control.ValidateApproval(a, "", "deadbeef"); err == nil {
		t.Error("expected error when nonce is empty, got nil")
	}
}
