-- 0004_daemon_control.sql: Feature 003 daemon control-plane state
-- (specs/003-daemon-telegram-control/data-model.md).

-- SQLite can't ALTER a CHECK constraint in place, so the tasks table is rebuilt with
-- 'paused' and 'cancelled' added to its status values. Everything else about the table
-- (columns, foreign keys, data) is unchanged.
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
