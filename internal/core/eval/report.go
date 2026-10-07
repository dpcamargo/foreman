package eval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReportMode is the kind of report.
type ReportMode string

const (
	ModeSmoke    ReportMode = "smoke"
	ModeSpot     ReportMode = "spot"
	ModeDecision ReportMode = "decision"
)

// Report is a stored spot check, smoke pass or decision replay (data-model.md).
type Report struct {
	ID              string
	Mode            ReportMode
	Seed            int64
	Arms            []string
	K               int
	Sample          []string // record ids, in draw order
	BudgetTokens    *int64
	BudgetUSD       *float64
	ProjectedTokens *int64
	ProjectedUSD    *float64
	Spent           Spend
	UsageChecked    bool
	ParentID        string
	RunIDs          []string
	CreatedAt       time.Time
}

// ReportView is everything a rendered report shows.
type ReportView struct {
	Report               Report
	Mix                  Mix
	Metrics              []ArmMetrics
	BestBaseline         string
	Comparisons          []Comparison
	Thresholds           []ThresholdRow
	Rows                 []RunRow
	RecordsSinceLastSpot int
}

// BuildView computes metrics, paired comparisons against the best baseline and, for
// non-baseline arms, the go/no-go threshold table (contracts/report.md).
func BuildView(rep Report, rows []RunRow, sample []Record, sinceLastSpot int) ReportView {
	v := ReportView{Report: rep, Mix: SampleMix(sample), Rows: rows, RecordsSinceLastSpot: sinceLastSpot}
	byArm := map[string][]RunRow{}
	for _, r := range rows {
		byArm[r.Run.Key.Arm] = append(byArm[r.Run.Key.Arm], r)
	}
	for _, arm := range rep.Arms {
		v.Metrics = append(v.Metrics, ComputeMetrics(arm, rows, rep.K))
	}
	if rep.Mode == ModeSpot {
		return v
	}
	v.BestBaseline = BestBaseline(v.Metrics)
	if v.BestBaseline == "" {
		return v
	}
	var best ArmMetrics
	for _, m := range v.Metrics {
		if m.Arm == v.BestBaseline {
			best = m
		}
	}
	for _, m := range v.Metrics {
		if m.Arm == v.BestBaseline {
			continue
		}
		success := Compare(byArm[m.Arm], byArm[v.BestBaseline], m.Arm, v.BestBaseline, "success", rep.Seed)
		v.Comparisons = append(v.Comparisons, success,
			Compare(byArm[m.Arm], byArm[v.BestBaseline], m.Arm, v.BestBaseline, "tokens", rep.Seed))
		if !IsBaseline(m.Arm) {
			v.Thresholds = append(v.Thresholds, Thresholds(m, best, success)...)
		}
	}
	return v
}

type jsonRun struct {
	RunID        string    `json:"run_id"`
	RecordID     string    `json:"record_id"`
	Arm          string    `json:"arm"`
	Rep          int       `json:"rep"`
	Status       RunStatus `json:"status"`
	Claimed      Claim     `json:"claimed"`
	Success      bool      `json:"success"`
	FalseSuccess bool      `json:"false_success"`
	HiddenPassed int       `json:"hidden_passed"`
	HiddenFailed int       `json:"hidden_failed"`
	Tokens       int64     `json:"tokens_billable"`
	USD          *float64  `json:"usd"`
	WallS        float64   `json:"wall_s"`
	Cached       bool      `json:"cached"`
	Error        string    `json:"error,omitempty"`
}

