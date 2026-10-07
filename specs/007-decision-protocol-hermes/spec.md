# Feature Specification: Decision Protocol and Hermes Integration

**Feature Branch**: `007-decision-protocol-hermes`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Add the cross-family decision protocol for high-impact or
irreversible decision points (frame, propose, filter, critique, experiment, decide, gate,
record-ADR), and wire in Hermes as the research worker and Telegram conversational relay with
read-only MCP tools, no approval authority."

**Source**: `ARCHITECTURE.md` §3.10/3.14, §4 (conditional roles), §5.2, §13 Phase 6,
Appendix B.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Settle a genuinely disputed, irreversible decision with evidence, not a
vote (Priority: P1)

As the operator, when a spec flags a decision point (high impact or irreversible, at least two
plausible options, no single test settles it), the system runs the full decision protocol —
frozen criteria, blind cross-family proposals, filtering, critique, an experiment where an
empirical claim is disputed, and a final decision scored against the frozen criteria — and
records the result as an ADR, rather than letting the specifier's default stand unchallenged
or letting models vote.

**Why this priority**: This is constitution Principle VIII's gated-deliberation machinery,
made concrete — the architecture is explicit that voting on design choices is forbidden and
that criteria must be frozen before proposals are seen, to stop the decider from rationalizing
an answer after the fact.

**Independent Test**: Can be tested by running the worked example from ARCHITECTURE.md
Appendix B (Redis vs. SQLite vs. PostgreSQL for a cart/session store) through the protocol and
confirming: criteria are frozen before any proposal is generated, Redis is filtered out with a
stated reason, the SQLite/PostgreSQL question is resolved by an experiment with a
pre-agreed target, and the decision plus dissent is recorded as an ADR.

**Acceptance Scenarios**:

1. **Given** a spec flags a decision point, **When** the decide protocol starts,
   **Then** the decider frames the question, hard constraints, weighted criteria, and the
   evidence that would discriminate between options — and this framing is frozen (recorded,
   immutable) BEFORE any proposal is generated.
2. **Given** frozen criteria, **When** 2–3 proposers (different model families, blind to each
   other's output) respond, **Then** each proposal states how it meets each constraint/
   criterion, its assumptions/risks, cost/reversibility, and falsifiable predictions.
3. **Given** a proposal that violates a frozen hard constraint, **When** the filter step runs,
   **Then** that proposal is dropped with a stated reason, not merely outscored.
4. **Given** surviving proposals disagree on an empirical (not preference) claim and a cheap
   experiment fits the budget, **When** the experiment step runs, **Then** it executes in a
   sandbox against a metric agreed before the experiment ran, and that metric — not a model's
   post-hoc impression — settles that sub-question.
5. **Given** a final decision, **When** it is recorded, **Then** the system writes both an ADR
   markdown file in the target repo and a `decisions` row capturing criteria, options,
   evidence, the choice, a confidence score, the strongest dissent, and what would change the
   decision.
6. **Given** at NO point in this flow, **When** the decision protocol runs, **Then** proposals
   are never resolved by a vote — disputes are resolved by constraint filtering, evidence, or
   an experiment, never by counting how many proposers favored an option.

---

### User Story 2 - Gate irreversible, low-confidence decisions to the operator (Priority: P1)

As the operator, if a decision is irreversible and either the decider's confidence is below
0.7 or the critics' dissent is unresolved, I get a Telegram brief (the problem, the options,
one recommendation) before the decision is acted on — rather than the system proceeding
autonomously on a call it isn't confident about.

**Why this priority**: This is the decision protocol's own safety valve, and it reuses Feature
003's nonce-bound approval machinery for exactly the highest-stakes case the architecture
identifies.

**Independent Test**: Can be tested by forcing a decision through with confidence below 0.7
and confirming a Telegram approval-style brief is sent and the decision does not proceed until
answered; and separately, forcing a high-confidence, reversible decision and confirming it
proceeds without a gate.

**Acceptance Scenarios**:

1. **Given** a decision is irreversible AND (confidence < 0.7 OR critic dissent is unresolved),
   **When** the decide step completes, **Then** a Telegram brief is sent via Feature 003's
   control plane, and the decision's downstream nodes remain blocked until the operator
   responds.
2. **Given** a decision is reversible, or confidence ≥ 0.7 with no unresolved dissent,
   **When** the decide step completes, **Then** the task proceeds without a human gate, with
   the decision still recorded as an ADR.
