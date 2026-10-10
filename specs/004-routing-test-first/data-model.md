# Data Model: Routing, Provider Health, and Test-First

All new state lives in `$FOREMAN_HOME/state/foreman.db` (SQLite, WAL mode). New migration: `migrations/0006_routing.sql`.

## `providers` (new)

Provider health state, updated from CLI/API error signals and usage reports (FR-001).

| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PRIMARY KEY | provider identifier (e.g. `openai`, `anthropic`, `google`) |
| status | TEXT NOT NULL CHECK | `ok`, `cooling_down`, `quota_low` |
| cooldown_until | TEXT | RFC3339 UTC; NULL when status is `ok` |
| backoff_seconds | INTEGER NOT NULL DEFAULT 0 | current exponential backoff (doubles on consecutive failures) |
| quota_reset_at | TEXT | RFC3339 UTC; provider-reported quota window reset |
| quota_remaining | INTEGER | tokens/requests remaining in current window |
| updated_at | TEXT NOT NULL | |

## `routes` (new)

A (harness, provider, model, effort) tuple eligible for a given role. Seeded from config, not dynamic.

| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PRIMARY KEY | e.g. `codex/gpt-5`, `hermes/claude-sonnet-5`, `agy/gemini-2.5-pro` |
| role | TEXT NOT NULL CHECK | `implementer`, `reviewer`, `test_author`, `specifier`, `triage` |
| provider | TEXT NOT NULL REFERENCES providers(id) | |
| model_family | TEXT NOT NULL CHECK | `openai`, `anthropic`, `google` |
| harness | TEXT NOT NULL | `codex`, `hermes`, `agy` |
| cost_per_1k_tokens | REAL NOT NULL DEFAULT 0 | API cost; 0 for subscription routes |
| quota_pressure | REAL NOT NULL DEFAULT 0 | penalty term for subscription-quota routes (§5.4) |
| p_min | REAL NOT NULL DEFAULT 0 | minimum P(verified success) for this route to be eligible (risk-tier dependent) |
| enabled | INTEGER NOT NULL DEFAULT 1 | operator can disable a route |

## `routing_stats` (new)

Per (route_id, task_class) Beta-prior statistics (FR-003, FR-011).

| Column | Type | Notes |
|--------|------|-------|
| route_id | TEXT NOT NULL REFERENCES routes(id) | |
| task_class | TEXT NOT NULL | e.g. `feature`, `refactor`, `bugfix`, `docs` |
| alpha | INTEGER NOT NULL DEFAULT 1 | successes + 1 (Beta prior) |
| beta | INTEGER NOT NULL DEFAULT 1 | failures + 1 (Beta prior) |
| samples | INTEGER NOT NULL DEFAULT 0 | total observations |
| updated_at | TEXT NOT NULL | |

PRIMARY KEY (route_id, task_class).

## `tasks` (altered)

Add lane and triage columns.

| Column | Type | Notes |
|--------|------|-------|
| lane | TEXT CHECK | `F`, `S`, `D`; NULL for pre-004 tasks |
| task_class | TEXT | triage output |
| risk_level | TEXT | triage output: `low`, `medium`, `high` |
| triage_source | TEXT CHECK | `local`, `api`, `manual` |
| accumulated_usd | REAL NOT NULL DEFAULT 0 | spend tracking for budget enforcement (FR-010) |
| not_before | TEXT | node parking timestamp (FR-005); NULL normally |

## `nodes` (altered)

Add `not_before` column.

| Column | Type | Notes |
|--------|------|-------|
| not_before | TEXT | RFC3339 UTC; set when all routes are cooling down (FR-005) |

Also: `name` CHECK constraint gains `test_author` as a valid node name.

## `test_author_results` (new)

Test-author node output (FR-007).

| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PRIMARY KEY | `tar_<YYYYMMDDHHMMSS>_<8hex>` |
| task_id | TEXT NOT NULL REFERENCES tasks(id) | |
| node_id | TEXT NOT NULL REFERENCES nodes(id) | |
| visible_patch | BLOB NOT NULL | test patch for visible tests (copied into implementer workspace) |
| hidden_patch | BLOB NOT NULL | test patch for hidden tests (stored outside workspaces, verifier-only) |
| fails_on_base | INTEGER NOT NULL DEFAULT 0 | verifier confirmed every new test fails on unmodified base |
| model_family | TEXT NOT NULL | family of the test-author model |
| created_at | TEXT NOT NULL | |

## `policy` (not a table)

`policy.yaml` lives on the filesystem at `$FOREMAN_HOME/policy.yaml`. Parsed at daemon startup. Invalid or missing lane → refuse to dispatch (FR-009).

## State transitions

### Provider health
- `ok` → `cooling_down` on 429/plan-limit/5xx/expired-auth (FR-004)
- `cooling_down` → `ok` when `cooldown_until` elapses (automatic, no manual reset)
- `ok` → `quota_low` on usage report below threshold
- `quota_low` → `ok` when quota resets

### Node parking (FR-005)
- `pending` stays `pending` with `not_before` set when all routes cooling down
- Reconciler picks up when `not_before` has passed
- No attempt counted while parked

### Test-author (FR-007/008)
1. `test_author` node runs before `implement` when lane has `test_first: true`
2. Produces visible + hidden test patches
3. Verifier confirms every hidden test fails on unmodified base commit
4. If any test passes on base → reject, re-run test-author with that evidence
5. If any test fails to compile → test-author attempt failure (retry/escalate)
6. Hidden patch stored outside all workspaces, mounted read-only only into verifier