// JSON renders the machine-readable report (contracts/report.md, "Report JSON").
func (v ReportView) JSON() ([]byte, error) {
	r := v.Report
	metrics := map[string]ArmMetrics{}
	for _, m := range v.Metrics {
		metrics[m.Arm] = m
	}
	runs := make([]jsonRun, 0, len(v.Rows))
	for _, row := range v.Rows {
		j := jsonRun{RunID: row.Run.ID, RecordID: row.Record.ID, Arm: row.Run.Key.Arm, Rep: row.Run.Key.Rep,
			Status: row.Run.Status, Claimed: row.Run.Claimed, Tokens: row.Run.Usage.Billable(), USD: row.Run.Usage.USD,
			WallS: row.Run.Wall.Seconds(), Cached: row.Cached, Error: row.Run.Error}
		if g := row.Grade; g != nil {
			j.Success, j.FalseSuccess, j.HiddenPassed, j.HiddenFailed = g.Success, g.FalseSuccess, g.HiddenPassed, g.HiddenFailed
		}
		runs = append(runs, j)
	}
	out := map[string]any{
		"id": r.ID, "mode": r.Mode, "parent_id": r.ParentID, "seed": r.Seed, "k": r.K, "arms": r.Arms,
		"created_at": r.CreatedAt,
		"sample": map[string]any{
			"records": r.Sample, "repos": v.Mix.Repos, "categories": v.Mix.Categories,
			"single_repo_majority": v.Mix.SingleRepoMajority,
		},
		"spend": map[string]any{
			"tokens": r.Spent.Tokens, "usd": r.Spent.USD, "wall_s": r.Spent.WallS, "runs": r.Spent.Runs,
			"cache_hits": r.Spent.CacheHits,
			"projected":  map[string]any{"tokens": r.ProjectedTokens, "usd": r.ProjectedUSD},
			"budget":     map[string]any{"tokens": r.BudgetTokens, "usd": r.BudgetUSD},
		},
		"best_baseline": v.BestBaseline, "metrics": metrics,
		"comparisons": nonNil(v.Comparisons), "thresholds": nonNil(v.Thresholds), "runs": runs,
		"records_since_last_spot": v.RecordsSinceLastSpot,
	}
	return json.MarshalIndent(out, "", "  ")
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Markdown renders the human report (contracts/report.md, "Report markdown").
func (v ReportView) Markdown() string {
	r := v.Report
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("# Eval report %s (%s)\n\n", r.ID, r.Mode)
	switch r.Mode {
	case ModeSpot:
		p("**TREND ONLY — not a decision.** A spot check replays recent records once through one arm.\n\n")
	case ModeSmoke:
		p("**SMOKE** pass (k=1) ahead of a decision replay.\n\n")
	}
	p("- created: %s\n- seed: %d\n- arms: %s\n- k: %d\n", r.CreatedAt.UTC().Format(time.RFC3339), r.Seed, strings.Join(r.Arms, ", "), r.K)
	if r.ParentID != "" {
		p("- smoke pass: %s\n", r.ParentID)
	}

	p("\n## Sample\n\n%d records.\n\n| repository | records |\n|---|---|\n", len(r.Sample))
	for _, repo := range sortedKeys(v.Mix.Repos) {
		p("| %s | %d |\n", repo, v.Mix.Repos[repo])
	}
	p("\n| category | records |\n|---|---|\n")
	for _, c := range sortedKeys(v.Mix.Categories) {
		p("| %s | %d |\n", c, v.Mix.Categories[c])
	}
	if v.Mix.SingleRepoMajority {
		p("\n**Warning:** one repository is more than half of the sample; results may only describe that codebase.\n")
	}

	p("\n## Spend\n\n- tokens: %d billable\n- dollars: %s\n- wall: %s over %d run(s); %d cache hit(s)\n",
		r.Spent.Tokens, money(r.Spent.USD), r.Spent.Wall.Round(time.Second), r.Spent.Runs, r.Spent.CacheHits)
	if r.ProjectedTokens != nil {
		p("- projected from the smoke pass: %d tokens", *r.ProjectedTokens)
		if r.BudgetTokens != nil {
			p(" (budget %d)", *r.BudgetTokens)
		}
		p("\n")
	}

	p("\n## Metrics\n\n| arm | graded | success | pass^k | false success | regression | clarification | tokens/success | $/success | median wall | contaminated | infra | rescues |\n")
	p("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, m := range v.Metrics {
		p("| %s | %d | %s | %s | %s | %s | %s | %s | %s | %.0fs | %d | %d | %s |\n", m.Arm, m.Graded,
			pct(&m.SuccessRate), pct(m.PassK), pct(&m.FalseSuccessRate), pct(m.RegressionRate), pct(m.ClarificationAccuracy),
			num(m.TokensPerSuccess), money(m.USDPerSuccess), m.MedianWallS, m.Contaminated, m.InfraErrors, m.Rescues)
	}

	p("\n## Comparisons\n\n")
	if len(v.Comparisons) == 0 {
		p("None (%s).\n", map[bool]string{true: "spot checks don't compare arms", false: "only one arm"}[r.Mode == ModeSpot])
	} else {
		p("Paired per record against the best baseline, **%s**. 95%% bootstrap interval over records.\n\n| arm | vs | metric | estimate | 95%% CI | verdict |\n|---|---|---|---|---|---|\n", v.BestBaseline)
		for _, c := range v.Comparisons {
			verdict := "different"
			if c.Indistinguishable {
				verdict = "indistinguishable"
			}
			p("| %s | %s | %s | %+.3f | %+.3f..%+.3f | %s |\n", c.Arm, c.Vs, c.Metric, c.Estimate, c.CI95[0], c.CI95[1], verdict)
		}
	}
	if len(v.Thresholds) > 0 {
		p("\n## Thresholds\n\nARCHITECTURE.md §16 against **%s**. The go/no-go decision itself is Feature 002's.\n\n| arm | criterion | value | met |\n|---|---|---|---|\n", v.BestBaseline)
		for _, t := range v.Thresholds {
			p("| %s | %s | %s | %s |\n", t.Arm, t.Criterion, t.Value, t.Met)
		}
	}
	if r.Mode == ModeSpot {
		p("\n## Live vs replay\n\n| record | live outcome | replay |\n|---|---|---|\n")
		for _, row := range v.Rows {
			live := "-"
			if row.Record.Live != nil {
				live = string(row.Record.Live.Outcome)
			}
			p("| %s | %s | %s |\n", row.Record.ID, live, row.Run.Status)
		}
	}

	p("\n## Problem runs\n\n")
	problems := 0
	for _, row := range v.Rows {
		if row.Run.Status == StatusContaminated || row.Run.Status == StatusInfraError {
			problems++
			p("- %s %s rep %d on %s: **%s** %s\n", row.Run.ID, row.Run.Key.Arm, row.Run.Key.Rep, row.Record.ID, row.Run.Status, row.Run.Error)
		}
	}
	if problems == 0 {
		p("None.\n")
	}
	p("\n---\n%d replayable records since the last spot check.\n", v.RecordsSinceLastSpot)
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

func num(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f", *v)
}

func money(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("$%.2f", *v)
}
