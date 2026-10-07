# Feature Specification: Routing, Provider Health, and Test-First

**Feature Branch**: `004-routing-test-first`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Add multi-route model routing with provider health, quota
tracking and cost accounting, enforce cross-model-family rules, add a dedicated test-author
node ahead of every lane S/D implementation, and introduce lane F/S/D budget and gate
differentiation."

**Source**: `ARCHITECTURE.md` §3.4/3.5, §4, §5, §13 Phase 3.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Route around a rate-limited or exhausted provider automatically
(Priority: P1)

As the operator, when a provider returns a 429, a plan-limit error, or a 5xx, the task doesn't
fail — the router puts that provider in cooldown and automatically tries the next eligible
route for that role, respecting family-exclusion rules, until either one succeeds or every
route is cooling down (in which case the node parks, not fails).

**Why this priority**: Without this, Feature 002's two-runner loop is fragile against the
everyday reality the architecture documents (subscription quota windows, plan limits) — this
is what makes unattended operation actually reliable rather than merely durable.

**Independent Test**: Can be tested by forcing a provider error (mocked 429) on the primary
route for a role and confirming the router selects the next eligible route without operator
intervention, then confirming the first provider automatically becomes eligible again after
its cooldown window.

**Acceptance Scenarios**:

1. **Given** a role (e.g. implementer) with an ordered list of eligible routes, **When** the
   primary route's provider returns a 429 or reports a plan/quota limit, **Then** the router
   marks that provider in cooldown until its reported reset time (or an exponential backoff if
   none is reported) and selects the next eligible route.
2. **Given** every route for a role is currently cooling down, **When** the engine requests a
   route, **Then** the node parks with a `not_before` timestamp rather than failing, and the
   operator is notified (via Feature 003) only if the stall exceeds roughly one hour.
3. **Given** a provider's cooldown window has elapsed, **When** the router is next consulted,
   **Then** that provider becomes eligible again without manual reset.
4. **Given** a role's routing policy excludes certain model families (for example, the
   implementer's family when selecting its reviewer), **When** the router computes candidates,
   **Then** excluded families never appear in the candidate list, full stop.

---

### User Story 2 - Write acceptance tests from the spec before implementation starts
(Priority: P1)

As the operator, for every lane S or D task, a dedicated test-author step — using a model
family different from whichever family will implement the task — writes acceptance tests from
the spec before any implementation attempt runs, and the verifier confirms those tests fail on
the unmodified base commit before the implementer ever sees them.

**Why this priority**: This is constitution Principle III, previously only partially covered
in Feature 002 (which assumed tests already existed from the eval suite); this feature makes
test-authoring a first-class, spec-driven node for arbitrary production tasks, not just eval
tasks.

**Independent Test**: Can be tested by running a lane-S task through test-authoring only (no
implementation yet) and confirming the authored tests compile/run and fail against the
unmodified base commit.

**Acceptance Scenarios**:

