# Implementation Plan: Daemon + Telegram Control Plane

**Branch**: `003-daemon-telegram-control` | **Date**: 2026-10-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/003-daemon-telegram-control/spec.md`

## Summary

Turn `fm run`'s one-shot CLI loop (Feature 002) into a long-lived `foreman` daemon under
`launchd`, controlled entirely from Telegram: deterministic command/button parsing (no LLM),
nonce-bound approvals tied to a sha256 of the exact artifact, pause/pause-now/cancel, an
append-only notification outbox with a durable cursor over Feature 002's `events` table, a
startup reconciler (reused from 002) before any new dispatch, and an integrator that is the
only component holding a GitHub push credential — applying verified patches to `agent/<task>`
branches and opening draft PRs, merging only on a second nonce-bound approval. Feature 002's
engine, ladder, and verification logic are reused unchanged; only the dispatch trigger changes
from a CLI argument to Telegram commands and the reconciler's own requeue.

## Technical Context

**Language/Version**: Go 1.27 (matches `go.mod`; no new language/runtime).

**Primary Dependencies**: standard library only for new surfaces — `net/http` (unix-socket
local API and the Telegram long-polling client), `encoding/json`, `crypto/sha256`,
`crypto/rand`, `os/signal`. `golang.org/x/sys/unix` is promoted from indirect to direct (see
Complexity Tracking) for the local API's peer-credential check. No Telegram SDK, no HTTP
framework, no job-queue library — see research.md R1/R2.

**Storage**: SQLite (WAL), same `$FOREMAN_HOME/state/foreman.db` as Features 001/002. One new
migration (`0004_daemon_control.sql`) adds `approvals`, `allowlist`, `outbox_cursors`, and
`deliveries`; `tasks` gains `paused`/`cancelled` to its status CHECK constraint (see
data-model.md).

**Testing**: table-driven unit tests for core packages with fakes (existing pattern in
`internal/core/engine/*_test.go`); adapter tests against a real local socket, a fake Telegram
HTTP server (`httptest.Server`), and real SQLite (existing pattern in
`internal/adapters/sqlite/*_test.go`). No live Telegram or GitHub calls in the test suite —
quickstart.md covers manual verification against the real Bot API and GitHub, same split as
Feature 001/002's quickstart vs. unit tests.

**Target Platform**: macOS host process under `launchd`, running as a dedicated `foreman` OS
user per ADR 0001. Until that user exists, the daemon may run as the operator for attended
testing only (ADR 0001 Consequences) — this plan's quickstart calls that out explicitly as an
operator setup step, not something the code enforces.

**Project Type**: single Go module, two binaries (`cmd/fm` CLI, new `cmd/foreman` daemon)
sharing `internal/core` and `internal/adapters`, per AGENTS.md's existing target layout.

**Performance Goals**: not latency-sensitive (one operator, Telegram's own round-trip
dominates). The one measured goal from the spec is operational: SC-003's crash recovery must
complete (to a steady state where outstanding work is requeued) within one reconciler tick
after restart — no fixed-wall-clock number in the spec, so this plan does not invent one.

**Constraints**: FR-002/FR-006 — zero LLM calls between a Telegram update and a control action.
FR-005 — every approval nonce bound to a specific artifact's sha256; a changed artifact
invalidates the nonce. FR-013 — the local socket API rejects any peer not running as the same
OS user as the daemon. FR-014/FR-016 — only the integrator holds a GitHub push credential, and
merging to the default branch always needs an explicit approval plus a second confirmation.

**Scale/Scope**: one operator, one Telegram chat, the concurrency ceiling from
ARCHITECTURE.md §8.5 (at most 2 implement/verify attempts in flight at once on this host).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design below.*

| Principle | Check | Mechanism |
|---|---|---|
| I. Deterministic orchestration | Telegram command/button parsing and the approval nonce check are pure Go switch/compare logic; no model call sits in that path (FR-002, FR-006) | `internal/adapters/telegram/commands.go` parser; `internal/core/control` nonce validation; unit tests assert zero LLM client is constructed to handle a command |
| II. Evidence-based verification | Unchanged — Feature 002's `engine.Verifier` still decides "done"; this feature only changes what triggers a task, never what completes one | No new verifier path; `internal/core/engine` untouched except the Supervisor wrapper (research.md R3) |
| III. Acceptance criteria first | Unchanged — reused from Feature 002 | n/a (no new spec/implement logic) |
| IV. Single-threaded writes | The Supervisor caps concurrent task dispatch (config, default 2) so at most that many implement/verify attempts run at once, each in its own disposable clone | `internal/core/engine/supervisor.go` semaphore; `supervisor_test.go` |
| V. Cross-family review | Unchanged | n/a |
| VI. Isolation by default | Integrator applies patches with git hooks disabled and `core.fsmonitor=false`, same hardening as `internal/adapters/sandbox/git.go`'s existing `gitEnv()`/`git()` helpers, reused rather than reimplemented | `internal/adapters/integrator/integrator.go` |
| VII. Durable & resumable state | FR-010: daemon runs Feature 002's reconciler at startup, before any Telegram-triggered dispatch is accepted; SIGTERM handling stops accepting new work and lets the reconciler recover the rest on next start | `cmd/foreman/main.go` startup order; `internal/core/engine.Reconciler` (existing, from 002) |
| VIII. Gated deliberation & approval | FR-005/FR-016: approvals are nonce-bound to a sha256 of the exact artifact; merging to the default branch needs an approval plus a second confirmation step; nothing is ever auto-approved on a timeout (FR-006, edge case) | `internal/core/control.Approval`, `ValidateApproval`; `cmd/foreman` merge handler requires two separate Telegram round-trips |
| IX. Minimal infrastructure | No job queue, no message bus, no Telegram SDK; long polling + stdlib HTTP; SQLite polling plus in-process channel wakeups (constitution Technology Stack: "level-triggered reconcile loop plus in-process event wakeups") | research.md R1–R4 |
| X. Hexagonal structure | New core: `internal/core/control` (approvals, allowlist check, outbox cursor advance — pure). New adapters: `internal/adapters/telegram`, `internal/adapters/localapi`, `internal/adapters/notifier`, `internal/adapters/integrator`. `cmd/foreman` only wires | `internal/archtest` (existing) extended to cover the new core package |

No violations requiring Complexity Tracking beyond the one dependency-promotion entry below.

## Project Structure

### Documentation (this feature)

```text
specs/003-daemon-telegram-control/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── contracts/
│   ├── telegram-commands.md
│   ├── local-api.md
│   └── approval-nonce.md
├── quickstart.md        # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit-tasks — not produced by this plan)
```

### Source Code (repository root)

```text
cmd/
├── fm/                        # unchanged: CLI (eval, run)
└── foreman/                   # NEW: daemon — wiring only
    └── main.go                #   opens store, runs reconciler, starts Supervisor,
                                #   Telegram poller, local API listener, outbox notifier;
                                #   SIGTERM -> stop new dispatch, exit for launchd restart

internal/
├── core/
│   ├── task/                  # existing (002): add Paused/Cancelled TaskStatus + transitions
│   ├── engine/                # existing (002): unchanged except supervisor.go (new)
│   │   └── supervisor.go      # NEW: bounded-concurrency dispatch, pause/cancel via context,
│   │                          #   core/engine port (ctx map keyed by task id — pure Go, no I/O)
│   └── control/                # NEW: approvals, allowlist check, outbox cursor advance,
│       ├── approval.go        #   delivery record — all pure, no I/O (ports declared here)
│       ├── allowlist.go
│       └── outbox.go
└── adapters/
    ├── telegram/               # NEW: bot client (long polling), command parser, formatter
    │   ├── client.go           #   implements control.Notifier
    │   ├── commands.go
    │   └── client_test.go      #   against httptest.Server, no live Telegram
    ├── localapi/                # NEW: unix-socket HTTP server + peer-credential check
    │   ├── server.go
    │   └── server_test.go
    ├── notifier/                # NEW: outbox consumer — polls events table, pushes via
    │   └── notifier.go          #   control.Notifier port (telegram adapter satisfies it)
    └── integrator/               # NEW: applies verified patches, pushes agent/<task>,
        ├── integrator.go        #   opens draft PR via GitHub REST API
        └── integrator_test.go

migrations/
└── 0004_daemon_control.sql    # NEW: approvals, allowlist, outbox_cursors, deliveries;
                                #   tasks.status gains 'paused', 'cancelled'

deploy/launchd/
└── com.foreman.plist          # NEW: launchd service definition (ARCHITECTURE.md §14)
```

**Structure Decision**: `internal/core/control` is a new core package, not an extension of
`internal/core/task`, because approvals/allowlist/outbox/delivery are a distinct aggregate —
authorization and notification state, not task execution state — matching the precedent of
Feature 002 splitting `internal/core/verify` out of `internal/core/engine` when the domain
concept genuinely differed (AGENTS.md Principle X: "small interfaces, declared where used").
Pause/cancel, by contrast, are literal `Task` state transitions (`StatusPaused`,
`StatusCancelled` alongside the existing `StatusPending`/`StatusRunning`/...), so they extend
`internal/core/task` the same way `BlockHuman`/`Complete` already do there — no new package for
those two methods. `cmd/foreman` is a new binary, not a mode flag on `cmd/fm`, because AGENTS.md
already names it as the daemon's home in the target layout, and the two processes have
different lifecycles (long-running supervisor vs. one-shot CLI) that would otherwise force
`cmd/fm run` to grow daemon-only flags it doesn't need.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| `golang.org/x/sys/unix` promoted from indirect to direct dependency | FR-013 requires rejecting unix-socket peers that aren't the daemon's own OS user; the standard library exposes no portable way to read `SO_PEERCRED`/`LOCAL_PEERCRED` from a `net.UnixConn` | Trusting the socket's filesystem permissions alone (0700 + same-user directory) was considered, but a stale socket file surviving a `chmod`/`chown` mistake would then be the only line of defense for FR-013 "MUST reject connections from other principals" — the peer-credential check is cheap and the module is already in `go.sum` transitively, so this adds no new supply-chain surface, only a direct import of code already vendored |
