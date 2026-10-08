# Research & Architecture Decisions: Durable Execution Loop (Feature 002)

**Date**: 2026-10-07
**Status**: Complete
**Spec**: [spec.md](spec.md)

This document records the design decisions and technical investigations for Feature 002 (`fm run`), satisfying the Phase 0 requirements of `/speckit-plan`.

---

## R1: State Machine & Database Schema (Tasks, Nodes, Runs, Events)

### Context
FR-001, FR-002, and Principle VII require deterministic, crash-safe persistence in SQLite (WAL mode). Each state transition and its event must commit in a single transaction. Feature 001 introduced `records`, `runs`, and `reports`. Feature 002 adds the execution orchestration entities: `tasks`, `nodes`, `attempts` (runs), and `events`.

### Decision
Extend SQLite schema in migration `0003_tasks_and_nodes.sql`:
1. `tasks`: `id` (e.g. `tsk_...`), `task_type` (default `'code'`), `repo_url`, `base_commit`, `request_text`, `status` (`pending`, `running`, `succeeded`, `failed`, `blocked_human`), `current_node_id`, `created_at`, `updated_at`.
2. `nodes`: `id` (`nod_...`), `task_id`, `name` (`spec`, `implement`, `verify`, `review`), `step_order`, `status` (`pending`, `running`, `succeeded`, `failed`, `skipped`), `created_at`, `updated_at`.
3. `runs` (extend or link): represents one execution attempt of a node on a specific route (`arm`/`model`/`family`). Carries `attempt_num`, `ladder_rung` (1, 2, 3), `status` (`succeeded`, `failed`, `infra_error`, `timed_out`, `budget_exceeded`), `usage` (JSON), `patch_path`, `failure_class`, `failure_sig`.
4. `events`: append-only ledger (`id`, `entity_type`, `entity_id`, `event_type`, `payload`, `created_at`) written in the same transaction as every status update.

### Rationale
- Keeps table structures simple and relational.
- The linear execution chain for Feature 002 is represented as nodes with sequential steps (`spec` -> `implement` -> `verify` -> `review`), allowing Feature 006 (DAG) to add dependencies later without rewriting the schema.
- Transactional atomicity: `BEGIN IMMEDIATE; UPDATE nodes ...; INSERT INTO events ...; COMMIT;` prevents partial state.

### Alternatives Considered
- *Separate orchestrator database*: rejected per Principle IX (minimal infrastructure). One database `$FOREMAN_HOME/state/foreman.db` holds both eval ledger and task execution state.
- *Embedding node state inside a JSON blob on tasks*: rejected because SQL queries for reconciler sweeps and retry tracking need indexed columns.

---

## R2: Workspace & Dedicated OS User Isolation (FR-003, FR-019)

### Context
FR-003 and Principle VI require that every attempt executes in a disposable workspace pinned to the base commit, with no access to host credentials or shared `.git` hooks. ADR 0001 specifies hosting under a dedicated `foreman` macOS user. However, during development and initial operator runs (prerequisite table in ROADMAP.md), the operator may invoke `fm run` before the separate macOS user is provisioned.

### Decision
1. **Disposable Clones**: Use Feature 001's sandbox pattern (`internal/adapters/sandbox`). For each attempt, clone the target repository to a unique attempt directory: `$FOREMAN_HOME/work/tasks/<task_id>/nodes/<node_id>/runs/<run_id>/ws`.
2. **Trusted Git Directory**: Isolate git configuration by keeping a read-only trusted git mirror (`trusted.git`) outside the working tree, and exporting patches via `git diff` into `<run_dir>/patch.diff`. No shared worktrees.
3. **OS User Separation**: If `FOREMAN_RUN_AS_USER` is set (or running under the `foreman` user), processes are spawned with `setuid`/`sudo -u`. When running under the operator user during local development, enforce filesystem confinement via permissions (`0700` directories) and runner sandboxes (Codex read-only deny profile, Hermes Docker isolation).

### Rationale
- Matches the exact pattern already validated in Feature 001.
- Works identically whether running as the dedicated `foreman` user or during developer testing.

### Alternatives Considered
- *Git worktrees*: Forbidden by Principle VI (shared `.git` common directory exposes hooks and credentials).

---

## R3: The Specify Step & Pre-Implementation Verification (FR-004)

### Context
FR-004 and Principle I/III require a single strong-model "specify" step producing a `TaskSpec`. The spec must define acceptance criteria, concrete verification commands, and must be validated against the base commit before any code is written.

### Decision
1. **Structured Schema**: `schemas/task_spec.json` defines the shape: `acceptance_criteria` (list of strings), `verify_commands` (list of argv arrays, e.g. `["go", "test", "./..."]`), `cited_files` (list of repo paths), and `approach_summary`.
2. **Grounding Check**: Before accepting the spec, the engine verifies on the base commit:
   - Every path in `cited_files` exists in `git ls-tree -r <base>`.
   - The verification commands succeed or fail deterministically in the base container environment without syntax or missing-tool errors.
   - If validation fails, reject the spec and record `spec_validation_error`.

