# Data Model: Durable Execution Loop (Feature 002)

All state lives in `$FOREMAN_HOME/state/foreman.db` (SQLite, WAL mode, `foreign_keys=ON`, `busy_timeout=5000`). Blobs live under `$FOREMAN_HOME/state/{tasks,nodes,runs,reports}/<id>/`.

Migrations are plain numbered SQL files in `migrations/`, applied in order inside one transaction each and tracked in `schema_migrations`.

IDs are `<prefix>_<YYYYMMDDHHMMSS>_<8 hex>` (time-sortable, from `crypto/rand`). Prefixes:
- `tsk`: task (`fm run` unit)
- `nod`: node (stage within a task: `spec`, `implement`, `verify`, `review`)
- `run`: run attempt (an execution of a node on a route)
- `evt`: event (audit entry)
- `rpt`: task report (markdown and JSON)

Timestamps are RFC 3339 UTC text.

---

## Tables (migration `0003_tasks_and_nodes.sql`)

### `tasks`: one row per `fm run` invocation

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `tsk_…` |
| task_type | TEXT NOT NULL | `code` in 002; data-driven for future types (FR-020) |
| repo_url | TEXT NOT NULL | path or url to target repository |
| base_commit | TEXT NOT NULL | 40-hex commit SHA |
| request_text | TEXT NOT NULL | user request or task description |
| status | TEXT NOT NULL | `pending`, `running`, `succeeded`, `failed`, `blocked_human` |
| current_node_id | TEXT FK → nodes | current active node |
| arm | TEXT | `harness` (default) or explicit runner route |
| record_id | TEXT FK → records | linked Feature 001 task record emitted on completion |
| created_at | TEXT NOT NULL | RFC 3339 UTC |
| updated_at | TEXT NOT NULL | RFC 3339 UTC |

### `nodes`: linear stage steps within a task

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `nod_…` |
| task_id | TEXT NOT NULL FK → tasks | parent task |
| name | TEXT NOT NULL | `spec`, `implement`, `verify`, `review` |
| step_order | INTEGER NOT NULL | 1 (`spec`), 2 (`implement`), 3 (`verify`), 4 (`review`) |
| status | TEXT NOT NULL | `pending`, `running`, `succeeded`, `failed`, `skipped` |
| started_at | TEXT | |
| ended_at | TEXT | |
| created_at | TEXT NOT NULL | |
| updated_at | TEXT NOT NULL | |

Unique constraint: `(task_id, step_order)`.

### `runs`: attempts for a node

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `run_…` |
| task_id | TEXT NOT NULL FK → tasks | parent task |
| node_id | TEXT NOT NULL FK → nodes | node being executed |
| attempt_num | INTEGER NOT NULL | 1..N attempts on this node |
| ladder_rung | INTEGER NOT NULL | 1 (same route + evidence), 2 (cross-family fresh), 3 (human block) |
| route | TEXT NOT NULL | e.g. `codex/default`, `hermes/claude-sonnet-5` |
| model_family | TEXT NOT NULL | `openai`, `anthropic` |
| status | TEXT NOT NULL | `running`, `succeeded`, `failed`, `infra_error`, `timed_out`, `budget_exceeded` |
| failure_class | TEXT | `infra`, `provider`, `attempt`, `policy` (FR-009) |
| failure_sig | TEXT | hash of failing test names + error summary (FR-011) |
| diff_hash | TEXT | sha256 of generated patch diff (FR-011) |
| tokens_input | INTEGER | |
| tokens_output | INTEGER | |
| tokens_cached | INTEGER | |
| tokens_billable | INTEGER | |
| wall_s | INTEGER | |
| pid | INTEGER | for dead process detection |
| pid_started_at | TEXT | |
| error | TEXT | error message |
| started_at | TEXT NOT NULL | |
| ended_at | TEXT | |

Unique constraint: `(node_id, attempt_num)`.

### `events`: state transition audit log (FR-002, Principle VII)

| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | `evt_…` |
| entity_type | TEXT NOT NULL | `task`, `node`, `run` |
| entity_id | TEXT NOT NULL | id of the target entity |
| event_type | TEXT NOT NULL | e.g. `task_started`, `node_transition`, `attempt_failed`, `ladder_escalated` |
| payload | TEXT NOT NULL | JSON payload with transition context |
| created_at | TEXT NOT NULL | RFC 3339 UTC |

---

## State Machines

### Task State Machine

```text
[pending] ---> [running] ---> [succeeded]
                  |
                  +----------> [failed]
                  |
                  +----------> [blocked_human] (Rung 3)
```

Transitions:
- `pending` -> `running`: `fm run` picks up task.
- `running` -> `succeeded`: all nodes (`spec`, `implement`, `verify`, `review`) reach `succeeded`.
- `running` -> `failed`: unrecoverable policy failure or exhausted ladder without human resolution.
- `running` -> `blocked_human`: ladder reaches Rung 3 (multiple failures or oscillations).

### Escalation Ladder State Transitions

```text
Implement Attempt (Rung 1)
         |
         v
    Verification
      /        \
  [Pass]      [Fail]
    |            |
 Review    Loop Guards (Signature repeated? Diff repeated? Pass count flat?)
                 /             \
             [Yes]             [No]
               |                 |
       Escalate to Rung 2    Retry on Rung 1 (with verifier evidence)
               |                 |
      Verification (Rung 2)      | (fails again)
         /            \          v
     [Pass]          [Fail] -> Escalate to Rung 2
       |                |
    Review       Escalate to Rung 3 ([blocked_human])
```

---

## Structured JSON Schemas

### 1. `schemas/task_spec.json` (FR-004)

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "TaskSpec",
  "type": "object",
  "required": ["summary", "acceptance_criteria", "verify_commands", "cited_files"],
  "properties": {
    "summary": { "type": "string" },
    "acceptance_criteria": {
      "type": "array",
      "items": { "type": "string" }
    },
    "verify_commands": {
      "type": "array",
      "items": {
        "type": "array",
        "items": { "type": "string" }
      }
    },
    "cited_files": {
      "type": "array",
      "items": { "type": "string" }
    }
  }
}
```

### 2. `schemas/review_findings.json` (FR-007)

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ReviewFindings",
  "type": "object",
  "required": ["status", "summary", "findings"],
  "properties": {
    "status": { "type": "string", "enum": ["approved", "changes_requested"] },
    "summary": { "type": "string" },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["severity", "file", "message"],
        "properties": {
          "severity": { "type": "string", "enum": ["critical", "major", "minor", "advisory"] },
          "file": { "type": "string" },
          "line": { "type": "integer" },
          "message": { "type": "string" },
          "evidence": { "type": "string" },
          "repro": { "type": "string" }
        }
      }
    }
  }
}
```

### 3. Project Configuration: `.foreman/project.yaml` (FR-016)

```yaml
version: 1
verifier_image: "foreman-verifier-go:latest"
setup:
  - ["go", "mod", "download"]
verify:
  - ["go", "test", "-v", "./..."]
protected_paths:
  - ".foreman/**"
  - "internal/core/engine/**"
  - "go.mod"
  - "go.sum"
```
