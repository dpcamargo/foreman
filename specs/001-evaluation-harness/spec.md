# Feature Specification: Evaluation Harness

**Feature Branch**: `001-evaluation-harness`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Build the evaluation harness first: a task suite with hidden
tests drawn from real repos, plus a baseline runner for single Claude Code session, single
Codex exec session, and Hermes /goal, so every future harness change can be scored
automatically before it's trusted."

**Source**: `ARCHITECTURE.md` §0 (evaluation-first ordering), §13 Phase 0, §16, Appendix D.

## Clarifications

### Session 2026-10-07

- Q: What should foreman orchestrate? → A: Any kind of work (code, research, docs, ops), with
  code as one task type. The eval harness is organized by task type; code is the first type and
  the subject of the go/no-go, and each further type gets its own task set before it is enabled.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Score a single arm against a task suite (Priority: P1)

As the operator, I package 15–20 real tasks (bug fix, small feature, refactor, dependency
change, research/diagnosis, deliberately ambiguous) from my own repos, each pinned to a base
commit with hidden tests and, where judgment is needed, a rubric. I run one "arm" (for
example, a single-shot Hermes session on Claude) against the whole suite and get back a score per task
and an aggregate.

**Why this priority**: Without this, there is no way to tell whether foreman (built in later
features) is actually better than doing nothing. The constitution (Principle IX, and the
Development Workflow's "Evaluation precedes the harness") forbids adopting new orchestrator
capability without this baseline existing first.

**Independent Test**: Can be fully tested by running the suite against a single, trivially
available arm (A1, `codex exec` alone) end to end and getting a scored report, with zero foreman
orchestrator code involved.

**Acceptance Scenarios**:

1. **Given** a packaged eval task with a pinned base commit, a prompt, and hidden tests,
   **When** the baseline runner executes arm A0 (one single-shot Hermes session on Claude) against it,
   **Then** the runner produces a pass/fail result graded in a clean container against the
   hidden tests, with no network access during grading.
2. **Given** a task whose correct behavior is to stop and ask rather than guess,
   **When** any arm is run against it, **Then** the grader scores "asked for clarification"
   as success and "guessed and proceeded" as failure, per that task's rubric.
3. **Given** three repetitions (k=3) of the same arm on the same task from the same base,
   **When** all three runs complete, **Then** the report includes the pass^3 reliability
   metric (all three succeeded) alongside the simple success rate.

---

### User Story 2 - Compare arms on cost and time, not just pass/fail (Priority: P1)

As the operator, after running multiple arms (A0: single-shot Hermes on Claude, A1: Codex exec alone,
A2: Hermes `/goal`) against the same suite, I get a comparison report showing success rate,
false-success rate, cost (API-equivalent dollars and/or subscription quota units), wall-clock
time, and human-rescue count per arm, so I can set the numeric bar that a later harness
(`H1` in Feature 002) must clear.

**Why this priority**: The go/no-go decision for every subsequent feature in this repository
depends on having real baseline numbers, not estimates. Section 16's go/no-go rule names
specific thresholds (≥10 points of verified success, or ≥30% fewer rescues at equal success;
false-success rate ≤ half the baseline's; cost ≤ 2× baseline's) that only mean something once
baseline numbers exist.

**Independent Test**: Can be tested by running A0, A1, and A2 against the same 15–20 task
suite and producing one comparison table with all required columns populated from real
captured data (not placeholders).

**Acceptance Scenarios**:

1. **Given** completed runs for A0, A1, and A2 across the full suite, **When** the comparison
   report is generated, **Then** it shows, per arm: success rate, pass^3 reliability,
   false-success rate (claimed done but hidden tests fail), regression rate, iterations per
   success where applicable, wall-clock time, and cost.
2. **Given** a task graded by rubric (research/diagnosis, ~10% of the suite), **When** that
   task is scored, **Then** the human or process doing the grading is blind to which arm
   produced the result.
3. **Given** two arms whose scores differ by less than roughly 15–20 points on a 40-task×3-run
   suite, **When** the report is generated, **Then** it flags the difference as statistically
   indistinguishable (noise) rather than reporting a false winner.

---

### User Story 3 - Add a new eval task without breaking existing scoring (Priority: P2)

As the operator, I add a new task (a past commit from one of my repos, reset to its parent,
with hidden tests derived from that commit or hand-written) to the suite, and it is
immediately runnable by every existing arm and included in the next report without changing
any arm's code.

**Why this priority**: The suite must grow over time (15–20 tasks now, ~40 eventually per
§16) without becoming a maintenance burden that discourages adding tasks.

**Independent Test**: Can be tested by adding one new task directory under `eval/tasks/<id>/`
and re-running the existing comparison command with no other changes.

**Acceptance Scenarios**:

1. **Given** a new task directory containing `task.yaml`, a `hidden/` test directory, a
   `reference.patch`, and (if judgment-graded) a `rubric.md`, **When** the suite runner is
   invoked, **Then** the new task is picked up automatically and included in all arms' next
   run.
2. **Given** a malformed task directory (missing hidden tests or an unparsable `task.yaml`),
   **When** the suite runner loads tasks, **Then** it reports the specific validation failure
   for that task and continues scoring the remaining valid tasks rather than aborting the run.

---

### Edge Cases

- What happens when a task's hidden tests pass on the unmodified base commit (a broken task
  that can't discriminate pass/fail)? The suite loader MUST reject it at load time with a
  clear error, per the agentic-benchmark-checklist practice cited in Appendix D.
- What happens when an arm's run exceeds the task's wall-clock budget? It MUST be marked
  `TimedOut`/failed for that run, not silently excluded from the aggregate.
- What happens when the grading container itself fails to start (for example, a missing base
  image)? The task run MUST be recorded as an infrastructure failure distinct from a task
  failure, and MUST NOT silently count as either a pass or a fail in the success-rate
  denominator.
- How does the system handle a task whose reference diff no longer applies cleanly to the
  pinned base (dependency drift)? The suite loader MUST flag this at load time rather than
  let the task silently produce a misleading score.
- What happens when two different arms produce functionally different patches that both pass
  hidden tests? Both MUST be scored as successes; the comparison report is not a single
  "correct answer" diff match except where the regression-rate metric explicitly uses
  diff-against-reference as a secondary signal.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST package each eval task as a directory under `eval/tasks/<id>/`
  declaring its task type and containing: the task prompt text, the inputs it starts from (for
  code: a pinned `repo@base`), held-back checks no arm can see (for code: a hidden test
  directory), a reference result (for code: a reference diff), and, for judgment-graded parts, a
  rubric.
- **FR-002**: The system MUST classify each task into one of: bug fix, small feature, refactor,
  dependency/tooling change, research/diagnosis, or ambiguous/impossible, at roughly the
  proportions in Appendix D (30/25/15/10/10/10%).
- **FR-003**: The system MUST provide a runner for each of the three baseline arms (A0: one
  single-shot Hermes session on Claude, using the operator's existing Anthropic subscription
  login; A1: one `codex exec` session; A2: Hermes `/goal` or a Kanban `--goal` card) that takes
  a task directory and produces a patch (or no-op/clarification-request) plus a transcript.
  Claude-family runs MUST be operator-triggered (the operator starts the eval run); nothing in
  this feature schedules them automatically.
- **FR-004**: The system MUST grade every run in a clean, isolated container: apply the
  produced patch to a fresh clone of the pinned base commit, run the hidden tests, and record
  pass/fail per hidden test plus any rubric-graded verdict.
- **FR-005**: The system MUST support two run stages: a smoke stage (a small task subset,
  k=1) that MUST pass cleanly before any decision-grade stage is started, and a decision-grade
  stage that runs each (task, arm) pair k=3 times from the same base, under the same budget,
  computing both the simple success rate and the pass^3 reliability metric.
- **FR-006**: The system MUST capture, per run: success/failure, which hidden tests passed or
  failed, wall-clock time, token/dollar cost or subscription-quota units consumed, and whether
  human rescue was invoked (and must therefore not be auto-resolved).
- **FR-007**: The system MUST compute, per arm across the suite: success rate, false-success
  rate (claimed done but hidden tests fail), regression rate, cost per verified success, time
  to completion, and human-intervention rate, as defined in Appendix D's metrics table.
- **FR-008**: For rubric-graded tasks, the grading step MUST NOT reveal which arm produced the
  result being graded.
- **FR-009**: The system MUST reject, at load time, any task whose hidden tests pass
  unmodified on the pinned base commit, or whose reference diff does not apply cleanly to that
  base.
- **FR-010**: The system MUST produce a comparison report (markdown and/or JSON) across all
  arms run against the suite, flagging score differences smaller than the suite's detectable
  effect size (roughly 15–20 points at ~40 tasks × 3 runs, per Appendix D) as statistically
  indistinguishable rather than a winner.
- **FR-011**: The system MUST allow a new task to be added by creating a new `eval/tasks/<id>/`
  directory, with no code change required in any arm runner.
- **FR-012**: The system MUST NOT give any arm's workspace access to the hidden tests before or
  during that arm's run.
- **FR-013**: The system MUST record, for every run, enough identifying metadata (task id, arm,
  attempt number, base commit, timestamps) to support the paired per-task statistical
  comparison described in Appendix D (McNemar's test for success, bootstrap confidence
  intervals for cost/time) — the harness itself does not have to compute the statistics, but
  MUST NOT discard the paired data needed to compute them later.
- **FR-014**: This feature MUST own the clean-room grader (fresh clone at the pinned base, patch
  applied, hidden tests mounted read-only, no network, no host credentials, no bind mounts
  beyond the patched clone, the hidden tests, and read-only dependency caches) as a reusable
  component. Feature 002's verifier reuses and extends it; this feature MUST NOT depend on 002.
- **FR-015**: Every arm run MUST have a hard token cap and a wall-clock cap; a run that hits
  either cap is stopped and recorded as failed for that run, never silently extended.
- **FR-016**: Baseline results MUST be cached by (task id, base commit, arm, arm version) and
  reused across later comparisons; a baseline arm is re-run only when one of those keys changes.
- **FR-017**: The system MUST report the total tokens, quota units, and wall-clock time spent by
  each eval run itself, per arm and in aggregate, and MUST refuse to start a decision-grade stage
  whose projected token spend (from smoke-stage measurements) exceeds an operator-set budget.
- **FR-018**: Grading MUST follow the task type's declared verifier: code checks first (for code:
  build and hidden tests; for other types, e.g., cited URLs resolve and contain the quoted text),
  then rubric grading by a model family different from the arm's, blind to the arm, against
  criteria fixed before the run. A task type with no declared verifier MUST be rejected at load
  time. The first decision-grade suite contains code tasks only.

