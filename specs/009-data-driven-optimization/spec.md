# Feature Specification: Data-Driven Optimization

**Feature Branch**: `009-data-driven-optimization`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Add data-driven optimization once enough labelled outcomes
exist: a learned or statistically-informed router, eval-gated changes to foreman's own
prompts/routes/policy as continuous CI, and auto-merge eligibility for task classes with a
proven low false-success rate."

**Source**: `ARCHITECTURE.md` §5.4, §13 Phase 8, §16's continuous-evaluation note.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Treat every change to foreman itself as a change that must pass the eval
suite (Priority: P1)

As the operator, when I (or an agent, under supervision) change foreman's own prompts, routes,
or policy, that change does not get trusted in production until it has run against the Feature
001 eval suite and shown no regression — the eval suite is foreman's own CI, continuously, not
a one-time gate that was satisfied back in Feature 001/002.

**Why this priority**: This operationalizes the constitution's Development Workflow
requirement that "any change to foreman's own prompts, routes, or policy MUST run against the
eval suite before being trusted in production" — without this feature, that rule has no
enforcement mechanism as the system evolves past its initial build.

**Independent Test**: Can be tested by deliberately introducing a prompt change that degrades
performance on a known eval task and confirming the continuous-evaluation gate flags the
regression before the change is allowed to affect production routing.

**Acceptance Scenarios**:

