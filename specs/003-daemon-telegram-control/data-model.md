# Data Model: Daemon + Telegram Control Plane

All new state lives in the same `$FOREMAN_HOME/state/foreman.db` as Features 001/002 (SQLite,
WAL, `foreign_keys=ON`). New migration: `migrations/0004_daemon_control.sql`. Timestamps use the
existing `tsLayout` convention (`internal/adapters/sqlite/store.go`: RFC 3339, UTC, fixed
width, lexically sortable) for consistency with Features 001/002's columns.

## `tasks` (altered, Feature 002's table)

Add two values to the existing `status` CHECK constraint: `'paused'`, `'cancelled'`. No new
columns — pause/cancel are pure status transitions on the row Feature 002 already created.
SQLite can't alter a CHECK constraint in place, so the migration rebuilds the table
(`CREATE TABLE tasks_new ... ; INSERT INTO tasks_new SELECT ... FROM tasks; DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;`), matching the pattern already used for VACUUM-free
schema changes elsewhere in the codebase's migrations.

## `approvals`: one row per gate that needs a human decision

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `apv_…` |
| task_id | TEXT NOT NULL REFERENCES tasks | |
| node_id | TEXT REFERENCES nodes | nullable — a delivery-merge approval has no single node |
| kind | TEXT NOT NULL | `spec`, `deliver`, `merge` (extensible; code-driven, not hard-coded to a fixed list in SQL) |
| question | TEXT NOT NULL | shown in the Telegram message |
| options_json | TEXT NOT NULL | JSON array, e.g. `["approve","reject","edit"]` |
| subject_sha256 | TEXT NOT NULL | sha256 of the exact artifact bytes being approved (R5) |
| nonce | TEXT NOT NULL | 32 random bytes, hex, from `crypto/rand` |
| status | TEXT NOT NULL | `pending`, `approved`, `rejected`, `expired` |
| response | TEXT | which option was chosen, once responded |
| reason | TEXT | required for `rejected` (FR-011) |
| created_at | TEXT NOT NULL | |
| responded_at | TEXT | |

Index: `(task_id, status)`, `(nonce)` (unique — a nonce is never reused).

## `allowlist`: authorized Telegram operators

| Column | Type | Notes |
|---|---|---|
| telegram_user_id | INTEGER PK | numeric Telegram user id (FR-003) |
| label | TEXT | operator-facing note, e.g. "primary phone" |
| added_at | TEXT NOT NULL | |

Loaded once at startup and on a config-reload signal; checked on every incoming update before
any command parsing runs (FR-003's "operate in private chat only" is enforced by also checking
`chat.type == "private"` in the adapter, not stored here).

## `outbox_cursors`: durable position per notification subscriber

| Column | Type | Notes |
|---|---|---|
| subscriber | TEXT PK | `telegram` today; room for a second subscriber (e.g. `ntfy`) without a schema change |
| last_event_id | INTEGER NOT NULL | foreign key in spirit only — `events.id` is an autoincrement int, already present from Feature 002 |
| updated_at | TEXT NOT NULL | |

The notifier adapter (`internal/adapters/notifier`) reads `events WHERE id > last_event_id
ORDER BY id`, delivers (coalescing a burst into one digest message), and advances the cursor in
the same transaction as the delivery's local bookkeeping — so a crash between "sent to
Telegram" and "cursor advanced" is safe to re-deliver (at-least-once, never silently lost),
which is the behavior SC-004 requires.

## `deliveries`: one row per task that reached the integrator

| Column | Type | Notes |
|---|---|---|
| task_id | TEXT PK REFERENCES tasks | one delivery per task |
| branch | TEXT NOT NULL | `agent/<task_id>` |
| pr_url | TEXT | set once the draft PR is opened |
| diff_sha256 | TEXT NOT NULL | sha256 of the exact delivered diff — the subject of any `merge` approval (R5) |
| status | TEXT NOT NULL | `pending`, `pushed`, `pr_open`, `merged`, `closed`, `failed` |
| approval_id | TEXT REFERENCES approvals | the `merge` approval that authorized a merge, if any |
| created_at | TEXT NOT NULL | |
| updated_at | TEXT NOT NULL | |

## Retention (FR-018 — enforced by a daemon housekeeping tick, not a new table)

- Full transcripts, prompts-as-sent, context packs, verifier logs: 90 days (already on disk per
  Feature 001/002's artifact store; this feature adds the sweep, not new storage).
- Container output and failed-run workspaces: 7 days (configurable up to 14).
- Successful-run workspaces: deleted on success (already true in Feature 002 for a one-shot
  `fm run`; the daemon must preserve this on its own code path too).
- Structured records in SQLite (`tasks`, `nodes`, `runs`, `approvals`, `deliveries`): kept
  permanently.
- Cancelled tasks' workspaces: retained 7 days for forensics (FR-009), then swept by the same
  housekeeping tick.
