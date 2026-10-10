# Research: Routing, Provider Health, and Test-First

**Feature**: `004-routing-test-first`

## R1. Router design: pure computation, no LLM

**Decision**: `internal/core/router/` is a pure Go package. `Select(role, taskClass, risk, excludedFamilies)` returns an ordered candidate list. No I/O, no model calls — it reads pre-computed provider health and routing statistics passed in as arguments.

**Rationale**: Constitution Principle I — routing is part of the control loop and must be deterministic. The router is consulted on every node dispatch; adding an LLM call there would make every dispatch non-deterministic and expensive.

**Alternatives considered**:
- LLM-based routing (model picks the route): rejected — violates Principle I, adds latency and cost to every dispatch.
- Rule-based YAML config only (operator writes priority lists): rejected as the *sole* mechanism — doesn't support FR-003's `argmin cost / P(verified success)` or automatic cooldown failover.

## R2. Provider health state machine

**Decision**: In-memory state (`internal/core/router/health.go`) backed by SQLite persistence (`provider_health` table). States: `OK`, `CoolingDown(until)`, `QuotaLow`. Transitions driven by error signals (429/plan-limit/5xx/expired-auth → cooldown) and usage reports (quota threshold → quota-low). Cooldown uses the provider's reported reset time if present, otherwise exponential backoff (1m, 2m, 4m, ... capped at 1h).

**Rationale**: FR-001/FR-004. In-memory for fast access during routing; persisted so a daemon restart doesn't lose cooldown state. Exponential backoff matches the pattern already used in `telegram.Client.Poll` and `Integrator.PushWithBackoff`.

**Alternatives considered**:
- Redis/in-memory cache: rejected — adds infrastructure (Principle IX).
- Checking provider health via a live API call before each routing decision: rejected — adds latency and network dependency to the routing path.

## R3. Beta-prior routing statistics

**Decision**: Per `(route_id, task_class)` pair, store `alpha` (successes + 1) and `beta` (failures + 1). `P(verified success) = alpha / (alpha + beta)`. Until 10 samples exist, the prior dominates (starts at alpha=1, beta=1 → P=0.5). Updated after every verified run (FR-011).

**Rationale**: FR-003 explicitly requires Beta prior until ≥10 samples. This is a well-known conjugate prior for binomial outcomes — simple to implement in stdlib, no external math library needed.

**Alternatives considered**:
- Simple success rate (successes/total): rejected — spec edge case explicitly says "MUST use a Beta prior, not a point estimate" for sparse data.
- Bayesian normal approximation: rejected — overkill for binary outcomes, needs more math.

## R4. Test-author node design

**Decision**: New `test_author` node type in `internal/core/task/`. Runs before `implement` when the lane has `test_first: true`. Uses the router to select a model family *different* from the implementer's assigned family (FR-007). Produces two test sets: visible (copied into the implementer's workspace) and hidden (stored outside all workspaces, mounted read-only only into the verifier). The verifier confirms every hidden test fails on the unmodified base commit before the implementer starts (FR-008).

**Rationale**: Constitution Principle III — acceptance criteria must be fixed before execution, by a different model family. Feature 002 already handles hidden tests for eval-provided tasks; this generalizes it to spec-driven test-authoring.

**Alternatives considered**:
- Reusing Feature 002's existing test input (tasks must arrive with tests): rejected — spec says this lifts that restriction for lanes S/D.
- Running test-author and implement in parallel: rejected — violates Principle IV (single-threaded writes) and defeats the purpose (tests must exist before implementation).

## R5. Local triage classifier

**Decision**: In-process classifier using a small quantized encoder model loaded at daemon startup. Runtime: `github.com/nlpodyssey/gopickle` (Go-native PyTorch model loading) or a minimal ONNX runtime via `github.com/yalue/onnxruntime_go`. Model pinned by exact version + checksum (FR-015). Output: `(task_class, risk, confidence)`. If confidence < threshold or model fails to load, fall back to API-based triage (FR-014).

**Rationale**: FR-013 — triage is upstream of the router and can't depend on the providers it protects against. In-process avoids a sidecar/container (Principle IX). The fallback covers cold-start accuracy gaps.

**Alternatives considered**:
- Separate classification microservice: rejected — adds infrastructure (Principle IX).
- Using the existing codex/hermes arms for triage: rejected — that's the API-based fallback, not the primary path. FR-013 specifically requires local.
- `onnxruntime-go` vs `gopickle`: ONNX is more portable (export from Python), gopickle is more Go-native. Decision deferred to implementation — both work; ONNX is likely safer for model distribution.

## R6. `policy.yaml` format

**Decision**: YAML file at `$FOREMAN_HOME/policy.yaml` (or repo root). Schema follows ARCHITECTURE.md §14:

```yaml
lanes:
  F:
    max_usd: 0.50
    max_minutes: 10
    review: light
    test_first: false
  S:
    max_usd: 5.00
    max_minutes: 30
    review: full
    test_first: true
  D:
    max_usd: 20.00
    max_minutes: 120
    review: full
    test_first: true
    deliberation: true
api_spend_without_approval_usd_per_day: 10.00
claude_subscription_unattended: false
```

**Rationale**: FR-009 — lane definitions loaded from `policy.yaml`, refuse to dispatch if missing/malformed. The format matches the spec's Assumptions section.

**Alternatives considered**:
- SQLite table for lane config: rejected — operator-editable config should be a file (easier to version, review, edit).
- TOML/JSON: rejected — spec says YAML, and the existing `.foreman/project.yaml` already uses YAML (well, a line-based subset).

## R7. Lane-aware budget enforcement

**Decision**: The engine tracks accumulated spend per task. Before dispatching each node, it checks `accumulated_spend + estimated_cost <= lane.max_usd`. If exceeded, the node is blocked with a `budget_exceeded` reason (FR-010). Spend is estimated from the route's pricing (API) or $0 (subscription) with a `quota_pressure` penalty.

**Rationale**: FR-010 — "block dispatch before an overspend occurs rather than detecting it after the fact." The engine already tracks tokens/wall-clock per run; this adds a dollar-denominated check.

**Alternatives considered**:
- Post-hoc detection (check after the run): rejected — spec explicitly says "block dispatch before an overspend occurs."
- Per-node budgets: rejected — spec says per-task accumulated spend against lane cap.

## R8. `agy` (Antigravity/Gemini) arm

**Decision**: New `internal/adapters/runner/agy/` package implementing `engine.Implementer` and `engine.Reviewer`. Uses `agy -p` (headless mode) with structured output, same pattern as `codex` and `hermes` arms.

**Rationale**: Spec Assumptions — adds `agy` as the third routable family. Cross-family rules need 3 families.

**Alternatives considered**:
- Only 2 families: rejected — can't satisfy "different family" for both reviewer AND test-author.

## R9. Node parking (FR-005)

**Decision**: Add `not_before` timestamp column to the `nodes` table. When all routes for a role are cooling down, set `not_before = earliest_cooldown_expiry` and leave the node in `pending` state (not `failed`). The reconciler picks it up when `not_before` has passed. Notify operator via Feature 003's ntfy channel only if stall exceeds 1 hour.

**Rationale**: FR-005 — "park a node (set `not_before`, no attempt counted) rather than fail it." This preserves the attempt count for the retry ladder.

**Alternatives considered**:
- Immediate failure + retry: rejected — burns a ladder rung for what's a transient provider issue.
- Busy-wait loop: rejected — wastes resources, violates Principle VII (durable state).
