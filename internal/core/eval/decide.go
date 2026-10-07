package eval

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Decision-replay outcomes the CLI maps to exit codes (contracts/cli.md).
var (
	ErrSmokeNotClean    = errors.New("smoke pass not clean")
	ErrUsageCheckNeeded = errors.New("check the Anthropic usage page, then re-run with --usage-checked")
	ErrOverBudget       = errors.New("projected spend over budget")
)

const smokeSize = 3

// ReportStore persists reports. Adapter: internal/adapters/sqlite.
type ReportStore interface {
	SaveReport(ctx context.Context, r Report) (Report, error)
	Report(ctx context.Context, id string) (Report, error)
	FindReport(ctx context.Context, mode ReportMode, seed int64, arms, sample []string) (*Report, error)
	SetUsageChecked(ctx context.Context, id string) error
	LastSpotCheck(ctx context.Context) (*Report, error)
}

// Evaluator runs spot checks and decision replays (contracts/report.md).
type Evaluator struct {
	Replayer Replayer
	Reports  ReportStore
}

// IsClaudeArm reports whether an arm runs on the Anthropic subscription (A0, A2).
func IsClaudeArm(name string) bool {
	base, err := ParseArm(name)
	return err == nil && (base == "a0" || base == "a2")
}

// DecideRequest asks for a decision replay.
type DecideRequest struct {
	Arms         map[string]Arm // instance name -> arm
	ArmOrder     []string
	N, K         int
	Seed         int64
	BudgetTokens int64
	BudgetUSD    *float64
	UsageChecked bool
}

// DecideResult is a decision replay's smoke report, decision report and rows.
type DecideResult struct {
	Smoke     Report
	Decision  *Report
	Projected Spend
	Rows      []RunRow
	Sample    []Record
}

// Decide runs a decision replay: seeded sample, clean smoke pass, Claude usage gate,
// budget projection, then every (record, arm, rep). It resumes through the cache.
func (e Evaluator) Decide(ctx context.Context, req DecideRequest) (DecideResult, error) {
	pool, err := e.replayable(ctx)
	if err != nil {
		return DecideResult{}, err
	}
	sample, err := Sample(pool, req.N, req.Seed)
	if err != nil {
		return DecideResult{}, err
	}
	smokeRecs := sample[:min(smokeSize, len(sample))]
	smokeRows, err := e.runAll(ctx, smokeRecs, req.Arms, req.ArmOrder, 1)
	if err != nil {
		return DecideResult{}, err
	}
	for _, r := range smokeRows {
		if r.Run.Status == StatusInfraError || r.Grade == nil {
			return DecideResult{}, fmt.Errorf("%w: %s %s rep %d: %s %s", ErrSmokeNotClean, r.Record.ID, r.Run.Key.Arm, r.Run.Key.Rep, r.Run.Status, r.Run.Error)
		}
	}
	smoke, err := e.smokeReport(ctx, req, smokeRecs, smokeRows)
	if err != nil {
		return DecideResult{}, err
	}
	res := DecideResult{Smoke: smoke, Sample: sample}

	claude := slices.ContainsFunc(req.ArmOrder, IsClaudeArm)
	if claude && !smoke.UsageChecked {
		if !req.UsageChecked {
			return res, ErrUsageCheckNeeded
		}
		if err := e.Reports.SetUsageChecked(ctx, smoke.ID); err != nil {
			return res, err
		}
		smoke.UsageChecked, res.Smoke.UsageChecked = true, true
	}

	todo, err := e.uncached(ctx, sample, req)
	if err != nil {
		return res, err
	}
	res.Projected = project(smokeRows, todo)
	if res.Projected.Tokens > req.BudgetTokens ||
		(req.BudgetUSD != nil && res.Projected.USD != nil && *res.Projected.USD > *req.BudgetUSD) {
		return res, fmt.Errorf("%w: projected %d tokens, %s for %d run(s) (budget %d tokens)",
			ErrOverBudget, res.Projected.Tokens, money(res.Projected.USD), todo, req.BudgetTokens)
	}

	rows, err := e.runAll(ctx, sample, req.Arms, req.ArmOrder, req.K)
	if err != nil {
		return res, err
	}
	res.Rows = rows
	budget, projected := req.BudgetTokens, res.Projected.Tokens
	dec, err := e.Reports.SaveReport(ctx, Report{
		Mode: ModeDecision, Seed: req.Seed, Arms: req.ArmOrder, K: req.K, Sample: recordIDs(sample),
		BudgetTokens: &budget, BudgetUSD: req.BudgetUSD, ProjectedTokens: &projected, ProjectedUSD: res.Projected.USD,
		Spent: SpendOf(rows), UsageChecked: claude, ParentID: smoke.ID, RunIDs: runIDs(rows),
	})
	if err != nil {
		return res, err
	}
	res.Decision = &dec
	return res, nil
}

