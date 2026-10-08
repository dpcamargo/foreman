# Contract: `fm run` CLI (Feature 002)

The binary is `fm` (`cmd/fm`). Subcommands are dispatched with standard-library `flag`.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Success (task reached verified done and passed review) |
| 1 | Attempt failure or ladder exhausted (blocked_human, test failure, policy violation) |
| 2 | Usage error (invalid arguments, missing task file or project config) |
| 3 | Infrastructure error (Docker unavailable, runner CLI missing, disk full) |

Output is human text on stdout by default. `--json` prints one structured JSON result object instead. Errors go to stderr.

---

## `fm run <task.yaml>`

Executes one task end to end through the durable execution loop (FR-001):
`spec` -> `implement` -> `verify` -> `review` -> `report`.

```text
fm run <task.yaml> [--arm <harness|codex|hermes>] [--max-rung <1..3>]
                   [--budget-tokens <N>] [--budget-usd <X>]
                   [--wall-timeout <duration>] [--json]
```

### Arguments and Flags

- `<task.yaml>` (positional, required): Path to task definition file (or a Feature 001 record ID like `rec_...`).
- `--arm <name>`: Runner arm configuration. Defaults to `harness` (standard durable loop with Codex implementer and Claude reviewer). Alternatively, explicitly selects an implementer route.
- `--max-rung <1..3>`: Maximum escalation ladder rung before stopping. Default `3` (`blocked_human`).
- `--budget-tokens <N>`: Hard cap on total billable tokens across all attempts for this task.
- `--budget-usd <X>`: Hard cap on total dollar cost (if applicable).
- `--wall-timeout <duration>`: Hard wall-clock timeout for the entire task. Default `30m`.
- `--json`: Output structured JSON report on stdout.

### Task File Format (`task.yaml`)

```yaml
version: 1
task_type: code
repo: "/path/to/repo.git"
base_commit: "7fb3e561ee1cee9f9f02a6dccaacbee1212cb336"
request: |
  Add subtraction function Sub(a, b int) int to package addrepo.
visible_tests: "testdata/fixtures/addrepo/visible" # optional
hidden_tests: "testdata/fixtures/addrepo/hidden"   # required
test_cmd: ["go", "test", "-json", "./..."]
category: feature
```

### Execution Steps

1. **Reconciliation Sweep**: Run `SweepLostRuns` to detect dead processes from prior runs.
2. **Task Ingestion**: Create task row and initialize nodes (`spec`, `implement`, `verify`, `review`) in one SQLite transaction.
3. **Node 1 - Spec**: Call specifier model with `schemas/task_spec.json`. Validate cited files exist in git tree on base commit; validate verification commands.
4. **Node 2 - Implement**: Prepare fresh clone in disposable workspace. Dispatch implementer (e.g. Codex) with `TaskSpec`. Collect patch diff.
5. **Node 3 - Verify**: Run diff policy checks (test deletion, protected paths, secret scan, symlinks). Run offline containerized build, visible tests, hidden tests. Generate `VerifyReport`.
6. **Escalation / Retry Loop**:
   - If verification fails: evaluate loop guards (signature repeat, oscillation). Advance ladder rung (Rung 1: retry with evidence; Rung 2: escalate family; Rung 3: `blocked_human`).
7. **Node 4 - Review**: Dispatch cross-family reviewer (e.g. Claude via Hermes) with diff and spec. Require concrete evidence/repro for major/critical findings.
8. **Report & Record**: Write task report (`state/reports/rpt_<id>.{md,json}`) and Feature 001 task record (`records`).
