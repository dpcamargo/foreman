<!--
Sync Impact Report
Version change: 1.1.1 → 2.0.0
Rationale: MAJOR — Principles II and III are redefined. Operator decision (spec 001
  clarification, 2026-10-07): foreman orchestrates any kind of work, with code as one task type.
  Code tasks keep exactly the old rules; non-code task types may now complete through a
  declared verifier (code checks + cross-family rubric + human approval), which the old
  Principle II did not allow.
Modified principles:
  - II. Execution-Based Verification Decides "Done" → II. Evidence-Based Verification Decides "Done"
  - III. Test-First, by a Different Model Family, Failing on Base → III. Acceptance Criteria
    First, by a Different Model Family
  - VIII. adds a dry-run-then-approve rule for task types with external side effects.
Modified sections: mission paragraph; Development Workflow ("Evaluation precedes" now per task type).
Deferred: none.

Previous report (v1.1.1):
Version change: 1.1.0 → 1.1.1
Rationale: PATCH — resolves the deferred TODO(ORCHESTRATOR_HOSTING) with the operator's
  decision (docs/adr/0001-orchestrator-hosting.md). No principle changed.
Modified sections: Technology Stack (Orchestrator line).
Deferred: none.

Previous report (v1.1.0):
Version change: 1.0.0 → 1.1.0
Rationale: MINOR — one rule redefined to match its source (unattended model use is now
  per-vendor terms, not "API keys only"), and one materially new quality gate (token economy).
Modified sections:
  - Technology Stack: "Unattended model use" rewritten per vendor terms; Claude worker is
    now "Claude via Hermes" (operator decision); orchestrator hosting marked OPEN.
  - Development Workflow: baseline arms renamed to match spec 001; new "Token economy" gate.
Principles I–IX: unchanged.
Deferred: TODO(ORCHESTRATOR_HOSTING) — launchd host process vs container; must be decided
  before /speckit-plan (see specs/ROADMAP.md).

Previous report (v1.0.0):
Version change: (none, template) → 1.0.0
Rationale: MINOR/MAJOR not applicable to an initial ratification — treated as the baseline
  major version per semver convention for a project's first governing document.
Modified principles: n/a (initial adoption)
Added sections:
  - Core Principles I–IX (Deterministic Orchestration; Execution-Based Verification;
    Test-First by a Different Model Family; Single-Threaded Writes; Cross-Family Review
    With Evidence; Isolation by Default; Durable & Resumable State; Deliberation and
    Human Approval Are Gated; Minimal, Justified Infrastructure)
  - Technology Stack & Component Boundaries
  - Development Workflow & Quality Gates
  - Governance
Removed sections: n/a (initial adoption)
Deferred placeholders: none — all template tokens resolved from
  /Users/dpcamargo/repos/harness-architecture/ARCHITECTURE.md (dated 2026-10-06), which is
  this project's source design document.
Templates requiring follow-up: none checked yet against this constitution (no plan.md/
  tasks.md exist). Re-validate .specify/templates/plan-template.md's Constitution Check
  section against these principles the first time /speckit-plan runs.
-->

# Foreman Constitution

Foreman is a deterministic Go orchestrator for autonomous work. Code changes in git
repositories are the first and strictest task type; other work (research, documentation,
operations) is added as further task types, each behind its own declared verifier. foreman owns
task state, budgets and approval gates; dispatches headless agents (Codex, Antigravity, and
Claude via Hermes) as workers; and treats verification by evidence, not model agreement, as the
sole arbiter of "done." Hermes is the conversational front end, web-research worker and
personal memory — never the control loop. This constitution codifies the non-negotiable design
corrections in `/Users/dpcamargo/repos/harness-architecture/ARCHITECTURE.md`; it is binding on
every spec, plan and task produced for this repository.

## Core Principles