func (e Evaluator) smokeReport(ctx context.Context, req DecideRequest, recs []Record, rows []RunRow) (Report, error) {
	existing, err := e.Reports.FindReport(ctx, ModeSmoke, req.Seed, req.ArmOrder, recordIDs(recs))
	if err != nil {
		return Report{}, err
	}
	if existing != nil {
		return *existing, nil
	}
	return e.Reports.SaveReport(ctx, Report{
		Mode: ModeSmoke, Seed: req.Seed, Arms: req.ArmOrder, K: 1, Sample: recordIDs(recs),
		Spent: SpendOf(rows), RunIDs: runIDs(rows),
	})
}

// project estimates the remaining spend from the smoke runs' recorded usage.
func project(smoke []RunRow, todo int) Spend {
	if len(smoke) == 0 || todo == 0 {
		return Spend{}
	}
	var u Usage
	for _, r := range smoke {
		u = u.Add(r.Run.Usage)
	}
	s := Spend{Tokens: u.Billable() / int64(len(smoke)) * int64(todo), Runs: todo}
	if u.USD != nil {
		v := *u.USD / float64(len(smoke)) * float64(todo)
		s.USD = &v
	}
	return s
}

func (e Evaluator) uncached(ctx context.Context, sample []Record, req DecideRequest) (int, error) {
	todo := 0
	for _, rec := range sample {
		for _, name := range req.ArmOrder {
			for rep := 1; rep <= req.K; rep++ {
				hit, err := e.Replayer.Cached(ctx, ReplayRequest{Record: rec, Arm: req.Arms[name], ArmName: name, Rep: rep})
				if err != nil {
					return 0, err
				}
				if !hit {
					todo++
				}
			}
		}
	}
	return todo, nil
}

func (e Evaluator) runAll(ctx context.Context, recs []Record, arms map[string]Arm, order []string, k int) ([]RunRow, error) {
	var rows []RunRow
	for _, rec := range recs {
		for _, name := range order {
			arm, ok := arms[name]
			if !ok {
				return nil, fmt.Errorf("no adapter for arm %s", name)
			}
			for rep := 1; rep <= k; rep++ {
				res, err := e.Replayer.ReplayOnce(ctx, ReplayRequest{Record: rec, Arm: arm, ArmName: name, Rep: rep})
				if err != nil {
					return nil, fmt.Errorf("%s %s rep %d: %w", rec.ID, name, rep, err)
				}
				rows = append(rows, RunRow{Record: rec, Run: res.Run, Grade: res.Grade, Cached: res.Reused})
			}
		}
	}
	return rows, nil
}

func (e Evaluator) replayable(ctx context.Context) ([]Record, error) {
	all, err := e.Replayer.Records.Records(ctx, RecordFilter{ReplayableOnly: true})
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range all {
		if r.TaskType == TaskTypeCode && r.Replayable != nil && *r.Replayable {
			out = append(out, r)
		}
	}
	return out, nil
}

// SpotRequest asks for a spot check.
type SpotRequest struct {
	Arm       Arm
	ArmName   string
	RecordIDs []string // explicit records; otherwise those added since the last spot check
	Limit     int      // default 5
}

