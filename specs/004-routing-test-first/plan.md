# Implementation Plan: Routing, Provider Health, and Test-First

**Branch**: `004-routing-test-first` | **Date**: 2026-10-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/004-routing-test-first/spec.md`

## Summary

Add multi-route model routing with provider health tracking and cost-aware selection, a dedicated test-author node for lane S/D tasks, and lane-based budget/gate differentiation. The router selects among eligible routes via `argmin cost / P(verified success)` with Beta-prior estimation, puts providers in cooldown on 429/plan-limit/5xx signals, and parks nodes (rather than failing) when all routes are cooling down. A test-author node writes acceptance tests from the spec before implementation for lanes S/D, with hidden-test handling matching Feature 002's pattern. Lane F/S/D differentiation comes from `policy.yaml` with per-lane budget caps and review intensity. A local in-process classifier handles triage (task class + risk) with API fallback.

## Technical Context

**Language/Version**: Go 1.27 (same as existing codebase)

**Primary Dependencies**: `modernc.org/sqlite` (existing), `golang.org/x/sys` (existing). New: local inference runtime for triage classifier (research R5 — likely `github.com/nlpodyssey/gopickle` or a minimal ONNX runtime; deferred to research.md). No new external services.

**Storage**: SQLite (WAL mode) — new tables for provider health, routing statistics, lane definitions. `policy.yaml` on the filesystem.

**Testing**: `go test ./...` with table-driven tests. Core logic tested with fakes; adapters against real SQLite. Container tests for verifier integration.

**Target Platform**: macOS (launchd host process), same as existing daemon.

**Project Type**: Go library + daemon (extends existing `internal/core/` + `internal/adapters/` layout).

**Performance Goals**: Routing decision must complete in <10ms (pure computation over cached state). Triage classification must complete in <500ms (local model inference).

**Constraints**: No LLM in the control loop (Principle I). No new always-on services (Principle IX). Core packages remain stdlib-only (Principle X). Test-author node must use a different model family than the implementer (Principle III).

**Scale/Scope**: 3 model families (codex, hermes/claude, agy), 3 lanes (F/S/D), ~5 route configurations per role. Routing statistics accumulate over time (Beta prior until 10 samples per route/class pair).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Mechanism |
|-----------|--------|-----------|
| I. Deterministic Orchestration | ✅ | Router is pure computation over provider health + routing stats. No LLM in routing decisions. |
| II. Evidence-Based Verification | ✅ | Test-author output verified to fail on base before implementer sees it (FR-008). |
| III. Acceptance Criteria First | ✅ | Test-author node is the core of this feature — writes tests from spec before implementation (FR-007). |
| IV. Single-Threaded Writes | ✅ | Test-author and implementer run sequentially, never in parallel. |
| V. Cross-Family Review | ✅ | Test-author uses different family than implementer (FR-007). Router enforces family exclusion (FR-002). |
| VI. Isolation by Default | ✅ | No change to isolation model — test-author runs in same sandbox as other nodes. |
| VII. Durable & Resumable State | ✅ | Provider health and routing stats live in SQLite. Node parking (FR-005) uses `not_before` column. |
| VIII. Gated Deliberation | ✅ | Lane differentiation (US3) gates heavier machinery to lane D. Lane F gets light review only. |
| IX. Minimal Infrastructure | ✅ | Local classifier is in-process (not a service). `policy.yaml` is a file. No new databases. |
| X. Hexagonal Structure | ✅ | Router lives in `internal/core/router/`. Classifier is an adapter. Policy parsing is an adapter. |

## Project Structure

### Documentation (this feature)

```text
specs/004-routing-test-first/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/core/
├── router/              # NEW: route selection, provider health, cooldown, Beta prior
│   ├── router.go        # Router type, Select() method, family exclusion
│   ├── health.go        # ProviderHealth state machine (OK/cooling/quota-low)
│   ├── stats.go         # Beta-prior routing statistics
│   └── *_test.go
├── policy/              # NEW: lane definitions (F/S/D), budget caps, policy.yaml parsing
│   ├── policy.go        # Lane type, LoadPolicy(), budget enforcement
│   └── *_test.go
├── task/                # EXTEND: add test_author node type, lane field, not_before column
└── engine/              # EXTEND: integrate router into dispatch, add test-author step

internal/adapters/
├── classifier/          # NEW: local triage classifier (in-process model)
│   ├── classifier.go    # Load model, Classify() → (task_class, risk, confidence)
│   └── *_test.go
├── runner/              # EXTEND: add agy (Antigravity) arm
└── sqlite/              # EXTEND: provider_health, routing_stats, lane columns

cmd/foreman/             # EXTEND: wire router, classifier, policy loading
```

**Structure Decision**: Extends the existing hexagonal layout. `internal/core/router/` and `internal/core/policy/` are new core packages (stdlib-only). `internal/adapters/classifier/` wraps the local inference runtime. No new top-level directories.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Local classifier runtime (new dependency) | FR-013: triage must work offline, no network call | API-only triage fails when provider is down; triage is upstream of the router that protects against provider outages |
| `agy` (Antigravity/Gemini) as third family | Cross-family rules (FR-007, V) need 3 families for reviewer/test-author/adjudicator separation | 2 families can't satisfy "different family" for both reviewer AND test-author on the same task |
| `policy.yaml` file format | FR-009: lane definitions need operator-editable config | Hardcoding lanes in Go makes operator tuning require a rebuild |
