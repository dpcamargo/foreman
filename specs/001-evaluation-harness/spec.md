# Feature Specification: Evaluation Harness (Measure As We Go)

**Feature Branch**: `001-evaluation-harness`

**Created**: 2026-10-06

**Status**: Draft — clarified, ready for `/speckit-plan`

**Input**: User description: "Build the evaluation harness first: a task suite with hidden
tests drawn from real repos, plus a baseline runner for single Claude Code session, single
Codex exec session, and Hermes /goal, so every future harness change can be scored
automatically before it's trusted." Revised 2026-10-07 by operator decision: no upfront task
suite; foreman measures itself on its real work as it goes (see Clarifications).

**Source**: `ARCHITECTURE.md` §0 (evaluation-first ordering), §13 Phase 0, §16, Appendix D.

## Clarifications

### Session 2026-10-07

- Q: What should foreman orchestrate? → A: Any kind of work (code, research, docs, ops), with
  code as one task type. The eval harness is organized by task type; code is the first type and
  the subject of the go/no-go, and each further type gets its own task set before it is enabled.
- Q: Which non-code kind of work comes first, and when? → A: Code first (002 go/no-go), then
  research, then docs, then ops. This feature's first decision-grade suite stays code-only; the
  research task set is delivered with the research task-type feature.
- Q: Where do the eval tasks come from? → A: Nowhere up front. Mining past commits is dropped
  (the operator's repos are too thin, and it isn't needed), and so is hand-writing a 15–20 task
  suite. foreman records every task it runs. A task that came with acceptance tests can be
  replayed from its base commit, and the replay set grows from real work. Every so often, and
  for every go/no-go, a sample of recorded tasks is replayed through the plain single-agent
  baselines to confirm foreman is still worth its overhead. This supersedes the "task set before
  it is enabled" part of the first answer: a task type is judged by replaying its own recorded
  tasks once it has enough of them (constitution v3.0.0, "Measure as we go").

**Terminology**: other specs' "eval suite" means this feature's **replay set**: every recorded
task that is replayable.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Every task leaves a record (Priority: P1)

As the operator, every task foreman runs leaves a record: what was asked, what it started
from, the acceptance tests it was held to, how it ended, how many attempts and tokens it took,
how long it took, and whether I had to step in. I can also record a task I did outside foreman
(for example, by hand with Claude Code), so measurement starts before foreman exists.

**Why this priority**: The records are both the running scoreboard and the only source of eval
tasks. Without them there is nothing to replay and no way to tell whether foreman beats doing
nothing (constitution Principle IX and "Measure as we go").

**Independent Test**: Record one task by hand with `fm eval record` against a small fixture
repo (a base commit, a request, and a failing test). The record is listed, and the clean-room
grader confirms its hidden tests fail on the base commit. No foreman orchestrator code is
involved.

**Acceptance Scenarios**:

1. **Given** a task with a repo pinned to a base commit, a request, and acceptance tests with a
   hidden subset, **When** it is recorded, **Then** it is stored as replayable once the grader
   confirms the hidden tests fail on the base commit.
2. **Given** a task whose hidden tests already pass on the base commit, **When** it is recorded,
   **Then** it is kept for metrics and marked not replayable, with that reason.
3. **Given** a task that came without acceptance tests, **When** it is recorded, **Then** its
   outcome and costs count in the running metrics, but it is never offered for replay.

---

### User Story 2 - Replay a recorded task through a baseline arm (Priority: P1)

As the operator, I pick recorded tasks and replay them through a plain single-agent arm (A0:
one single-shot Hermes session on Claude; A1: one `codex exec` session; A2: Hermes `/goal`). Each
arm starts from a fresh clone at the task's base commit with the original request, under the
same caps, and is graded in a clean container against the task's hidden tests.

**Why this priority**: A replay is the only fair comparison. It runs the same task, from the
same start, against the same hidden tests, with and without foreman.

**Independent Test**: Replay the hand-recorded task from User Story 1 through A1 (`codex exec`
alone) and get a graded result with cost and time, with zero foreman orchestrator code.

