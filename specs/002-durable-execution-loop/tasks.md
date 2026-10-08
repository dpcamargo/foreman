# Tasks: Durable Execution Loop (Feature 002)

**Input**: Design documents from `/specs/002-durable-execution-loop/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

All tasks follow test-first development (AGENTS.md): write tests first, ensure they fail before implementing.

## Format: `[ID] [P?] [Story] Description`
- **[P]**: Parallelizable (different files, no dependencies)
- **[Story]**: User story identifier (`[US1]`, `[US2]`, `[US3]`)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project setup and schemas for Feature 002

- [X] T001 Verify existing schemas `schemas/task_spec.json` and `schemas/review_findings.json` conform to draft-07
- [X] T002 [P] Create test fixtures and sample task definitions in `testdata/fixtures/addrepo/task.yaml` and `task_failing_hidden.yaml`
- [X] T003 [P] Add `.foreman/project.yaml` fixture in `testdata/fixtures/addrepo/project.yaml`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Database schema, core domain entities, and store adapter required by all stories

- [X] T004 Write migration `migrations/0003_tasks_and_nodes.sql` adding `tasks`, `nodes`, `runs`, and extending `events` per data-model.md
- [X] T005 [P] Write unit tests in `internal/core/task/task_test.go` covering Task, Node, and Run state transitions, validation, and status constraints
- [X] T006 Implement domain types, statuses, and state machines in `internal/core/task/task.go` and ports in `internal/core/task/ports.go` to pass T005
- [X] T007 [P] Write store tests in `internal/adapters/sqlite/task_store_test.go` covering `CreateTask`, `UpdateTaskStatus`, `NodesForTask`, `UpdateNodeStatus`, and atomic event insertion in same transaction
- [X] T008 Implement `internal/adapters/sqlite/task_store.go` to pass T007
- [X] T009 Update `internal/archtest/archtest_test.go` to ensure `internal/core/task` imports only standard library and fails if adapters are imported

**Checkpoint**: Foundation ready - task and node state persistence verified.

---

## Phase 3: User Story 2 - Trust "Done" Only via Container Verification (Priority: P1)

**Goal**: Provide deterministic verification via diff policy checks and containerized hidden tests before any attempt is considered passing.

**Independent Test**: Feed the verifier patches with test deletions, protected path edits, and hidden test failures; assert each fails with specific policy/verifier evidence without leaking hidden test code.

### Tests for User Story 2

- [ ] T010 [P] [US2] Write unit tests in `internal/core/verify/policy_test.go` testing diff policy rules: test file deletion detection, protected paths matching, secret scanning with core redactor, and symlink escape rejection
- [ ] T011 [P] [US2] Write unit tests in `internal/core/verify/config_test.go` testing `.foreman/project.yaml` parsing and validation
- [ ] T012 [US2] Write container tests in `internal/adapters/container/verifier_test.go` (-tags container) verifying offline execution (`--network=none`), visible test execution, and hidden test mounting

### Implementation for User Story 2

- [ ] T013 [P] [US2] Implement `.foreman/project.yaml` parser in `internal/core/verify/config.go` to pass T011
- [ ] T014 [US2] Implement diff policy engine in `internal/core/verify/policy.go` to pass T010
- [ ] T015 [US2] Implement container verifier adapter in `internal/core/verify/verifier.go` wrapping the Feature 001 grader with diff policy checks to pass T012

**Checkpoint**: User Story 2 complete. Verifier reliably rejects test deletion, protected edits, secrets, and failing tests.

---

## Phase 4: User Story 1 - Run One Task End to End (Priority: P1) 🎯 MVP

**Goal**: Execute `fm run` end to end through `spec` -> `implement` -> `verify` -> `review` with escalation ladder rungs 1 through 3 and cross-family review.

**Independent Test**: Run a task with acceptance criteria on `addrepo` fixture; assert `TaskSpec` is generated, attempt executed in fresh clone, verified offline, reviewed across families, and reported.

### Tests for User Story 1

- [ ] T016 [P] [US1] Write unit tests in `internal/core/engine/spec_test.go` testing `TaskSpec` grounding check on base commit (cited files and verify commands validation)
- [ ] T017 [P] [US1] Write unit tests in `internal/core/engine/ladder_test.go` testing escalation rungs 1-3, failure classification (`infra`, `provider`, `attempt`, `policy`), and loop guards (signature repeat, diff oscillation, pass count flatline)
- [ ] T018 [P] [US1] Write unit tests in `internal/core/engine/review_test.go` testing cross-family model pairing and downgrading of major findings lacking repro/evidence
- [ ] T019 [US1] Write unit tests in `internal/core/engine/engine_test.go` testing the full linear run loop (`spec` -> `implement` -> `verify` -> `review`) with fakes for all ports

### Implementation for User Story 1

- [ ] T020 [P] [US1] Define ports (`Specifier`, `Implementer`, `Verifier`, `Reviewer`) and types in `internal/core/engine/ports.go`
- [ ] T021 [US1] Implement spec grounding validation in `internal/core/engine/spec.go` to pass T016
- [ ] T022 [US1] Implement escalation ladder and loop guards in `internal/core/engine/ladder.go` to pass T017
- [ ] T023 [US1] Implement cross-family review coordinator in `internal/core/engine/review.go` to pass T018
- [ ] T024 [US1] Implement main execution engine loop in `internal/core/engine/engine.go` to pass T019
- [ ] T025 [P] [US1] Implement task reporting in `internal/core/engine/report.go` writing markdown and JSON reports
- [ ] T026 [US1] Extend `internal/adapters/runner/codex/codex.go` to implement `Specifier`, `Implementer`, and `Reviewer` ports
- [ ] T027 [US1] Extend `internal/adapters/runner/hermes/oneshot.go` to implement `Reviewer` and `Implementer` ports for Claude route
- [ ] T028 [US1] Implement `internal/adapters/runner/harness/h1.go` satisfying Feature 001's `eval.Arm` interface (arm `H1`)

**Checkpoint**: User Story 1 functional with complete execution loop and cross-family review.

---

## Phase 5: User Story 3 - Crash Safety & Resumability (Priority: P2)

**Goal**: Survive process interruption or machine sleep; detect dead PIDs, mark lost runs, and resume from clean clone without state corruption.

**Independent Test**: Start `fm run`, kill with SIGKILL mid-attempt, re-run; assert dead run marked `infra_error (lost)` and cleanly restarted from base commit.

### Tests for User Story 3

- [ ] T029 [P] [US3] Write unit tests in `internal/core/engine/reconcile_test.go` verifying startup sweep marks dead PIDs as lost and leaves active PIDs untouched
- [ ] T030 [US3] Write integration tests in `cmd/fm/run_test.go` covering CLI flag parsing, task file ingestion, and interrupted run resumption

### Implementation for User Story 3

- [ ] T031 [US3] Implement startup reconciler sweep in `internal/core/engine/reconcile.go` using `internal/adapters/proc` to pass T029
- [ ] T032 [US3] Implement `fm run` command in `cmd/fm/run.go` wiring sqlite, sandbox, container verifier, codex, hermes, and engine
- [ ] T033 [US3] Wire Feature 001 task record emission (`RecordStore.AddRecord`) in `cmd/fm/run.go` upon task completion (FR-021)
- [ ] T034 [US3] Add `run` subcommand dispatch to `cmd/fm/main.go`

**Checkpoint**: User Stories 1, 2, and 3 complete. `fm run` is fully resilient and resumable.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: End-to-end verification, quality gates, and cleanup

- [ ] T035 [P] Run secret redaction check across run directories and report outputs (`grep -E` for token patterns)
- [ ] T036 Execute quickstart steps 1 through 7 in `specs/002-durable-execution-loop/quickstart.md` against real Docker and Codex
- [ ] T037 Run `gofmt -l .`, `go vet ./...`, and `go test ./...` ensuring zero issues and 100% test pass
- [ ] T038 Measure per-attempt overhead on warm cache against the 60s performance goal
- [ ] T039 Update `specs/ROADMAP.md` recording Feature 002 implementation progress and test results

---

## Dependencies & Execution Order

### Phase Dependencies
- **Setup (Phase 1)**: Can start immediately.
- **Foundational (Phase 2)**: Depends on Phase 1. Blocks all user stories.
- **User Story 2 (Phase 3)**: Depends on Phase 2. Provides the verifier and diff-policy needed by the engine.
- **User Story 1 (Phase 4)**: Depends on Phase 2 and Phase 3 (Verifier port implementation).
- **User Story 3 (Phase 5)**: Depends on Phase 4 (Engine loop wiring and CLI).
- **Polish (Phase 6)**: Depends on all user stories completed.

### Parallel Opportunities
- In Phase 1: T002 and T003 can run in parallel.
- In Phase 2: T005, T007, and T009 can be developed in parallel.
- In Phase 3: T010, T011, and T013 can run in parallel.
- In Phase 4: T016, T017, T018 can run in parallel; T020 and T025 can run in parallel.
- In Phase 5: T029 can run in parallel with CLI wiring prep.

---

## Implementation Strategy

### MVP Scope (Phases 1-4)
1. Setup + Foundation: Schema, Task/Node models, SQLite store.
2. US2 Verifier: Diff-policy checks, container verification.
3. US1 Engine: Spec grounding, implement attempt, ladder rungs 1-3, cross-family review.
4. **Stop and Validate**: Run `fm run testdata/fixtures/addrepo/task.yaml` end-to-end.

### Incremental Delivery
- PR 1: Phases 1 & 2 (Foundation).
- PR 2: Phase 3 (US2 Verifier & Policy).
- PR 3: Phase 4 (US1 Engine Loop & Ladder).
- PR 4: Phase 5 (US3 CLI Wiring & Resumability).
- PR 5: Phase 6 (Polish & Documentation).
