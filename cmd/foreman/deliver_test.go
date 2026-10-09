package main

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

type fakePusher struct {
	pushed     []string // task ids pushed via PushBranch
	openedPR   []string // task ids
	merged     []int    // pull numbers
	prURL      string
	pushErr    error
	openPRErr  error
	mergePRErr error
}

func (f *fakePusher) PushBranch(ctx context.Context, taskID string, patch []byte, repoURL, baseCommit string) error {
	f.pushed = append(f.pushed, taskID)
	return f.pushErr
}

func (f *fakePusher) OpenDraftPR(ctx context.Context, taskID string, patch []byte) (string, error) {
	f.openedPR = append(f.openedPR, taskID)
	if f.openPRErr != nil {
		return "", f.openPRErr
	}
	return f.prURL, nil
}

func (f *fakePusher) MergePullRequest(ctx context.Context, taskID string, pullNumber int) error {
	f.merged = append(f.merged, pullNumber)
	return f.mergePRErr
}

func succeededReport(taskID string, patch []byte) engine.TaskReport {
	return engine.TaskReport{TaskID: taskID, Status: task.StatusSucceeded, Patch: patch, DiffHash: "h"}
}

// resolveNextApproval polls store for a pending approval and resolves it via the fake
// dispatcher's channel, unblocking requestDeliveryApproval's WaitForApproval. Runs until it
// resolves one approval or times out — call it in a goroutine alongside the call under test.
func resolveNextApproval(t *testing.T, store *fakeTaskStore, disp *fakeDispatcher, approved bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for id, a := range store.approvals {
			if a.Status == "pending" {
				status, resp := "approved", "approve"
				if !approved {
					status, resp = "rejected", "reject"
				}
				_ = store.ResolveApproval(context.Background(), id, status, resp, "")
				disp.ResolveApproval(id, approved)
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("no pending approval appeared to resolve")
}

// T041: a successful run creates a Delivery row; rejecting the deliver approval marks the
// delivery failed and never calls the integrator.
func TestRequestDeliveryApprovalRejected(t *testing.T) {
	store := newFakeTaskStore()
	disp := &fakeDispatcher{}
	sender := &fakeSender{}
	ig := &fakePusher{prURL: "https://github.com/o/r/pull/1"}

	go resolveNextApproval(t, store, disp, false)

	patch := []byte("diff --git a/x b/x\n+hello\n")
	if err := requestDeliveryApproval(context.Background(), store, disp, sender, ig,
		succeededReport("tsk_1", patch)); err != nil {
		t.Fatalf("requestDeliveryApproval: %v", err)
	}

	d, err := store.Delivery(context.Background(), "tsk_1")
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if d.DiffSHA256 != diffSHA256(patch) {
		t.Errorf("DiffSHA256 = %q, want %q", d.DiffSHA256, diffSHA256(patch))
	}
	if d.Status != "failed" {
		t.Errorf("delivery status = %q, want failed for a rejected delivery", d.Status)
	}
	if len(ig.openedPR) != 0 {
		t.Errorf("integrator should not be called for a rejected delivery, got %+v", ig.openedPR)
	}
}

// T041/T042: approving delivery opens a PR and creates a merge approval bound to the diff
// hash (not the task id) — a since-changed diff invalidates it (SC-002).
func TestRequestDeliveryApprovalApprovedCreatesMergeApproval(t *testing.T) {
	store := newFakeTaskStore()
	disp := &fakeDispatcher{}
	sender := &fakeSender{}
	ig := &fakePusher{prURL: "https://github.com/o/r/pull/7"}

	go resolveNextApproval(t, store, disp, true)

	patch := []byte("diff --git a/x b/x\n+hello\n")
	if err := requestDeliveryApproval(context.Background(), store, disp, sender, ig,
		succeededReport("tsk_1", patch)); err != nil {
		t.Fatalf("requestDeliveryApproval: %v", err)
	}

	if len(ig.openedPR) != 1 || ig.openedPR[0] != "tsk_1" {
		t.Errorf("expected OpenDraftPR called once for tsk_1, got %+v", ig.openedPR)
	}

	d, _ := store.Delivery(context.Background(), "tsk_1")
	if d.Status != "pr_open" || d.PRURL != "https://github.com/o/r/pull/7" {
		t.Errorf("delivery = %+v, want status pr_open with the PR URL", d)
	}

	// A merge approval must exist, bound to the diff hash — not the task id.
	var mergeApproval *control.Approval
	for _, a := range store.approvals {
		if a.Kind == "merge" {
			aa := a
			mergeApproval = &aa
		}
	}
	if mergeApproval == nil {
		t.Fatal("expected a merge approval to be created after delivery")
	}
	if mergeApproval.SubjectSHA256 != diffSHA256(patch) {
		t.Errorf("merge approval SubjectSHA256 = %q, want diff hash %q",
			mergeApproval.SubjectSHA256, diffSHA256(patch))
	}
	if mergeApproval.SubjectSHA256 == "tsk_1" {
		t.Error("merge approval must not bind to the task id (SC-002)")
	}
}

// T042/FR-016: merging is never a single tap — the first [Merge] arms a confirmation, only
// a second tap (or /confirm) actually calls the GitHub merge endpoint.
func TestHandleMergeCallbackRequiresTwoTaps(t *testing.T) {
	delete(pendingMerge, "apv_merge") // reset global state between tests
	store := newFakeTaskStore()
	patch := []byte("diff --git a/x b/x\n+hello\n")
	store.CreateDelivery(context.Background(), "tsk_1", "agent/tsk_1", diffSHA256(patch), patch)
	store.UpdateDeliveryStatus(context.Background(), "tsk_1", "pr_open", "https://github.com/o/r/pull/7")
	store.CreateApproval(context.Background(), control.Approval{
		ID: "apv_merge", TaskID: "tsk_1", Kind: "merge",
		SubjectSHA256: diffSHA256(patch), Nonce: "noncevalue", Status: "pending",
	})
	disp := &fakeDispatcher{}
	sender := &fakeSender{}
	ig := &fakePusher{prURL: "https://github.com/o/r/pull/7"}
	cb := telegram.CallbackData{ApprovalID: "apv_merge", Nonce: "noncevalue", Decision: "merge"}

	handleMergeCallback(context.Background(), store, disp, ig, cb, sender)
	if len(ig.merged) != 0 {
		t.Errorf("first tap merged immediately — FR-016 requires a second confirmation, got %+v", ig.merged)
	}
	if !pendingMerge["apv_merge"] {
		t.Error("first tap should arm pendingMerge")
	}

	handleMergeCallback(context.Background(), store, disp, ig, cb, sender)
	if len(ig.merged) != 1 || ig.merged[0] != 7 {
		t.Errorf("second tap should merge PR 7, got %+v", ig.merged)
	}
	a, _ := store.Approval(context.Background(), "apv_merge")
	if a.Status != "approved" {
		t.Errorf("merge approval status = %q, want approved", a.Status)
	}
}

// T042/SC-002: a since-changed diff invalidates a previously-issued merge approval.
func TestHandleMergeCallbackRejectsChangedDiff(t *testing.T) {
	delete(pendingMerge, "apv_merge2")
	store := newFakeTaskStore()
	original := []byte("diff --git a/x b/x\n+hello\n")
	changed := []byte("diff --git a/x b/x\n+hello\n+world\n")
	store.CreateDelivery(context.Background(), "tsk_1", "agent/tsk_1", diffSHA256(original), original)
	store.CreateApproval(context.Background(), control.Approval{
		ID: "apv_merge2", TaskID: "tsk_1", Kind: "merge",
		SubjectSHA256: diffSHA256(original), Nonce: "noncevalue", Status: "pending",
	})
	// Someone re-delivered a changed diff since the approval was requested.
	store.CreateDelivery(context.Background(), "tsk_1", "agent/tsk_1", diffSHA256(changed), changed)

	disp := &fakeDispatcher{}
	ig := &fakePusher{}
	cb := telegram.CallbackData{ApprovalID: "apv_merge2", Nonce: "noncevalue", Decision: "merge"}
	handleMergeCallback(context.Background(), store, disp, ig, cb, &fakeSender{})
	if len(ig.merged) != 0 {
		t.Errorf("a since-changed diff must not merge, got %+v", ig.merged)
	}
	if pendingMerge["apv_merge2"] {
		t.Error("a rejected merge must not arm pendingMerge")
	}
}