### I. Deterministic Orchestration (No LLM in the Control Loop)
The orchestrator — task/node state machine, DAG, reconciler, scheduler, budgets, retries,
timeouts and approval gates — MUST be deterministic code (Go, backed by SQLite), never a
model. LLMs MUST be called only at explicitly defined decision points (triage, specify,
test-authoring, review, decide), and every such call MUST require structured JSON-schema
output. No feature may introduce a "manager agent," a standing "monitor agent," or any
LLM-driven branch of the control flow itself.
Rationale: controlled studies show independent multi-agent control loops amplify errors
17.2× versus 4.4× under centralized coordination, and sequential-planning variants lost
39–70% performance (ARCHITECTURE.md §1). An LLM in the loop is non-deterministic, not
durable, injectable, and costs money every tick.

### II. Evidence-Based Verification Decides "Done"
Every task type MUST declare, before it can be used, a verifier that decides "done" from
evidence produced outside the agent that did the work. For code tasks this is unchanged: a
`VerifyReport` from code running real builds, lint/vet, visible tests, hidden tests, and
diff-policy checks in a clean, network-isolated container. For non-code task types the verifier
MUST run every check code can run (for example: cited URLs resolve and contain the quoted text;
documents build and their links resolve; an operation's post-conditions are probed), then grade
the rest against a rubric fixed before execution, by a model family different from the
author's, and require human approval for whatever neither can settle. A commit, a diff, an
artifact, or an agent's self-report of completion MUST NEVER by itself complete a task. An LLM
judge MUST NOT be used for any question a test or code check can settle.
Rationale: this is the project's central correction — "what works in coding is a strong single
writer plus tests plus candidate selection" (ARCHITECTURE.md §1), and verification is the
cheapest, most reliable judgment available.

### III. Acceptance Criteria First, by a Different Model Family
For every task beyond trivial (lane F) work, acceptance criteria MUST be fixed from the spec,
by a model family different from the executor, BEFORE execution begins. For code tasks these
are acceptance tests that the verifier confirms fail on the base commit, with a hidden subset
withheld from the implementer's workspace at all times. For non-code task types they are
machine-checkable criteria plus a rubric, with a held-back subset the executor never sees
wherever the task type allows it.
Rationale: this turns "did the implementer build the right thing?" from a judgment call into
an execution check, and catches test-gaming that a same-family implementer could otherwise
satisfy superficially.

### IV. Single-Threaded Writes, Parallel Only When Disjoint
At most one writer MUST operate on a given piece of code at a time. Parallelism is permitted
only for genuinely disjoint DAG nodes, or for read-only work: grounding, repo scouting, web
research, and independent review. Best-of-N or best-of-2 candidate generation is permitted
only in lane D or after repeated failure, never as a default execution strategy.
Rationale: "writes stay single-threaded" even where several agents contribute intelligence
(ARCHITECTURE.md §1, citing Cognition's 2026 multi-agent findings); parallel writers on shared
code amplify errors without centralized verification to contain them.

### V. Cross-Family Review With Evidence
A reviewer MUST belong to a different model family than the implementer it reviews; the same
model family MUST NEVER review its own output. A blocking review finding (severity `major` or
above) MUST carry evidence — a failing repro test, a concrete exploit path, or a cited spec
clause — or it MUST be automatically downgraded to advisory. Disagreements between an
implementer and a reviewer that survive one rebuttal MUST go to an adjudicator from a third,
previously uninvolved model family, not to a vote.
Rationale: LLM evaluators are measurably biased toward their own generations, and voting
captures most of debate's benefit only for outputs a test can check — not design judgment
(ARCHITECTURE.md §1, §11.2, Appendix B).

### VI. Isolation by Default
Every write-capable agent run MUST execute in a workspace that is a fresh, disposable clone
pinned to a base commit, with no secrets and an explicit egress allowlist — never a linked
git worktree sharing the main repository's `.git` common directory (hooks and config are not a
security boundary). Every verification run MUST execute with no network access and no host
credentials. Results MUST leave the workspace only as an exported patch or bundle, applied on
the host with hooks disabled.
Rationale: a compromised or merely careless agent must be contained to one disposable
workspace, one branch and that run's budget — isolation is phase 1 infrastructure, not a
later hardening pass (ARCHITECTURE.md §1 item 3–4, §8).

### VII. Durable & Resumable State
All task, node and run state MUST live in SQLite (WAL mode), committed with its corresponding
event in a single transaction. The system MUST survive process crashes and host sleep without
corruption or silent loss: a reconciler MUST, on every tick and at startup, expire stale
leases and detect dead PIDs (via start-time fingerprint, not bare PID reuse) and requeue their
work automatically. Every run MUST be idempotent, starting from a pinned base commit, with its
node transition committed only after artifacts validate.
Rationale: a laptop that sleeps and moves around "can't be always available" — resumability
has to be structural, not best-effort (ARCHITECTURE.md §0, §7.4).

### VIII. Deliberation and Human Approval Are Gated, Not Default
Most tasks MUST route straight to a single implementer plus verification (lane F); multi-
proposal deliberation, critique, experiments and the decide protocol MUST trigger only when a
spec explicitly flags a decision point as high-impact or irreversible, with at least two
plausible options that no single test can settle. Voting on design choices is forbidden;
voting is permitted only on outputs a test can check. High-risk specs, deliveries and merges
to a protected branch MUST require an explicit, nonce-bound human approval tied to the exact
artifact approved; approvals MUST NEVER be granted automatically on a timeout, and a
compromised approval channel MUST be constrained by policy from approving production deploys,
secret changes, or policy changes. Any task type that acts on systems outside foreman's own
workspace (operations) MUST first produce a dry-run or plan artifact, and MUST NOT perform an
irreversible external action without a nonce-bound human approval of that artifact.
Rationale: deliberation costs the same whether or not it is needed, and an Anthropic
multi-agent research system used roughly 15× the tokens of a plain chat for exactly this
reason (ARCHITECTURE.md §1, §2.2, §9).

### IX. Minimal, Justified Infrastructure
New always-on services, databases, message buses, or orchestration frameworks MUST NOT be
added unless an existing approach (SQLite in WAL mode with FTS5, plain files, git) is measured
— not assumed — to fall short of a real requirement. Each vendor agent CLI (Codex, Claude
Code, Antigravity) MUST be used through its own mature sandbox and structured-output flags
rather than reimplemented as a custom agent loop. Any new dependency, service, or agent role
introduced in a plan MUST be justified in that plan's Complexity Tracking against which
principle it serves.
Rationale: "a roughly 100-line, bash-only agent scores above 74% on SWE-bench Verified" — the
scaffold is rarely the bottleneck, and every unjustified service is something this one-person,
one-host system now has to operate forever (ARCHITECTURE.md §1, §3.7, §17).

## Technology Stack & Component Boundaries

- **Orchestrator**: Go, one binary, run by `launchd` as a host process under a dedicated
  `foreman` macOS user (ADR 0001). It reaches containers only through its own container-runtime
  instance whose VM mounts nothing but foreman's workspace root — never the operator's default
  runtime socket. Containerizing foreman is revisited in Feature 008. It runs a
  level-triggered reconcile loop plus in-process event wakeups. No workflow framework
  (Temporal, DBOS, LangGraph) until the DAG outgrows a data-driven model — see Principle IX.
- **Store**: SQLite in WAL mode (`modernc.org/sqlite` or `mattn/go-sqlite3`), FTS5, `sqlc`,
  versioned migrations. One writer process. PostgreSQL is out of scope until multiple hosts
  write concurrently.
- **Coding workers**: headless vendor agents only — `codex exec`, `agy -p`, and Claude via a
  dedicated Hermes profile (no MCP servers, file/terminal tools only) — each
  normalized to a `RunResult` (status, structured JSON, usage, transcript path, patch,
  failure class/signature). No custom coding-agent loop.
- **Research/front end**: Hermes, invoked via its API server or MCP tools, scoped to
  conversation, web research and personal memory. Hermes MUST NOT hold approve/cancel
  authority or repo write/push credentials.
- **Memory**: SQLite + FTS5 for episodes, lessons, failures and decisions; git for
  `AGENTS.md`, `.foreman/project.yaml`, and ADRs. No vector DB, graph DB, or event bus unless
  measured recall or throughput demonstrably requires one.
- **Credentials**: API keys, the Telegram bot token, and a fine-grained, repo-scoped GitHub
  integrator token live only on the trusted host process. Agent sandboxes and the verifier
  MUST NEVER hold personal `gh`/SSH credentials or network access to secrets.
- **Unattended model use**: subscription logins MUST be used only as each vendor's terms
  allow. The Anthropic subscription (Claude via Hermes) MUST be used only for tasks the
  operator started (`claude_subscription_unattended: false`); vendors whose terms allow
  automated plan use (e.g. Codex on a ChatGPT plan) may run unattended. Any API-key route MUST
  carry a per-task budget cap.

## Development Workflow & Quality Gates

- **Evaluation precedes the harness.** A task suite with hidden tests and a baseline runner
  (single-shot Hermes on Claude, single Codex session, Hermes `/goal`) MUST exist and be
  scoreable before new orchestrator capability is adopted; a change is adopted only if it
  beats the best single-agent baseline by the thresholds in ARCHITECTURE.md §16/Appendix D, or
  is rejected. Each task type is enabled only after its own eval task set and baseline
  comparison exist; code is first.
- **The escalation ladder is enforced by the engine, not the model.** Attempt failures move
  through: retry same route with evidence → escalate to a different model family, fresh from
  the spec → replan (specifier sees all evidence) → block for human input. Loop guards (repeat
  failure signature, no rising pass count, oscillating diff hash) MUST skip straight to the
  next rung.
- **Hard caps are mandatory and engine-enforced**: maximum nodes per task, max DAG depth, max
  revisions, max review rounds, max replans per lane, plus wall-clock and dollar budgets per
  lane (F/S/D). No task may exceed these caps regardless of what a model reports about its own
  progress or difficulty.
- **Diff policy gates every integration**: no test deletion, no undisclosed dependency/lockfile
  changes, no edits to protected paths without approval, a secret scan (e.g. gitleaks), and no
  symlinks escaping the workspace. A violation MUST stop the node, record a security event, and
  alert — never pass silently.
- **Continuous evaluation is the harness's own CI.** Any change to foreman's own prompts,
  routes, or policy MUST run against the eval suite before being trusted in production, and
  prompt versions MUST be hashed per run so changes are attributable.
- **Token economy.** Every LLM-calling step MUST have a token cap, use the cheapest route that
  meets its role's requirements, and reuse cached results (baselines, verified specs, context
  packs) instead of recomputing them. Token and quota spend MUST be reported per task and per
  eval run; a component with no measured spend is not done.