3. **Given** the operator responds to a gated decision brief, **When** the response is
   processed, **Then** it uses the same nonce-bound approval mechanism as Feature 003's other
   gates — a stale or mismatched decision brief cannot be approved by a later, different
   brief's button.

---

### User Story 3 - Let Hermes research and relay conversation, with zero control authority
(Priority: P2)

As the operator, I can ask Hermes (via Telegram free text, per Feature 003) about a task's
status, get web research folded into a spec's grounding step, and ask "why did we choose X?" —
all without Hermes ever being able to approve, cancel, or change policy, and without Hermes
holding any repo write or GitHub push credential.

**Why this priority**: This is the architecture's explicit split: Hermes is the conversational
front end, research worker, and personal memory — never the orchestrator (constitution
Principle I) — made concrete as read-only MCP tools and a scoped research profile.

**Independent Test**: Can be tested by asking Hermes, via Telegram free text, to explain a
past decision and confirming it answers using `foreman_why`/`foreman_search_memory` MCP tools
against Feature 005's authoritative store, and separately confirming Hermes has no tool or
credential capable of approving, cancelling, or pushing code.

**Acceptance Scenarios**:

1. **Given** a GROUND step needs outside facts, **When** the Hermes research profile
   (`foreman-research`) is invoked via Hermes's API server, **Then** it returns a
   `ResearchReport` (claims, each with a cited URL) that the specifier can use as grounding
   input.
2. **Given** a Telegram free-text message (not a command), **When** it is relayed per Feature
   003, **Then** Hermes answers using `foreman_create_task`, `foreman_status`,
   `foreman_list_tasks`, `foreman_why`, and `foreman_search_memory` MCP tools — and no other
   tools capable of approving, cancelling, changing policy, or writing to a repo.
3. **Given** Hermes's own model/provider configuration, **When** it operates as the chat/
   research front end for this feature, **Then** it is NOT running on the same credential path
   used for unattended Claude API calls elsewhere in the system (per ARCHITECTURE.md §5.3's
   Pro-vs-API-key rule), to avoid conflating conversational use with automated use.

---

### Edge Cases

- What happens when a decision point's criteria are frozen but a proposer later claims a
  constraint was "unfair" or missing? The frozen framing MUST NOT be revised mid-protocol;
  disagreement with the framing itself is out of band — it is a product of the specifier's
  decision-point flag, revisited (if at all) only via a subsequent replan, not by editing
  frozen criteria.
- How does the system handle only one proposal surviving the filter step (all others violated
  hard constraints)? The decider MUST still score it against the frozen criteria (it is not
  automatically "the decision" just because it's the only survivor) and MUST still record
  confidence and dissent (even if dissent is "none observed").
- What happens when an experiment's result is ambiguous (doesn't clearly favor either side
  against the pre-agreed target)? The decider MUST treat this as low confidence for that
  option and MUST NOT silently round an ambiguous result into a confident decision.
- What happens when Hermes's research step returns a claim with no citable URL? That claim
  MUST be excluded from the `ResearchReport`, or clearly flagged as uncited, never presented
  with the same confidence as a cited claim.
- What happens when a Telegram free-text question asks Hermes to "just approve it"? Per
  Feature 003's FR-006, this MUST be refused — Hermes has no approval tool, so the request
  cannot succeed regardless of phrasing.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST trigger the decision protocol only when a spec explicitly flags
  a decision point meeting all of: high impact or irreversible, ≥2 plausible options, no
  single test settles it — otherwise the specifier's default stands, recorded as a minor
  decision (no protocol run).
- **FR-002**: The decider MUST frame and freeze the question, hard constraints, weighted
  criteria, and discriminating evidence BEFORE any proposal is generated or seen.
- **FR-003**: The system MUST solicit 2–3 proposals from different model families, each blind
  to the others' content, each stating constraint/criterion fit, assumptions/risks, cost/
  reversibility, and falsifiable predictions.
- **FR-004**: The system MUST filter out, with a stated reason, any proposal violating a frozen
  hard constraint, before critique or scoring.
- **FR-005**: The system MUST run a critique step where each surviving proposal is examined by
  a different model family than its author, surfacing only constraint violations, factual
  errors, or hidden costs — each critique point MUST cite evidence or be explicitly marked
  speculative.
