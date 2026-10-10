-- 0006_routing.sql: Feature 004 routing, provider health, and test-first
-- (specs/004-routing-test-first/data-model.md).

-- Provider health state (FR-001). Updated from CLI/API error signals and usage reports.
CREATE TABLE providers (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL CHECK (status IN ('ok', 'cooling_down', 'quota_low')),
    cooldown_until  TEXT,               -- RFC3339 UTC; NULL when status is 'ok'
    backoff_seconds INTEGER NOT NULL DEFAULT 0,
    quota_reset_at  TEXT,               -- RFC3339 UTC; provider-reported quota window reset
    quota_remaining INTEGER,
    updated_at      TEXT NOT NULL
);

-- A (harness, provider, model, effort) tuple eligible for a given role (FR-002).
CREATE TABLE routes (
    id                  TEXT PRIMARY KEY,
    role                TEXT NOT NULL CHECK (role IN ('implementer', 'reviewer', 'test_author', 'specifier', 'triage')),
    provider            TEXT NOT NULL REFERENCES providers (id),
    model_family        TEXT NOT NULL CHECK (model_family IN ('openai', 'anthropic', 'google')),
    harness             TEXT NOT NULL,
    cost_per_1k_tokens  REAL NOT NULL DEFAULT 0,
    quota_pressure      REAL NOT NULL DEFAULT 0,
    p_min               REAL NOT NULL DEFAULT 0,
    enabled             INTEGER NOT NULL DEFAULT 1
);

-- Per (route, task_class) Beta-prior routing statistics (FR-003, FR-011).
CREATE TABLE routing_stats (
    route_id    TEXT NOT NULL REFERENCES routes (id),
    task_class  TEXT NOT NULL,
    alpha       INTEGER NOT NULL DEFAULT 1,   -- successes + 1
    beta        INTEGER NOT NULL DEFAULT 1,    -- failures + 1
    samples     INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (route_id, task_class)
);

-- Test-author node output (FR-007).
CREATE TABLE test_author_results (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks (id),
    node_id         TEXT NOT NULL REFERENCES nodes (id),
    visible_patch   BLOB NOT NULL,           -- test patch for implementer's workspace
    hidden_patch    BLOB NOT NULL,           -- test patch for verifier only (outside workspaces)
    fails_on_base   INTEGER NOT NULL DEFAULT 0,
    model_family    TEXT NOT NULL,
    created_at      TEXT NOT NULL
);

-- tasks gains lane and triage columns (FR-009, FR-013).
ALTER TABLE tasks ADD COLUMN lane TEXT CHECK (lane IS NULL OR lane IN ('F', 'S', 'D'));
ALTER TABLE tasks ADD COLUMN task_class TEXT;
ALTER TABLE tasks ADD COLUMN risk_level TEXT;
ALTER TABLE tasks ADD COLUMN triage_source TEXT CHECK (triage_source IS NULL OR triage_source IN ('local', 'api', 'manual'));
ALTER TABLE tasks ADD COLUMN accumulated_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN not_before TEXT;   -- node parking timestamp (FR-005)

-- nodes gains not_before for node parking (FR-005).
ALTER TABLE nodes ADD COLUMN not_before TEXT;
