# Feature Roadmap

This repository's spec-kit features are derived directly from the phased build order in
`/Users/dpcamargo/repos/harness-architecture/ARCHITECTURE.md` §13 ("Minimal viable
implementation: phased plan") and §16 ("First implementation: the smallest thing that tests
the core hypothesis"). Each feature below maps to one architecture phase. Build strictly in
order — each phase's exit criterion in §13 is a prerequisite for starting the next, per the
constitution's "Measure as we go" rule and Principle IX (minimal, justified
infrastructure).

| # | Feature | Architecture source | Phase | Exit criterion (from ARCHITECTURE.md) |
|---|---|---|---|---|
| 001 | [Evaluation Harness](001-evaluation-harness/spec.md) | §0, §16, Appendix D | 0 | A recorded task replays through every baseline arm and is graded automatically, k=3 |
| 002 | [Durable Execution Loop](002-durable-execution-loop/spec.md) | §1, §3.1/3.2/3.7/3.9/3.12, §7.5, §16 | 1 | Beats the best single-agent arm on a decision replay of ≥15 recorded real tasks, or stop |
| 003 | [Daemon + Telegram Control Plane](003-daemon-telegram-control/spec.md) | §3.1/3.13, §7.2-7.4, §9 | 2 | Run a task end to end from a phone; killing foreman mid-task loses nothing |
| 004 | [Routing, Provider Health, Test-First](004-routing-test-first/spec.md) | §3.4/3.5, §4, §5 | 3 | Same or better success at lower $ per verified success |
| 005 | [Memory Service](005-memory-service/spec.md) | §3.11, §6 | 4 | Fewer repeated failures, fewer attempts per success on repeat-repo tasks |
| 006 | [DAG Decomposition & Parallelism](006-dag-parallelism/spec.md) | §3.1, §7.1 | 5 | Lower wall time on multi-part tasks, no regression in success |
| 007 | [Decision Protocol & Hermes](007-decision-protocol-hermes/spec.md) | §3.10/3.14, §4, §5.2, Appendix B | 6 | Better outcomes on decision-heavy tasks without raising cost elsewhere |
| 008 | [Strong Isolation & Always-On](008-strong-isolation-always-on/spec.md) | §3.8, §8.3/8.4 | 7 | A red-team task set can't reach secrets or the network |
| 009 | [Data-Driven Optimization](009-data-driven-optimization/spec.md) | §5.4, §16 continuous-eval note | 8 | Cost per verified success trends down; false-success rate holds |

## Status

All nine specs are drafted. 001 is implemented: `fm eval record|validate|list|replay|spot|decide|report`.
67 of its 71 tasks are done and verified against real Docker and Codex. Still open: the live
Hermes checks (T054, T056), the `a0` part of quickstart step 6 (T068) and the final clean-home
pass (T071). All four need the `foreman-arm` Hermes profile and Claude subscription tokens, so
the operator runs them.

