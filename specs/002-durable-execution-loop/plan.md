# Implementation Plan: Durable Execution Loop (`fm run` MVP)

**Branch**: `002-durable-execution-loop` | **Date**: 2026-10-07 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/002-durable-execution-loop/spec.md`

## Summary

`fm run` builds the smallest durable execution loop that tests the architecture's core hypothesis (Principle I/II): deterministic Go orchestration, SQLite state (`tasks`, `nodes`, `runs`, `events`), disposable workspace clone per attempt, structured `TaskSpec`, containerized verification with diff-policy checks, cross-family review, and escalation ladder rungs 1 through 3, emitting task records and reports.

- **Linear Node Chain**: executes `spec` -> `implement` -> `verify` -> `review` -> `report`.
- **Escalation Ladder & Loop Guards**: Rung 1 retries with verifier evidence; Rung 2 escalates to a different model family fresh from spec (no prior diff); Rung 3 marks `blocked_human`. Loop guards break repetitive or oscillating failure loops early.
- **Diff Policy**: gates verification on test preservation, protected path integrity, secret hygiene, and no escaping symlinks.
- **Evaluation Integration**: every task ends with a Feature 001 record emission, and the loop exposes arm `H1` so recorded tasks can be benchmarked against baselines A0, A1, and A2.

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27` in `go.mod`; toolchain go1.27.1 darwin/arm64).

**Primary Dependencies**:
- `modernc.org/sqlite` v1.60.1: pure-Go SQLite with no cgo (reused from Feature 001).
- Everything else is standard library: `database/sql`, `encoding/json`, `os/exec`, `log/slog`, `context`, `io/fs`, `flag`.
- External tools: `git`, `docker` (Colima context), `codex`, `hermes`.

**Storage**:
- SQLite in WAL mode at `$FOREMAN_HOME/state/foreman.db`, migration `0003_tasks_and_nodes.sql`.
- Blobs and workspaces under `$FOREMAN_HOME/state/` and `$FOREMAN_HOME/work/`.

**Testing**:
- Unit tests in `internal/core/task`, `internal/core/engine`, `internal/core/verify`, table-driven with fakes.
- `internal/archtest` enforcing hexagonal boundary (core imports no adapters).
- `-tags container` for container verifier and diff policy tests.
- `-tags live` for real runner CLI tests.

**Target Platform**: macOS on Apple Silicon with Colima (4 CPU / 6 GiB).

**Project Type**: CLI command (`fm run`) within the single `fm` binary.

**Performance Goals**: per-attempt overhead (clone + verify + report, excluding LLM time) < 60s.

**Constraints**:
- Every write in disposable clone; no shared `.git` directory.
- Offline container verification with `--network=none`.
- Reviewer family must differ from implementer family.
- Hard budgets on tokens and wall time.

## Constitution Check

*Gate: Checked before Phase 0 research and verified post-design. Result: **PASS**.*

| Principle | How this feature satisfies it | Enforced by / proven by |
|---|---|---|
| I. Deterministic orchestration | Go engine owns state machine, ladder transitions, retries, and budgets. Models are called only at explicit nodes (`spec`, `implement`, `review`) with JSON schemas | `internal/core/engine` loop; no LLM in control decisions; unit tests with fakes |
| II. Evidence decides "done" | Verification in clean container with hidden tests and diff-policy checks decides success. Agent's claim never completes task | `internal/core/verify` report; SC-003 test |
| III. Acceptance criteria first | `TaskSpec` generated and validated against base commit before implement; hidden tests withheld | `spec` node validation; runner prompts omit hidden tests |
| IV. Single-threaded writes | One writer per attempt in a fresh disposable clone. No parallel writers | `internal/adapters/sandbox` clone per attempt |
| V. Cross-Family review | Reviewer must be different family from implementer (Codex <-> Claude). Findings require evidence/repro for major/critical | `internal/core/engine/review.go` route pairing; downgraded if lacking evidence |
| VI. Isolation by default | Fresh clones, no shared `.git`. Verification in `--network=none` container with read-only mounts. Patches exported via trusted git | `internal/adapters/sandbox`, `internal/adapters/container` |
| VII. Durable & resumable state | SQLite WAL; state transition + event in single transaction. Startup sweep detects dead PIDs via start-time fingerprint | `0003_tasks_and_nodes.sql`; `SweepLostRuns`; crash safety test |
| VIII. Gated deliberation & approval | Rung 3 marks `blocked_human` instead of looping or auto-approving | Engine ladder stops at Rung 3; exits 1 with report |
| IX. Minimal infrastructure | Standard library + pure-Go SQLite. No workflow engines or daemons | `go.mod` untouched; no new services |
| X. Hexagonal, minimal structure | `internal/core/task`, `internal/core/engine`, `internal/core/verify` contain pure logic and port definitions. Adapters implement ports. `cmd/fm/run.go` wires | `internal/archtest` passes |
| Measure as we go | Writes Feature 001 record on task completion; implements arm `H1` | FR-021, FR-022; `fm eval replay --arm h1` |
| Token economy | Budget caps on tasks; no prior diff passed to Rung 2 to avoid token bloat and anchoring | `ImplementInput.TokenBudget`; prompt construction |
| Continuous evaluation | Prompt versions hashed; recorded tasks join replay pool | `arm_version` hashing; record storage |

