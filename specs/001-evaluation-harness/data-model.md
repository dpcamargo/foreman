# Data Model: Evaluation Harness

All state lives in `$FOREMAN_HOME/state/foreman.db` (SQLite, WAL mode, `foreign_keys=ON`,
`busy_timeout=5000`). Blobs live under `$FOREMAN_HOME/state/{records,runs,reports}/<id>/`.
Migrations are plain numbered SQL files in `migrations/`, applied in order inside one
transaction each and tracked in `schema_migrations`.

IDs are `<prefix>_<YYYYMMDDHHMMSS>_<8 hex>` (time-sortable, from `crypto/rand`). The prefixes
are `rec`, `run` and `rpt`. Timestamps are RFC 3339 UTC text. Token counts are integers;
dollars are REAL and API-equivalent estimates unless `cost_source` says otherwise.

## Tables (migration `0001_eval.sql`)

### `records`: one row per task record (spec FR-001, FR-002)

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `rec_…` |
| format_version | INTEGER NOT NULL | starts at 1 (FR-003) |
| source | TEXT NOT NULL | `fm-run` (Feature 002) or `manual` (`fm eval record`) |
| task_type | TEXT NOT NULL | `code` only in 001 |
| category | TEXT NOT NULL | `bug_fix`, `feature`, `refactor`, `dependency`, `research`, `ambiguous` |
| request | TEXT NOT NULL | the original request text, verbatim |
| repo_url | TEXT NOT NULL | anything `git clone` accepts |
| base_commit | TEXT NOT NULL | full 40-hex SHA |
| test_command | TEXT NOT NULL | JSON array (argv) run inside the grader, e.g. `["go","test","-json","./..."]` |
| test_format | TEXT NOT NULL | `go-test-json` or `exit-code` |
| full_test_command | TEXT | JSON array, optional; used for the regression rate |
| wall_budget_s | INTEGER NOT NULL | default 1800 |
| expects_clarification | INTEGER NOT NULL DEFAULT 0 | 1 when the operator confirmed the right outcome was to ask |
| live_outcome | TEXT | `verified_success`, `failure`, `blocked`, `rescued` or `clarification`; NULL for manual records without one |
| live_attempts | INTEGER | |
| live_tokens_billable | INTEGER | uncached input + output + cache writes |
| live_usd | REAL | |
| live_wall_s | INTEGER | |
| live_interventions | INTEGER | human rescues (planned approvals are not counted) |
| replayable | INTEGER | NULL = not yet validated, 1 = yes, 0 = no |
| not_replayable_reason | TEXT | set when `replayable = 0` |
| validated_at | TEXT | |
| created_at | TEXT NOT NULL | |

Blob directory `records/<id>/`: `visible/` (files copied into the arm's workspace before it
starts), `hidden/` (files the arm never sees), and an optional `reference.patch` (the live
verified result).

Indexes: `(replayable, created_at)` and `(repo_url)`.