**Acceptance Scenarios**:

1. **Given** a replayable task, **When** arm A0 replays it, **Then** the produced patch is graded
   in a clean container against the hidden tests, with no network access during grading.
2. **Given** three repetitions (k=3) of the same arm on the same task, **When** all three
   complete, **Then** the result includes pass^3 (all three succeeded) alongside the simple
   success rate.
3. **Given** a baseline result already exists for the same (task, base commit, arm, arm
   version), **When** a replay is requested, **Then** the cached result is reused and nothing is
   re-run.

---

### User Story 3 - Check that foreman still earns its overhead (Priority: P1)

As the operator, every so often I run a cheap **spot check**: a few recent recorded tasks
replayed once through one baseline arm and compared with foreman's live outcomes. It shows the
trend and never decides anything. For a go/no-go or any adoption decision I run a
**decision replay**: a sample of recorded tasks, with every compared arm (foreman included)
replayed k=3 from the same base under the same caps. The report shows success rate, false-success
rate, cost, time, and rescues per arm, and flags differences too small to trust.

**Why this priority**: This is what replaces the upfront go/no-go suite. The thresholds in
ARCHITECTURE.md §16 (≥10 points of verified success, or ≥30% fewer rescues at equal success;
false-success rate ≤ half the baseline's; cost ≤ 2× the baseline's) are applied to replays of
real work.

**Independent Test**: With the hand-recorded fixture tasks, run a spot check (A1 vs. a
recorded outcome) and a decision replay of A0 against A1, and get both reports with every
column filled from real captured data.

**Acceptance Scenarios**:

1. **Given** recorded tasks since the last check, **When** a spot check runs, **Then** it replays
   them once (k=1) through one baseline arm, and the report is labelled as a trend, not a
   decision.
2. **Given** a decision replay is requested, **When** it runs, **Then** it samples from all
   replayable tasks regardless of how foreman did on them (failed, blocked and rescued tasks
   included), replays every compared arm k=3, and the report states how the sample was drawn.
3. **Given** two arms whose scores differ by less than the replay's detectable effect size,
   **When** the report is generated, **Then** it flags the difference as indistinguishable rather
   than naming a winner.
4. **Given** a decision replay sample, **When** more than half of it comes from one repository,
   **Then** the report says so, because the result may only describe that codebase.

---

### Edge Cases