## Project Structure

### Documentation (this feature)

```text
specs/002-durable-execution-loop/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
└── contracts/
    ├── cli.md           # fm run commands and flags
    ├── ports.md         # TaskStore, Specifier, Implementer, Verifier, Reviewer
    └── ladder.md        # escalation rungs, loop guards, evidence injection
```

### Source Code (repository root)

```text
cmd/fm/
├── main.go                     # wire "run" subcommand
└── run.go                      # flag parsing + wiring adapters into core engine
internal/core/
├── task/
│   ├── task.go                 # Task, Node, Run domain types, state transitions
│   ├── task_test.go
│   └── ports.go                # TaskStore port
├── engine/
│   ├── engine.go               # RunTask loop: spec -> implement -> verify -> review
│   ├── engine_test.go
│   ├── ladder.go               # Rungs 1-3, loop guards (signature, diff oscillation)
│   ├── ladder_test.go
│   ├── review.go               # cross-family review rules, evidence gating
│   └── ports.go                # Specifier, Implementer, Reviewer, Verifier ports
└── verify/
    ├── policy.go               # diff policy: test deletion, protected paths, secrets, symlinks
    ├── policy_test.go
    └── verifier.go             # wraps container grader with visible + hidden tests
internal/adapters/
├── sqlite/
│   ├── store.go                # implements task.TaskStore
│   └── store_test.go
├── runner/
│   ├── codex/                  # implementer / reviewer support
│   ├── hermes/                 # implementer / reviewer support
│   └── harness/                # implements eval.Arm as H1
│       ├── h1.go
│       └── h1_test.go
migrations/
└── 0003_tasks_and_nodes.sql    # tasks, nodes, runs, events tables
schemas/
├── task_spec.json
└── review_findings.json
```

## Delivery

| PR | Contents | User Story | Verification |
|---|---|---|---|
| 1 | Migration `0003`, `internal/core/task` (Task, Node, Run state machines), SQLite store implementation | Foundation | Unit tests + archtest |
| 2 | `internal/core/verify` (diff policy + container verifier extension), `schemas/task_spec.json`, project.yaml parser | US2 (verifier gates) | Container tests, policy violation checks |
| 3 | `internal/core/engine` (run loop, ladder rungs 1-3, loop guards, cross-family review), `schemas/review_findings.json` | US1 & US2 | Engine unit tests with fakes |
| 4 | `cmd/fm/run.go` wiring, `internal/adapters/runner/harness` (arm `H1`), record emission | US1, US3, SC-004 | Quickstart steps 2-6 end-to-end |
| 5 | Polish, documentation, carry-overs, clean FOREMAN_HOME validation | US1-US3 complete | Quickstart full pass, gofmt, vet, test |

## Complexity Tracking

| Change / Addition | Principle | Justification |
|---|---|---|
| `internal/core/task`, `engine`, `verify` packages | Principle X, IX | Target hexagonal layout defined in `AGENTS.md`. No third-party modules added. |
| Migration 0003 (`tasks`, `nodes`) | Principle VII | Durable relational persistence for multi-node task lifecycle. |
