# Tasks: Daemon + Telegram Control Plane (Feature 003)

**Input**: Design documents from `/specs/003-daemon-telegram-control/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

All tasks follow test-first development (AGENTS.md): write tests first, ensure they fail before implementing.

## Format: `[ID] [P?] [Story] Description`
- **[P]**: Parallelizable (different files, no dependencies)
- **[Story]**: User story identifier (`[US1]`, `[US2]`, `[US3]`, `[US4]`)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: New binary skeleton and static deploy artifact

- [X] T001 [P] Create `cmd/foreman/main.go` skeleton (exit codes, `usage()`, `run(args, stdout, stderr) int` returning usage help only — no wiring yet), mirroring `cmd/fm/main.go`'s existing pattern
- [X] T002 [P] Create `deploy/launchd/com.foreman.plist` referencing the `cmd/foreman` binary path, `$FOREMAN_HOME`, and `KeepAlive`/`RunAtLoad` for launchd restart-on-crash (ARCHITECTURE.md §14)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, core control-plane types, the Supervisor, and daemon startup sequencing that every user story depends on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T003 Write `migrations/0004_daemon_control.sql`: rebuild `tasks` with `'paused'`/`'cancelled'` added to its status CHECK constraint (SQLite table-rebuild pattern); create `approvals`, `allowlist`, `outbox_cursors`, `deliveries` per data-model.md
- [X] T004 [P] Write `internal/core/task/task_test.go` additions: table-driven tests for `Task.Pause(now)`, `Task.Resume(now)`, `Task.Cancel(now)` transition validity (e.g. cannot cancel an already-cancelled task, cannot resume a task that isn't paused)
- [X] T005 [P] Extend `internal/core/task/task.go`: add `StatusPaused`, `StatusCancelled` constants and the `Pause`/`Resume`/`Cancel` methods to pass T004
- [X] T006 [P] Write `internal/core/control/approval_test.go`: table-driven tests for `ValidateApproval` — pending-only, nonce mismatch, subject-sha256 mismatch (stale artifact), success case (contracts/approval-nonce.md)
- [X] T007 [P] Create `internal/core/control/approval.go`: `Approval` struct, nonce generation (`crypto/rand`, hex-encoded), `ValidateApproval(a Approval, suppliedNonce, currentSubjectSHA256 string) error` using `crypto/subtle.ConstantTimeCompare` for the nonce check, to pass T006
- [X] T008 [P] Write `internal/core/control/allowlist_test.go`: unit tests for `IsAllowed(userID int64)` against a fake store (allowed id, unknown id, zero id)
- [X] T009 [P] Create `internal/core/control/allowlist.go`: `Allowlist` type declaring the store interface it needs and `IsAllowed`, to pass T008
- [X] T010 [P] Write `internal/core/control/outbox_test.go`: unit tests for coalescing multiple events after a cursor position into one digest batch, and a no-op when there are no new events
- [X] T011 [P] Create `internal/core/control/outbox.go`: pure `AdvanceOutbox(events []Event, cursor int64) (digest Digest, newCursor int64)` function, to pass T010
- [X] T012 Write `internal/core/engine/supervisor_test.go`: unit tests with the existing `fakeStore`/`fakeCloner` pattern from `engine_test.go` — bounded concurrency (a 3rd dispatched task waits for a slot), `Cancel(taskID)` aborts a running dispatch via context cancellation, graceful pause stops before the next ladder attempt without killing the current one, and a task whose `engine.Config.ApprovalGate` blocks is resumed once the gate function returns
- [X] T013 Create `internal/core/engine/supervisor.go`: `Supervisor` with a bounded semaphore (default concurrency 2), `Dispatch(ctx, task.Task)` running `engine.RunTask` in a goroutine, `map[taskID]context.CancelFunc`, `Cancel(taskID)`, `PauseGraceful(taskID)`/`ResumeGraceful(taskID)`, to pass T012
- [X] T014 Extend `internal/core/engine/ports.go`/`engine.go`: add optional `Config.ApprovalGate func(ctx context.Context, taskID string, spec TaskSpec) (approved bool, err error)` field, called once after `ValidateTaskSpec` succeeds and before the implement ladder starts; confirm all existing `engine_test.go` cases still pass unmodified with the field left `nil` (research.md R7)
- [X] T015 [P] Write `internal/adapters/sqlite/control_store_test.go`: adapter tests against real SQLite for approvals/allowlist/outbox_cursors/deliveries CRUD (existing `openTest(t)` pattern from `task_store_test.go`)
- [X] T016 [P] Create `internal/adapters/sqlite/control_store.go`: implements the store interfaces `internal/core/control` declares, to pass T015
- [X] T017 Write `internal/adapters/localapi/server_test.go`: a same-UID unix-socket connection is served; a connection whose peer UID doesn't match the listener's own process UID is closed with no response (simulate via a second process or a stubbed peer-cred check in the test)
- [X] T018 Create `internal/adapters/localapi/server.go`: unix-socket listener at `$FOREMAN_HOME/state/foreman.sock` (mode `0700`), `LOCAL_PEERCRED`-equivalent check via `golang.org/x/sys/unix` before any request is read, `net/http` routing skeleton with no story-specific handlers yet, to pass T017 (FR-013; promotes `golang.org/x/sys/unix` to a direct dependency per plan.md Complexity Tracking)
- [X] T019 Wire `cmd/foreman/main.go`: open the sqlite store, run `engine.Reconciler.Sweep` before accepting any new dispatch (FR-010), construct the `Supervisor`, start the `localapi.Server` listener, install a SIGTERM/SIGINT handler that stops accepting new dispatch and exits cleanly for `launchd` to restart

**Checkpoint**: Foundation ready — user story implementation can now begin.

---

## Phase 3: User Story 1 - Create and track a task from my phone (Priority: P1) 🎯 MVP

**Goal**: `/task <repo> <text>` from an allowlisted Telegram chat creates a task and returns an
id within seconds; `/status`/`/tasks` read it back.

**Independent Test**: Send `/task` from a Telegram chat on the allowlist with the daemon
otherwise idle; observe the immediate acknowledgement and at least one subsequent pushed update.

### Tests for User Story 1

- [X] T020 [P] [US1] Write `internal/adapters/telegram/commands_test.go`: table-driven tests parsing `/task <repo> <text>`, `/status [id]`, `/tasks`, `help`; a non-allowlisted sender and a non-private-chat sender get no reply at all (FR-003)
- [X] T021 [P] [US1] Write `internal/adapters/telegram/client_test.go`: against `httptest.Server` — `getUpdates` long-poll loop resumes from the last `update_id` after a simulated error, `sendMessage` posts the expected JSON body

### Implementation for User Story 1

- [X] T022 [US1] Create `internal/adapters/telegram/client.go`: `Client` wrapping `net/http` for `getUpdates` (25s long-poll timeout, backoff on error), `sendMessage`, `editMessageReplyMarkup`, `answerCallbackQuery`, to pass T021
- [X] T023 [US1] Create `internal/adapters/telegram/commands.go`: command parser/dispatcher for `/task`, `/status`, `/tasks`, `help`; checks `control.Allowlist.IsAllowed` and `chat.type == "private"` before any command executes, to pass T020
- [X] T024 [US1] Wire `/task` in `cmd/foreman`: on receipt, create a `task.Task` row (`status=pending`) via the sqlite store, record a `TaskCreated` event, call `Supervisor.Dispatch`, reply with the new task id
- [X] T025 [US1] Wire `/status [id]` and `/tasks` in `cmd/foreman`: read task/node state via the sqlite store, format the reply (current phase, attempts, tokens/cost so far; omitted id means the most recently created task)
- [X] T026 [US1] Implement `POST /v1/tasks`, `GET /v1/tasks/{id}`, `GET /v1/tasks` on `internal/adapters/localapi/server.go` (contracts/local-api.md; mirrors T024/T025's semantics for the local `fm` CLI)

**Checkpoint**: User Story 1 functional and independently testable — a task created from
Telegram is tracked end to end through Feature 002's loop.

---

## Phase 4: User Story 2 - Approve or reject without an LLM anywhere near the decision (Priority: P1)

**Goal**: A spec-approval gate shows nonce-bound inline buttons; a stale nonce or a
since-changed artifact is rejected; free text never approves anything.

**Independent Test**: Trigger an approval gate, change the underlying spec/diff after the
approval request is sent (simulating a race), confirm the bot rejects a stale approval attempt
bound to the old nonce.

### Tests for User Story 2

- [X] T027 [P] [US2] Write `internal/adapters/telegram/commands_test.go` additions: `/reject <id> <reason>` requires a reason (prompts if omitted, does not reject until supplied — FR-011), `/answer <id> <text>` parses correctly, free text gets the fixed "chat isn't enabled yet; use /commands" reply and is never routed to the approval path (FR-006)
- [X] T028 [P] [US2] Write `internal/core/engine/supervisor_test.go` additions: a task whose spec requires approval blocks at the `ApprovalGate` hook until `Supervisor` is told the approval resolved (approved or rejected), and a rejected approval fails the task without entering the implement ladder

### Implementation for User Story 2

- [X] T029 [US2] Extend `internal/adapters/telegram/commands.go`: inline-button callback handling (`[Approve] [Reject] [Edit]`), `/reject <id> <reason>`, `/answer <id> <text>`; callback data carries `{approval_id, nonce}` and is validated via `control.ValidateApproval` before anything is applied
- [X] T030 [US2] Implement `POST /v1/approvals/{id}/respond` on `internal/adapters/localapi/server.go`, calling the same `control.ValidateApproval` path as T029 (contracts/approval-nonce.md: one enforcement point, not two)
- [X] T031 [US2] Wire the spec-approval gate in `cmd/foreman`: set `engine.Config.ApprovalGate` to a function that creates an `Approval` row (`kind=spec`, `subject_sha256` of the generated `TaskSpec`), sends the Telegram message with bound buttons, and blocks on the `Supervisor`'s per-approval wait until T029/T030 resolves it (spec.md Assumptions: the hard-coded stub rule — every Telegram-created task gets a spec-approval gate, since Feature 002 has no lane concept yet)

**Checkpoint**: User Stories 1 AND 2 both work independently — tasks are created, and their
spec gate is approved or rejected only through a bound button, never free text.

---

## Phase 5: User Story 3 - Pause, resume, or cancel from anywhere, and recover cleanly from a restart (Priority: P2)

**Goal**: `/pause`, `/pause now`, `/resume`, `/cancel` control dispatch and in-flight work; a
killed daemon recovers all leases on restart with no operator action beyond restarting it.

**Independent Test**: Send `/pause`, confirm no new nodes dispatch while in-flight ones finish;
send `/pause now`, confirm running processes are killed and their runs marked
infra-cancelled-and-retriable; kill the daemon process entirely, restart it, confirm the
reconciler recovers all leases.

### Tests for User Story 3

- [X] T032 [P] [US3] Write `internal/adapters/telegram/commands_test.go` additions: `/pause [id|all] [now]`, `/resume [id]`, `/cancel <id>` parse correctly, including the `all` target
- [ ] T033 [US3] Write a `cmd/foreman` integration test (`cmd/foreman/run_test.go`, mirroring `cmd/fm/run_test.go`'s `TestRunInterruptedResumption` pattern): start a task, SIGKILL the daemon process mid-attempt, restart it, confirm the dead run is marked `infra_error` (lost) via the reconciler and the task is retried cleanly from a fresh clone — no duplicate or inconsistent rows

### Implementation for User Story 3

- [X] T034 [US3] Extend `internal/adapters/telegram/commands.go`: `/pause [id|all] [now]`, `/resume [id]`, `/cancel <id>`, to pass T032
- [X] T035 [US3] Wire `/pause`/`/resume`/`/cancel` in `cmd/foreman`: `/pause <id>` calls `Supervisor.PauseGraceful`; `/pause <id> now` calls `Supervisor.Cancel` and marks the resulting run infra-cancelled-and-retriable (not an attempt failure, FR-008); `/cancel <id>` calls `Supervisor.Cancel`, marks the task `status=cancelled` via `Task.Cancel`, and retains its workspace (no immediate deletion — the 7-day sweep is Polish phase, FR-009)
- [X] T036 [US3] Implement `POST /v1/tasks/{id}/pause`, `POST /v1/tasks/{id}/resume`, `POST /v1/tasks/{id}/cancel` on `cmd/foreman/api.go`'s mux (same file as the other `/v1/tasks` routes added in Phase 3/US2, not `internal/adapters/localapi/server.go` which is the generic unix-socket transport — mirrors T030's precedent), mirroring T035
- [ ] T037 [US3] Confirm (no new code expected; add a regression test if a gap is found) that `cmd/foreman/main.go`'s startup sequencing from T019 already satisfies T033 end to end — the reconciler sweep must run and complete before `Supervisor.Dispatch` is reachable from any adapter

**Checkpoint**: All three user stories independently functional — pause/resume/cancel work, and
a crash recovers cleanly.

---

## Phase 6: User Story 4 - Deliver a verified result as a draft PR, merge only on approval (Priority: P1)

**Goal**: A succeeded, reviewed task becomes a draft PR on `agent/<task>`; merging to the
default branch needs a second nonce-bound approval plus a confirmation step; no other
component ever holds the GitHub push credential.

**Independent Test**: Run one task with acceptance tests end to end from Telegram; confirm a
draft PR appears on an `agent/*` branch with spec/findings/evidence attached, main is untouched,
and a merge happens only after the bound button is tapped and confirmed.

### Tests for User Story 4

- [ ] T038 [P] [US4] Write `internal/adapters/integrator/integrator_test.go`: applying a patch to `agent/<task>` in a test bare mirror uses the hardened git invocation (hooks disabled, no system/global config — same env as `internal/adapters/sandbox/git.go`'s `gitEnv()`); opening a draft PR posts the expected JSON to a fake GitHub HTTP server (`httptest.Server`); a push failure is classified as an infra/provider failure, retried with backoff, and never discards or regenerates the verified patch (FR-017)
- [ ] T039 [P] [US4] Write `internal/core/control/approval_test.go` additions: a `merge` approval's `subject_sha256` is the delivered diff's hash, not the task id — a since-changed diff invalidates a previously-issued merge approval the same way a changed spec invalidates a spec approval (SC-002, now exercised against `deliveries.diff_sha256`)

### Implementation for User Story 4

- [ ] T040 [US4] Create `internal/adapters/integrator/integrator.go`: applies the verified patch to `agent/<task>` in the existing per-repo bare mirror using `sandbox`'s hardened git helpers, pushes the branch, opens a draft PR via `POST /repos/{owner}/{repo}/pulls` (plain `net/http`, bearer token from the `foreman` OS user's environment — never the workspace or verifier, FR-014), to pass T038
- [ ] T041 [US4] Wire delivery in `cmd/foreman`: on a task's `engine.RunTask` returning `StatusSucceeded`, create a `Delivery` row and a `deliver`-kind `Approval` (same gate pattern as T031, now after `RunTask` returns rather than inside it — research.md R7); on approval, call the integrator (T040) and update `deliveries.status`/`pr_url`
- [ ] T042 [US4] Wire merge in `cmd/foreman`: on **[Merge]** tap, validate the nonce against `deliveries.diff_sha256` via `control.ValidateApproval` (T039), then require one additional confirmation round-trip before calling the GitHub REST API's merge endpoint (FR-016 — never a single tap)
- [ ] T043 [US4] Implement `/diff <id>` in `internal/adapters/telegram/commands.go`: sends the task's current patch as a `.patch` file attachment (Telegram's 4096-character message cap)

**Checkpoint**: All four user stories independently functional. `foreman` can take a task from
a Telegram message to a merged PR, gated by two separate nonce-bound approvals.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Retention, housekeeping, and end-to-end quality gates

- [ ] T044 [P] Write `internal/core/control/retention_test.go`: pure tests for the retention decision function — given a kind (transcript/container-output/successful-workspace/cancelled-workspace) and an age, returns whether it should be swept, per data-model.md's Retention section (90d / 7d-configurable-to-14 / immediate-on-success / 7d respectively); structured SQLite rows are never selected for deletion by this function
- [ ] T045 Create `internal/core/control/retention.go`, to pass T044; wire a daemon housekeeping tick in `cmd/foreman/main.go` that calls it against `$FOREMAN_HOME/work/runs/*` and `state/reports/*`
- [ ] T046 [P] Implement the unanswered-approval secondary-channel ping: `internal/adapters/ntfy/ntfy.go` (one `net/http` POST), wired into the housekeeping tick from T045 for any `approvals` row `pending` past the configured threshold (default 4h, spec.md Assumptions) — never auto-resolves the approval itself
- [ ] T047 [P] Implement `/logs <id> [node]` and `/budget` in `internal/adapters/telegram/commands.go`
- [ ] T048 Run a secret-redaction check across `$FOREMAN_HOME/work/runs/` and `state/reports/` after a full quickstart pass (same `grep -E` token-pattern check as Feature 002's T035)
- [ ] T049 Execute quickstart.md steps 1 through 7 against a real Telegram bot token and a real GitHub fine-grained PAT on a disposable test repo (operator action — needs the allowlist bootstrap and, for unattended testing, the dedicated `foreman` OS user per ADR 0001)
- [ ] T050 Run `gofmt -l .`, `go vet ./...`, and `go test ./...` ensuring zero issues and 100% test pass, including `internal/archtest` covering the new `internal/core/control` package
- [ ] T051 Update `specs/ROADMAP.md` recording Feature 003 implementation progress and quickstart results

---

## Dependencies & Execution Order

### Phase Dependencies
- **Setup (Phase 1)**: Can start immediately.
- **Foundational (Phase 2)**: Depends on Phase 1. Blocks all user stories.
- **User Story 1 (Phase 3)**: Depends on Phase 2.
- **User Story 2 (Phase 4)**: Depends on Phase 2 and Phase 3 (needs a task to exist and the
  Telegram command dispatcher from US1).
- **User Story 3 (Phase 5)**: Depends on Phase 2 (the Supervisor); independent of US2's approval
  gate, but exercised against tasks created the US1 way.
- **User Story 4 (Phase 6)**: Depends on Phase 2 and Phase 4 (reuses the approval mechanism
  US2 builds).
- **Polish (Phase 7)**: Depends on all four user stories being complete.

### Parallel Opportunities
- In Phase 1: T001 and T002 can run in parallel.
- In Phase 2: T004–T011 (task/control core types and their tests) can be developed in parallel;
  T015–T016 (sqlite control store) can run in parallel with T017–T018 (local API) once T003's
  migration lands.
- In Phase 3: T020 and T021 (tests) can run in parallel.
- In Phase 4: T027 and T028 can run in parallel.
- In Phase 6: T038 and T039 can run in parallel.
- In Phase 7: T044, T046, and T047 can run in parallel.

---

## Parallel Example: Phase 2 (Foundational)

```bash
# Launch the independent core-type test+implementation pairs together:
Task: "Write internal/core/task/task_test.go additions for Pause/Resume/Cancel"
Task: "Write internal/core/control/approval_test.go for ValidateApproval"
Task: "Write internal/core/control/allowlist_test.go for IsAllowed"
Task: "Write internal/core/control/outbox_test.go for AdvanceOutbox"
```

---

## Implementation Strategy

### MVP Scope (Phases 1–3)
1. Setup + Foundational: schema, task/control core types, Supervisor, daemon startup order.
2. US1: Telegram `/task`/`/status`/`/tasks` end to end.
3. **Stop and validate**: send `/task` from an allowlisted chat, confirm the acknowledgement and
   at least one pushed update, per spec.md's own Independent Test for US1.

### Incremental Delivery
- PR 1: Phases 1–2 (Setup + Foundational).
- PR 2: Phase 3 (US1 — the MVP: create and track from Telegram).
- PR 3: Phase 4 (US2 — nonce-bound approvals).
- PR 4: Phase 5 (US3 — pause/resume/cancel/crash recovery).
- PR 5: Phase 6 (US4 — delivery and merge). **Rollout risk**: this PR is the first to touch real
  GitHub state (push + PR creation); quickstart.md step 6 requires testing against a disposable
  repo before any real one, and the integrator's token must be scoped to that repo only before
  this PR lands anywhere the operator cares about.
- PR 6: Phase 7 (Polish — retention, secondary-channel ping, full quickstart, docs).