- **Spec and plan review follow the Constitution Check.** Every `/speckit-plan` MUST map each
  touched principle above to a concrete mechanism (which file enforces it, which test proves
  it) before implementation starts.

## Governance

- This constitution supersedes ad hoc practice for this repository. Where a spec, plan, or
  task conflicts with a principle here, the principle wins unless this document is amended
  first.
- **Amendments** are made by editing this file directly (via `/speckit-constitution`),
  recording a Sync Impact Report, and following semantic versioning: MAJOR for backward-
  incompatible principle removal/redefinition, MINOR for a new principle or materially
  expanded guidance, PATCH for wording/clarification only. `LAST_AMENDED_DATE` updates on
  every change; `RATIFICATION_DATE` never changes after initial adoption.
- **Compliance review**: every plan's Constitution Check section, and every PR touching
  `internal/engine`, `internal/verify`, `internal/policy`, or `internal/gitops`, MUST state
  explicitly how it satisfies or is exempt from each Core Principle. A deliberate exception
  (e.g. a new dependency) MUST be logged in that plan's Complexity Tracking with the principle
  it trades off against.
- **Irreversible or high-impact architectural decisions** (e.g. choosing a datastore, adding a
  new always-on service, changing the approval/isolation model) MUST follow the Decision
  Protocol in `ARCHITECTURE.md` Appendix B — framed criteria before proposals, cross-family
  proposals and critique, an experiment where an empirical claim is disputed, and a recorded
  ADR — rather than being settled by discussion alone.
- Use `ARCHITECTURE.md` (`/Users/dpcamargo/repos/harness-architecture/ARCHITECTURE.md`) as the
  authoritative design reference for anything this constitution does not itself resolve.

**Version**: 2.0.0 | **Ratified**: 2026-10-06 | **Last Amended**: 2026-10-07
