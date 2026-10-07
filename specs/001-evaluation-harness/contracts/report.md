# Contract: replay algorithms and reports

## Decision replay (`fm eval decide`)

```text
decide(arms, n, k, seed, budgetTokens, budgetUSD, usageChecked):
  sweepLostRuns()
  pool   := records(replayable=1, task_type='code')              # regardless of live outcome
  if len(pool) < n: fail(1, "only len(pool) replayable records; need n")
  sample := seededShuffle(pool, seed)[:n]                        # recorded in draw order

  smoke := findOrCreateReport(mode='smoke', seed, arms, k=1, sample[:3])
  for rec in sample[:3], arm in arms:
      r := replayOnce(rec, arm, rep=1)                           # cache-aware
      if r.status == infra_error or r.grade == nil: fail(3, "smoke not clean", r)
  saveReport(smoke)

  if any(arm.family == claude for arm in arms) and not smoke.usage_checked:
      if not usageChecked: fail(1, "check the Anthropic usage page, then re-run with --usage-checked")
      markUsageChecked(smoke)

  perRun := smoke.spent / count(smoke.runs that were not cache hits)
  todo   := count((rec, arm, rep) in sample × arms × 1..k not already cached)
  projected := perRun * todo
  if projected.tokens > budgetTokens or (budgetUSD and projected.usd > budgetUSD):
      fail(1, "projected spend over budget", projected)

  dec := newReport(mode='decision', parent=smoke, seed, arms, k, sample, budgets, projected)
  for rec in sample, arm in arms, rep in 1..k:
      r := replayOnce(rec, arm, rep)                             # each run commits on its own
      link(dec, r)
  saveReport(dec with metrics(dec))
```

`replayOnce(rec, arm, rep)`:

```text
key := (rec.id, arm.name, arm.version(), rep, rec.base)
run, reused := store.StartRun(key, myPID)          # tx: insert running row, or return cached
if reused: return run
ws, err := workspaces.Prepare(rec.repo, rec.base, rec.visible)
if err is ErrBaseNotFound:
    store.SetReplayable(rec.id, false, "base commit no longer fetchable")   # tx: + event
    store.FinishRun(run as infra_error); return                     # tx: status + event
if err: store.FinishRun(run as infra_error); return
io  := artifacts.IODir(run.id); tr := artifacts.Transcript(run.id)
res := arm.Attempt(ws, prompt(rec), io, tr, rec.wallBudget, tokenCap)  # adapter writes io/prompt.md
workspaces.Export(ws, artifacts.PatchPath(run.id))                # trusted git dir (research R6)
artifacts.Collect(run.id)                                          # io/* → state/runs/<id>/, redacted
g   := grader.Grade(rec, artifacts.PatchPath(run.id))             # infra error → infra_error
g.contaminated = leakScan(run.dir/transcript.jsonl, stateDir, names(rec.hidden))
g.success, g.false_success = rules(data-model.md)
store.FinishRun(run with res, g)                                   # tx: status + grade + event
workspaces.Destroy(ws)
```

Runs execute one at a time by default (`--parallel` is not built; YAGNI). An interrupted
`decide` is resumed by running the same command again: finished runs are cache hits, and lost
ones are swept and retried.

## Spot check (`fm eval spot`)

The same `replayOnce` with k=1 for one arm over the chosen records. The report compares each
record's `live_outcome` with the replay's success and labels itself a trend. It never applies
go/no-go thresholds.

## Metrics (per arm, over that report's graded runs; `infra_error` excluded)

| Metric | Definition |
|---|---|
| success_rate | successes / graded runs |
| pass_k | share of records where all k reps succeeded (shown when k ≥ 2; "pass^3" at k=3) |
| false_success_rate | false_success runs / graded runs |
| regression_rate | runs with `full_suite_regressed=1` / runs on records that have a full suite |
| clarification_accuracy | on `expects_clarification` records: asked / runs |
| tokens_per_verified_success | billable tokens / successes |
| usd_per_verified_success | usd / successes (NULL when the arm has no dollar figure) |
| median_wall_s | |
| contaminated_runs | count; any non-zero value is shown in red in the markdown |
| rescues | from live records only (research R10); replays show "n/a" |

## Paired comparison (decision mode)

- The best baseline is the baseline arm (a0, a1, a2) with the highest success rate; ties go to
  the lower cost per verified success.
- For every other arm X versus the best baseline B:
  - `d_task` = mean success of X minus mean success of B, per record;
  - estimate = mean of `d_task`;
  - 95% interval = bootstrap over records (10,000 resamples, report seed);
  - `indistinguishable` = the interval contains 0.
- The same is done for tokens and dollars per run.

The threshold table (ARCHITECTURE.md §16 / Appendix D) is shown for each non-baseline arm (H1
from Feature 002):

| Criterion | Value | Met? |
|---|---|---|
| success ≥ B + 10 points, interval excluding 0 | | yes / no / indistinguishable |
| false-success rate ≤ ½ × B's | | |
| cost per verified success ≤ 2 × B's | | |
| regression rate ≤ B's | | |
| equal success with ≥ 30% fewer rescues | not measurable by replay | n/a |

The report states the numbers. The go/no-go decision and its ADR are Feature 002's (SC-006).

## Report JSON (`state/reports/<id>.json`)

```json
{
  "id": "rpt_…", "mode": "decision", "parent_id": "rpt_…", "seed": 42, "k": 3,
  "arms": ["a0", "a1", "a2"],
  "sample": {"records": ["rec_…"], "repos": {"<url>": 9}, "categories": {"feature": 6},
             "single_repo_majority": true},
  "spend": {"tokens": 0, "usd": 0.0, "wall_s": 0, "cache_hits": 0,
            "projected": {"tokens": 0, "usd": 0.0}, "budget": {"tokens": 0, "usd": null}},
  "metrics": {"a1": {"success_rate": 0.0, "pass_k": 0.0, "false_success_rate": 0.0, "...": 0}},
  "comparisons": [{"arm": "a0", "vs": "a1", "metric": "success",
                   "estimate": 0.0, "ci95": [0.0, 0.0], "indistinguishable": true}],
  "thresholds": [],
  "runs": [{"run_id": "run_…", "record_id": "rec_…", "arm": "a1", "rep": 1,
            "status": "succeeded", "claimed": "done", "success": true, "false_success": false,
            "hidden": {"passed": 4, "failed": 0}, "tokens_billable": 0, "usd": null,
            "wall_s": 0, "cached": false}],
  "records_since_last_spot": 0
}
```

The `runs` array is the paired data that FR-009 requires (one entry per record × arm × rep).

## Report markdown (`state/reports/<id>.md`)

Sections in this order:
1. Header: mode, date, seed, arms, k. Spot reports add "TREND ONLY — not a decision". Smoke
   reports add "SMOKE".
2. Sample: count, repositories and categories, and a single-repository warning when one repo
   is more than half the sample (FR-012).
3. Spend: tokens, dollars and time; projected versus budget; cache hits (FR-016).
4. Metrics table (one row per arm).
5. Comparisons, with the interval and an "indistinguishable" flag (FR-013).
6. Thresholds (decision mode, non-baseline arms only).
7. Contaminated or infrastructure-error runs, listed.
8. Footer: replayable records since the last spot check (FR-017).
