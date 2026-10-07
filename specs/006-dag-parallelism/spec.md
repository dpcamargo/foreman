# Feature Specification: DAG Decomposition and Parallelism

**Feature Branch**: `006-dag-parallelism`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Add DAG-based decomposition and parallel execution: the
specifier emits an implementation sub-DAG, independent nodes run concurrently under
concurrency limits, an integrate node merges node branches with a full re-verify, and best-of-2
candidate generation is available for hard tasks."

**Source**: `ARCHITECTURE.md` §3.1, §7.1, §13 Phase 5.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Split a task into independent pieces that run at the same time
(Priority: P1)

As the operator, when a spec identifies genuinely independent sub-parts of a task (touching
disjoint file sets with no data dependency between them), the engine dispatches their
implement nodes concurrently, up to a configured concurrency limit, instead of serializing
everything through Feature 002's single-chain loop.

**Why this priority**: This is the first feature that moves beyond a strictly linear
task → node chain into the DAG model the architecture requires for anything beyond trivial
tasks, while explicitly preserving the "never parallel writes to the same code" rule
(constitution Principle IV).

**Independent Test**: Can be tested by giving the specifier a task that decomposes into two
disjoint-path sub-nodes and confirming both dispatch and run concurrently (overlapping
wall-clock windows), not sequentially.

**Acceptance Scenarios**:

1. **Given** a `TaskSpec` whose `nodes` list declares two implement nodes with non-overlapping
   `paths` and no `depends_on` relationship between them, **When** the engine dispatches ready
   nodes, **Then** both nodes run concurrently, each in its own fresh clone.
2. **Given** a concurrency limit (e.g. 2 implement nodes at a time, per
   ARCHITECTURE.md §8.5's M1 resource guidance), **When** more than that many nodes are READY
   simultaneously, **Then** the engine dispatches only up to the limit and queues the rest.
3. **Given** two nodes whose declared `paths` overlap even partially, **When** the specifier
   emits the sub-DAG, **Then** the spec-check step (extending Feature 002's spec validation)
   MUST reject the DAG as invalid — overlapping-path nodes are not independent and MUST NOT be
   scheduled concurrently.

---

### User Story 2 - Merge independently-developed branches and re-verify as a whole
(Priority: P1)

As the operator, once every node in a task's DAG has succeeded, an `integrate` node merges
each node's branch into the task branch and runs a full re-verification (the complete test
suite, not just each node's own slice) before the task is considered deliverable.

**Why this priority**: Parallel development of disjoint pieces can still interact at
integration time (for example, both touching a shared interface in compatible-looking but
subtly conflicting ways); a full re-verify at merge time is the only place that's caught.

**Independent Test**: Can be tested by running a two-node DAG to individual success, then
confirming the `integrate` node merges both branches and a full-suite verification runs
against the merged result — not just a re-report of each node's own prior verification.

**Acceptance Scenarios**:

1. **Given** all of a task's DAG nodes have reached `SUCCEEDED`, **When** the `integrate` node
   runs, **Then** it merges each node's branch commit into the task branch in the host mirror,
   with git hooks disabled (reusing Feature 002's hardened-apply approach).
2. **Given** a successful merge, **When** the integrate node verifies, **Then** it runs the
   full test suite (not a subset) plus a fresh secret scan (gitleaks) against the merged
   result, and only a passing full re-verify allows the task to proceed to delivery.
3. **Given** the full re-verify fails after an otherwise-successful per-node merge, **When**
   that failure is processed, **Then** the engine creates a targeted `revise` node scoped to
   the actual conflict, rather than re-running either original implement node from scratch.

---

### User Story 3 - Try two candidates when a task is hard enough to warrant it
(Priority: P2)

As the operator, for a node that has already failed verification once in lane D (or any lane
after repeated failure), the engine can dispatch two independent implementation attempts in
parallel (best-of-2) and select the one whose verification result is strictly better, rather
than committing to a single serial retry.

**Why this priority**: Implements the architecture's explicit, narrow allowance for
parallelism on writes — "best-of-2 only in lane D or after repeated failure" — without
violating the single-writer default from constitution Principle IV.

**Independent Test**: Can be tested by forcing a lane-D node into its best-of-2 path and
confirming two independent attempts run in separate clones, with only the verifier-selected
winner's branch advancing (the loser's branch/workspace is discarded, not merged).

**Acceptance Scenarios**:

1. **Given** a lane-D node, or any node after one failed verification, **When** the engine
   decides to use best-of-2, **Then** it dispatches two implement attempts on different
   routes/families, each in its own fresh clone, never writing to the same branch.
2. **Given** both best-of-2 candidates complete, **When** verification runs on each, **Then**
   the engine selects the candidate with the better `VerifyReport` (more passing hidden tests,
   fewer diff-policy violations) and discards the other's workspace and branch.
3. **Given** best-of-2 is used, **When** it is used outside lane D, **Then** it MUST only occur
   after that node has already failed verification at least once — best-of-2 MUST NEVER be the
   first attempt at a node outside lane D.

---

### Edge Cases

- What happens when a node the specifier declared independent turns out, at runtime, to touch
  a file outside its declared `paths` (a spec error)? The diff-policy check (from Feature 002)
  MUST flag paths-touched-outside-declared-scope as a violation, not silently allow it.
- How does the system handle a node whose dependencies are a mix of SUCCEEDED and still-RUNNING
  siblings? It MUST remain `PENDING` (not `READY`) until every declared dependency reaches
  `SUCCEEDED`.
- What happens when the integrate node's merge itself produces a textual conflict (not just a
  test failure)? This MUST be surfaced as a distinct integration failure, with its own
  evidence (the conflicting hunks), not conflated with a generic attempt failure.
- What happens if both best-of-2 candidates fail verification? The node MUST advance the
  escalation ladder (per Feature 002/004) exactly as if a single candidate had failed — best-
  of-2 does not get its own separate ladder.
- What happens to a concurrency-limited queue if the daemon restarts (Feature 003) while
  multiple nodes are mid-dispatch? The reconciler MUST re-derive READY state from dependency
  completion in SQLite, not from any in-memory queue that didn't survive the restart.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The specifier MUST be able to emit a `TaskSpec.nodes` list where each node
  declares its kind, dependencies (`depends_on`), and the file-system `paths` it is expected to
  touch.
- **FR-002**: The engine MUST reject, at spec-validation time, any sub-DAG where two
  concurrently-schedulable nodes (no dependency relationship between them) declare overlapping
  `paths`.
- **FR-003**: The engine MUST compute node readiness from the DAG (`PENDING` → `READY` only
  once all `depends_on` nodes are `SUCCEEDED`), stored and re-derivable entirely from SQLite
  state — not from an in-memory scheduler queue.
- **FR-004**: The engine MUST dispatch all currently-`READY` nodes up to a configured
  concurrency limit (per resource class, e.g. implement nodes vs. verify nodes), queuing any
  excess.
- **FR-005**: The system MUST provide an `integrate` node kind that merges every `SUCCEEDED`
  node's branch into the task branch (hooks disabled, per Feature 002's hardened-apply
  approach) and runs a full-suite re-verification plus a fresh secret scan against the merged
  result.
