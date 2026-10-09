package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/dpcamargo/foreman/migrations"
)

func mustReadMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(migrations.FS, name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}

// T049 (live quickstart) found this: migration 0004 rebuilds the tasks table (to extend its
// status CHECK constraint with 'paused'/'cancelled') via INSERT INTO tasks_new; DROP TABLE
// tasks; ALTER TABLE tasks_new RENAME TO tasks. With foreign_keys=ON (the DSN sets it), a
// DROP TABLE tasks fails the moment any row in nodes or runs references a task — which is
// every non-empty database. Fresh test databases never reproduced this because their nodes/
// runs tables are empty at migration time. This test seeds the live shape first: child rows
// pointing at tasks, then runs the full migration chain, and asserts 0004 applies without a
// FOREIGN KEY error.
func TestMigrateRebuildsTasksTableWithExistingChildRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "foreman.db") +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// migrate() creates schema_migrations before applying anything; applyOne assumes it
	// exists, so bootstrap it here the same way.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	// Apply only 0001-0003 (the pre-0004 schema) via applyOne, so each is recorded in
	// schema_migrations and the later migrate() call doesn't try to re-run them.
	for _, name := range []string{"0001_eval.sql", "0002_report_counts.sql", "0003_tasks_and_nodes.sql"} {
		if err := applyOne(ctx, db, name, "2026-10-08T00:00:00Z"); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO tasks (id, task_type, repo_url, base_commit, request_text, status, created_at, updated_at)
		VALUES ('tsk_1', 'code', '/repo', 'abc', 'Add Sub', 'running', '2026-10-08T00:00:00Z', '2026-10-08T00:00:00Z')`); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO nodes (id, task_id, name, step_order, status, created_at, updated_at)
		VALUES ('nod_1', 'tsk_1', 'spec', 1, 'succeeded', '2026-10-08T00:00:00Z', '2026-10-08T00:00:00Z')`); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO runs (id, task_id, node_id, attempt_num, ladder_rung, route, model_family, status, started_at)
		VALUES ('run_1', 'tsk_1', 'nod_1', 1, 1, 'codex', 'openai', 'running', '2026-10-08T00:00:00Z')`); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Now the full migrate() chain must apply 0004 (and 0005) over this populated schema
	// without a FOREIGN KEY constraint failure.
	if err := migrate(ctx, db, "2026-10-08T00:00:00Z"); err != nil {
		t.Fatalf("migrate over a populated tasks table: %v", err)
	}

	// The rebuilt tasks table must still hold the row, and its child rows must still resolve.
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = 'tsk_1'`).Scan(&status); err != nil {
		t.Fatalf("task row lost during rebuild: %v", err)
	}
	if status != "running" {
		t.Errorf("task status = %q, want 'running'", status)
	}
	var nodeCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE task_id = 'tsk_1'`).Scan(&nodeCount); err != nil {
		t.Fatalf("child nodes lost during rebuild: %v", err)
	}
	if nodeCount != 1 {
		t.Errorf("child node count = %d, want 1", nodeCount)
	}
	// The new CHECK constraint must accept 'paused'/'cancelled' — that's the whole reason
	// 0004 rebuilds the table.
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET status = 'paused' WHERE id = 'tsk_1'`); err != nil {
		t.Errorf("rebuilt tasks table still rejects 'paused': %v", err)
	}
}