002 is implemented: `fm run <task.yaml> [--arm codex|hermes]` executes spec -> implement ->
verify -> review with ladder rungs 1-3, diff-policy enforcement, offline container
verification, cross-family review, task reports, and a Feature 001 record per run. The H1
eval arm replays those records (`fm eval replay --arm h1`). All 39 tasks done and verified:
quickstart steps 1-7 ran against real Docker (Colima), Codex 0.161.0 and the `foreman-arm`
Hermes profile, including crash recovery (SIGKILL -> `fm run --resume` marks the run
`infra_error (lost)`), hidden-test escalation to `blocked_human`, and BLOCKING review
findings correctly downgrading an otherwise-approved task to `changes_requested`. Two bugs
were found and fixed during live testing: cited-file validation checked the CLI's cwd
instead of the base commit's git tree, and `GateReviewFindings` auto-approved reviews whose
findings used `blocking` severity. Per-attempt wall clock on a warm cache measured 30-37s
against the 60s goal. The exit criterion ("beats the best single-agent arm on a decision
replay of >=15 recorded real tasks") is not yet measured: that needs 15+ replayable records,
accumulated as development tasks run through `fm run`.

003 is code-complete but not live-verified: 49 of its 51 tasks are done and unit-tested
(`foreman` daemon + `fm` CLI, Telegram `/task /status /tasks /pause /resume /cancel /reject
/answer /diff /logs /budget`, nonce-bound spec/deliver/merge approvals with FR-016's two-tap
merge confirmation, GitHub delivery/merge via a draft-PR-first integrator, and a housekeeping
tick for retention + unanswered-approval ntfy pings). `gofmt -l .`, `go vet ./...` and
`go test ./...` are all clean, including `internal/archtest` covering the new
`internal/core/control` package. Still open: T048 (secret-redaction check) and T049
(quickstart steps 1-7) — both need a real Telegram bot token, a real GitHub fine-grained PAT
on a disposable test repo, and (for unattended runs) the `foreman` macOS user per ADR 0001,
so the operator runs them. The exit criterion ("run a task end to end from a phone; killing
foreman mid-task loses nothing") is covered by unit and integration tests
(`TestServeRecoversLostRunsOnStartup`, the two-tap merge tests) but is not yet confirmed
live. A disposable private test repo (`dpcamargo/foreman-test`, default branch `main`) is
already provisioned for T049.

## Why this order, not a different one

- **001 before 002**: 002 writes a task record for every run and is judged by replaying those
  records through 001's baseline arms; both need 001's record format, arms and grader first
  (constitution: "Measure as we go"). No eval suite is written up front.
- **002 is the architecture's own go/no-go gate** (§16): if the harness arm doesn't beat the
  best single-agent baseline by the stated thresholds, the problem is spec/verification
  quality, and the correct response is to fix 001/002, not to add features. The decision runs
  once ≥15 replayable tasks exist; until then foreman's own development goes through `fm run`
  so the records accumulate.
- **003 before 004-009**: every later feature assumes unattended, remotely-observable
  operation; building routing/memory/DAG/decision-protocol sophistication before the control
  plane exists would make them hard to operate or debug.
- **004-006 build engine sophistication** (routing, memory, parallelism) on top of a daemon
  that's already controllable and recoverable.
- **007 (decision protocol, Hermes)** deliberately comes after the engine's core loop is
  trustworthy — per constitution Principle VIII, deliberation machinery is the one thing most
  likely to be reached for prematurely, and the architecture explicitly orders it late.
- **008 (strong isolation) before "always-on"**: the Phase 1 dedicated-OS-user isolation
  (built inside Feature 002) is real but intentionally provisional; moving to an unattended,
  always-on host without closing this gap first would violate constitution Principle VI.
- **009 (data-driven optimization) is last by construction**: it requires labelled outcome
  history that only exists once 001-008 have been running for a while, and governs the
  highest-stakes lever (auto-merge) last, per the constitution's Governance section.

## Planned task-type features (not yet specified)

| # | Task type | Earliest start | Why there |
|---|---|---|---|
| 010 | Research | after 002 "go" and 003 | Cheapest to verify by code (cited URLs must load and contain the quoted text); Hermes already does research. Takes over the Hermes research-worker part of 007 so it isn't blocked behind the decision protocol |
| 011 | Docs | after 010 | Verifiable by build, link and lint checks plus a cross-family rubric; no external side effects |
| 012 | Ops | after 011 and 008 | Changes real systems: needs dry-run-then-approve (003, constitution VIII) and credential injection through the egress proxy (008) so agents never hold target-system credentials |

Each one ships its own verifier. Its go/no-go is a decision replay of its own recorded tasks against a single-agent baseline, once it has enough of them.

## Decisions

- **Scope** — decided 2026-10-07 (spec 001 clarification): foreman orchestrates any kind of
  work, with code as one task type. Code comes first and carries the go/no-go; each further
  task type (research, docs, ops) needs its own verifier, and is judged by replaying its own recorded tasks
  (constitution v2.0.0 Principles II/III/VIII; v3.0.0 "Measure as we go"). Order decided: code → research → docs → ops
  (see "Planned task-type features").
- **Evaluation** — decided 2026-10-07 (spec 001 clarification): measure as we go. No upfront
  eval suite and no mining of past commits; every task foreman runs is recorded, tasks with
  acceptance tests are replayable, and spot checks plus decision replays compare foreman with
  the single-agent baselines (constitution v3.0.0).
- **Code layout** — decided 2026-10-07: hexagonal core/adapters split, KISS/YAGNI
  (constitution Principle X; rules and target tree in [`AGENTS.md`](../AGENTS.md)). Sources: Go's
  official module layout guide, Ardan Labs' `service` template, the operator's
  `fullcycle-clean-architecture`.
- **Orchestrator hosting** — decided 2026-10-07: `launchd` host process under a dedicated
  `foreman` macOS user with its own Colima profile mounting only its workspace root
  ([ADR 0001](../docs/adr/0001-orchestrator-hosting.md), constitution v1.1.1).

## Prerequisites (ARCHITECTURE.md §18)

| Item | Status |
|---|---|
| Rotate the `gho_` token in `~/.hermes/config.yaml` | Done by operator; verified no inline token remains — config references `${GITHUB_PERSONAL_ACCESS_TOKEN}` from `.env` |
| Pin MCP servers | Done: `@playwright/mcp@0.0.83` (latest, past 7-day cooldown). GitHub moved off the deprecated npm package to GitHub's official server v1.12.2 (newest release past the 7-day cooldown), installed from the release binary with verified sha256 at `~/.hermes/bin/github-mcp-server-1.12.2`; authenticated call verified |
| Move Hermes off Anthropic OAuth | Not doing (operator decision). Claude runs go through Hermes, operator-triggered only. Check the account's usage page after the first eval smoke stage for extra-usage billing |
| Create the `foreman` macOS user | Not created (operator decision: no automatic run). Manual, needs sudo; required before Feature 002 |
| Start a `foreman` Colima profile | Manual, as the `foreman` user, mounting only its workspace root (ADR 0001); required before Feature 002 |
| Raise container VM resources | Done: Colima default profile 2 CPU / 2 GiB → 4 CPU / 6 GiB, verified from inside a container |

## Carry-overs for `/speckit-plan` (implementation choices moved out of specs)

- 004/005 local models: an in-process inference runtime (ONNX Runtime is the leading candidate;
  Go bindings, CPU on Apple Silicon); a prompt-injection classifier in the Prompt Guard size
  class (22M–86M params); a small sentence-embedding model (22M–100M params).
- 005 vector index: `sqlite-vec` inside the existing SQLite file, only if BM25 recall is
  measured short.
- 001 grader image: pinned to `golang:1.27.0-bookworm@sha256:ded31c68…` (the newest build past the
  7-day cooldown on 2026-10-07). Bump to 1.27.1 after 2026-10-13.
- 001 arm confinement: T-codex-deny and T-leak-scan passed live on 2026-10-07 (codex-cli
  0.160.0). The Codex profile needs the Homebrew prefix readable (research R1).
- 001/002 grader and verifier: image choice per language (Go/Node), dependency-cache strategy,
  exact `docker run` hardening flags.
- 002 Claude runner: exact Hermes invocation for a confined, MCP-free implementer profile.

## Next steps

1. Operator: create the `foreman-arm` Hermes profile (001 quickstart step 0), then run the Hermes
   live tests and the `a0` decision gate (001 T054, T056, T068, T071).
2. `/speckit-plan` for 002. Features after 002 may be built through `fm run` before the go/no-go
   (that is how the records accumulate); a "no-go" stops them until 001/002 are fixed.