- **FR-006**: The system MUST run a sandboxed experiment, against a metric agreed before the
  experiment executes, when surviving proposals disagree on an empirical (not preference)
  claim and a cheap experiment fits budget.
- **FR-007**: The decider MUST score anonymized, normalized proposals against the frozen
  criteria using available evidence, and MUST output: the choice, rationale, a confidence
  score, the strongest dissent, and what would change the decision.
- **FR-008**: The system MUST NEVER resolve a decision-protocol disagreement by voting; votes
  are permitted elsewhere only on outputs a test can check (per constitution Principle V/VIII),
  never on protocol decisions.
- **FR-009**: The system MUST gate a decision to human approval (via Feature 003's nonce-bound
  mechanism) whenever it is irreversible AND (confidence < 0.7 OR unresolved critic dissent).
- **FR-010**: The system MUST record every decision (gated or not) as both a git-committed ADR
  markdown file and a `decisions` row, per Feature 005's schema.
- **FR-011**: The system MUST expose a `foreman-research` Hermes profile, invoked via Hermes's
  API server, with web and read-only file access, returning a `ResearchReport` of claims each
  carrying a citable URL.
- **FR-012**: The system MUST expose exactly these MCP tools to Hermes: `foreman_create_task`,
  `foreman_status`, `foreman_list_tasks`, `foreman_why`, `foreman_search_memory` — and MUST NOT
  expose any tool capable of approval, cancellation, policy change, or repo write/push.
- **FR-013**: Hermes's own conversational/research model credential path MUST be distinct from
  any credential path used for unattended automated LLM calls elsewhere in the system.

### Key Entities *(include if feature involves data)*

- **Decision Point**: a spec-flagged question meeting the trigger criteria in FR-001.
- **Proposal**: one proposer's blind response to frozen criteria — fit, assumptions, cost/
  reversibility, falsifiable predictions.
- **Critique**: a cross-family examination of a surviving proposal, evidence-cited or marked
  speculative.
- **ExperimentReport**: the result of a sandboxed experiment against a pre-agreed metric.
- **Decision/ADR**: the decider's final output — choice, rationale, confidence, dissent,
  reversibility — recorded both in SQLite and as repo markdown (reuses Feature 005's entity).
- **ResearchReport**: Hermes's research-profile output — claims, each with a citable URL.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Running the Appendix B worked example (datastore choice) through the protocol
  produces: a dropped Redis proposal with a stated constraint-violation reason, an experiment
  with a pre-agreed target metric, and a recorded ADR with confidence and dissent — matching
  the worked example's shape.
- **SC-002**: In testing, zero decision-protocol disagreements are resolved by counting
  proposer votes; every resolution traces to either constraint filtering, cited evidence, or
  an experiment result.
- **SC-003**: Every decision gated to the operator (irreversible + low-confidence/dissent) is
  blocked from proceeding until an operator response is received and validated through
  Feature 003's nonce mechanism; every ungated decision proceeds without waiting for one.
- **SC-004**: Hermes, when probed with every exposed MCP tool plus adversarial free-text
  requests asking it to approve/cancel/push, never succeeds at any of those actions — because
  no such tool is exposed to it.
- **SC-005**: Every `ResearchReport` produced in testing has a citable URL for every included
  claim; no uncited claim appears with the same presentation as a cited one.

## Assumptions

- This feature depends on Feature 003 (Telegram nonce-bound approvals) and Feature 005
  (memory/ADR schema) already existing; it adds the decision-protocol orchestration and
  Hermes wiring on top of both.
- The "different model family" requirements throughout (proposers, critics, deciders) reuse
  Feature 004's router and family-exclusion rules rather than reimplementing family selection
  logic independently.
- Hermes's exact invocation mechanism (API server streaming run events, per
  ARCHITECTURE.md §3.14) is treated as already available infrastructure from the Hermes
  project itself; this feature specifies the contract (profiles, MCP tools, report shape) it
  consumes, not Hermes's own internals.
- The confidence threshold (0.7) and reversibility classification come directly from
  ARCHITECTURE.md Appendix B and §5.1's framing; they are operator-adjustable via
  `policy.yaml` in a future iteration but ship as fixed defaults in this feature.
- Calibrating confidence scores against actual outcomes (Brier score tracking per
  ARCHITECTURE.md Appendix B) is noted as a future enhancement once sufficient decision volume
  exists; this feature records confidence and outcome data sufficient to compute it later, but
  does not implement the calibration logic itself.
