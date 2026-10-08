package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// TestMigration0004SchemaExists confirms migration 0004 landed: the tasks status CHECK
// constraint accepts the two new daemon-control values, and the four new tables exist
// with the columns data-model.md specifies.
func TestMigration0004SchemaExists(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC()

	tsk := task.Task{
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repos/addrepo.git",
		BaseCommit:  "0123456789abcdef0123456789abcdef01234567",
		RequestText: "Implement Sub",
		Status:      task.StatusPending,
	}
	created, err := s.CreateTask(ctx, tsk, task.NewLinearNodes("", now))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	for _, status := range []string{"paused", "cancelled"} {
		if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status = ? WHERE id = ?;`, status, created.ID); err != nil {
			t.Errorf("set status %q: %v", status, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status = 'not_a_real_status' WHERE id = ?;`, created.ID); err == nil {
		t.Error("expected CHECK constraint to reject an unknown status")
	}

	nonceID := "apv_test"
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO approvals (id, task_id, kind, question, options_json, subject_sha256, nonce, status, created_at)
		VALUES (?, ?, 'spec', 'approve?', '["approve","reject"]', 'deadbeef', 'noncevalue', 'pending', ?);`,
		nonceID, created.ID, now.Format(tsLayout)); err != nil {
		t.Fatalf("insert approvals row: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO approvals (id, task_id, kind, question, options_json, subject_sha256, nonce, status, created_at)
		VALUES ('apv_dup', ?, 'spec', 'approve?', '[]', 'x', 'noncevalue', 'pending', ?);`,
		created.ID, now.Format(tsLayout)); err == nil {
		t.Error("expected the unique nonce index to reject a duplicate nonce")
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO allowlist (telegram_user_id, label, added_at) VALUES (123, 'operator', ?);`,
		now.Format(tsLayout)); err != nil {
		t.Fatalf("insert allowlist row: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO outbox_cursors (subscriber, last_event_id, updated_at) VALUES ('telegram', 0, ?);`,
		now.Format(tsLayout)); err != nil {
		t.Fatalf("insert outbox_cursors row: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deliveries (task_id, branch, diff_sha256, status, created_at, updated_at)
		VALUES (?, 'agent/tsk', 'feedface', 'pending', ?, ?);`,
		created.ID, now.Format(tsLayout), now.Format(tsLayout)); err != nil {
		t.Fatalf("insert deliveries row: %v", err)
	}
}
