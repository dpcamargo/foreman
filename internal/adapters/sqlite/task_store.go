package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// CreateTask persists a new task and its initial linear nodes in one transaction,
// recording the creation event (constitution VII).
func (s *Store) CreateTask(ctx context.Context, t task.Task, nodes []task.Node) (task.Task, error) {
	at := s.clock()
	if t.ID == "" {
		id, err := s.newID("tsk", at)
		if err != nil {
			return task.Task{}, err
		}
		t.ID = id
	}
	if t.TaskType == "" {
		t.TaskType = task.TaskTypeCode
	}
	if t.Status == "" {
		t.Status = task.StatusPending
	}
	t.CreatedAt = at
	t.UpdatedAt = at

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (id, task_type, repo_url, base_commit, request_text, status, current_node_id, arm, record_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			t.ID, t.TaskType, t.RepoURL, t.BaseCommit, t.RequestText, string(t.Status),
			nullString(t.CurrentNodeID), nullString(t.Arm), nullString(t.RecordID),
			t.CreatedAt.Format(tsLayout), t.UpdatedAt.Format(tsLayout),
		)
		if err != nil {
			return fmt.Errorf("insert task: %w", err)
		}

		for i := range nodes {
			if nodes[i].ID == "" {
				nodeID, err := s.newID("nod", at)
				if err != nil {
					return err
				}
				nodes[i].ID = nodeID
			}
			nodes[i].TaskID = t.ID
			if nodes[i].Status == "" {
				nodes[i].Status = task.NodeStatusPending
			}
			nodes[i].CreatedAt = at
			nodes[i].UpdatedAt = at

			_, err = tx.ExecContext(ctx, `
				INSERT INTO nodes (id, task_id, name, step_order, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?);`,
				nodes[i].ID, nodes[i].TaskID, string(nodes[i].Name), nodes[i].StepOrder, string(nodes[i].Status),
				nodes[i].CreatedAt.Format(tsLayout), nodes[i].UpdatedAt.Format(tsLayout),
			)
			if err != nil {
				return fmt.Errorf("insert node %s: %w", nodes[i].Name, err)
			}
		}

		return s.insertEvent(ctx, tx, "task", t.ID, nil, string(t.Status), nil)
	})
	if err != nil {
		return task.Task{}, err
	}
	return t, nil
}

func (s *Store) Task(ctx context.Context, id string) (task.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, task_type, repo_url, base_commit, request_text, status, current_node_id, arm, record_id, created_at, updated_at
		FROM tasks WHERE id = ?;`, id)
	var t task.Task
	var cur, arm, rec sql.NullString
	var created, updated string
	var st string
	err := row.Scan(&t.ID, &t.TaskType, &t.RepoURL, &t.BaseCommit, &t.RequestText, &st, &cur, &arm, &rec, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, task.ErrNotFound
	}
	if err != nil {
		return task.Task{}, err
	}
	t.Status = task.TaskStatus(st)
	t.CurrentNodeID = cur.String
	t.Arm = arm.String
	t.RecordID = rec.String
	t.CreatedAt, _ = time.Parse(tsLayout, created)
	t.UpdatedAt, _ = time.Parse(tsLayout, updated)
	return t, nil
}

func (s *Store) UpdateTaskStatus(ctx context.Context, id string, status task.TaskStatus, currentNodeID string) error {
	at := s.clock()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var oldStatus string
		err := tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ?;`, id).Scan(&oldStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return task.ErrNotFound
		}
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE tasks SET status = ?, current_node_id = ?, updated_at = ? WHERE id = ?;`,
			string(status), nullString(currentNodeID), at.Format(tsLayout), id)
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, "task", id, &oldStatus, string(status), nil)
	})
}

func (s *Store) Node(ctx context.Context, id string) (task.Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, name, step_order, status, started_at, ended_at, created_at, updated_at
		FROM nodes WHERE id = ?;`, id)
	var n task.Node
	var name, st string
	var started, ended sql.NullString
	var created, updated string
	err := row.Scan(&n.ID, &n.TaskID, &name, &n.StepOrder, &st, &started, &ended, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return task.Node{}, task.ErrNotFound
	}
	if err != nil {
		return task.Node{}, err
	}
	n.Name = task.NodeName(name)
	n.Status = task.NodeStatus(st)
	n.CreatedAt, _ = time.Parse(tsLayout, created)
	n.UpdatedAt, _ = time.Parse(tsLayout, updated)
	if started.Valid {
		t, _ := time.Parse(tsLayout, started.String)
		n.StartedAt = &t
	}
	if ended.Valid {
		t, _ := time.Parse(tsLayout, ended.String)
		n.EndedAt = &t
	}
	return n, nil
}