### `replay_runs`: one row per (record, arm, repetition) attempt (FR-009, FR-015)

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `run_…` |
| record_id | TEXT NOT NULL FK → records | |
| arm | TEXT NOT NULL | `a0`, `a1`, `a2`; `h1` from Feature 002 |
| arm_version | TEXT NOT NULL | `<cli version>/<model>/<prompt sha256[:12]>` |
| rep | INTEGER NOT NULL | 1..k |
| base_commit | TEXT NOT NULL | copied from the record; part of the cache key |
| status | TEXT NOT NULL | see state machine below |
| claimed | TEXT | `done`, `needs_clarification` or `none` (what the arm said, not what's true) |
| tokens_input, tokens_cached, tokens_cache_write, tokens_output, tokens_reasoning | INTEGER | raw from the CLI |
| tokens_billable | INTEGER | derived, compared with the cap |
| usd | REAL | `estimated_cost_usd` from Hermes; NULL for Codex on a ChatGPT plan |
| cost_source | TEXT | e.g. `hermes-estimate`, `none` |
| wall_s | INTEGER | |
| pid, pid_started_at | INTEGER, TEXT | for lost-run detection |
| error | TEXT | for infrastructure failures |
| started_at, ended_at | TEXT | |

The unique constraint `(record_id, arm, arm_version, rep, base_commit)` is the cache key
(FR-015). Requesting an existing finished row reuses it; an `infra_error` row is retried.

Blob directory `runs/<id>/`: `prompt.md`, `transcript.jsonl`, `last.json` (structured
result), `patch.diff`, `grade.json` and `test-output.jsonl`.

### `grades`: one row per graded run (FR-006, FR-010)

| Column | Type | Notes |
|---|---|---|
| run_id | TEXT PK FK → replay_runs | |
| hidden_passed, hidden_failed | INTEGER NOT NULL | |
| per_test | TEXT NOT NULL | JSON `[{"package","test","result"}]` |
| full_suite_regressed | INTEGER | NULL when the record has no `full_test_command` |
| contaminated | INTEGER NOT NULL DEFAULT 0 | leak scan hit (research R1) |
| rubric_verdict | TEXT | always NULL in 001 (research R11) |
| success | INTEGER NOT NULL | see rules below |
| false_success | INTEGER NOT NULL | |
| graded_at | TEXT NOT NULL | |

### `reports`: one row per spot check or decision replay (FR-011–FR-013, FR-016, FR-017)

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `rpt_…` |
| mode | TEXT NOT NULL | `smoke`, `spot` or `decision` |
| seed | INTEGER NOT NULL | sampling and bootstrap seed |
| arms | TEXT NOT NULL | JSON array |
| k | INTEGER NOT NULL | |
| sample | TEXT NOT NULL | JSON array of record ids, in draw order |
| budget_tokens, budget_usd | INTEGER, REAL | operator-set limits (decision mode) |
| projected_tokens, projected_usd | INTEGER, REAL | from the smoke pass |
| spent_tokens, spent_usd, spent_wall_s | INTEGER, REAL, INTEGER | what this report's runs consumed; cached runs count 0 |
| usage_checked | INTEGER NOT NULL DEFAULT 0 | research R13 |
| parent_id | TEXT FK → reports | a decision report points at its smoke report |
| created_at | TEXT NOT NULL | |

`report_runs(report_id, run_id)` links a report to its runs, including reused cached runs.

Blobs: `reports/<id>.md` and `reports/<id>.json` (see `contracts/report.md`).

## Replay run state machine

```text
queued ──start──▶ running ──arm exits──▶ grading ──▶ succeeded | failed
                     │                       │
                     ├─ wall cap ──▶ timed_out          (counts as failure)
                     ├─ token cap ─▶ budget_exceeded    (counts as failure)
                     ├─ infra ─────▶ infra_error        (excluded from the denominator; retryable)
                     └─ process gone at startup ─▶ infra_error ("lost")
grading ── leak scan hit ──▶ contaminated               (counts as failure; flagged in report)
```

Every transition is a single `UPDATE … WHERE id=? AND status=?` inside a transaction. A row is
only moved out of `running` by its own process, or by the startup sweep. The sweep marks
`running` rows `infra_error` with error `lost` when their PID is gone or its start time
doesn't match (constitution VII).

## Validation rules (not obvious from the column types)

- **Replayable** (FR-004) requires all of:
  1. the base commit is fetchable;
  2. `task_type = 'code'` and no rubric is needed;
  3. the grader on the unpatched base (with the visible tests applied) reports at least one
     hidden test failed, or a non-zero exit for `exit-code`;
  4. if `reference.patch` exists, the grader on base + reference reports every hidden test
     passed.

  A failure records the first rule that failed as `not_replayable_reason`. Rule 4 catches
  broken hidden tests.
- **success** = status `succeeded` AND not contaminated AND:
  - if `expects_clarification = 1`: `claimed = 'needs_clarification'`;
  - otherwise: `hidden_failed = 0`, `hidden_passed > 0`, and `full_suite_regressed` is not 1.
- **false_success** = `claimed = 'done'` AND NOT success.
- `fm eval record` rejects inputs that would make a record unusable: an empty `hidden/`, a
  `test_command` that isn't a JSON array, a `base_commit` that isn't 40 hex characters, or a
  hidden or visible file path that escapes the repo (`..`, absolute paths, symlinks).
- A record is immutable after creation, except for `replayable`, `not_replayable_reason` and
  `validated_at`. A changed task becomes a new record.

## Core types (`internal/core/eval`)

```go
type Record struct {
    ID, Source, TaskType, Category, Request, RepoURL, BaseCommit string
    TestCommand, FullTestCommand []string
    TestFormat                   TestFormat
    WallBudget                   time.Duration
    ExpectsClarification         bool
    Live                         *LiveOutcome // nil for manual records without one
    Replayable                   *bool        // nil = not validated
    NotReplayableReason          string
}

type ReplayRun struct {
    ID, RecordID, Arm, ArmVersion, BaseCommit string
    Rep                                       int
    Status                                    RunStatus
    Claimed                                   Claim
    Usage                                     Usage
    Wall                                      time.Duration
}

type Grade struct {
    RunID                        string
    HiddenPassed, HiddenFailed   int
    PerTest                      []TestResult
    FullSuiteRegressed           *bool
    Contaminated                 bool
    Success, FalseSuccess        bool
}
```

The ports this package declares (and only these) are in `contracts/ports.md`.
