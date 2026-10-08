# Research: Daemon + Telegram Control Plane

Each decision below was non-obvious enough to need its own entry (plan.md's project structure
and constitution check summarize the conclusions; this file is why).

## R1: Telegram transport — long polling via stdlib `net/http`, no SDK

**Decision**: Poll `https://api.telegram.org/bot<token>/getUpdates` with a long `timeout`
parameter (25s), using `net/http.Client` and `encoding/json`, in a loop that backs off on error
and resumes from the last `update_id` on reconnect.

**Why**: The daemon needs about a dozen Bot API calls total (`getUpdates`, `sendMessage`,
`editMessageReplyMarkup`, `sendDocument` for `/diff`, `answerCallbackQuery`). Each is one JSON
POST/GET. A webhook would need a public HTTPS endpoint and TLS cert management the operator's
single-host, no-reverse-proxy setup doesn't have; long polling needs only outbound HTTPS, which
already works from the host. The spec's own edge case ("Telegram unreachable") is explicitly
handled by polling-with-backoff plus the outbox, not by a webhook retry policy.

**Alternatives considered**:
- A third-party Telegram SDK (e.g. `go-telegram-bot-api`) — rejected per constitution
  Principle IX: a dozen hand-written calls don't justify a dependency, and a library's own
  update loop would hide exactly the long-polling/backoff behavior FR-007 and the Telegram
  outage edge case need to be auditable.
- Webhooks — rejected: needs a public endpoint this one-host setup doesn't expose, and
  reintroduces the "what if delivery fails" problem the outbox is already solving for the pull
  side.

## R2: Local API — unix socket, stdlib `net/http`, peer-credential check

**Decision**: `net.Listen("unix", socketPath)` at `0700` inside `$FOREMAN_HOME/state/`, serving
a small `net/http.Server` (routes: `POST /v1/tasks`, `GET /v1/tasks/{id}`, `POST /v1/pause`,
etc., mirroring the Telegram commands so `fm` the local CLI and the bot share one code path).
Every connection is checked with `golang.org/x/sys/unix`'s peer-credential syscall
(`LOCAL_PEERCRED` on Darwin) and rejected unless its UID matches the daemon process's own UID
(FR-013).

**Why**: `net/http` over a unix socket is a stdlib one-liner (`http.Serve(listener, handler)`);
no RPC framework or gRPC is justified for a single local client. The filesystem permission bit
alone is not enough for FR-013's "MUST reject connections from other principals" — a socket
file's mode can be loosened by a later `chmod` without anyone noticing, so the connection-time
peer-credential check is the actual enforcement point, with the filesystem permission as
defense in depth, not the only line.

**Alternatives considered**: gRPC over the socket — rejected, no second language client exists
or is planned, and protobuf codegen is unjustified machinery for a handful of JSON endpoints.

## R3: Turning a one-shot `engine.RunTask` into daemon-dispatched, pause/cancel-able work

**Decision**: Add `internal/core/engine.Supervisor`: a bounded-concurrency dispatcher that
accepts `(ctx, task.Task)`, runs `engine.RunTask` in a goroutine under a semaphore (default
concurrency 2, per ARCHITECTURE.md §8.5), and keeps an in-memory `map[taskID]context.CancelFunc`
so `/cancel <id>` and `/pause <id> now` can cancel that one task's context without touching any
other task's goroutine. Graceful `/pause <id>` (no `now`) sets a flag the Supervisor checks
between ladder attempts — `engine.RunTask`'s retry loop already has a natural checkpoint there
(it already re-evaluates `currentRung`/`currentFamily` per iteration) — so the running attempt
finishes, but no new attempt starts.

**Why**: Feature 002's `engine.RunTask` already threads `context.Context` through to every
`exec.CommandContext` call in the Codex and Hermes runners (confirmed in
`internal/adapters/runner/codex/codex.go` and `.../hermes/oneshot.go`), so cancelling that
context already kills the underlying process the way `/pause <id> now` and `/cancel <id>`
require (FR-008, FR-009) — no new process-group-killing code is needed, only something that
calls `cancel()` at the right id. This stays inside `internal/core/engine` (not a new package)
because it is pure Go concurrency bookkeeping over an existing core type, with no I/O of its
own; it is exactly the kind of orchestration logic Principle I already requires to be
deterministic code, now explicit about concurrency instead of implicit in `cmd/fm`'s single
synchronous call.

**Alternatives considered**: a full job-queue library (e.g. an embedded task-queue package) —
rejected per Principle IX; the daemon's own concurrency need (at most ~2 concurrent attempts,
one process, one host) is smaller than what any queue library is built to solve, and SQLite
polling plus channels already covers it (see R4).

## R4: Work discovery — polling plus in-process wakeups, not a message bus

**Decision**: The daemon runs a level-triggered loop: every 2s, query `tasks` for
`status IN ('pending','running')` without a currently-running Supervisor slot, and dispatch
any that are found. A new task created via Telegram or the local API pushes directly onto an
in-process Go channel the same loop also selects on, so the common case (operator sends
`/task`) dispatches immediately rather than waiting up to 2s for the next poll.

