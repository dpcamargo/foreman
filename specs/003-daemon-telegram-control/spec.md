# Feature Specification: Daemon + Telegram Control Plane

**Feature Branch**: `003-daemon-telegram-control`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Turn foreman into a long-running daemon with a Telegram control
plane: launchd service, local unix-socket API, bot commands and inline-button approvals bound
to a nonce, an outbox notifier, pause/resume/cancel, and a reconciler that recovers cleanly on
restart."

**Source**: `ARCHITECTURE.md` §3.1/3.13, §7.2–7.3/7.4 (pause/cancel/approvals), §9, §13 Phase 2.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Create and track a task from my phone (Priority: P1)

As the operator, I send `/task <repo> <text>` from Telegram and get an immediate
acknowledgement, then pull `/status <id>` or receive pushed updates as the task moves through
the loop built in Feature 002, without needing to be at my laptop.

**Why this priority**: This is the entire reason to run foreman as a daemon instead of a
one-shot CLI — unattended operation requires a way to start and observe work remotely.

**Independent Test**: Can be fully tested by sending `/task` from a Telegram chat on the
allowlist and observing both the immediate acknowledgement and at least one subsequent pushed
update, with the daemon otherwise idle.

**Acceptance Scenarios**:

1. **Given** the daemon is running and the sender's numeric Telegram user id is on the
   allowlist, **When** `/task <repo> <text>` is received, **Then** the bot replies with an
   acknowledgement containing a task id within a few seconds, and a `TaskCreated` event is
   recorded.
2. **Given** a message from a user id NOT on the allowlist, **When** any command is received,
   **Then** the bot does not execute it and does not leak any task state in its (non-)response.
3. **Given** a task created via `/task`, **When** the operator sends `/status <id>`, **Then**
   the bot replies with the task's current phase and, if applicable, cost/time spent so far.

---

### User Story 2 - Approve or reject without an LLM anywhere near the decision (Priority: P1)

As the operator, when a task reaches a gate that requires my approval, I get a Telegram message
with inline **[Approve] [Reject] [Edit]** buttons bound to that specific spec/diff's hash via a
nonce; tapping Approve can never apply to a different, later version of that artifact, and no
free-text message I send can be mistaken for an approval.

**Why this priority**: Directly implements constitution Principle VIII — approvals must be
nonce-bound to the exact artifact approved, parsed by deterministic code, never inferred by a
model from conversation.

**Independent Test**: Can be tested by triggering an approval gate, changing the underlying
spec/diff after the approval request is sent (simulating a race), and confirming the bot
rejects a stale approval attempt bound to the old nonce.

**Acceptance Scenarios**:

1. **Given** a task reaches a policy-gated step (per Feature 002's ladder outcomes or a
   future policy), **When** the gate fires, **Then** Telegram receives a message with the
   gate's question, the relevant summary, and buttons bound to a nonce tied to the sha256 of
   the artifact being approved.
2. **Given** an approval request's underlying artifact has since changed (for example, a
   replanned spec), **When** the operator taps the stale button, **Then** the system rejects
   the approval as invalid (nonce/hash mismatch) and does not apply it.
3. **Given** the operator sends a free-text message instead of tapping a button, **When** the
   relay processes it, **Then** it is treated as conversation (routed to Hermes per Feature
   007) and MUST NOT be interpreted as an approval, rejection, or policy change under any
   circumstance.
4. **Given** the operator taps **[Reject]**, **When** the rejection is recorded, **Then** the
   system requires a reason (prompted if not supplied) and records it for the replan/lesson
   pipeline.

---

### User Story 3 - Pause, resume, or cancel from anywhere, and recover cleanly from a restart
(Priority: P2)

As the operator, I can send `/pause`, `/pause now`, `/resume`, or `/cancel <id>` at any time,
and if the daemon itself crashes or the host restarts, every in-flight task resumes correctly
without my having to do anything beyond restarting the process (which `launchd` does for me).