### Key Entities *(include if feature involves data)*

- **Eval Task**: a packaged, pinned (repo, base commit, prompt, hidden tests, reference diff,
  optional rubric) unit of work; has a category (bug fix / feature / refactor / dependency /
  research / ambiguous) and a wall-clock budget.
- **Arm**: a named way of attempting a task (A0 single-shot Hermes on Claude, A1 Codex exec,
  A2 Hermes `/goal`, and later H1+ from Feature 002 onward); produces a patch or a structured
  "asked for clarification" result plus a transcript. Its version (CLI/model/prompt) is part of
  the baseline cache key.
- **Run**: one attempt of (task, arm) at a given repetition index; has a status, cost, timing,
  and a graded result.
- **Grading Result**: per-run outcome: which hidden tests passed/failed, rubric verdict if
  applicable, and the derived success/false-success classification.
- **Comparison Report**: an aggregate, per-arm rollup of all runs in a suite execution, plus
  the paired per-task data needed for later statistical comparison.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A smoke stage (about 5 tasks, k=1) and then a decision-grade stage (15–20 tasks,
  k=3) can be run against the baseline arms (A0, A1, A2) and produce a complete comparison report,
  without any manual grading step for the ~80% of tasks that are hidden-test-graded.
- **SC-002**: Every run's grading happens in a container with no network access, and 100% of
  graded runs have a recorded cost/quota figure and wall-clock time.