**Why**: This is the literal mechanism the constitution's Technology Stack section already
specifies for the orchestrator ("a level-triggered reconcile loop plus in-process event
wakeups") — this feature is the first to need it, not a new decision. SQLite has no
`LISTEN`/`NOTIFY`; a 2s poll against an indexed `(status, created_at)` column (already present
from Feature 002's `tasks_status` index) is negligible load for one operator's task volume.

**Alternatives considered**: a message bus (Redis pub/sub, NATS) — rejected per Principle IX,
no measured requirement exceeds what polling plus a channel already provides.

## R5: Approval nonce — bound to sha256 of the artifact, not the approval row's id

**Decision**: An `Approval` row stores `subject_sha256` (the sha256 of the exact spec text, diff
bytes, or other artifact being approved) and a `nonce` (32 random bytes, hex-encoded, from
`crypto/rand`, independent of the approval id). The inline button's callback data carries both
the approval id and the nonce. `ValidateApproval(approvalID, nonce, currentSubjectSHA256 string)`
in `internal/core/control` returns an error unless: the approval is still `pending`, the
supplied nonce matches the stored one exactly, and the supplied current-subject hash matches
`subject_sha256`. Any mismatch — stale nonce, or (for the belt-and-suspenders replan case) a
changed artifact — is rejected, never silently applied to the new artifact.

**Why**: FR-005 and SC-002 require 100% rejection of a stale approval bound to a since-changed
artifact. Hashing the actual artifact bytes (not just trusting the approval row's foreign key)
is what makes "the spec changed after I sent the approval request" detectable — a replan that
creates a new spec but reuses the same task/node id would otherwise pass a naive `task_id`
check. The nonce itself defends against the Telegram-account-compromise scenario
(ARCHITECTURE.md §9's security note): even with knowledge of the task id, an attacker without
the nonce from the original message cannot forge an approval.

**Alternatives considered**: a monotonic version counter per artifact instead of a content
hash — rejected, because it requires the engine to remember to bump the counter on every
mutation, whereas hashing the bytes at validation time can never be "forgotten" for a new
mutation path added later.

## R6: Integrator credential and push path

**Decision**: The integrator holds one fine-grained GitHub personal access token, scoped to
the specific repos foreman is configured for, with permissions limited to "Contents: write" and
"Pull requests: write" on those repos, stored only in the `foreman` OS user's environment (per
ADR 0001 — never in the workspace or the verifier). It applies the verified patch to
`agent/<task>` in the existing host-side bare mirror (the same mirror `internal/adapters/sandbox`
already maintains per repo), using the mirror's hardened git invocation
(`internal/adapters/sandbox/git.go`'s `git()`/`gitEnv()` helpers — hooks path `/dev/null`, no
system/global config), then pushes that branch and calls the GitHub REST API
(`POST /repos/{owner}/{repo}/pulls`, draft: true) directly over `net/http` to open the PR.

**Why**: Reusing `sandbox`'s existing hardened git invocation (FR-015's "hardened git" is
exactly what that package already guarantees for Feature 002's clone-per-attempt workspaces)
avoids a second, divergent implementation of "git with hooks disabled." A direct `net/http` call
to GitHub's REST API for PR creation needs no SDK — it is one JSON POST with a bearer token.

**Alternatives considered**: `go-github` (the common GitHub API Go client) — rejected per
Principle IX for the same reason as R1: the integrator needs push, one PR-creation call, and
one merge call — three operations, not a general-purpose GitHub client's full surface.

## R7: Where the spec-approval gate pauses, given `engine.RunTask` already runs end to end

**Decision**: Feature 002's `engine.RunTask` (spec → implement → verify → review, one
synchronous call) gains exactly one new, optional `Config` field:
`ApprovalGate func(ctx context.Context, spec TaskSpec) (approved bool, err error)`. It is called
once, synchronously, immediately after `ValidateTaskSpec` succeeds and before the implement
ladder starts. When `nil` (Feature 002's `fm run` never sets it), behavior is byte-for-byte
unchanged — this is the one correction to plan.md's Constitution Check, which otherwise
correctly says Feature 002's engine logic is untouched. When the daemon sets it, the function
blocks until the Supervisor's approval bookkeeping resolves; the actual waiting (a channel or
condition variable keyed by approval id) lives in `internal/core/engine/supervisor.go`, not
inside `engine.RunTask` itself, so no polling loop is duplicated between the two packages — the
hook's body is a single call out to the Supervisor, not new control flow in the engine loop.

The **deliver/merge** approval needs no such hook: Feature 002 has no integration step at all
(`engine.RunTask` returns a `TaskReport` and stops), so the daemon's "create a Delivery row,
request the deliver approval, wait, then invoke the integrator" sequence happens entirely in
`cmd/foreman`'s wiring, strictly after `RunTask` returns — a new downstream step, not a pause
inserted into an existing one.

**Why**: Rejecting both "fork `engine.RunTask` for the daemon" and "poll task status from
outside the engine loop to decide when to pause" — the first duplicates Feature 002's ladder
logic (a second copy to keep in sync), the second can't actually stop the implement ladder from
starting once `RunTask` has been called, since nothing inside the loop would check anything.
One optional, nil-safe hook keeps `fm run`'s path provably unchanged while giving the daemon a
real pause point, and keeps the pause/resume mechanism itself in one place (the Supervisor)
instead of two.

**Alternatives considered**: Decomposing `engine.RunTask` into separate `Specify`/`ImplementLoop`
methods the daemon calls one at a time — rejected for this feature: it's a bigger refactor of
tested Feature 002 code than the gate hook, and Feature 006 (DAG decomposition) is where
`engine.RunTask`'s monolithic shape is expected to change anyway; doing it piecemeal now risks
two half-migrations.