### Rationale
- Catches hallucinations and prompt drifts before wasting tokens on implementation.

---

## R4: Escalation Ladder & Loop Guards (FR-008, FR-009, FR-011)

### Context
FR-008 and ARCHITECTURE.md §7.5 define escalation rungs 1 through 3. FR-011 defines loop guards that skip retries when an agent is stuck.

### Decision
1. **Failure Classification** (FR-009):
   - `infra`: docker failure, lost PID, disk full. Does NOT advance ladder; retried cleanly.
   - `provider`: rate limit (429), API outage, timeout. Does NOT advance ladder; backoff & retry.
   - `attempt`: tests failed, syntax error, compiler error. Advances ladder.
   - `policy`: diff policy violation (test deleted, secret leaked, protected path touched). Advances ladder.
2. **Escalation Ladder**:
   - **Rung 1 (Retry Same Route)**: Same model/family, but prompt includes verifier failure evidence (failing test names, error outputs) without leaking hidden test source code.
   - **Rung 2 (Cross-Family Escalation)**: Escalate to a different model family (e.g. Codex -> Claude via Hermes, or Hermes -> Codex). Prompt contains the original `TaskSpec` and a high-level summary of prior failure modes, but **never the prior failing diff** (prevents anchoring bias).
   - **Rung 3 (Human Attention)**: Mark task `blocked_human`. Output structured summary and markdown report detailing all attempted rungs and verifier logs.
3. **Loop Guards** (FR-011):
   - Calculate `failure_sig = hash(failing_test_names + error_prefix)`.
   - If `failure_sig[attempt_N] == failure_sig[attempt_N-1]`, skip to next rung immediately.
   - If `diff_hash[attempt_N] == diff_hash[attempt_M]`, skip to next rung immediately.
   - If passing test count does not strictly increase across 2 attempts on rung 1, escalate to rung 2.

### Rationale
- Prevents token-wasting loops when an agent repeatedly tries the same broken fix.

---

## R5: Verification & Diff-Policy Checks (FR-006, FR-016, FR-018)

### Context
FR-006 and FR-018 require reusing Feature 001's offline clean-room container grader, augmenting it with diff-policy checks, visible-test execution, and project configuration.

### Decision
1. **Project Config**: `.foreman/project.yaml` in the target repo defines:
   - `version: 1`
   - `verifier_image`: docker image name
   - `setup`: commands to run before tests
   - `verify`: commands to run for visible test pass
   - `protected_paths`: list of file globs that require human approval to edit
2. **Diff-Policy Engine** (`internal/core/verify/policy.go`):
   - Inspect git diff patch before running tests.
   - **No test deletion**: Check that no test files existing on base were deleted or renamed away.
   - **No protected path edit**: Check file paths against `protected_paths`.
   - **No secret leaks**: Scan patch with `internal/core/eval/redact.go` patterns.
   - **No symlink escapes**: Verify no newly introduced symlinks point outside workspace.
3. **Execution Pipeline**:
   `DiffPolicyCheck` -> `CleanContainerBuild` -> `VisibleTests` -> `HiddenTests` -> `VerifyReport`.

### Rationale
- Keeps verification deterministic and independent of agent self-reporting.

---

## R6: Cross-Family Review & Blocking Finding Repro (FR-007, Principle V)

### Context
FR-007 requires that any review must be performed by a different model family than the implementer, with structured findings.

### Decision
1. **Route Pairing**: If implementer was `codex` (OpenAI family), reviewer MUST be `hermes` (Anthropic family), and vice versa.
2. **Schema**: `schemas/review_findings.json`:
   - `status`: `approved` | `changes_requested`
   - `findings`: array of `{severity, file, line, message, evidence, repro}`
   - `summary`: string
3. **Evidence Enforcement**: Any finding with severity `major` or `critical` that lacks concrete evidence or repro is downgraded to `advisory` by the core review handler.

### Rationale
- Eliminates same-family bias and prevents unsubstantiated review blocks.

---

## R7: Feature 001 Integration & Arm H1 (FR-021, FR-022)

### Context
FR-021 and FR-022 require that every `fm run` writes a Feature 001 task record, and that foreman's execution loop implements the `eval.Arm` port as arm `H1`.

### Decision
1. **Record Generation**: When `fm run` finishes (success or failure), it writes a row to `records` with the request, base commit, visible/hidden tests, and execution outcome, enabling future replays.
2. **Harness Arm H1**: Implement `internal/adapters/runner/harness/h1.go` satisfying `eval.Arm`. An eval replay with `--arm h1` runs the full durable loop (`spec` -> `implement` -> `verify` -> `review`) inside the eval harness, allowing direct benchmark comparison against A0, A1, and A2.

### Rationale
- Fulfills the "Measure as we go" mandate of constitution v3.0.0 without extra scaffolding.