1. **Given** a `TaskSpec` with acceptance criteria, **When** the test-author node runs,
   **Then** it produces a test patch covering those criteria, using a model family chosen to
   differ from the role about to implement (per the router's family rule).
2. **Given** an authored test patch, **When** the verifier checks it against the unmodified
   base commit, **Then** every new test MUST fail (if a new test instead passes on base, the
   test-author step is rejected and re-run with that evidence).
3. **Given** a test-author output, **When** the implement node later runs, **Then** it receives
   only the visible subset of the authored tests in its workspace; the hidden subset the test
   author produced is stored outside every workspace and mounted read-only only into the
   verifier, exactly as Feature 002 handles eval-provided hidden tests.

---

### User Story 3 - Spend differently depending on how much a task is worth (Priority: P2)

As the operator, trivial tasks (lane F) go straight to implementer + light review at minimal
cost, while risky or complex tasks (lane S/D) get test-authoring, fuller review, and (for lane
D) the heavier deliberation machinery — each lane enforcing its own dollar and wall-clock
budget from `policy.yaml`, with the router's cost-aware selection (`argmin cost/p`) picking
routes accordingly.

**Why this priority**: Implements constitution Principle VIII (deliberation is gated, not
default) and Principle IX (minimal, justified spend) at the routing layer — without
lane-aware budgets, every task would cost the same regardless of how much scrutiny it needs.

**Independent Test**: Can be tested by running one trivial task under lane F and one complex
task under lane S side by side, and confirming lane F completes without a test-author step or
heavy review while lane S includes both, and that each respects its own budget cap from
`policy.yaml`.

**Acceptance Scenarios**:

1. **Given** `policy.yaml` defines lane F with `max_usd` and `max_minutes` caps and
   `review: light`, **When** a lane-F task runs, **Then** it skips the test-author node and
   uses a lighter review pass, and the engine blocks it if it would exceed its budget cap.
2. **Given** `policy.yaml` defines lane S/D with `test_first: true`, **When** a lane S/D task
   runs, **Then** the test-author node executes before implementation, unconditionally.
3. **Given** the router must choose among multiple eligible routes for a role, **When** it
   selects one, **Then** it uses `argmin cost / P(verified success)` among candidates meeting
   the risk-appropriate minimum success probability, using a Beta prior until at least 10
   historical samples exist for that route/class pair.
4. **Given** a task's actual spend approaches its lane's `max_usd`, **When** the engine
   evaluates the next node, **Then** it blocks dispatch and reports a budget-exceeded reason
   rather than silently overspending.

---

### User Story 4 - Classify triage locally, with zero network dependency (Priority: P2)

As the operator, the very first step every task goes through — classifying its task class and
risk level — runs on a small, local classifier model loaded in-process in the foreman daemon,
so the one step that touches literally every task never depends on a model provider being
reachable or inside its quota window.

**Why this priority**: Triage sits upstream of this feature's own router and provider-health
machinery (User Story 1) — it cannot itself depend on the thing it's about to protect tasks
from (provider outages, cooldowns). Removing the network dependency from this single universal
step shrinks the system's external failure surface at near-zero marginal cost, consistent with
constitution Principle IX (justified here by removing, not adding, a dependency).

**Independent Test**: Can be tested by disabling network access entirely and confirming
`/task` submissions are still triaged (task class + risk assigned) correctly for a labeled set
of sample requests.

**Acceptance Scenarios**:

1. **Given** a task request's text, **When** triage runs, **Then** a local classifier model,
   loaded in-process in the foreman daemon, assigns a task class and risk level without making
   any network call.
2. **Given** the local classifier's confidence for a given input falls below a configured
   threshold, **When** triage evaluates that result, **Then** it falls back to the existing
   cheap-API-model triage route (Gemini Flash/GPT Luna, per User Story 1's routing) rather than
   committing to a low-confidence local classification.
3. **Given** the local classifier model file is missing or fails to load at daemon startup,
   **When** the daemon starts, **Then** it logs the condition clearly and falls back to the
   API-based triage route for all tasks, rather than failing task creation outright.

---

### Edge Cases

- What happens when a route has fewer than 10 historical (route, class) samples? The router
  MUST use a Beta prior (not a point estimate) for `P(verified success)` rather than treating
  sparse data as ground truth.
- How does the system handle a model's self-reported "this is hard" signal? Per
  ARCHITECTURE.md §5.4, the router MUST NEVER escalate routes based on a model's self-reported
  difficulty — only on verifier failures and provider/infra signals.
- What happens when the only two available model families are already implementer and
  reviewer, and a third-family adjudicator is needed (Feature 007 trigger)? This feature's
  router MUST surface "no eligible route" rather than silently reusing a used family, since
  adjudication is out of scope here — Feature 007 is responsible for handling that case when
  it arrives.
- What happens when `policy.yaml` itself is malformed or missing a referenced lane? The engine
  MUST refuse to dispatch rather than guessing a default lane silently.
- What happens when the test-author's new test accidentally also fails to compile (not just
  fails to pass)? That MUST be treated as a test-author attempt failure and retried/escalated
  like any other attempt failure, not silently discarded.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST maintain provider health state (OK, cooling down until T, quota
  low) per provider, updated from CLI/API error signals and usage reports.
- **FR-002**: The system MUST compute an ordered candidate route list per (role, task class,
  risk, excluded families) request, filtering by policy, provider health, and family rules.
- **FR-003**: The system MUST select among eligible candidates using
  `argmin cost / P(verified success)`, with `P` estimated via a Beta prior until ≥10 samples
  exist for that (route, class) pair, restricted to candidates meeting the risk tier's minimum
  success probability.
- **FR-004**: The system MUST put a provider in cooldown on a 429/plan-limit/5xx/expired-auth
  signal, using the provider's reported reset time if present, otherwise exponential backoff,
  and MUST make that provider eligible again automatically once its cooldown elapses.
- **FR-005**: The system MUST park a node (set `not_before`, no attempt counted) rather than
  fail it when every eligible route for its role is currently cooling down.
- **FR-006**: The system MUST NEVER escalate a route or advance the ladder based on a model's
  self-reported difficulty or confidence — only on verifier, provider, or infra signals.
- **FR-007**: The system MUST add a `test_author` node that runs before `implement` whenever
  the task's lane requires `test_first: true`, using a model family different from the
  implementer's assigned family for that task. The test author MUST produce both a visible test
  set and a separate hidden test set; the hidden set MUST be stored outside every agent
  workspace and exposed only to the verifier, read-only. This lifts Feature 002's
  "tasks must come with tests" restriction for lanes S and D.
- **FR-008**: The system MUST verify every test-author output fails on the unmodified base
  commit before it is handed to the implementer, and MUST re-run test-authoring with that
  evidence if any new test instead passes on base.
- **FR-009**: The system MUST load lane definitions (F, S, D) from `policy.yaml`, each
  specifying at minimum `max_usd`, `max_minutes`, and a review intensity, and MUST refuse to
  dispatch a task whose declared lane is missing or malformed in policy.
- **FR-010**: The system MUST block dispatch of a task's next node once its accumulated spend
  would exceed its lane's `max_usd`, reporting the specific budget constraint as the block
  reason.
- **FR-011**: The system MUST record, per run, enough data (route, class, provider, verified
  outcome) to update the Beta-prior statistics used by FR-003 for future routing decisions.
- **FR-012**: The system MUST expose cost/quota accounting queryable at minimum by provider and
  by time window (supporting the `/budget` command introduced in Feature 003).
- **FR-013**: The system MUST run triage through a local classifier model, loaded in-process in
  the foreman daemon, as the primary route for assigning task class and risk level, requiring
  no network call for the common case.
- **FR-014**: The system MUST fall back to the existing API-based triage route (Flash/Luna)
  whenever the local classifier's confidence falls below a configured threshold, or whenever
  its model file is missing or fails to load at startup.
- **FR-015**: The local classifier's model weights MUST be pinned by exact version and
  checksum at install/build time — never pulled as `@latest` — per the project's dependency-
  pinning practice for anything the daemon auto-loads and runs.

### Key Entities *(include if feature involves data)*

- **Route**: a (harness, provider, model, effort) tuple eligible for a given role.
- **Provider State**: status (OK/cooling-down/quota-low), cooldown-until timestamp, and quota
  window metadata, per provider.
- **Lane**: F, S, or D — a named policy bundle (budget caps, review intensity, gating flags)
  loaded from `policy.yaml`.
- **Routing Statistics**: per (route, task class) historical verified-success counts feeding
  the Beta-prior success-probability estimate.
- **TestAuthorResult**: the test-author node's structured output — the test patch and
  confirmation it fails on base.
- **Local Classifier Model**: a small, host-local encoder model used for triage (task class +
  risk), pinned by exact version and checksum, loaded in-process by the foreman daemon at
  startup.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A simulated provider outage (forced 429) on the primary route for a role results
  in automatic failover to the next eligible route with zero manual intervention, and that
  provider resumes eligibility after its cooldown elapses, in 100% of tested cases.
- **SC-002**: Every lane-S/D task run produces a test-author artifact that is confirmed to fail
  on base before any implement attempt begins; zero lane-S/D tasks skip this step.
- **SC-003**: A lane-F task and a lane-S task run on comparable work show measurably different
  cost and step count (lane F cheaper, no test-author node; lane S includes test-authoring and
  fuller review), confirming lane differentiation is real, not cosmetic.
- **SC-004**: No task in testing exceeds its lane's configured `max_usd` budget; the engine
  blocks dispatch before an overspend occurs rather than detecting it after the fact.
- **SC-005**: Cross-family rules hold under routing: in no observed run does a reviewer, test
  author, or adjudicator share a model family with the implementer it is meant to check.
- **SC-006**: With network access disabled, triage still produces a class+risk assignment for
  100% of submitted tasks in testing, with accuracy on a held-out labeled set within an
  operator-defined tolerance of the existing API-based baseline.

## Assumptions

- This feature extends, rather than replaces, Feature 002's two-runner (`codex`, `claude`) set
  by adding `agy` (Antigravity/Gemini) as the third routable family, since cross-family rules
  (reviewer, test-author) benefit from three available families rather than two.
- `policy.yaml`'s schema follows the excerpt in ARCHITECTURE.md §14 (`lanes`, `approvals`,
  `api_spend_without_approval_usd_per_day`, `claude_subscription_unattended`, `caps`) as a
  starting point; fields not yet needed by this feature (e.g. decision-protocol flags used by
  Feature 007) are accepted but ignored until their consuming feature lands.
