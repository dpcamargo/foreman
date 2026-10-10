# Tasks: Routing, Provider Health, and Test-First

**Input**: Design documents from `specs/004-routing-test-first/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/router-and-policy.md

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

---

## Phase 1: Setup

**Purpose**: Schema and policy infrastructure

- [X] T001 Write `migrations/0006_routing.sql`: `providers`, `routes`, `routing_stats`, `test_author_results` tables; `tasks` gains `lane`, `task_class`, `risk_level`, `triage_source`, `accumulated_usd`, `not_before` columns; `nodes` gains `not_before` column and `test_author` in name CHECK

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core types that all user stories depend on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T002 [P] Create `internal/core/router/health.go`: `ProviderHealth` struct, `ProviderStatus` type (`ok`/`cooling_down`/`quota_low`), `RecordFailure()` (429/plan-limit/5xx/expired-auth → cooldown with reported reset or exponential backoff), `IsEligible()` — with `health_test.go` covering cooldown transitions, auto-recovery, and backoff escalation
- [X] T003 [P] Create `internal/core/router/stats.go`: `RoutingStats` type with Beta-prior `alpha`/`beta`, `PSuccess()` method (alpha/(alpha+beta)), `Update()` (success/failure), `Samples()` — with `stats_test.go` covering prior behavior before 10 samples and posterior after
- [X] T004 [P] Create `internal/core/policy/policy.go`: `Lane` struct, `Policy` struct, `LoadPolicy()` (parse `policy.yaml`, refuse if lane missing/malformed per FR-009), `CheckBudget()` (FR-010) — with `policy_test.go` covering valid/invalid YAML, missing lanes, budget enforcement

**Checkpoint**: Core types ready — user story implementation can now begin

---

## Phase 3: User Story 1 — Route around a rate-limited or exhausted provider automatically (Priority: P1) 🎯 MVP

**Goal**: When a provider returns 429/plan-limit/5xx, the router puts it in cooldown and tries the next eligible route. When all routes are cooling down, the node parks (not fails).

**Independent Test**: Force a 429 on the primary route and confirm automatic failover; confirm provider resumes after cooldown; confirm node parks when all routes are down.

- [X] T005 [US1] Create `internal/core/router/router.go`: `Router` type, `Select()` method (FR-002/003) — ordered candidate list per (role, task class, risk, excluded families), filtered by policy, provider health, and family rules. `argmin cost / P(verified success)` scoring. `ErrNoEligibleRoute` when no candidate qualifies. — with `router_test.go` covering family exclusion, cooldown filtering, cost-aware selection, and no-eligible-route error
- [X] T006 [US1] Implement provider cooldown recording in `internal/core/router/health.go`: `RecordFailure()` with reported-reset-time support and exponential backoff fallback (FR-004). Auto-recovery when `cooldown_until` elapses (FR-004).
- [X] T007 [US1] Add SQLite persistence for provider health and routing stats in `internal/adapters/sqlite/routing.go`: `SaveProviderHealth()`, `LoadProviderHealth()`, `SaveRoutingStats()`, `LoadRoutingStats()`, `RecordRouteOutcome()` (FR-011) — with `routing_test.go` against real SQLite
- [X] T008 [US1] Implement node parking in `internal/core/engine/engine.go`: when `Select()` returns `ErrNoEligibleRoute`, set `not_before` on the node (FR-005), leave status `pending`, don't count an attempt. Reconciler picks up when `not_before` has passed. Operator notified via ntfy only if stall >1h.
- [X] T009 [US1] Wire router into the engine's dispatch path in `internal/core/engine/engine.go`: call `router.Select()` before each node dispatch, handle `ErrNoEligibleRoute` (park), handle cooldown recording on provider errors (FR-001/004). Integrate with `cmd/foreman/serve.go` wiring.

**Checkpoint**: Provider failover, cooldown auto-recovery, and node parking all work

---

## Phase 4: User Story 2 — Write acceptance tests from the spec before implementation starts (Priority: P1)

**Goal**: For lane S/D tasks, a test-author node writes acceptance tests from the spec before implementation. Hidden tests stored outside workspaces. Verifier confirms tests fail on base.

**Independent Test**: Run a lane-S task through test-authoring only and confirm the authored tests fail against the unmodified base commit.

- [ ] T010 [US2] Add `test_author` node type to `internal/core/task/task.go`: node name, `NewLinearNodes()` includes `test_author` before `implement` when lane has `test_first: true` — with `task_test.go` updates
- [ ] T011 [US2] Create `internal/core/engine/testauthor.go`: `TestAuthor` port (interface), `TestAuthorInput`/`TestAuthorResult` types per contracts/router-and-policy.md. Wire into `RunTask` to execute before `implement` for lane S/D (FR-007).
- [ ] T012 [US2] Implement visible/hidden test split in `internal/core/engine/testauthor.go`: `TestAuthorResult` produces `VisiblePatch` (copied into implementer workspace) and `HiddenPatch` (stored outside all workspaces, mounted read-only only into verifier — FR-007). Store in `test_author_results` table via `internal/adapters/sqlite/routing.go`.
- [ ] T013 [US2] Implement base-commit verification of test-author output in `internal/core/engine/testauthor.go`: verifier confirms every hidden test fails on the unmodified base commit (FR-008). If any test passes on base → reject and re-run test-author with that evidence. If any test fails to compile → test-author attempt failure (retry/escalate).
- [ ] T014 [US2] Wire test-author into the engine's node sequence in `internal/core/engine/engine.go`: insert `test_author` node before `implement` when lane has `test_first: true`, using router to select a model family different from the implementer's (FR-007). Integrate with `cmd/foreman/serve.go` wiring.

**Checkpoint**: Test-author runs before implement for lane S/D, hidden tests verified to fail on base

---

## Phase 5: User Story 3 — Spend differently depending on how much a task is worth (Priority: P2)

**Goal**: Lane F gets light review (no test-author), lane S/D gets full review + test-authoring. Budget caps enforced per lane from `policy.yaml`.

**Independent Test**: Run a trivial task (lane F) and a complex task (lane S) side by side; confirm lane F skips test-author and costs less; confirm each respects its budget cap.

- [ ] T015 [US3] Implement lane-aware node sequencing in `internal/core/engine/engine.go`: lane F → skip `test_author`, use `review` with light intensity (single round); lane S/D → include `test_author`, full review (up to two rounds) (FR-009, US3 acceptance 1/2)
- [ ] T016 [US3] Implement budget enforcement in `internal/core/engine/engine.go`: track `accumulated_usd` per task, check `CheckBudget()` before each node dispatch (FR-010). Block with `budget_exceeded` reason when exceeded. Wire `accumulated_usd` updates from run usage reports.
- [ ] T017 [US3] Implement cost-aware route selection in `internal/core/router/router.go`: `argmin cost / P(verified success)` using route pricing (API cost per 1k tokens) and `quota_pressure` penalty for subscription routes (FR-003). Apply `p_min` risk-tier threshold.
- [ ] T018 [US3] Add lane field to task creation in `cmd/foreman/telegram_handlers.go` and `cmd/foreman/tasks.go`: `/task` and `/new` accept optional `--lane F|S|D` flag (default: S). Store in `tasks.lane`. Load lane definition from `policy.yaml` at dispatch time.

**Checkpoint**: Lane differentiation, budget enforcement, and cost-aware routing all work

---

## Phase 6: User Story 4 — Classify triage locally, with zero network dependency (Priority: P2)

**Goal**: Triage (task class + risk) runs on a local in-process classifier. Falls back to API when confidence is low or model is unavailable.

**Independent Test**: Disable network access and confirm `/task` submissions are still triaged correctly.

- [ ] T019 [US4] Create `internal/adapters/classifier/classifier.go`: `Classifier` type, `Load()` (load pinned model file from `$FOREMAN_HOME/models/`), `Classify()` method returning `TriageResult{TaskClass, Risk, Confidence, Source}` (FR-013). Confidence threshold configurable. Fall back to API triage when confidence < threshold or model missing (FR-014).
- [ ] T020 [US4] Implement model pinning in `internal/adapters/classifier/classifier.go`: verify model file checksum at load time (FR-015). Refuse to load if checksum mismatch. Log clearly on model load failure and fall back to API triage (FR-014).
- [ ] T021 [US4] Wire classifier into task creation in `cmd/foreman/tasks.go`: call `classifier.Classify()` during `createTask()`, store `task_class`, `risk_level`, `triage_source` on the task row. Fall back to API triage (existing cheap-model route) when local classifier unavailable.
- [ ] T022 [US4] Validate classifier accuracy against Feature 001's recorded task labels in `internal/adapters/classifier/classifier_test.go`: load a held-out labeled set, confirm accuracy within operator-defined tolerance of the API-based baseline (spec Assumptions).

**Checkpoint**: Local triage works offline, falls back to API when needed

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Integration, wiring, and validation

- [ ] T023 [P] Add `internal/adapters/runner/agy/` package implementing `engine.Implementer` and `engine.Reviewer` (third model family for cross-family rules) — with `agy_test.go`
- [ ] T024 [P] Update `internal/adapters/sqlite/routing.go` to integrate with `runs` table: record `route_id`, `task_class`, `provider`, `verified outcome` per run (FR-011) for Beta-prior updates
- [ ] T025 Wire router, classifier, and policy loading into `cmd/foreman/serve.go`: load `policy.yaml` at startup (refuse if invalid per FR-009), initialize classifier (log fallback if model missing), construct router with provider health + routing stats from SQLite
- [ ] T026 Add `/budget` provider/time-window breakdown to `cmd/foreman/telegram_handlers.go` (FR-012): query cost by provider and by time window
- [ ] T027 Run quickstart.md validation: all 7 scenarios (provider cooldown failover, node parking, test-author before implement, lane differentiation, budget enforcement, local triage, cross-family rules)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup — BLOCKS all user stories
- **US1 (Phase 3)**: Depends on Foundational — no dependency on other stories
- **US2 (Phase 4)**: Depends on Foundational — no dependency on other stories (but benefits from US1's router for family selection)
- **US3 (Phase 5)**: Depends on Foundational — integrates with US1 (router) and US2 (test-author node)
- **US4 (Phase 6)**: Depends on Foundational — independent of US1/US2/US3
- **Polish (Phase 7)**: Depends on all desired user stories

### User Story Dependencies

- **US1 (P1)**: Router + provider health → foundation for US2 (family selection) and US3 (cost-aware selection)
- **US2 (P1)**: Test-author node → uses router for family exclusion (US1), uses lane field (US3)
- **US3 (P2)**: Lane differentiation → uses router (US1), uses test-author node (US2)
- **US4 (P2)**: Local classifier → independent, triage is upstream of everything

### Parallel Opportunities

- T002, T003, T004 are parallel (different core packages)
- T005/T006 (router) and T019/T020 (classifier) are parallel (different packages)
- US1 and US4 can proceed in parallel after Foundational
- US2 and US3 can proceed in parallel after Foundational (with coordination on node sequencing)

---

## Implementation Strategy

### MVP First (US1 Only)

1. Complete Phase 1: Setup (migration)
2. Complete Phase 2: Foundational (health, stats, policy)
3. Complete Phase 3: US1 (router + failover + parking)
4. **STOP and VALIDATE**: provider failover works, node parking works
5. Deploy if ready

### Incremental Delivery

1. Setup + Foundational → foundation ready
2. US1 → provider failover works → deploy
3. US2 → test-author works → deploy
4. US3 → lane differentiation works → deploy
5. US4 → local triage works → deploy

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
