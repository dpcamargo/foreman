# Contract: `fm eval` CLI

The binary is `fm` (`cmd/fm`). Subcommands are dispatched with standard-library `flag` (one
`FlagSet` per subcommand). Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | the command ran but its result is negative (e.g. record not replayable, budget refused) |
| 2 | usage error |
| 3 | infrastructure error (Docker down, git fetch failed, arm CLI missing) |

Output is human text on stdout by default. `--json` prints one JSON object instead. Errors go
to stderr. Every command that runs arms prints the tokens, dollars and time it spent before it
exits (FR-016).

## `fm eval record`

Records a task done outside foreman (FR-002). Feature 002 calls the same use case in-process.

```text
fm eval record --repo <url> --base <sha> --request-file <path>
               [--hidden <dir> --test-cmd '<json argv>'] [--test-format go-test-json|exit-code]
               [--visible <dir>] [--reference <patch>] [--full-test-cmd '<json argv>']
               [--category bug_fix|feature|refactor|dependency|research|ambiguous]
               [--expects-clarification] [--wall-budget 30m]
               [--live-outcome <o> --live-attempts N --live-tokens N --live-usd X
                --live-wall 12m --live-interventions N]
               [--no-validate] [--json]
```

- `--hidden` and `--test-cmd` go together. Without them the record is kept for metrics only:
  not replayable, with reason `no acceptance tests` (spec US1 scenario 3).
- Copies `--hidden` and `--visible` into `state/records/<id>/`, rejecting escaping paths and
  symlinks.
- Validates immediately (as `fm eval validate <id>`) unless `--no-validate` is given.
- Prints the record id and whether it is replayable or why not. Exits 1 if not replayable.
- Defaults: `--category feature`, `--test-format go-test-json`, `--wall-budget 30m`.

## `fm eval validate [<record-id>... | --pending | --all]`

Runs the replayability rules (data-model.md) and updates `replayable`. `--pending` (the
default) validates only records never validated. `--all` revalidates everything, for example
after a grader image bump.

## `fm eval list [--replayable] [--since <rpt-id>|<duration>] [--repo <url>] [--json]`

One line per record: id, category, repo, live outcome, replayable or reason, and created_at.
The footer always states how many replayable records exist since the last spot check (FR-017).

## `fm eval replay --record <id> --arm <a0|a1|a2> [--k 1] [--max-tokens N] [--no-cache] [--json]`

Ad-hoc replay of one record. Reuses cached runs (FR-015) unless `--no-cache` is given, and
prints each run's grade. `--no-cache` exists for validation (quickstart steps 5 and 7); its
runs are stored but never reused.

## `fm eval spot [--arm a1] [--since-last | --records id,...] [--limit 5] [--max-tokens N]`

Spot check (FR-011):
- k=1, one baseline arm, over replayable records added since the last spot check (default),
  newest first, up to `--limit`.
- Compares each record's live outcome with the arm's replay.
- The report's mode is `spot`, and its header states that it is a trend, not a decision.
- Default arm: `a1`, the cheapest unattended-allowed arm.

## `fm eval decide --arms a0,a1,a2[,h1] [--n 15] [--k 3] [--seed <int>]
                  --budget-tokens N [--budget-usd X] [--usage-checked] [--max-tokens N]`

Decision replay. The algorithm is in `contracts/report.md`. In summary:

1. Sample `n` replayable records with the seed (random if omitted, and always recorded).
2. Smoke pass: k=1 for every arm on the first 3 sampled records. Any infrastructure error or
   ungradeable run stops here (exit 3).
3. If a Claude-family arm took part and this smoke report hasn't been marked usage-checked,
   stop with instructions (exit 1). Re-running with `--usage-checked` reuses the cached smoke
   runs.
4. Project the full spend from the smoke pass. If it exceeds a budget, refuse (exit 1) and
   print the projection.
5. Run every (record, arm, rep), reusing cached runs, then write the decision report.

`--budget-tokens` is required (constitution: token economy).

## Arm names

`a0`, `a1` and `a2` (and `h1` from Feature 002). An instance suffix `#n` (e.g. `a1#2`) runs the
same arm under its own cache key and report column. It is used to measure noise: comparing an
arm with itself must come out `indistinguishable` (SC-004).

## `fm eval report <rpt-id> [--json]`

Re-renders a stored report from the database. Read-only.

## Environment

| Variable | Default | Use |
|---|---|---|
| `FOREMAN_HOME` | `~/.foreman` | state and work roots (research R2) |
| `FOREMAN_DOCKER_CONTEXT` | current docker context | grader and Hermes arm containers |
| `FOREMAN_HERMES_HOME` | `~/.hermes/profiles/foreman-arm` | Hermes profile for A0/A2 |
| `FOREMAN_GRADER_IMAGE` | `golang:1.27.1-bookworm@sha256:<pinned>` | grader image |