- The router's cost model uses each provider's published or CLI-reported per-token pricing
  (ARCHITECTURE.md §5.1) for API routes, and treats subscription-quota routes as $0 marginal
  cost with a `quota_pressure` penalty term as described in §5.4 — exact pricing tables are
  implementation detail resolved during `/speckit-plan`, not fixed here.
- Lane-F "light review" is defined for this feature as: review runs, but skips the
  test-author-driven hidden-test gate and allows a single review round instead of up to two;
  it is not "no review at all."
- The minimum success probability threshold per risk tier (`p_min(risk)` in
  ARCHITECTURE.md §5.4) starts at operator-set defaults (documented in `policy.yaml` comments)
  rather than being derived statistically, since there is not yet enough historical data to
  derive it (that arrives naturally as FR-011's statistics accumulate).
- The local triage classifier (FR-013–015) runs in-process inside the foreman daemon via a
  local inference runtime loading a small quantized encoder model (runtime choice is a
  `/speckit-plan` decision) — it is not a separate container, sidecar process, or network
  service, consistent with constitution Principle IX (minimal, justified infrastructure).
- Claude-family routes run through Hermes on the operator's Anthropic subscription login and
  are eligible only for operator-triggered tasks (a task the operator started via `/task` or
  `fm run`, including its retries), per `claude_subscription_unattended: false` in
  `policy.yaml`. For anything started without the operator (e.g. a scheduled job), the router
  MUST exclude the Claude family and use Codex (ChatGPT plan) or `agy` (Google plan) routes.
- The classifier's accuracy is validated against Feature 001's eval-suite task-classification
  labels before being trusted as the primary triage route; a small pre-trained or few-shot-
  calibrated encoder classifier is acceptable for v1, since FR-014's confidence-based fallback
  to the API route covers any cold-start accuracy gap.
