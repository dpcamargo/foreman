# Quickstart: Routing, Provider Health, and Test-First

## Prerequisites

- Feature 003 daemon running (`bin/foreman`)
- `policy.yaml` at `$FOREMAN_HOME/policy.yaml` (see contracts/router-and-policy.md for schema)
- Local triage classifier model file at `$FOREMAN_HOME/models/triage.onnx` (pinned version, see FR-015)
- At least 2 of 3 model families available (codex, hermes, agy)

## Setup

```sh
# 1. Create policy.yaml
cat > $FOREMAN_HOME/policy.yaml << 'EOF'
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
EOF

# 2. Rebuild and restart daemon (new migration 0006)
go build -o bin/foreman ./cmd/foreman
# restart daemon...
```

## Scenario 1: Provider cooldown failover (US1, SC-001)

1. Send `/task addrepo add a Sum function` (lane S, test_first: true).
2. While the implementer is running, force a 429 from the primary provider (mock or real rate limit).
3. **Verify**: router selects the next eligible route without operator intervention. Check `providers` table shows `cooling_down` with a `cooldown_until` timestamp.
4. Wait for cooldown to elapse.
5. **Verify**: provider returns to `ok` status automatically. Next task can use it again.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, status, cooldown_until from providers;"
```

## Scenario 2: All routes cooling down → node parks (US1)

1. Force 429 on ALL providers simultaneously.
2. Send a task.
3. **Verify**: node status is `pending` (not `failed`), `not_before` is set to the earliest cooldown expiry. No attempt counted.
4. **Verify**: operator receives ntfy notification only after 1 hour of stalling.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select name, status, not_before from nodes where task_id='<id>';"
```

## Scenario 3: Test-author runs before implement (US2, SC-002)

1. Send `/task addrepo add a Sum function that returns a+b` (lane S).
2. **Verify**: `test_author` node runs before `implement` in the node list.
3. **Verify**: test-author produces visible + hidden test patches.
4. **Verify**: verifier confirms every hidden test fails on the unmodified base commit.
5. **Verify**: implementer receives only visible tests in its workspace.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select name, status from nodes where task_id='<id>' order by step_order;"
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, fails_on_base, model_family from test_author_results where task_id='<id>';"
```

## Scenario 4: Lane differentiation (US3, SC-003)

1. Send `/task addrepo add a doc comment to Add` (lane F — trivial).
2. Send `/task addrepo add a Sum function with full test coverage` (lane S — complex).
3. **Verify**: lane F task skips test-author node, uses light review (single round).
4. **Verify**: lane S task includes test-author node, uses full review.
5. **Verify**: lane F task's `accumulated_usd` is lower than lane S's.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, lane, accumulated_usd from tasks order by created_at desc limit 2;"
sqlite3 $FOREMAN_HOME/state/foreman.db "select name from nodes where task_id='<lane_F_id>' order by step_order;"
```

## Scenario 5: Budget enforcement (US3, SC-004)

1. Send a task with lane F (`max_usd: 0.50`).
2. Let it run until spend approaches $0.50.
3. **Verify**: engine blocks the next node dispatch with `budget_exceeded` reason. No overspend.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, lane, accumulated_usd from tasks where id='<id>';"
sqlite3 $FOREMAN_HOME/state/foreman.db "select name, status from nodes where task_id='<id>' order by step_order;"
```

## Scenario 6: Local triage with fallback (US4, SC-006)

1. Disable network access (or simulate by removing API keys).
2. Send `/task addrepo add a Sum function`.
3. **Verify**: task gets `task_class` and `risk_level` assigned from the local classifier (`triage_source = 'local'`).
4. **Verify**: no network call was made during triage.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, task_class, risk_level, triage_source from tasks where id='<id>';"
```

## Scenario 7: Cross-family rules hold (SC-005)

1. Run a lane S task through the full pipeline.
2. **Verify**: test-author's `model_family` differs from implementer's assigned family.
3. **Verify**: reviewer's `model_family` differs from implementer's assigned family.

```sh
sqlite3 $FOREMAN_HOME/state/foreman.db "select id, model_family from test_author_results where task_id='<id>';"
sqlite3 $FOREMAN_HOME/state/foreman.db "select route, model_family from runs where task_id='<id>';"
```
