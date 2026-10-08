package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
)

// CreateApproval generates an id if needed and persists the approval row
// (migrations/0004_daemon_control.sql).
func (s *Store) CreateApproval(ctx context.Context, a control.Approval) (control.Approval, error) {
	at := s.clock()
	if a.ID == "" {
		id, err := s.newID("apv", at)
		if err != nil {
			return control.Approval{}, err
		}
		a.ID = id
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	a.CreatedAt = at

	optsJSON, err := json.Marshal(a.Options)
	if err != nil {
		return control.Approval{}, fmt.Errorf("marshal approval options: %w", err)
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO approvals (id, task_id, node_id, kind, question, options_json, subject_sha256, nonce, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			a.ID, a.TaskID, nullString(a.NodeID), a.Kind, a.Question, string(optsJSON),
			a.SubjectSHA256, a.Nonce, a.Status, a.CreatedAt.Format(tsLayout),
		)
		if err != nil {
			return fmt.Errorf("insert approval: %w", err)
		}
		return nil
	})
	if err != nil {
		return control.Approval{}, err
	}
	return a, nil
}

// Approval loads one approval by id.
func (s *Store) Approval(ctx context.Context, id string) (control.Approval, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, COALESCE(node_id, ''), kind, question, options_json, subject_sha256,
		       nonce, status, COALESCE(response, ''), COALESCE(reason, ''), created_at, responded_at
		FROM approvals WHERE id = ?;`, id)

	var a control.Approval
	var optsJSON, createdAt string
	var respondedAt sql.NullString
	if err := row.Scan(&a.ID, &a.TaskID, &a.NodeID, &a.Kind, &a.Question, &optsJSON,
		&a.SubjectSHA256, &a.Nonce, &a.Status, &a.Response, &a.Reason, &createdAt, &respondedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return control.Approval{}, fmt.Errorf("approval %s: %w", id, ErrNotFoundControl)
		}
		return control.Approval{}, fmt.Errorf("query approval: %w", err)
	}
	if err := json.Unmarshal([]byte(optsJSON), &a.Options); err != nil {
		return control.Approval{}, fmt.Errorf("unmarshal approval options: %w", err)
	}
	createdTime, err := time.Parse(tsLayout, createdAt)
	if err != nil {
		return control.Approval{}, fmt.Errorf("parse approval created_at: %w", err)
	}
	a.CreatedAt = createdTime
	if respondedAt.Valid {
		t, err := time.Parse(tsLayout, respondedAt.String)
		if err != nil {
			return control.Approval{}, fmt.Errorf("parse approval responded_at: %w", err)
		}
		a.RespondedAt = &t
	}
	return a, nil
}

// ResolveApproval records a human's decision (spec FR-005, FR-011).
func (s *Store) ResolveApproval(ctx context.Context, id, status, response, reason string) error {
	at := s.timestamp()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE approvals SET status = ?, response = ?, reason = ?, responded_at = ? WHERE id = ?;`,
			status, nullString(response), nullString(reason), at, id)
		if err != nil {
			return fmt.Errorf("resolve approval: %w", err)
		}
		return nil
	})
}

// PendingApprovalForTask returns taskID's pending approval (the one request/text commands
// like "/reject <task_id> <reason>" resolve by task id, trusting the already-passed
// allowlist+private-chat check rather than requiring a nonce the user never sees).
func (s *Store) PendingApprovalForTask(ctx context.Context, taskID string) (control.Approval, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id FROM approvals WHERE task_id = ? AND status = 'pending' ORDER BY created_at DESC LIMIT 1;`, taskID)
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return control.Approval{}, fmt.Errorf("no pending approval for task %s: %w", taskID, ErrNotFoundControl)
		}
		return control.Approval{}, fmt.Errorf("query pending approval: %w", err)
	}
	return s.Approval(ctx, id)
}

// AddAllowedUser adds a Telegram user id to the allowlist (spec FR-003).
func (s *Store) AddAllowedUser(ctx context.Context, userID int64, label string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO allowlist (telegram_user_id, label, added_at) VALUES (?, ?, ?)
		ON CONFLICT (telegram_user_id) DO UPDATE SET label = excluded.label;`,
		userID, nullString(label), s.timestamp())
	if err != nil {
		return fmt.Errorf("add allowed user: %w", err)
	}
	return nil
}

