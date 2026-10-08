package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/task"
)

func newTestTaskForControl(t *testing.T, ctx context.Context, s *Store) string {
	t.Helper()
	tsk := task.Task{
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repos/addrepo.git",
		BaseCommit:  "0123456789abcdef0123456789abcdef01234567",
		RequestText: "Implement Sub",
		Status:      task.StatusPending,
	}
	created, err := s.CreateTask(ctx, tsk, task.NewLinearNodes("", time.Now().UTC()))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return created.ID
}

func TestControlStoreApprovalCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	taskID := newTestTaskForControl(t, ctx, s)

	nonce, err := control.NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	a := control.Approval{
		TaskID:        taskID,
		Kind:          "spec",
		Question:      "approve this spec?",
		Options:       []string{"approve", "reject"},
		SubjectSHA256: "deadbeef",
		Nonce:         nonce,
		Status:        "pending",
	}
	created, err := s.CreateApproval(ctx, a)
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected a generated approval id")
	}

	got, err := s.Approval(ctx, created.ID)
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if got.Nonce != nonce || got.SubjectSHA256 != "deadbeef" || got.Status != "pending" {
		t.Errorf("unexpected approval round-trip: %+v", got)
	}
	if len(got.Options) != 2 {
		t.Errorf("expected 2 options, got %v", got.Options)
	}

	if err := s.ResolveApproval(ctx, created.ID, "approved", "approve", ""); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	got, err = s.Approval(ctx, created.ID)
	if err != nil {
		t.Fatalf("Approval after resolve: %v", err)
	}
	if got.Status != "approved" || got.Response != "approve" {
		t.Errorf("expected resolved approval, got %+v", got)
	}
	if got.RespondedAt == nil {
		t.Error("expected RespondedAt to be set")
	}
}

func TestControlStoreApprovalNotFound(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, err := s.Approval(ctx, "apv_missing"); err == nil {
		t.Error("expected an error for a missing approval")
	}
}

func TestPendingApprovalForTask(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	taskID := newTestTaskForControl(t, ctx, s)

	if _, err := s.PendingApprovalForTask(ctx, taskID); err == nil {
		t.Error("expected an error when there is no pending approval yet")
	}

	created, err := s.CreateApproval(ctx, control.Approval{
		TaskID: taskID, Kind: "spec", Question: "approve?", Options: []string{"approve", "reject"},
		SubjectSHA256: "deadbeef", Nonce: "noncevalue", Status: "pending",
	})
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	got, err := s.PendingApprovalForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("PendingApprovalForTask: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("expected %s, got %s", created.ID, got.ID)
	}

	if err := s.ResolveApproval(ctx, created.ID, "approved", "approve", ""); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if _, err := s.PendingApprovalForTask(ctx, taskID); err == nil {
		t.Error("expected an error once the approval is resolved (no longer pending)")
	}
}

func TestControlStoreAllowlist(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	if err := s.AddAllowedUser(ctx, 12345, "operator"); err != nil {
		t.Fatalf("AddAllowedUser: %v", err)
	}
	allowed, err := s.IsAllowedUser(ctx, 12345)
	if err != nil {
		t.Fatalf("IsAllowedUser: %v", err)
	}
	if !allowed {
		t.Error("expected 12345 to be allowed")
	}
	allowed, err = s.IsAllowedUser(ctx, 99999)
	if err != nil {
		t.Fatalf("IsAllowedUser unknown: %v", err)
	}
	if allowed {
		t.Error("expected 99999 to be rejected")
	}

	if err := s.AddAllowedUser(ctx, 67890, "second operator"); err != nil {
		t.Fatalf("AddAllowedUser second: %v", err)
	}
	ids, err := s.AllowedUserIDs(ctx)
	if err != nil {
		t.Fatalf("AllowedUserIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 allowed user ids, got %v", ids)
	}
}

func TestControlStoreOutboxCursor(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	cursor, err := s.OutboxCursor(ctx, "telegram")
	if err != nil {
		t.Fatalf("OutboxCursor (new subscriber): %v", err)
	}
	if cursor != 0 {
		t.Errorf("expected cursor 0 for a new subscriber, got %d", cursor)
	}

	if err := s.SetOutboxCursor(ctx, "telegram", 42); err != nil {
		t.Fatalf("SetOutboxCursor: %v", err)
	}
	cursor, err = s.OutboxCursor(ctx, "telegram")
	if err != nil {
		t.Fatalf("OutboxCursor after set: %v", err)
	}
	if cursor != 42 {
		t.Errorf("expected cursor 42, got %d", cursor)
	}

	if err := s.SetOutboxCursor(ctx, "telegram", 50); err != nil {
		t.Fatalf("SetOutboxCursor again: %v", err)
	}
	cursor, _ = s.OutboxCursor(ctx, "telegram")
	if cursor != 50 {
		t.Errorf("expected cursor to update to 50, got %d", cursor)
	}
}

func TestControlStoreDeliveryCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	taskID := newTestTaskForControl(t, ctx, s)

	if err := s.CreateDelivery(ctx, taskID, "agent/tsk_1", "feedface"); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	got, err := s.Delivery(ctx, taskID)
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.Branch != "agent/tsk_1" || got.DiffSHA256 != "feedface" || got.Status != "pending" {
		t.Errorf("unexpected delivery round-trip: %+v", got)
	}

	if err := s.UpdateDeliveryStatus(ctx, taskID, "pr_open", "https://example.com/pr/1"); err != nil {
		t.Fatalf("UpdateDeliveryStatus: %v", err)
	}
	got, err = s.Delivery(ctx, taskID)
	if err != nil {
		t.Fatalf("Delivery after update: %v", err)
	}
	if got.Status != "pr_open" || got.PRURL != "https://example.com/pr/1" {
		t.Errorf("expected updated delivery, got %+v", got)
	}
}
