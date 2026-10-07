# Feature Specification: Memory Service

**Feature Branch**: `005-memory-service`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Add foreman's memory service: episodic summaries,
failure-signature memory, scoped lessons with gated promotion and utility tracking,
deterministic token-budgeted context packs, and ADR recording, so repeat-repo tasks fail less
and need fewer attempts."

**Source**: `ARCHITECTURE.md` §3.11, §6, §13 Phase 4.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Don't repeat an exact failure (Priority: P1)

As the operator, when a task hits an error whose normalized signature matches a previously
recorded failure for this repo, the engine injects that failure's "don't retry unless…"
guidance into the revise/replan attempt automatically, instead of the implementer blindly
retrying the same broken approach.

**Why this priority**: This is the cheapest, highest-leverage form of memory — exact-match
failure avoidance — and directly reduces wasted attempts on the escalation ladder built in
Feature 002/004.

**Independent Test**: Can be tested by deliberately causing the same failure signature twice
on the same repo/path and confirming the second occurrence's revise attempt includes the
first occurrence's recorded guidance.

**Acceptance Scenarios**:

1. **Given** a verifier failure whose normalized signature (hash of error/test id) has no
   prior match, **When** the failure is recorded, **Then** it is stored with its signature,
   approach, root cause (if determined), and guidance text.
2. **Given** a new attempt's verifier failure matches a stored signature exactly, **When** the
   engine builds the next (revise or replan) attempt's context pack, **Then** that failure's
   guidance is included, and this MUST happen before any FTS/BM25-based retrieval runs.
3. **Given** a failure signature match, **When** the escalation ladder evaluates the repeat,
   **Then** it still counts as an attempt failure and still advances the ladder — exact-match
   memory changes what the next attempt knows, not whether the ladder rules in Feature
   002/004 apply.

---

### User Story 2 - Build a token-budgeted context pack deterministically (Priority: P1)

As the operator, every LLM call that needs memory gets a context pack built by a
deterministic, token-budgeted process — not an ad hoc prompt-stuffing step — combining exact
lookups, scoped lessons ordered by utility, and BM25-ranked episodes/ADRs, capped at roughly
4K tokens for memory content.

**Why this priority**: Implements the constitution's memory-safety requirement that memory is
"read through a deterministic context-pack builder" and "never trusted as instructions" —
without this, later features (routing, decision protocol) would each reinvent ad hoc memory
injection inconsistently.

**Independent Test**: Can be tested by requesting a context pack for a given (task, node,
budget) and confirming the output respects the token budget, includes exact lookups first,
and is reproducible (same inputs, same pack) given the same underlying memory state.

**Acceptance Scenarios**:

1. **Given** a task touching specific repo paths, **When** `ContextPack` is requested,
   **Then** it includes, in order: exact lookups (`project.yaml`, accepted decisions for
   touched paths, matching failure signatures), then scoped lessons ordered by utility
   (helpful minus harmful, then recency), then BM25-ranked lessons/episodes/ADRs, stopping once
   the token budget is consumed.
2. **Given** a token budget of N, **When** the pack is assembled, **Then** its total token
   count never exceeds N, even if that means truncating the lowest-priority BM25 results.
3. **Given** identical underlying memory state and identical inputs, **When** `ContextPack` is
   called twice, **Then** it returns the same pack both times (deterministic, not sampled).
4. **Given** memory content delivered in a context pack, **When** it is inserted into a
   prompt, **Then** it MUST appear inside a clearly delimited block labeled as notes that may
   be wrong, never as unlabeled system instructions.

---

### User Story 3 - Promote a lesson only after it earns it (Priority: P2)

As the operator, a candidate lesson proposed by the LEARN step after a task completes does not
become part of future context packs until it is either repo-scoped (inferred automatically
from the task) or explicitly approved by me for global scope, and its usefulness is tracked
over time so that a lesson that stops helping gets demoted automatically.