// IsAllowedUser implements control.AllowlistStore.
func (s *Store) IsAllowedUser(ctx context.Context, userID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM allowlist WHERE telegram_user_id = ?;`, userID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("query allowlist: %w", err)
	}
	return count > 0, nil
}

// AllowedUserIDs returns every allowlisted Telegram user id, used to broadcast an approval
// request to whichever operator(s) the daemon is configured for.
func (s *Store) AllowedUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT telegram_user_id FROM allowlist;`)
	if err != nil {
		return nil, fmt.Errorf("query allowlist ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan allowlist id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate allowlist ids: %w", err)
	}
	return ids, nil
}

// OutboxCursor returns subscriber's last-seen event id, 0 for a subscriber never seen before.
func (s *Store) OutboxCursor(ctx context.Context, subscriber string) (int64, error) {
	var cursor int64
	err := s.db.QueryRowContext(ctx, `SELECT last_event_id FROM outbox_cursors WHERE subscriber = ?;`, subscriber).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query outbox cursor: %w", err)
	}
	return cursor, nil
}

// SetOutboxCursor durably advances subscriber's cursor (spec FR-007).
func (s *Store) SetOutboxCursor(ctx context.Context, subscriber string, cursor int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO outbox_cursors (subscriber, last_event_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (subscriber) DO UPDATE SET last_event_id = excluded.last_event_id, updated_at = excluded.updated_at;`,
		subscriber, cursor, s.timestamp())
	if err != nil {
		return fmt.Errorf("set outbox cursor: %w", err)
	}
	return nil
}

// Delivery is a task's row in the deliveries table (data-model.md).
type Delivery struct {
	TaskID     string
	Branch     string
	PRURL      string
	DiffSHA256 string
	Status     string
}

// CreateDelivery starts tracking a task's path to a merged PR (spec FR-014).
func (s *Store) CreateDelivery(ctx context.Context, taskID, branch, diffSHA256 string) error {
	at := s.timestamp()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deliveries (task_id, branch, diff_sha256, status, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, ?);`,
		taskID, branch, diffSHA256, at, at)
	if err != nil {
		return fmt.Errorf("create delivery: %w", err)
	}
	return nil
}

// Delivery loads one task's delivery row.
func (s *Store) Delivery(ctx context.Context, taskID string) (Delivery, error) {
	var d Delivery
	var prURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id, branch, diff_sha256, status, pr_url FROM deliveries WHERE task_id = ?;`, taskID).
		Scan(&d.TaskID, &d.Branch, &d.DiffSHA256, &d.Status, &prURL)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, fmt.Errorf("delivery for task %s: %w", taskID, ErrNotFoundControl)
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("query delivery: %w", err)
	}
	if prURL.Valid {
		d.PRURL = prURL.String
	}
	return d, nil
}

// UpdateDeliveryStatus advances a delivery's status and, once known, its PR URL
// (spec FR-015, FR-016).
func (s *Store) UpdateDeliveryStatus(ctx context.Context, taskID, status, prURL string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = ?, pr_url = ?, updated_at = ? WHERE task_id = ?;`,
		status, nullString(prURL), s.timestamp(), taskID)
	if err != nil {
		return fmt.Errorf("update delivery status: %w", err)
	}
	return nil
}

// ErrNotFoundControl mirrors task.ErrNotFound for control-plane rows that don't have their
// own core-level not-found sentinel (control-package rows are adapter-local lookups, not
// domain entities with their own ErrNotFound).
var ErrNotFoundControl = errors.New("not found")
