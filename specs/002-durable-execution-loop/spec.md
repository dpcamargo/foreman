# Feature Specification: Durable Execution Loop (fm run MVP)

**Feature Branch**: `002-durable-execution-loop`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Build the smallest durable execution loop that tests the core
hypothesis: fm run CLI, SQLite state (tasks/runs/events), a clone-per-run manager running as a
dedicated OS user, codex and claude runners with structured output, a spec call, a no-network
containerized verifier with hidden tests and diff-policy checks, cross-family review, and
escalation ladder rungs 1 through 3, with markdown and JSON reports."

**Source**: `ARCHITECTURE.md` §1 ("the biggest correction"), §3.1–3.2/3.7/3.9/3.12, §7.5,
§13 Phase 1, §16.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run one task end to end and get a verified result (Priority: P1)

As the operator, I invoke `fm run <task.yaml> --arm harness` on one eval task (from Feature
001's suite) and get back, deterministically: a spec, an implementation attempt from a
headless coding CLI, a verification report from a clean no-network container, and a
cross-family review — recorded in SQLite, not just printed to a terminal.

**Why this priority**: This is the entire point of Feature 002 — proving that
spec + test-first-adjacent verification + cross-family review + bounded retries, run by
deterministic code, is the minimum viable version of the architecture's central correction
(Principle I/II of the constitution). Without this, nothing else in the roadmap has a
foundation to build on.

**Independent Test**: Can be fully tested by running `fm run` against a single task from
Feature 001's suite with no daemon, no Telegram, and no memory service running, and inspecting
the resulting SQLite rows and markdown report.

**Acceptance Scenarios**:

1. **Given** a task description and a target repo pinned to a base commit, **When** `fm run`
   executes, **Then** it produces, in order: a `TaskSpec` (acceptance criteria + a verification
   plan), one implement attempt in a fresh clone owned by a dedicated OS user, a `VerifyReport`
   from a container with `--network=none`, and a `ReviewFindings` from a model family different
   from the implementer's.
2. **Given** the verifier reports a failing hidden test, **When** the engine processes that
   result, **Then** it creates a new attempt on the SAME route carrying the failure evidence
   (not a fresh unguided retry), counted as rung 1 of the escalation ladder.
3. **Given** two consecutive attempts on the same route still fail verification, **When** the
   engine processes the second failure, **Then** it escalates to a different model family
   (rung 2), starting fresh from the spec plus an evidence summary, without passing the
   previous diff (no anchoring).
4. **Given** rung 2 also fails verification, **When** the engine processes that failure
   (rung 3), **Then** the task is marked for human attention with the accumulated evidence,
   rather than looping indefinitely.

---

### User Story 2 - Trust "done" only when a container says so (Priority: P1)

As the operator, I never have to take an agent's word that something works: every implement
attempt is followed by a verification pass that runs the real build, lint, and tests —
including a hidden subset the implementer never saw — inside a container with no network
access, and the diff is checked against policy (no test deletion, no protected-path edits, no
secrets, no symlink escapes) before anything is considered passing.

**Why this priority**: This directly implements constitution Principle II (execution-based
verification decides "done") and Principle VI (isolation by default) — the two principles the
architecture calls the most important correction to the originally proposed design.

**Independent Test**: Can be tested by feeding the verifier a patch that passes visible tests
but fails a hidden test, and confirming the run is marked failed, not succeeded, with the
specific hidden-test failure recorded as evidence (but the hidden test's source not leaked to
the implementer on the next attempt).

**Acceptance Scenarios**:

1. **Given** a patch that satisfies all visible tests but fails a hidden test, **When** it is
   verified, **Then** the `VerifyReport` records the hidden-test failure, the node is NOT
   marked succeeded, and the implementer's next attempt gets the failing behavior/assertion
   message but not the hidden test's source code.
2. **Given** a patch that deletes an existing test file, **When** the diff-policy check runs,
   **Then** verification fails on that ground alone, independent of whether the tests pass.
3. **Given** a patch that touches a path marked protected in `.foreman/project.yaml`,
   **When** the diff-policy check runs, **Then** verification fails and the event is recorded
   distinctly from a normal test failure.
4. **Given** the verifier container has no network access, **When** any run attempts an
   outbound connection during verification, **Then** that attempt fails closed (connection
   refused/unreachable), not silently allowed.

---

### User Story 3 - Survive a crash or a laptop sleep without corrupting state (Priority: P2)

As the operator, if `fm run` is killed mid-attempt, or the laptop sleeps during a run, I can
re-invoke the CLI and it picks up cleanly: no run is left half-applied, no SQLite row is left
in an ambiguous state, and the fresh clone for the interrupted attempt is simply redone.

**Why this priority**: Implements constitution Principle VII (durable & resumable state) —
required before any unattended/background use of this loop is trustworthy, and explicitly
called out in ARCHITECTURE.md §0 as a fact about the operator's environment (a laptop on
battery that sleeps and moves) that the design must accommodate.