- **SC-003**: Adding a new task to the suite requires creating exactly one new directory under
  `eval/tasks/` and zero changes to existing arm runner code.
- **SC-004**: The comparison report correctly flags a same-arm, repeated-run comparison (which
  should show no real difference) as statistically indistinguishable, confirming the noise
  threshold isn't over-sensitive.
- **SC-005**: The two deliberately ambiguous tasks in the suite are scored correctly (asking
  for clarification counted as success, guessing counted as failure) for every arm capable of
  producing a "stop and ask" result.
- **SC-006**: No arm run exceeds its token or wall-clock cap, no baseline is re-run when its
  cache key is unchanged, and every eval report states the total tokens and quota the eval
  itself consumed.

## Assumptions

- The operator (not an automated process) curates the initial 15–20 tasks from repos the
  operator chooses, by hand-selecting past commits and deriving or writing hidden tests; task
  curation itself is manual, tooling-assisted work, not something this feature automates.
- Grading runs in containers on the operator's Colima VM (4 CPUs / 6 GiB). Colima mounts the
  operator's home directory read-write into its VM, so the grader MUST pass only explicit
  mounts (FR-014); container image choice is a `/speckit-plan` decision.
- Claude-family arms run through Hermes on the operator's existing Anthropic subscription
  login, by operator decision. ARCHITECTURE.md §0 records that third-party apps on this login
  may be billed as extra usage, so the first smoke stage MUST be followed by a check of the
  account's usage page before any decision-grade stage is started.
- Token economy over statistical power: fewer tasks and repetitions means only large
  differences are detectable. That is acceptable here because the go/no-go thresholds in
  ARCHITECTURE.md §16 are large (≥10 points, or half the false-success rate).
- Arm A2 (Hermes `/goal`) is invoked through whatever interface Hermes exposes at the time this
  feature is built (API server or Kanban `--goal` card per ARCHITECTURE.md §1 option A row);
  the exact invocation mechanics are an implementation detail resolved during `/speckit-plan`,
  not fixed here.
- Cost/quota accounting captured in FR-006 reuses each CLI's own reported usage fields where
  available (Codex JSONL events, Claude's `--output-format json`, Hermes's run events) rather
  than independently re-measuring token counts.
- This feature produces a standalone `fm`-prefixed eval CLI and `eval/` directory structure
  that Feature 002's `fm run --arm` command later extends with the `H1` (foreman) arm; it does
  not depend on Feature 002 existing first.