1. **Given** a change to a prompt file, a route in `routes.yaml`, or `policy.yaml`,
   **When** that change is proposed, **Then** the eval suite (Feature 001) runs against it
   automatically, and the comparison report (per Feature 001's FR-010) is attached to the
   change before it is applied in production.
2. **Given** an eval run shows a statistically significant regression (not noise, per Feature
   001's detectable-effect-size threshold) on the changed configuration, **When** the result is
   evaluated, **Then** the change is blocked from taking effect in production until addressed.
3. **Given** a prompt change passes the eval suite with no regression, **When** it is applied,
   **Then** its prompt version hash (per ARCHITECTURE.md §7.4's `runs.prompt_hash`) is recorded
   so that any later outcome can be attributed to the specific prompt version that produced it.

---

### User Story 2 - Let routing improve from real outcomes, carefully (Priority: P2)

As the operator, once enough labelled (route, task class, verified outcome) samples exist —
materially more than the Beta-prior cold-start threshold from Feature 004 — the router's
success-probability and cost estimates are refined using that accumulated history, and,
if/when a learned router is introduced, it is evaluated against the existing
statistically-informed router on the eval suite before replacing it, never swapped in on
a vendor claim alone.

**Why this priority**: Implements ARCHITECTURE.md §5.1's explicit rejection of "a learned
router before you have hundreds of labelled tasks" — this feature is what makes that stated
precondition concrete and enforceable rather than aspirational.

**Independent Test**: Can be tested by running the existing Beta-prior router and a candidate
learned/refined router against the same eval suite slice and confirming the comparison report
(reusing Feature 001's format) shows whether the candidate actually improves cost-per-verified-
success before any production routing change is made.

**Acceptance Scenarios**:

1. **Given** fewer than a configured minimum number of labelled (route, class) samples exist,
   **When** routing decisions are made, **Then** the system continues using Feature 004's
   Beta-prior approach — this feature MUST NOT introduce a learned router before that minimum
   is met.
2. **Given** the minimum sample threshold is met, **When** a candidate routing refinement
   (statistically refined priors, or a learned model) is proposed, **Then** it MUST be
   evaluated against the current router on the eval suite, and MUST show a measurable
   improvement in cost per verified success before being adopted in production.
3. **Given** a routing refinement is adopted, **When** it later underperforms its own
   historical baseline on new labelled data, **Then** the system supports reverting to the
   prior routing approach using the same recorded evidence trail.

---

### User Story 3 - Earn auto-merge for a task class, one class at a time, with evidence
(Priority: P3)

As the operator, auto-merge-to-main is not a global switch — it becomes available only for a
specific, named task class (e.g. "dependency bump on repo X") once that class's measured
false-success rate on the eval suite is low enough to trust, and I can revoke it for that class
alone if real-world outcomes stop matching the eval numbers.

**Why this priority**: Directly implements the constitution's Governance stance and
ARCHITECTURE.md §17's explicit "don't build: auto-merge to main... not until the eval shows a
low false-success rate for that task class" — this is the last, most consequential lever this
roadmap unlocks, and it must be the most evidence-gated.

**Independent Test**: Can be tested by measuring a task class's false-success rate on the eval
suite, confirming auto-merge remains disabled below a configured threshold, then confirming it
becomes available only after that threshold is met for that specific class — and that enabling
it for one class does not enable it for any other class.

**Acceptance Scenarios**:

1. **Given** a task class has not yet met the configured false-success-rate threshold on the
   eval suite, **When** a task of that class completes successfully, **Then** it still
   requires the Feature 003 nonce-bound human approval before merging to main — auto-merge is
   not available for that class.
2. **Given** a task class's measured false-success rate, evaluated over a sufficient sample
   size, falls at or below the configured threshold, **When** the operator explicitly enables
   auto-merge for that specific class in `policy.yaml`, **Then** subsequent tasks of that exact
   class may merge without a human approval step, per the enabled policy.
3. **Given** auto-merge is enabled for class A but not class B, **When** a class-B task
   completes, **Then** it still requires human approval — enabling auto-merge for one class
   MUST NOT implicitly enable it for any other class.
4. **Given** real-world (production) outcomes for an auto-merge-enabled class start showing
   failures the eval suite didn't predict, **When** the operator revokes auto-merge for that
   class, **Then** all subsequent tasks of that class immediately require human approval again,
   with no in-flight task left ambiguously gated.

---

### Edge Cases

- What happens when the eval suite itself changes (new tasks added, per Feature 001's ongoing
  growth) between two prompt/route versions being compared? The comparison MUST be run on the
  same suite version for both sides, or explicitly flagged as not directly comparable if the
  suite changed between runs.
- How does the system handle a regression that only shows up in production, not in the eval
  suite (a gap in coverage)? This MUST be treated as a signal to add a new eval task capturing
  that gap (feeding back into Feature 001), not as a reason to bypass the continuous-evaluation
  gate for future changes.
- What happens when auto-merge is enabled for a class and then that class's definition itself
  changes (e.g. the policy glob is broadened)? The broadened class MUST be treated as a new,
  unproven class requiring its own threshold to be re-earned — auto-merge eligibility MUST NOT
  silently carry over to a redefined, broader class.
- What happens when a learned router's training data includes outcomes from a period when a
  bug in verification was later found to produce false passes? Those tainted outcomes MUST be
  excluded or the router recomputed once the tainted period is identified, since the
  router's training signal cannot be trusted to be better than the verification it was trained
  on.
- What happens if someone attempts to enable auto-merge for a class via a mechanism other than
  the documented `policy.yaml` operator edit? There is no such mechanism — this feature MUST
  NOT expose auto-merge enablement as something any agent role (including the decider or a
  learned router) can set; it is operator-only, consistent with constitution Principle VIII.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST run the Feature 001 eval suite automatically against any change
  to foreman's own prompts, `routes.yaml`, or `policy.yaml` before that change is trusted in
  production, and MUST attach the resulting comparison report to the change.
- **FR-002**: The system MUST block a change from taking effect in production if its eval run
  shows a statistically significant regression (beyond the suite's documented noise threshold)
  relative to the pre-change baseline.
- **FR-003**: The system MUST record a prompt-version hash for every prompt used in production,
  sufficient to attribute any later outcome to the exact prompt version that produced it.
- **FR-004**: The system MUST NOT introduce a learned (as opposed to Beta-prior/statistically-
  refined) router until a configured minimum number of labelled (route, task class, verified
  outcome) samples exists.
- **FR-005**: Any candidate routing refinement (statistical or learned) MUST be evaluated
  against the current production router on the eval suite and MUST show a measurable
  improvement in cost per verified success before being adopted.
- **FR-006**: The system MUST support reverting a routing refinement to its prior approach,
  using the same recorded evidence trail that justified adopting it.
- **FR-007**: Auto-merge-to-main eligibility MUST be configured per named task class in
  `policy.yaml`, defaulting to disabled, and MUST require that class's measured false-success
  rate on the eval suite to be at or below an operator-configured threshold before the
  operator can enable it for that class.
- **FR-008**: Enabling auto-merge for one task class MUST NOT enable it, implicitly or
  automatically, for any other task class.
- **FR-009**: Auto-merge enablement/revocation for a class MUST be an operator-only
  `policy.yaml` edit — no agent role, decider, or router MUST be able to set it.
- **FR-010**: If a task class's definition (its matching policy glob/criteria) changes, the
  resulting broadened or altered class MUST be treated as unproven and MUST require
  re-earning its own false-success-rate threshold before auto-merge applies to it.
- **FR-011**: The system MUST exclude, or trigger recomputation over, any historical outcome
  data later found to have been produced under a known verification bug, before that data is
  used to justify a routing refinement or an auto-merge threshold determination.

### Key Entities *(include if feature involves data)*

- **Eval Comparison Report**: reused from Feature 001 — the before/after comparison a
  prompt/route/policy change must pass.
- **Prompt Version**: a hashed, versioned prompt artifact, attributable to specific production
  outcomes.
- **Routing Refinement Candidate**: a statistically-refined or learned router proposal,
  evaluated against the current production router before adoption.
- **Task Class Auto-Merge Eligibility**: a per-class, operator-set policy flag plus the
  measured false-success-rate evidence that justified it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of tested prompt/route/policy changes in this feature's test suite are
  blocked from taking effect in production when they produce a statistically significant eval
  regression, and 100% proceed when they don't.
- **SC-002**: No learned router is adopted in testing before the configured minimum labelled-
  sample threshold is met, and no routing refinement is adopted without first demonstrating a
  measurable cost-per-verified-success improvement on the eval suite.
- **SC-003**: Auto-merge, once enabled for one task class in a test scenario, is confirmed
  disabled for every other task class in the same policy state.
- **SC-004**: Revoking auto-merge for a class immediately requires human approval for the very
  next task of that class in testing — no task completes via stale auto-merge eligibility
  after revocation.
- **SC-005**: A redefinition of an auto-merge-enabled class's matching criteria results in that
  (now-different) class requiring its threshold to be re-earned, confirmed by at least one
  test scenario exercising a class redefinition.

## Assumptions

- This feature assumes Features 001 (eval suite), 003 (approvals), and 004 (routing) are
  already in place; it adds governance and data-driven refinement on top of their existing
  mechanisms rather than replacing them.
- The "configured minimum number of labelled samples" for introducing a learned router
  (FR-004) defaults to the "hundreds of labelled tasks" language in ARCHITECTURE.md §5.1/§17
  as an order-of-magnitude starting point, refined by the operator as real data accumulates.
- A learned router, if and when one is introduced, is treated as an optional, evidence-gated
  enhancement — this feature does not mandate building one, only the gate it must pass if one
  is ever proposed (consistent with ARCHITECTURE.md §17's explicit "don't build: a learned
  model router" before data exists).
- Auto-merge, even once enabled for a class, is assumed to still go through the full Feature
  002/004/006 pipeline (spec, test-first, verify, cross-family review, integrate) — "auto-merge"
  in this feature means skipping the human-approval gate specifically, not skipping
  verification or review.
- This feature is explicitly the last phase in the architecture's own ordering (§13 Phase 8);
  its `/speckit-plan`, when written, should treat Features 001–008 as hard prerequisites rather
  than something to parallelize against.