**Independent Test**: Can be tested by starting `fm run`, killing the process mid-attempt
(SIGKILL), and re-invoking it; the run resumes or cleanly restarts the interrupted attempt with
no duplicate or inconsistent database rows.

**Acceptance Scenarios**:

1. **Given** a run process is killed (SIGKILL) while an implement attempt is in progress,
   **When** `fm run` is invoked again, **Then** it detects the dead process (via PID plus
   start-time fingerprint, not a reused PID), marks the attempt as lost, and retries it as an
   infrastructure failure (not counted against the attempt ladder).
2. **Given** a state transition is in progress when the process exits, **When** the process
   restarts, **Then** the SQLite WAL ensures the task/run row reflects either the fully-applied
   old state or the fully-applied new state — never a partial write.
3. **Given** a workspace clone from an interrupted attempt still exists on disk, **When** the
   attempt is retried, **Then** a fresh clone is made from the pinned base commit rather than
   reusing the possibly-corrupted interrupted workspace.

---

### Edge Cases

- What happens when the spec call itself produces a spec whose cited files or verification
  commands don't exist on the base commit? The engine MUST fail the spec step with that
  specific validation error rather than passing an unverifiable spec to the implementer.
- How does the system handle an implementer CLI that exits 0 but produces no patch (a silent
  no-op)? This MUST be recorded distinctly from both "succeeded" and "attempt failure" so it
  doesn't masquerade as either.
- What happens when the verifier container itself fails to start (for example, Docker daemon
  down)? This MUST be an infrastructure failure, not an attempt failure, and MUST NOT advance
  the escalation ladder.
- What happens when rung 2's "different model family" is unavailable (for example, no Claude
  credentials configured)? `fm run` MUST fail loudly with a clear configuration error rather
  than silently falling back to the same family as rung 1 (which would violate cross-family
  review).
- What happens when the same failure signature repeats on consecutive attempts within one
  rung? Per ARCHITECTURE.md §7.5's loop guards, this MUST skip directly to the next rung rather
  than retrying the identical failing approach a third time.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide an `fm run <task> [--arm <name>]` CLI (no daemon
  required) that executes one task end to end: spec → implement → verify → review → report.
- **FR-002**: The system MUST persist all task, run, and event state in SQLite (WAL mode),
  with each state transition and its corresponding event committed in a single transaction.
- **FR-003**: The system MUST prepare a fresh clone of the target repo, pinned to the task's
  base commit, for every attempt, running under a dedicated OS user with no access to the
  operator's home directory, SSH keys, or credentials.
- **FR-004**: The system MUST call a single strong-model "specify" step that produces a
  `TaskSpec` (acceptance criteria, a verification plan naming concrete commands, and — where
  relevant — a decomposition), and MUST reject a spec whose cited verification commands fail
  to run or whose cited files/symbols don't exist on the base commit.
- **FR-005**: The system MUST provide at least two implementer runners (`codex exec` and
  `claude -p`), each normalizing CLI output to a common result shape: status, structured JSON
  output, usage/cost, transcript path, exported patch, failure class, and failure signature.
- **FR-006**: The system MUST run every verification pass in a container with `--network=none`,
  executing: build, lint/vet, visible tests, then hidden tests (never present in the
  implementer's workspace), then diff-policy checks (no test deletion, no undisclosed
  dependency/lockfile change, no edits to protected paths without approval, a secret scan, no
  symlinks escaping the workspace).
- **FR-007**: The system MUST require the reviewer for any node to be a different model family
  than that node's implementer, and MUST record review findings with severity, location,
  evidence, and (for blocking findings) a repro.
- **FR-008**: The system MUST implement escalation ladder rungs 1–3 exactly as specified in
  ARCHITECTURE.md §7.5: rung 1 retries the same route with verifier evidence; rung 2 escalates
  to a different model family, fresh from the spec plus an evidence summary (no prior diff);
  rung 3 marks the task for human attention with accumulated evidence. Rungs beyond 3
  (replan, deeper escalation) are explicitly out of scope for this feature.