func (s *Store) NodesForTask(ctx context.Context, taskID string) ([]task.Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, name, step_order, status, started_at, ended_at, created_at, updated_at
		FROM nodes WHERE task_id = ? ORDER BY step_order ASC;`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []task.Node
	for rows.Next() {
		var n task.Node
		var name, st string
		var started, ended sql.NullString
		var created, updated string
		if err := rows.Scan(&n.ID, &n.TaskID, &name, &n.StepOrder, &st, &started, &ended, &created, &updated); err != nil {
			return nil, err
		}
		n.Name = task.NodeName(name)
		n.Status = task.NodeStatus(st)
		n.CreatedAt, _ = time.Parse(tsLayout, created)
		n.UpdatedAt, _ = time.Parse(tsLayout, updated)
		if started.Valid {
			t, _ := time.Parse(tsLayout, started.String)
			n.StartedAt = &t
		}
		if ended.Valid {
			t, _ := time.Parse(tsLayout, ended.String)
			n.EndedAt = &t
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) UpdateNodeStatus(ctx context.Context, id string, status task.NodeStatus) error {
	at := s.clock()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var oldStatus string
		err := tx.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = ?;`, id).Scan(&oldStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return task.ErrNotFound
		}
		if err != nil {
			return err
		}

		var started, ended any
		if status == task.NodeStatusRunning {
			started = at.Format(tsLayout)
		}
		if status == task.NodeStatusSucceeded || status == task.NodeStatusFailed || status == task.NodeStatusSkipped {
			ended = at.Format(tsLayout)
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE nodes
			SET status = ?,
			    started_at = COALESCE(started_at, ?),
			    ended_at = COALESCE(?, ended_at),
			    updated_at = ?
			WHERE id = ?;`,
			string(status), started, ended, at.Format(tsLayout), id)
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, "node", id, &oldStatus, string(status), nil)
	})
}

func (s *Store) RecordTaskRun(ctx context.Context, r task.Run) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, task_id, node_id, attempt_num, ladder_rung, route, model_family, status,
			                 failure_class, failure_sig, diff_hash, tokens_input, tokens_output, tokens_cached,
			                 tokens_billable, wall_s, pid, pid_started_at, error, started_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			r.ID, r.TaskID, r.NodeID, r.AttemptNum, r.LadderRung, r.Route, r.ModelFamily, string(r.Status),
			nullString(string(r.FailureClass)), nullString(r.FailureSig), nullString(r.DiffHash),
			r.TokensInput, r.TokensOutput, r.TokensCached, r.TokensBillable, int(r.Wall.Seconds()),
			r.PID, nullString(r.PIDStartedAt), nullString(r.Error), r.StartedAt.Format(tsLayout),
		)
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		return s.insertEvent(ctx, tx, "run", r.ID, nil, string(r.Status), nil)
	})
}

func (s *Store) FinishTaskRun(ctx context.Context, runID string, status task.RunStatus, failureClass task.FailureClass, failureSig string) error {
	at := s.clock()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var oldStatus string
		err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id = ?;`, runID).Scan(&oldStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return task.ErrNotFound
		}
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE runs
			SET status = ?,
			    failure_class = ?,
			    failure_sig = ?,
			    ended_at = ?
			WHERE id = ?;`,
			string(status), nullString(string(failureClass)), nullString(failureSig), at.Format(tsLayout), runID)
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, "run", runID, &oldStatus, string(status), nil)
	})
}
