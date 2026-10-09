-- 0004_daemon_control.sql: Feature 003 daemon control-plane state
-- (specs/003-daemon-telegram-control/data-model.md).
--
-- This migration rebuilds `tasks` to extend its status CHECK constraint with
-- 'paused'/'cancelled'. The obvious order (rebuild tasks, then carry on) fails on any
-- database that has actually run a task: `nodes` and `runs` both declare
-- `REFERENCES tasks(id)`, so `DROP TABLE tasks` is rejected by foreign_keys=ON (the DSN
-- sets it) the moment either child table holds a row. Fresh test DBs never caught this
-- because their child tables are empty at migration time (found during 003's live
-- quickstart, T049).
--
-- `PRAGMA defer_foreign_keys` does NOT help: it only defers DML checks, while DROP TABLE
-- enforces FK constraints immediately. And `PRAGMA foreign_keys=OFF` is a no-op inside the
-- migration transaction. The fix is ordering: park the child rows in FK-free holding tables,
-- drop `nodes`/`runs` (nothing references them) so no constraint points at `tasks` anymore,
-- rebuild `tasks` unconstrained, then recreate `nodes`/`runs` with their exact 0003 schema
-- and restore their rows. Lossless on both an empty and a populated DB.

-- 1. Park child rows in holding tables with no foreign keys (they reference nothing, so
--    nothing in the rebuild below can touch them).
CREATE TABLE nodes_hold AS SELECT * FROM nodes;
CREATE TABLE runs_hold AS SELECT * FROM runs;

-- 2. Drop children (runs first: runs.node_id references nodes), clearing the only foreign
--    keys pointing at tasks.
DROP TABLE runs;
DROP TABLE nodes;

-- 3. Rebuild tasks with 'paused'/'cancelled' added to its status CHECK constraint.
--    Everything else about the table (columns, foreign keys, data) is unchanged.
CREATE TABLE tasks_new (
    id              TEXT PRIMARY KEY,
    task_type       TEXT NOT NULL DEFAULT 'code',
    repo_url        TEXT NOT NULL,
    base_commit     TEXT NOT NULL,
    request_text    TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN (
                        'pending', 'running', 'succeeded', 'failed', 'blocked_human',
                        'paused', 'cancelled'
                    )),
    current_node_id TEXT,
    arm             TEXT,
    record_id       TEXT REFERENCES records (id),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
INSERT INTO tasks_new SELECT * FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
CREATE INDEX tasks_status ON tasks (status, created_at);

-- 4. Recreate nodes and runs exactly as migrations/0003_tasks_and_nodes.sql declared them
--    (same columns, constraints and indexes), now pointing at the rebuilt `tasks`, and
--    restore their rows from the holding tables.
CREATE TABLE nodes (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks (id),
    name        TEXT NOT NULL CHECK (name IN ('spec', 'implement', 'verify', 'review')),
    step_order  INTEGER NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    started_at  TEXT,
    ended_at    TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (task_id, step_order)
);
INSERT INTO nodes SELECT * FROM nodes_hold;
CREATE INDEX nodes_task ON nodes (task_id, step_order);

CREATE TABLE runs (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks (id),
    node_id         TEXT NOT NULL REFERENCES nodes (id),
    attempt_num     INTEGER NOT NULL,
    ladder_rung     INTEGER NOT NULL CHECK (ladder_rung IN (1, 2, 3)),
    route           TEXT NOT NULL,
    model_family    TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'infra_error', 'timed_out', 'budget_exceeded')),
    failure_class   TEXT CHECK (failure_class IS NULL OR failure_class IN ('infra', 'provider', 'attempt', 'policy')),
    failure_sig     TEXT,
    diff_hash       TEXT,
    tokens_input    INTEGER NOT NULL DEFAULT 0,
    tokens_output   INTEGER NOT NULL DEFAULT 0,
    tokens_cached   INTEGER NOT NULL DEFAULT 0,
    tokens_billable INTEGER NOT NULL DEFAULT 0,
    wall_s          INTEGER NOT NULL DEFAULT 0,
    pid             INTEGER,
    pid_started_at  TEXT,
    error           TEXT,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    UNIQUE (node_id, attempt_num)
);
INSERT INTO runs SELECT * FROM runs_hold;
CREATE INDEX runs_node ON runs (node_id, attempt_num);
CREATE INDEX runs_task ON runs (task_id);

-- 5. Holding tables are scaffolding only — they must not survive this migration.
DROP TABLE nodes_hold;
DROP TABLE runs_hold;

-- One row per gate that needs a human decision (spec FR-005, FR-011).
CREATE TABLE approvals (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks (id),
    node_id         TEXT REFERENCES nodes (id),
    kind            TEXT NOT NULL,
    question        TEXT NOT NULL,
    options_json    TEXT NOT NULL,
    subject_sha256  TEXT NOT NULL,
    nonce           TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    response        TEXT,
    reason          TEXT,
    created_at      TEXT NOT NULL,
    responded_at    TEXT
);
CREATE INDEX approvals_task ON approvals (task_id, status);
CREATE UNIQUE INDEX approvals_nonce ON approvals (nonce);

-- Numeric Telegram user ids authorized to issue commands (spec FR-003).
CREATE TABLE allowlist (
    telegram_user_id INTEGER PRIMARY KEY,
    label            TEXT,
    added_at         TEXT NOT NULL
);

-- Durable position per notification subscriber over the events table (spec FR-007).
CREATE TABLE outbox_cursors (
    subscriber     TEXT PRIMARY KEY,
    last_event_id  INTEGER NOT NULL,
    updated_at     TEXT NOT NULL
);

-- One row per task that reached the integrator (spec FR-014, FR-015, FR-016).
CREATE TABLE deliveries (
    task_id      TEXT PRIMARY KEY REFERENCES tasks (id),
    branch       TEXT NOT NULL,
    pr_url       TEXT,
    diff_sha256  TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'pushed', 'pr_open', 'merged', 'closed', 'failed')),
    approval_id  TEXT REFERENCES approvals (id),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
