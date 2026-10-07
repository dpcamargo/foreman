# Feature Roadmap

This repository's spec-kit features are derived directly from the phased build order in
`/Users/dpcamargo/repos/harness-architecture/ARCHITECTURE.md` §13 ("Minimal viable
implementation: phased plan") and §16 ("First implementation: the smallest thing that tests
the core hypothesis"). Each feature below maps to one architecture phase. Build strictly in
order — each phase's exit criterion in §13 is a prerequisite for starting the next, per the
constitution's "Evaluation precedes the harness" rule and Principle IX (minimal, justified
infrastructure).

| # | Feature | Architecture source | Phase | Exit criterion (from ARCHITECTURE.md) |
|---|---|---|---|---|
| 001 | [Evaluation Harness](001-evaluation-harness/spec.md) | §0, §16, Appendix D | 0 | Every arm scoreable automatically, k=3 repetitions |
| 002 | [Durable Execution Loop](002-durable-execution-loop/spec.md) | §1, §3.1/3.2/3.7/3.9/3.12, §7.5, §16 | 1 | Beats the best single-agent arm on Feature 001's suite, or stop |
| 003 | [Daemon + Telegram Control Plane](003-daemon-telegram-control/spec.md) | §3.1/3.13, §7.2-7.4, §9 | 2 | Run a task end to end from a phone; killing foreman mid-task loses nothing |
| 004 | [Routing, Provider Health, Test-First](004-routing-test-first/spec.md) | §3.4/3.5, §4, §5 | 3 | Same or better success at lower $ per verified success |
| 005 | [Memory Service](005-memory-service/spec.md) | §3.11, §6 | 4 | Fewer repeated failures, fewer attempts per success on repeat-repo tasks |
| 006 | [DAG Decomposition & Parallelism](006-dag-parallelism/spec.md) | §3.1, §7.1 | 5 | Lower wall time on multi-part tasks, no regression in success |
| 007 | [Decision Protocol & Hermes](007-decision-protocol-hermes/spec.md) | §3.10/3.14, §4, §5.2, Appendix B | 6 | Better outcomes on decision-heavy tasks without raising cost elsewhere |
| 008 | [Strong Isolation & Always-On](008-strong-isolation-always-on/spec.md) | §3.8, §8.3/8.4 | 7 | A red-team task set can't reach secrets or the network |
| 009 | [Data-Driven Optimization](009-data-driven-optimization/spec.md) | §5.4, §16 continuous-eval note | 8 | Cost per verified success trends down; false-success rate holds |

## Status

All nine specs are drafted (`/speckit-specify`-equivalent complete). None has an approved plan
or tasks breakdown yet (`/speckit-plan` / `/speckit-tasks` not yet run for any feature).

## Why this order, not a different one

- **001 before 002**: there is no way to know whether 002's loop is actually better than doing
  nothing without 001's scoring infrastructure existing first (constitution: "Evaluation
  precedes the harness").
- **002 is the architecture's own go/no-go gate** (§16): if the harness arm doesn't beat the
  best single-agent baseline by the stated thresholds, the problem is spec/verification
  quality, and the correct response is to fix 001/002, not to proceed to 003+.
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

## Next steps

Run `/speckit-clarify` against `001-evaluation-harness` and `002-durable-execution-loop` if any
open questions need resolving, then `/speckit-plan` for `001-evaluation-harness` first — per
the ordering above, no other feature's plan should start before 001's plan exists and 002's
go/no-go has run.