**Why this priority**: Builds on Feature 002's resumability (Principle VII) to make it
usable unattended — pause/cancel/resume are the minimum remote-control surface the
architecture requires before trusting this to run while the operator is away.

**Independent Test**: Can be tested by sending `/pause`, confirming no new nodes dispatch while
in-flight ones finish; sending `/pause now`, confirming running processes are killed and their
runs marked infra-cancelled-and-retriable; and killing the daemon process entirely, restarting
it, and confirming the reconciler recovers all leases.

**Acceptance Scenarios**:

1. **Given** a task has nodes in flight, **When** `/pause <id>` is received, **Then** no new
   node in that task is dispatched, but running nodes are allowed to finish normally.
2. **Given** a task has nodes in flight, **When** `/pause <id> now` is received, **Then** the
   engine kills those nodes' process groups, and the resulting runs are recorded as
   infra-cancelled (eligible for retry on resume), not as attempt failures.
3. **Given** the daemon process is killed entirely (simulating a crash or host restart),
   **When** it is restarted (by `launchd` or manually), **Then** the reconciler expires stale
   leases, detects dead PIDs via start-time fingerprint, and requeues affected work without
   operator intervention.
4. **Given** `/cancel <id>` is received, **When** processed, **Then** the task's running
   process groups and containers are killed, the task is marked `CANCELLED`, and its
   workspaces are retained for 7 days for forensics before cleanup.

---

### Edge Cases

- What happens when Telegram is unreachable (network outage, API down)? Tasks MUST continue
  running; outbound notifications MUST queue in the outbox and deliver, coalesced into a
  digest, once connectivity returns. Approvals MUST wait and MUST NEVER be auto-approved on a
  timeout.
- What happens when an approval goes unanswered for an extended period? Per
  ARCHITECTURE.md §9, after N hours unanswered, a secondary channel (ntfy or email) MUST ping
  the operator; the exact threshold N is deferred (see Assumptions).
- How does the system handle a burst of many notifications at once (for example, several tasks
  completing together)? They MUST be coalesced into a single digest rather than flooding the
  chat.
- What happens if two Telegram messages race to approve the same gate (for example, a double
  tap)? The second application of an already-consumed nonce MUST be rejected, not applied
  twice.
- What happens if the local unix-socket API receives a request from a process other than the
  bot or local `fm` CLI? It MUST be rejected — the socket is not exposed beyond the `foreman`
  user's own processes.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST run as a long-lived daemon under `launchd` (or an equivalent
  process supervisor), exposing a local API over a unix socket owned by the `foreman` user.
- **FR-002**: The system MUST implement a Telegram bot whose commands and inline-button
  callbacks are parsed entirely by deterministic code — no LLM call MUST sit between a
  received Telegram update and the control action it triggers.
- **FR-003**: The system MUST authorize every command against a numeric Telegram user id
  allowlist and MUST operate in private chat only (ignoring groups).
- **FR-004**: The system MUST support, at minimum, these commands: `/task <repo> <text>`,
  `/status [id]`, `/tasks`, `/pause [id|all] [now]`, `/resume [id]`, `/cancel <id>`,
  `/reject <id> <reason>`, `/answer <id> <text>`, `/diff <id>`, `/logs <id> [node]`, `/budget`.
  (`/approve` is normally the inline button, not typed.)
- **FR-005**: The system MUST bind every approval request to a nonce tied to the sha256 of the
  exact artifact (spec, diff) being approved, and MUST reject any approval attempt whose nonce
  does not match the current artifact's hash.
- **FR-006**: The system MUST treat free-text Telegram messages as conversational input routed
  to Hermes (Feature 007), and MUST NOT allow any such message, or any Hermes-generated
  summary, to approve, reject, cancel, or change policy.
- **FR-007**: The system MUST implement an append-only notification outbox with a durable
  cursor over the events table (from Feature 002), delivering queued notifications, coalesced
  into a digest, when Telegram connectivity is restored after an outage.
