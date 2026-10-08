package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// RunsForTask returns a task's run history, newest first. A non-empty nodeID filters to one
// node (Telegram's "/logs <id> [node]", contracts/telegram-commands.md).
func (s *Store) RunsForTask(ctx context.Context, taskID, nodeID string) ([]task.Run, error) {
	query := `SELECT id, task_id, node_id, attempt_num, ladder_rung, route, model_family,
	                 status, COALESCE(failure_class, ''), COALESCE(failure_sig, ''),
	                 COALESCE(diff_hash, ''), tokens_input, tokens_output, tokens_cached,
	                 tokens_billable, wall_s, COALESCE(pid, 0), COALESCE(pid_started_at, ''),
	                 COALESCE(error, ''), started_at, COALESCE(ended_at, '')
	          FROM runs WHERE task_id = ?`
	args := []any{taskID}
	if nodeID != "" {
		query += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY started_at DESC, id DESC;`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs for task: %w", err)
	}
	defer rows.Close()
	var runs []task.Run
	for rows.Next() {
		var r task.Run
		var status, failureClass, startedAt, endedAt string
		var wallS int64
		if err := rows.Scan(&r.ID, &r.TaskID, &r.NodeID, &r.AttemptNum, &r.LadderRung,
			&r.Route, &r.ModelFamily, &status, &failureClass, &r.FailureSig, &r.DiffHash,
			&r.TokensInput, &r.TokensOutput, &r.TokensCached, &r.TokensBillable, &wallS,
			&r.PID, &r.PIDStartedAt, &r.Error, &startedAt, &endedAt); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		r.Status = task.RunStatus(status)
		r.FailureClass = task.FailureClass(failureClass)
		r.Wall = time.Duration(wallS) * time.Second
		if t, err := time.Parse(tsLayout, startedAt); err == nil {
			r.StartedAt = t
		}
		if endedAt != "" {
			if parsed, err := time.Parse(tsLayout, endedAt); err == nil {
				r.EndedAt = &parsed
			}
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return runs, nil
}

// TaskBudget is one task's spend in a BudgetSummary.
type TaskBudget struct {
	TaskID         string
	TokensBillable int
	Runs           int
}

// BudgetSummary aggregates spend across all tasks (Telegram's "/budget",
// contracts/telegram-commands.md). Structured SQLite rows are the source of truth — this is
// a read-only aggregate, never a mutation.
type BudgetSummaryResult struct {
	TotalTokensBillable int
	ByTask              []TaskBudget
}

// BudgetSummary returns total and per-task billable-token spend across every run row.
func (s *Store) BudgetSummary(ctx context.Context) (BudgetSummaryResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, COALESCE(SUM(tokens_billable), 0), COUNT(*)
		FROM runs GROUP BY task_id ORDER BY task_id;`)
	if err != nil {
		return BudgetSummaryResult{}, fmt.Errorf("query budget summary: %w", err)
	}
	defer rows.Close()
	var out BudgetSummaryResult
	for rows.Next() {
		var b TaskBudget
		if err := rows.Scan(&b.TaskID, &b.TokensBillable, &b.Runs); err != nil {
			return BudgetSummaryResult{}, fmt.Errorf("scan budget row: %w", err)
		}
		out.ByTask = append(out.ByTask, b)
		out.TotalTokensBillable += b.TokensBillable
	}
	if err := rows.Err(); err != nil {
		return BudgetSummaryResult{}, fmt.Errorf("iterate budget rows: %w", err)
	}
	return out, nil
}

// PendingApprovalsSince returns approvals still pending whose created_at is at or before
// cutoff (T046's unanswered-approval ping: never auto-resolves them, only lists them for a
// secondary-channel nudge).
func (s *Store) PendingApprovalsSince(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM approvals WHERE status = 'pending' AND created_at <= ? ORDER BY created_at;`,
		cutoff.UTC().Format(tsLayout))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query stale approvals: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan approval id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approval ids: %w", err)
	}
	return ids, nil
}