// Spot replays records once through one arm. Its report is a trend, never a decision.
func (e Evaluator) Spot(ctx context.Context, req SpotRequest) (Report, []RunRow, error) {
	recs, err := e.spotRecords(ctx, req)
	if err != nil {
		return Report{}, nil, err
	}
	name := req.ArmName
	if name == "" {
		name = req.Arm.Name()
	}
	rows, err := e.runAll(ctx, recs, map[string]Arm{name: req.Arm}, []string{name}, 1)
	if err != nil {
		return Report{}, nil, err
	}
	rep, err := e.Reports.SaveReport(ctx, Report{
		Mode: ModeSpot, Arms: []string{name}, K: 1, Sample: recordIDs(recs), Spent: SpendOf(rows), RunIDs: runIDs(rows),
	})
	return rep, rows, err
}

func (e Evaluator) spotRecords(ctx context.Context, req SpotRequest) ([]Record, error) {
	if len(req.RecordIDs) > 0 {
		return e.Replayer.Records.Records(ctx, RecordFilter{IDs: req.RecordIDs})
	}
	recs, err := e.sinceLastSpot(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(recs, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	return recs[:min(limit, len(recs))], nil
}

func (e Evaluator) sinceLastSpot(ctx context.Context) ([]Record, error) {
	pool, err := e.replayable(ctx)
	if err != nil {
		return nil, err
	}
	last, err := e.Reports.LastSpotCheck(ctx)
	if err != nil || last == nil {
		return pool, err
	}
	var out []Record
	for _, r := range pool {
		if r.CreatedAt.After(last.CreatedAt) {
			out = append(out, r)
		}
	}
	return out, nil
}

// RecordsSinceLastSpot counts replayable records added since the last spot check (FR-017).
func (e Evaluator) RecordsSinceLastSpot(ctx context.Context) (int, error) {
	recs, err := e.sinceLastSpot(ctx)
	return len(recs), err
}

// View rebuilds a stored report's view from its runs.
func (e Evaluator) View(ctx context.Context, id string) (ReportView, error) {
	rep, err := e.Reports.Report(ctx, id)
	if err != nil {
		return ReportView{}, err
	}
	sample, err := e.Replayer.Records.Records(ctx, RecordFilter{IDs: rep.Sample})
	if err != nil {
		return ReportView{}, err
	}
	byID := map[string]Record{}
	for _, r := range sample {
		byID[r.ID] = r
	}
	var rows []RunRow
	for _, runID := range rep.RunIDs {
		run, err := e.Replayer.Runs.Run(ctx, runID)
		if err != nil {
			return ReportView{}, err
		}
		g, err := e.Replayer.Runs.RunGrade(ctx, runID)
		if err != nil {
			return ReportView{}, err
		}
		rows = append(rows, RunRow{Record: byID[run.Key.RecordID], Run: run, Grade: g})
	}
	since, err := e.RecordsSinceLastSpot(ctx)
	if err != nil {
		return ReportView{}, err
	}
	return BuildView(rep, rows, sample, since), nil
}

// Key computes the cache key a replay request would use.
func (p Replayer) Key(ctx context.Context, req ReplayRequest) (RunKey, error) {
	name := req.ArmName
	if name == "" {
		name = req.Arm.Name()
	}
	version, err := req.Arm.Version(ctx)
	if err != nil {
		return RunKey{}, fmt.Errorf("version of arm %s: %w", name, err)
	}
	armVersion := version + "/" + promptHash(p.PromptTemplate)
	if req.NoCacheNonce != "" {
		armVersion += "/nocache-" + req.NoCacheNonce
	}
	return RunKey{RecordID: req.Record.ID, Arm: name, ArmVersion: armVersion, Rep: req.Rep, Base: req.Record.BaseCommit}, nil
}

// Cached reports whether a finished run already answers req.
func (p Replayer) Cached(ctx context.Context, req ReplayRequest) (bool, error) {
	key, err := p.Key(ctx, req)
	if err != nil {
		return false, err
	}
	run, err := p.Runs.FindRun(ctx, key)
	if err != nil || run == nil {
		return false, err
	}
	return run.Status != StatusRunning && run.Status != StatusInfraError, nil
}

func recordIDs(rs []Record) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func runIDs(rows []RunRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Run.ID)
	}
	return out
}