- **FR-008**: The system MUST support `/pause <id>` (stop new dispatch, let running nodes
  finish) and `/pause <id> now` (also kill running process groups, marking those runs
  infra-cancelled and retriable) as distinct behaviors.
- **FR-009**: The system MUST support `/cancel <id>`, killing process groups/containers for
  that task, marking it `CANCELLED`, and retaining its workspaces for 7 days before cleanup.
- **FR-010**: On startup, the system MUST run the Feature 002 reconciler (lease expiry, dead-PID
  detection by start-time fingerprint) before accepting new Telegram-triggered dispatch, so a
  restart after a crash recovers in-flight work automatically.
- **FR-011**: The system MUST require a reason for every `/reject`, and MUST record that reason
  for later use by the replan/lesson pipeline (Features 002/005).
- **FR-012**: The system MUST push, without being asked, at minimum: task received, approval
  required, blocked (with the question), failures that escalated past ladder rung 2, and task
  delivered/failed — per the push list in ARCHITECTURE.md §9.
- **FR-013**: The local unix-socket API MUST be reachable only by processes running as the
  `foreman` user (the bot and the local `fm` CLI), and MUST reject connections from other
  principals.

### Key Entities *(include if feature involves data)*

- **Approval**: id, task/node id, kind, question, options, the sha256 of the approved subject,
  a nonce, status, response, reason, and timestamp — extends Feature 002's schema per
  ARCHITECTURE.md §7.4's `approvals` table.
- **Allowlist Entry**: a numeric Telegram user id authorized to issue commands.
- **Outbox Cursor**: a durable position into the events table per notification subscriber,
  surviving restarts.
- **Notification Digest**: a coalesced batch of queued notifications delivered after a
  reconnect or burst.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A task can be created, tracked, and (if gated) approved entirely from Telegram,
  with zero direct interaction with the host machine, for a task built on Feature 002's loop.
- **SC-002**: A stale approval attempt (nonce bound to a since-changed artifact) is rejected
  100% of the time in testing, never silently applied to the new artifact.
- **SC-003**: Killing the daemon process and restarting it recovers all in-flight leases and
  requeues lost runs with zero operator intervention beyond the restart itself.
- **SC-004**: A simulated Telegram outage (API unreachable) does not stop task execution;
  queued notifications are delivered as a single coalesced digest once connectivity returns.
- **SC-005**: No free-text message, in any tested phrasing (including ones that look like
  "yes, approved" or "go ahead"), is ever interpreted as an approval — only the bound inline
  button is.

## Assumptions

- The exact unanswered-approval escalation threshold ("N hours" in ARCHITECTURE.md §9) is set
  to a reasonable default of 4 hours during business-local time, configurable later via
  `policy.yaml` (Feature 004); it is not hard-coded as unconfigurable.
- The secondary notification channel for unanswered approvals (ntfy or email) is implemented
  as a pluggable notifier interface with one concrete backend (ntfy, being simplest to
  self-host) for this feature; adding email is not blocking.
- This feature assumes Feature 002's single-task `fm run` loop is adapted to run as
  daemon-dispatched work rather than a one-shot CLI invocation; the underlying engine,
  ladder, and verification logic from Feature 002 are reused unchanged, only the dispatch
  trigger (CLI arg vs. Telegram command vs. reconciler) changes.
- Policy-driven approval gating (which risk levels require approval) is a stub in this
  feature — a single hard-coded "all lane S/D specs and deliveries require approval" rule —
  with the full `policy.yaml`-driven version arriving in Feature 004.
- Two bots (one for foreman control, one for Hermes conversation) vs. one bot with two
  logical planes: this feature implements the single-bot-two-planes approach described as
  "simpler for you" in ARCHITECTURE.md §9, deferring the two-bot alternative unless the single
  bot proves awkward in practice.
