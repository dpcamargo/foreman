-- 0003_tasks_and_nodes.sql: Feature 002 task execution state machine
-- (specs/002-durable-execution-loop/data-model.md).

CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    task_type       TEXT NOT NULL DEFAULT 'code',
    repo_url        TEXT NOT NULL,
    base_commit     TEXT NOT NULL,
    request_text    TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'blocked_human')),
    current_node_id TEXT,
    arm             TEXT,
    record_id       TEXT REFERENCES records (id),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX tasks_status ON tasks (status, created_at);

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
CREATE INDEX runs_node ON runs (node_id, attempt_num);
CREATE INDEX runs_task ON runs (task_id);
