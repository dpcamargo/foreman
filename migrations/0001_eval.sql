-- 0001_eval.sql: Feature 001 schema (specs/001-evaluation-harness/data-model.md).
-- Timestamps are fixed-width RFC 3339 UTC text, so they sort lexically.

CREATE TABLE records (
    id                     TEXT PRIMARY KEY,
    format_version         INTEGER NOT NULL,
    source                 TEXT NOT NULL CHECK (source IN ('manual', 'fm-run')),
    task_type              TEXT NOT NULL,
    category               TEXT NOT NULL,
    request                TEXT NOT NULL,
    repo_url               TEXT NOT NULL,
    base_commit            TEXT NOT NULL,
    test_command           TEXT,              -- JSON argv; NULL only without hidden tests
    test_format            TEXT,
    full_test_command      TEXT,              -- JSON argv, optional
    base_full_suite_passed INTEGER,
    wall_budget_s          INTEGER NOT NULL,
    expects_clarification  INTEGER NOT NULL DEFAULT 0,
    live_outcome           TEXT,
    live_attempts          INTEGER,
    live_tokens_billable   INTEGER,
    live_usd               REAL,
    live_wall_s            INTEGER,
    live_interventions     INTEGER,
    replayable             INTEGER,           -- NULL = not validated, 1 = yes, 0 = no
    not_replayable_reason  TEXT,
    validated_at           TEXT,
    created_at             TEXT NOT NULL
);
CREATE INDEX records_replayable ON records (replayable, created_at);
CREATE INDEX records_repo ON records (repo_url);

CREATE TABLE replay_runs (
    id                 TEXT PRIMARY KEY,
    record_id          TEXT NOT NULL REFERENCES records (id),
    arm                TEXT NOT NULL,
    arm_version        TEXT NOT NULL,
    rep                INTEGER NOT NULL,
    base_commit        TEXT NOT NULL,
    status             TEXT NOT NULL,
    claimed            TEXT,
    tokens_input       INTEGER,
    tokens_cached      INTEGER,
    tokens_cache_write INTEGER,
    tokens_output      INTEGER,
    tokens_reasoning   INTEGER,
    tokens_billable    INTEGER,
    usd                REAL,
    cost_source        TEXT,
    wall_s             INTEGER,
    pid                INTEGER,
    pid_started_at     TEXT,
    error              TEXT,
    started_at         TEXT NOT NULL,
    ended_at           TEXT,
    UNIQUE (record_id, arm, arm_version, rep, base_commit)  -- the baseline cache key
);

CREATE TABLE grades (
    run_id               TEXT PRIMARY KEY REFERENCES replay_runs (id),
    hidden_passed        INTEGER NOT NULL,
    hidden_failed        INTEGER NOT NULL,
    per_test             TEXT NOT NULL,      -- JSON [{package, test, result}]
    full_suite_regressed INTEGER,
    contaminated         INTEGER NOT NULL DEFAULT 0,
    rubric_verdict       TEXT,
    success              INTEGER NOT NULL,
    false_success        INTEGER NOT NULL,
    graded_at            TEXT NOT NULL
);

CREATE TABLE reports (
    id               TEXT PRIMARY KEY,
    mode             TEXT NOT NULL CHECK (mode IN ('smoke', 'spot', 'decision')),
    seed             INTEGER NOT NULL,
    arms             TEXT NOT NULL,           -- JSON array
    k                INTEGER NOT NULL,
    sample           TEXT NOT NULL,           -- JSON array of record ids, in draw order
    budget_tokens    INTEGER,
    budget_usd       REAL,
    projected_tokens INTEGER,
    projected_usd    REAL,
    spent_tokens     INTEGER,
    spent_usd        REAL,
    spent_wall_s     INTEGER,
    usage_checked    INTEGER NOT NULL DEFAULT 0,
    parent_id        TEXT REFERENCES reports (id),
    created_at       TEXT NOT NULL
);

CREATE TABLE report_runs (
    report_id TEXT NOT NULL REFERENCES reports (id),
    run_id    TEXT NOT NULL REFERENCES replay_runs (id),
    PRIMARY KEY (report_id, run_id)
);

-- One row per state change, written in the same transaction as the change
-- (constitution VII). Feature 002 reuses it for task, node and run events.
CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity      TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    from_status TEXT,
    to_status   TEXT NOT NULL,
    detail      TEXT,                         -- JSON
    at          TEXT NOT NULL
);
CREATE INDEX events_entity ON events (entity, entity_id, id);