- **FR-006**: A task MUST NOT be eligible for delivery until its `integrate` node's full
  re-verification passes, independent of any individual node's own prior verification result.
- **FR-007**: The system MUST support a best-of-2 execution mode for a node, restricted to
  lane D or to any node that has already failed verification at least once, dispatching two
  implement attempts on different routes/families into separate clones and branches.
- **FR-008**: Given two best-of-2 candidates, the system MUST select the one with the better
  `VerifyReport` outcome and MUST discard (not merge, not retain as a branch) the losing
  candidate's workspace.
- **FR-009**: Best-of-2 failures (both candidates fail verification) MUST be processed as a
  single attempt-failure event on that node for escalation-ladder purposes, not as two
  independent ladder entries.
- **FR-010**: The engine MUST only ever write to one branch per node; concurrent nodes MUST
  write to distinct branches that are not merged into each other until the `integrate` step.
- **FR-011**: Node dispatch under concurrency limits MUST be correctly recoverable after a
  daemon restart (Feature 003), re-deriving the dispatch set purely from persisted DAG and
  node-status state.

### Key Entities *(include if feature involves data)*

- **Sub-DAG**: the set of nodes and `node_deps` edges emitted by the specifier for one task's
  implementation, extending the single-chain model from Feature 002.
- **Integrate Node**: a node kind that merges sibling node branches and performs a full
  re-verification; distinct from a per-node `verify` node.
- **Candidate (best-of-2)**: one of two parallel implement attempts on the same logical node,
  carrying its own branch, workspace, and `VerifyReport`, exactly one of which survives past
  selection.
- **Concurrency Slot**: a bounded resource (e.g. "at most 2 implement nodes + 1 verifier at a
  time") the scheduler allocates and releases per dispatched node.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A task whose spec declares two genuinely independent sub-nodes completes with
  measurably overlapping wall-clock execution windows for those two nodes (not sequential),
  while a task with path-overlapping declared nodes is rejected at spec-validation time before
  any dispatch occurs.
- **SC-002**: 100% of tested multi-node tasks reach delivery only after the `integrate` node's
  full-suite re-verification passes — no task is delivered on the strength of per-node
  verification alone.
- **SC-003**: Best-of-2 is never observed to run as a first attempt outside lane D in testing;
  it only triggers after at least one verified failure, or inside lane D.
- **SC-004**: After a simulated daemon restart mid-dispatch (building on Feature 003's
  reconciler), the set of nodes dispatched post-restart matches what pure DAG-dependency
  evaluation from persisted state would produce — no node is lost or double-dispatched.
- **SC-005**: Concurrency limits are respected under load: with more READY nodes available than
  the configured limit, the number of simultaneously-running nodes never exceeds that limit in
  any tested run.

## Assumptions

- This feature assumes Feature 004's routing (for selecting best-of-2's two distinct
  routes/families) and Feature 002's branch/clone/hardened-apply machinery already exist; it
  extends rather than replaces them.
- The default concurrency limits (e.g. "at most 2 implement nodes plus 1 verifier at a time")
  follow the M1-hardware guidance in ARCHITECTURE.md §8.5 as a starting default, configurable
  per-host as hardware changes (e.g. moving to a Mac mini per §18) rather than hard-coded
  permanently.
- Dynamic node creation limits (which node kinds may spawn which children — e.g. `spec` →
  `implement`/`test_author`/`decide`) follow the table in ARCHITECTURE.md §7.3's "Limits on
  dynamic creation," enforced by this feature's DAG-validation step.
- Best-of-2's candidate-selection criterion ("better VerifyReport") is defined for this
  feature as: strictly more passing hidden tests, then fewer diff-policy violations, then
  (tie) the first-completing candidate — a richer scoring model is not required until evidence
  shows this simple ordering picks badly.
- This feature does not yet implement the decision protocol's experiment nodes (Feature 007);
  "after repeated failure" best-of-2 triggering in this feature is a simple attempt-count
  check, not yet informed by the decision protocol's richer disagreement-classification logic.