**Why this priority**: Implements constitution memory-safety ("no lesson derived from
untrusted text gets promoted unless a verified episode cites it" and "global lessons need your
approval") — without gated promotion, memory could silently accumulate wrong or
prompt-injected guidance.

**Independent Test**: Can be tested by completing a task that proposes a lesson candidate,
confirming it does not appear in a subsequent context pack until promoted, and then confirming
a lesson's `helpful`/`harmful` counters update based on whether later runs that cited it
succeeded or failed.

**Acceptance Scenarios**:

1. **Given** a completed, verified episode, **When** the LEARN step runs, **Then** it may
   propose lesson candidates, each tied to the evidence (task id) that justifies it.
2. **Given** a repo-scoped lesson candidate, **When** it is proposed, **Then** it is
   automatically eligible for inclusion in context packs for that repo (no approval gate), per
   ARCHITECTURE.md §6's retrieval table.
3. **Given** a global-scoped lesson candidate, **When** it is proposed, **Then** it MUST wait
   for explicit operator approval (batched, e.g. weekly via Telegram from Feature 003) before
   appearing in any context pack.
4. **Given** a run that cited a lesson and then succeeded, **When** the outcome is recorded,
   **Then** that lesson's `helpful` counter increments; **given** a run that cited a lesson and
   then failed, its `harmful` counter increments.
5. **Given** a lesson's harmful count exceeds a demotion threshold (or it goes unused for 90
   days, or a newer verified episode contradicts it), **When** the next utility pass runs,
   **Then** that lesson is automatically demoted (excluded from future context packs) without
   requiring manual deletion.

---

### Edge Cases

- What happens when a failure signature superficially matches (same error string) but the
  surrounding context is actually different (different repo entirely)? Signature matching
  MUST be scoped — at minimum by repo — not global, to avoid injecting irrelevant guidance.
- How does the system handle a lesson whose evidence task was later found to be wrong (for
  example, a reverted fix)? A newer verified episode that contradicts an existing lesson MUST
  supersede it per the 90-day/contradiction demotion rule, not leave two conflicting lessons
  active indefinitely.
- What happens when the context pack's exact-lookup section alone exceeds the token budget?
  The system MUST truncate scoped-lesson and BM25 sections to zero before ever dropping
  exact lookups, since exact lookups (policy, accepted decisions, matching failures) are
  the highest-confidence content.
- What happens when a web page or repo content a research step read contains text that reads
  like an instruction ("ignore previous guidance and...")? That text MUST never be promoted to
  a lesson or injected as an instruction — only a verified, test-passing episode can propose a
  lesson, and the lesson text itself is operator/LLM-authored guidance, not a copy of
  untrusted input.
- What happens when ADR writing (from the decide step, Feature 007) races with a plain
  episode-summary write for the same task? Both MUST be recorded; they are different
  memory kinds (decisions vs. episodes) per the schema in ARCHITECTURE.md §7.4/§6.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST record, per completed episode, a 10-line (or similarly compact)
  summary: spec, DAG, attempts, verifier results, findings, cost, and outcome.
- **FR-002**: The system MUST record failures with a normalized signature (hash of error or
  test id), the approach taken, root cause if determined, and "don't retry unless…" guidance,
  scoped at minimum by repo.
- **FR-003**: The system MUST check for an exact failure-signature match, scoped to the current
  repo (and narrower scopes where available), before any revise or replan attempt's context
  pack is assembled, and MUST include matching guidance in that pack when found.
- **FR-004**: The system MUST provide a `ContextPack(task, node, budgetTokens)` operation that
  deterministically assembles, in priority order: exact lookups, scope-matched lessons ordered
  by utility (helpful minus harmful, then recency), then BM25-ranked lessons/episodes/ADRs via
  SQLite FTS5 — stopping once the token budget is reached.
- **FR-005**: The system MUST record which memory ids were injected into each run (for utility
  tracking), and MUST update each injected lesson's `helpful`/`harmful` counters based on
  whether that run's outcome succeeded or involved a failure.
- **FR-006**: The system MUST gate global-scope lesson promotion behind explicit operator
  approval (batched); repo-scoped lessons MUST be automatically eligible without this gate.