- A task's hidden tests pass on the unmodified base commit: it cannot tell pass from fail. It
  MUST NOT be replayable (Appendix D's agentic-benchmark checklist).
- A task's base commit is no longer fetchable (branch deleted, history rewritten): the replay is
  recorded as an infrastructure failure, the task is marked not replayable with that reason, and
  it does not count as a pass or a fail.
- An arm exceeds its token or wall-clock cap: that run is failed (`TimedOut`/`BudgetExceeded`),
  not silently dropped from the aggregate.
- The grading container fails to start (for example, a missing image): recorded as an
  infrastructure failure, outside the success-rate denominator.
- Two arms produce different patches that both pass the hidden tests: both are successes. The
  verified patch on the record is only a secondary signal for the regression-rate metric, never
  "the" answer.
- A task whose correct outcome was to stop and ask (the operator confirmed the request was
  ambiguous): on replay, asking counts as success and guessing as failure.
- The replay set is dominated by foreman's own development: the report flags single-repository
  dominance (User Story 3, scenario 4), and the operator can add tasks from other repos with
  `fm eval record`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every task foreman runs MUST produce a task record containing: task type, a
  category label (bug fix, small feature, refactor, dependency/tooling change,
  research/diagnosis, ambiguous), the request text, its inputs (for code: repo and base commit),
  its acceptance criteria (for code: visible and hidden tests), its outcome (verified success,
  failure, blocked, rescued, or clarification requested), attempts, tokens and quota units,
  wall-clock time, human interventions, and, on success, the verified result (for code: the
  patch). Failed, blocked and rescued tasks MUST be recorded too.
- **FR-002**: The system MUST provide `fm eval record` to record a task done outside foreman with
  the same fields; outcome metrics are optional for these.
- **FR-003**: Task records MUST be stored outside every workspace an arm can read, in a
  versioned format. Adding a task MUST NOT require a code change in any arm runner. The storage
  location is a `/speckit-plan` decision.
- **FR-004**: A record MUST be marked replayable only if its base commit is fetchable and the
  clean-room grader confirms its hidden tests fail on that base. Otherwise it MUST be kept for
  metrics with the reason it is not replayable.
- **FR-005**: The system MUST provide a runner for each baseline arm (A0: one single-shot Hermes
  session on Claude, using the operator's existing Anthropic subscription login; A1: one
  `codex exec` session; A2: Hermes `/goal` or a Kanban `--goal` card), each taking a task record
  and producing a patch (or a no-op or clarification request) plus a transcript. Arms MUST share
  one interface, so Feature 002 adds the foreman arm (H1) without changing the others.
- **FR-006**: This feature MUST own the clean-room grader as a reusable component. The grader
  applies a patch to a fresh clone of the base commit and runs the hidden tests, mounted
  read-only, with no network, no host credentials, and no bind mounts beyond the patched clone,
  the hidden tests, and read-only dependency caches. It records pass/fail per hidden test plus
  any rubric verdict. Feature 002's verifier reuses and extends it; this feature MUST NOT depend
  on 002.
- **FR-007**: No arm's workspace may contain or reach the hidden tests before or during its run.
- **FR-008**: Every arm run MUST have a hard token cap and a wall-clock cap. The wall-clock cap
  stops the run when reached. The token cap stops the run as soon as the arm's CLI reports usage
  past it; for a CLI that reports usage only at the end of a run, the cap is checked then. A run
  over either cap is recorded as failed, never silently extended.
- **FR-009**: The system MUST capture, per replay run: success or failure, which hidden tests
  passed or failed, wall-clock time, tokens/dollars or quota units, and whether human rescue
  was invoked. It MUST also keep enough metadata (task id, arm, arm version, repetition, base
  commit, timestamps) for the paired comparison in Appendix D: McNemar's test for success, and
  bootstrap intervals for cost and time. The harness need not compute those statistics, but MUST
  NOT discard the paired data.
- **FR-010**: The system MUST compute, per arm: success rate, pass^3 where k=3, false-success rate
  (claimed done but hidden tests fail), regression rate, cost per verified success, time to
  completion, and human-intervention rate, as defined in Appendix D's metrics table.
- **FR-011**: The system MUST support two replay modes. A **spot check** replays chosen records
  once (k=1) through one baseline arm, and its report is labelled as a trend that cannot decide
  anything. A **decision replay** replays every compared arm, foreman's included when it exists,
  k=3 from the same base under the same caps; foreman's live outcome MUST NOT stand in for its
  replay. A decision replay MUST be preceded by a clean k=1 smoke pass over about 3 of its tasks.
- **FR-012**: A decision replay MUST sample from all replayable records regardless of their live
  outcome, using a recorded seed. Its report MUST state the sample, its repository and category
  mix, and flag when one repository is more than half of it.
- **FR-013**: The system MUST produce a comparison report (markdown and JSON) per spot check or
  decision replay. It MUST flag score differences smaller than the replay's detectable effect
  size as indistinguishable, rather than naming a winner.
- **FR-014**: Grading MUST follow the task type's declared verifier: code checks first (for code:
  build and hidden tests; for other types, e.g. cited URLs resolve and contain the quoted text),
  then rubric grading against criteria fixed before the run, by a model family different from the
  arm's, blind to which arm produced the result. A task type with no declared verifier MUST NOT
  be replayable. The first decision replay contains code tasks only.
- **FR-015**: Baseline results MUST be cached by (task id, base commit, arm, arm version) and
  reused; a baseline is re-run only when one of those keys changes.
- **FR-016**: Every report MUST state the tokens, quota units and wall-clock time the replay
  itself consumed, per arm and in total. The system MUST refuse to start a decision replay whose
  projected spend (from its smoke pass) exceeds an operator-set budget.