- **FR-009**: The system MUST classify every failure into one of: infrastructure, provider,
  attempt, or policy (per ARCHITECTURE.md §7.5's table), and only "attempt" and "policy"
  failures MUST advance the escalation ladder.
- **FR-010**: The system MUST detect, at startup and before dispatching new work, any run whose
  process is no longer alive (dead PID, checked against process start time) and mark it lost,
  then retry it as an infrastructure failure, not an attempt failure.
- **FR-011**: The system MUST skip to the next escalation rung, without re-attempting
  identically, when: the same failure signature repeats on consecutive attempts within a rung,
  passing-test count does not rise across two attempts, or a diff hash repeats (oscillation).
- **FR-012**: The system MUST produce a human-readable (markdown) and machine-readable (JSON)
  report per task run, including: the route taken through the ladder, attempts, verifier
  results, review findings, and recorded cost/usage.
- **FR-013**: The system MUST NOT mark any task complete based solely on a commit, a diff, or
  an agent's self-reported completion status.
- **FR-014**: The system MUST export implementer results as a patch or bundle from the
  disposable clone, and MUST NOT grant any implementer or verifier process access to a shared
  `.git` common directory (no linked-worktree sharing of hooks/config).

### Key Entities *(include if feature involves data)*

- **Task**: one `fm run` invocation's unit of work — repo, base commit, request text, status,
  and eventually a result summary.
- **Node**: one step in the (initially linear, ladder-aware) flow for this feature: spec,
  implement, verify, review. (Full DAG decomposition is Feature 006; this feature's nodes form
  a simple chain with ladder-driven retries.)
- **Run**: one attempt at a node, on one route (harness/provider/model); carries status, cost,
  timing, patch path, and failure class/signature.
- **Event**: an append-only record of every state transition, written in the same transaction
  as the transition it describes.
- **VerifyReport**: the verifier's structured output — per-check evidence for build, lint,
  visible tests, hidden tests, and each diff-policy check.
- **ReviewFindings**: the reviewer's structured output — findings with severity, location,
  evidence, and repro where applicable.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `fm run` executes a complete task — spec through report — with zero manual steps
  beyond invoking the CLI once, for at least one task drawn from Feature 001's eval suite.
- **SC-002**: Killing `fm run` mid-attempt and re-invoking it results in the interrupted
  attempt being retried from a fresh clone, with no duplicate or orphaned SQLite rows, 100% of
  the time across repeated trials.
- **SC-003**: A deliberately-failing implementation (patch that fails a hidden test) never
  advances past verification as "succeeded," across 100% of test attempts.
- **SC-004**: The harness arm (`H1`, this feature) run against Feature 001's eval suite
  produces a report comparable in shape to the baseline arms' reports (same metrics columns),
  enabling the go/no-go comparison the architecture requires before building anything further.
- **SC-005**: No verifier run has network access at any point during build, lint, or test
  execution, confirmed by attempting (and observing failure of) an outbound connection from
  inside the verification container during at least one test run.

## Assumptions

- This feature targets the "minimal viable implementation" scope from ARCHITECTURE.md §16, not
  the fuller Phase 1 daemon described in §13 — no Telegram, no persistent daemon process, no
  policy.yaml-driven lanes/budgets beyond what's needed to run the ladder. Those arrive in
  Features 003 and 004.
- Only two implementer runners (`codex exec`, `claude -p`) are required for this feature; the
  third family (`agy`, Antigravity) is deferred to Feature 004 (routing), since rung 2's
  "different family" requirement is satisfiable with two families alone.
- The dedicated OS user and container-based verifier assume Docker Desktop is available and
  its VM resources have been raised per ARCHITECTURE.md §0/§8.5 recommendations; provisioning
  that VM is an operational prerequisite, not a deliverable of this feature.
- Replan (ladder rung 4) and human-approval gating via Telegram are explicitly out of scope;
  rung 3's "mark for human attention" in this feature means a clear CLI-reported block state,
  not a Telegram message (that channel is Feature 003).
- This feature depends on Feature 001's eval task format (`eval/tasks/<id>/`) existing, so that
  `fm run`'s task input and the harness arm comparison share one task representation; it does
  not require Feature 001's three baseline-arm runners to be functioning, only the task
  packaging format.