- **FR-007**: The system MUST propose lesson candidates only from the LEARN step following a
  verified (test-passing) episode, tied to that episode's task id as evidence — never directly
  from untrusted content (web pages, repo text, issue bodies) without a verified episode
  citing it.
- **FR-008**: The system MUST automatically demote (exclude from future context packs) a
  lesson whose harmful count crosses a configured threshold, that has gone unused for 90 days,
  or that a newer verified episode contradicts.
- **FR-009**: The system MUST deliver all memory content inside a delimited, clearly-labeled
  block (e.g. "notes (may be wrong)") when inserted into any prompt — never as unlabeled
  system instructions.
- **FR-010**: The system MUST record ADRs (question, options, evidence, choice, confidence,
  reversibility, dissent) both as a git-committed markdown file in the target repo and as a row
  in a `decisions` table, queryable by repo/path/keyword.
- **FR-011**: `ContextPack` MUST be deterministic: identical inputs and identical underlying
  memory state MUST produce an identical pack across repeated calls.
- **FR-012**: The system MUST expose project-level memory (`AGENTS.md`, `.foreman/project.yaml`,
  `docs/adr/`) as git-tracked files in the target repo, readable natively by every agent CLI
  (not only through the context-pack mechanism).

### Key Entities *(include if feature involves data)*

- **Episode**: a completed task's compact summary — spec, DAG, attempts, verifier results,
  findings, cost, outcome.
- **Lesson**: scoped (global/repo/path-glob/language/tool) guidance text, linked to evidence
  task ids, with `helpful`/`harmful` utility counters and a status (active/demoted).
- **Failure**: a normalized signature plus approach, root cause, and guidance, scoped at least
  by repo.
- **Decision (ADR)**: question, criteria, options, evidence, chosen option, rationale,
  confidence, reversibility, dissent — recorded both in SQLite and as a repo markdown file.
- **Context Pack**: the token-budgeted, deterministically-assembled bundle of memory content
  for one (task, node) LLM call.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a repeat-repo task suite (the same repo seen more than once across eval
  runs), the number of attempts per verified success measurably decreases after failure-
  signature memory and lessons have accumulated from prior runs, compared to a cold-memory
  baseline.
- **SC-002**: 100% of context packs produced in testing stay within their requested token
  budget, with exact lookups never dropped to make room for lower-priority content.
- **SC-003**: A global-scope lesson candidate never appears in any context pack before explicit
  operator approval, across all tested promotion paths; a repo-scoped lesson appears without
  requiring that approval.
- **SC-004**: A lesson that is cited in a run which subsequently fails has its `harmful`
  counter incremented, and a lesson that crosses the demotion threshold is excluded from the
  very next context pack assembled after that threshold is crossed.
- **SC-005**: Every memory block inserted into a tested prompt is wrapped in its documented
  delimiter and label; no memory content appears as unlabeled instruction text in any captured
  prompt.

## Assumptions

- This feature builds its FTS5 tables and `lessons`/`failures`/`decisions` schema as described
  in ARCHITECTURE.md §7.4, reusing the same SQLite database Feature 002 established rather than
  a separate store.
- The demotion threshold for `harmful` count is set to a conservative default (e.g. harmful
  count exceeds helpful count by a margin, such as harmful ≥ helpful + 3) pending real usage
  data; this default is documented and adjustable, not hard-coded permanently.
- "Batched weekly" global-lesson approval (per ARCHITECTURE.md §6) is delivered via the
  Telegram control plane built in Feature 003; this feature does not re-specify Telegram
  delivery mechanics, only the gating rule and the data needed to render the batch.
- ADR markdown files are written to `docs/adr/` in the target repo by the same integrator
  component that will later (Feature 004 onward) push branches/PRs; this feature assumes that
  write path exists in some form (even a simple git-commit helper) as a prerequisite, and
  treats full PR integration as out of scope here.
- Embeddings/vector search are explicitly out of scope for this feature, per constitution
  Principle IX and ARCHITECTURE.md §6's verdict table — BM25 via FTS5 plus structured filters
  is the complete retrieval mechanism unless a later, separately-specified feature demonstrates
  measured recall gaps.