- **FR-017**: Every report MUST state how many replayable records have accumulated since the last
  spot check. Replays are operator-triggered only; nothing in this feature schedules them.

### Key Entities *(include if feature involves data)*

- **Task Record**: one task foreman ran (or the operator recorded): type, category, request,
  inputs (code: repo and base commit), acceptance tests with a hidden subset, live outcome and
  costs, verified result if any, and whether it is replayable (and if not, why).
- **Replay Set**: all replayable task records; what other specs call the "eval suite". It grows
  from real work and is never authored up front.
- **Arm**: a named way of attempting a task (A0, A1, A2, and H1 from Feature 002). It produces a
  patch or a structured "asked for clarification" result plus a transcript. Its version
  (CLI, model, prompt hash) is part of the cache key.
- **Replay Run**: one attempt of (task record, arm) at a repetition index, with status, cost,
  timing and a grading result.
- **Grading Result**: which hidden tests passed or failed, the rubric verdict if any, and the
  derived success / false-success classification.
- **Comparison Report**: the per-arm rollup of one spot check or decision replay, with its mode,
  sample, seed, repository/category mix, its own spend, and the paired per-task data.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A task recorded with `fm eval record` can be replayed through A1 and graded end to
  end with zero foreman orchestrator code and no manual grading step.
- **SC-002**: Every replay is graded in a container with no network access, and 100% of graded
  runs have a recorded cost or quota figure and wall-clock time.
- **SC-003**: Adding a task to the replay set takes one record command (or one `fm run`) and
  zero changes to arm runner code.
- **SC-004**: A decision replay of an arm against itself is reported as indistinguishable,
  confirming the noise threshold isn't over-sensitive.
- **SC-005**: No arm run exceeds its token or wall-clock cap, no baseline is re-run when its cache
  key is unchanged, and every report states what the replay itself consumed.
- **SC-006**: No eval task is authored up front: the first decision replay uses only records
  produced by real work.
- **SC-007**: A record whose hidden tests pass on its base commit never enters a replay.

## Assumptions

- Real work supplies the tasks. The first source is expected to be foreman's own development
  run through `fm run` (Feature 002), whose spec-kit tasks are written test-first and so carry
  acceptance tests. That risks measuring mostly one codebase; FR-012 makes that visible, and
  `fm eval record` lets the operator add tasks from other repositories.
- Until Feature 002 exists, `fm eval record` and a small fixture repository are enough to build
  and test this feature.
- Grading runs in containers on a Colima VM (4 CPUs / 6 GiB). Colima mounts the user's home
  directory read-write into its VM, so the grader MUST pass only explicit mounts (FR-006).
  Container image choice is a `/speckit-plan` decision.
- Claude-family arms run through Hermes on the operator's existing Anthropic subscription login,
  by operator decision. ARCHITECTURE.md §0 records that third-party apps on this login may be
  billed as extra usage, so the first smoke pass MUST be followed by a check of the account's
  usage page before any decision replay.
- Token economy over statistical power: spot checks are k=1 on a handful of tasks, and decision
  replays (about 15 tasks × k=3) happen only at a go/no-go or adoption decision. That size
  detects only large differences, which is acceptable because the §16 thresholds are large.
- Arm A2 (Hermes `/goal`) is invoked through whatever interface Hermes exposes when this feature
  is built (API server or Kanban `--goal` card, per ARCHITECTURE.md §1 option A). The exact
  mechanics are a `/speckit-plan` decision.
- Cost and quota accounting reuses each CLI's own usage fields where available (Codex JSONL
  events, Claude's `--output-format json`, Hermes run events) rather than re-measuring tokens.
- This feature ships the `fm eval` commands and the task-record format. Feature 002's `fm run`
  writes a record for every task and adds the H1 arm; this feature does not depend on 002.
- Code follows constitution Principle X and `AGENTS.md`: the grader, arms and record store are
  adapters behind small interfaces declared in `internal/core`.
