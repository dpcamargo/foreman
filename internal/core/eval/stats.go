package eval

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"
)

// RunRow is one run with what's needed to score it.
type RunRow struct {
	Record Record
	Run    ReplayRun
	Grade  *Grade
	Cached bool // answered by the baseline cache in this command
}

// ArmMetrics is one arm's rollup (contracts/report.md, "Metrics").
type ArmMetrics struct {
	Arm                   string   `json:"arm"`
	Graded                int      `json:"graded_runs"`
	Successes             int      `json:"successes"`
	InfraErrors           int      `json:"infra_errors"`
	Contaminated          int      `json:"contaminated_runs"`
	SuccessRate           float64  `json:"success_rate"`
	PassK                 *float64 `json:"pass_k"`
	FalseSuccessRate      float64  `json:"false_success_rate"`
	RegressionRate        *float64 `json:"regression_rate"`
	ClarificationAccuracy *float64 `json:"clarification_accuracy"`
	TokensPerSuccess      *float64 `json:"tokens_per_verified_success"`
	USDPerSuccess         *float64 `json:"usd_per_verified_success"`
	MedianWallS           float64  `json:"median_wall_s"`
	TotalTokens           int64    `json:"total_tokens"`
	Rescues               string   `json:"rescues"` // live records only; replays show "n/a" (research R10)
}

func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	v := float64(n) / float64(d)
	return &v
}

// ComputeMetrics rolls up arm's graded runs; infra errors are excluded from every
// denominator.
func ComputeMetrics(arm string, rows []RunRow, k int) ArmMetrics {
	m := ArmMetrics{Arm: arm, Rescues: "n/a"}
	var falseSucc, regressDen, regressed, clarDen, clarOK int
	var usd float64
	haveUSD := false
	var walls []float64
	perRecord := map[string][2]int{} // record -> (graded, successes)
	for _, r := range rows {
		if r.Run.Key.Arm != arm {
			continue
		}
		if r.Run.Status == StatusInfraError {
			m.InfraErrors++
			continue
		}
		if !r.Run.Status.Graded() {
			continue
		}
		m.Graded++
		if r.Run.Status == StatusContaminated {
			m.Contaminated++
		}
		success := r.Grade != nil && r.Grade.Success
		if success {
			m.Successes++
		}
		if r.Grade != nil && r.Grade.FalseSuccess {
			falseSucc++
		}
		if r.Grade != nil && r.Grade.FullSuiteRegressed != nil {
			regressDen++
			if *r.Grade.FullSuiteRegressed {
				regressed++
			}
		}
		if r.Record.ExpectsClarification {
			clarDen++
			if r.Run.Claimed == ClaimClarification {
				clarOK++
			}
		}
		m.TotalTokens += r.Run.Usage.Billable()
		if r.Run.Usage.USD != nil {
			usd += *r.Run.Usage.USD
			haveUSD = true
		}
		walls = append(walls, r.Run.Wall.Seconds())
		pr := perRecord[r.Record.ID]
		pr[0]++
		if success {
			pr[1]++
		}
		perRecord[r.Record.ID] = pr
	}
	if m.Graded > 0 {
		m.SuccessRate = float64(m.Successes) / float64(m.Graded)
		m.FalseSuccessRate = float64(falseSucc) / float64(m.Graded)
	}
	if k >= 2 && len(perRecord) > 0 {
		all := 0
		for _, pr := range perRecord {
			if pr[0] == k && pr[1] == k {
				all++
			}
		}
		m.PassK = ratio(all, len(perRecord))
	}
	m.RegressionRate = ratio(regressed, regressDen)
	m.ClarificationAccuracy = ratio(clarOK, clarDen)
	if m.Successes > 0 {
		t := float64(m.TotalTokens) / float64(m.Successes)
		m.TokensPerSuccess = &t
		if haveUSD {
			u := usd / float64(m.Successes)
			m.USDPerSuccess = &u
		}
	}
	if len(walls) > 0 {
		slices.Sort(walls)
		m.MedianWallS = walls[len(walls)/2]
	}
	return m
}

// Comparison is a paired, per-record comparison of arm against vs (contracts/report.md).
type Comparison struct {
	Arm               string     `json:"arm"`
	Vs                string     `json:"vs"`
	Metric            string     `json:"metric"` // "success" or "tokens"
	Estimate          float64    `json:"estimate"`
	CI95              [2]float64 `json:"ci95"`
	Indistinguishable bool       `json:"indistinguishable"`
}

const bootstrapResamples = 10000

// Compare bootstraps the per-record difference in mean success (or tokens per run)
// over the records both arms have graded runs for. An interval containing 0 is
// reported as indistinguishable (research R9).
func Compare(armRows, vsRows []RunRow, arm, vs, metric string, seed int64) Comparison {
	a, b := perRecordMeans(armRows, metric), perRecordMeans(vsRows, metric)
	var ids []string
	for id := range a {
		if _, ok := b[id]; ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	c := Comparison{Arm: arm, Vs: vs, Metric: metric, Indistinguishable: true}
	if len(ids) == 0 {
		return c
	}
	diffs := make([]float64, len(ids))
	for i, id := range ids {
		diffs[i] = a[id] - b[id]
	}
	c.Estimate = mean(diffs)
	r := rand.New(rand.NewPCG(uint64(seed), 0xD1B54A32D192ED03))
	means := make([]float64, bootstrapResamples)
	buf := make([]float64, len(diffs))
	for i := range means {
		for j := range buf {
			buf[j] = diffs[r.IntN(len(diffs))]
		}
		means[i] = mean(buf)
	}
	slices.Sort(means)
	c.CI95 = [2]float64{means[int(0.025*bootstrapResamples)], means[int(0.975*bootstrapResamples)-1]}
	c.Indistinguishable = c.CI95[0] <= 0 && c.CI95[1] >= 0
	return c
}

func perRecordMeans(rows []RunRow, metric string) map[string]float64 {
	sums, counts := map[string]float64{}, map[string]float64{}
	for _, r := range rows {
		if !r.Run.Status.Graded() {
			continue
		}
		v := 0.0
		switch metric {
		case "tokens":
			v = float64(r.Run.Usage.Billable())
		default:
			if r.Grade != nil && r.Grade.Success {
				v = 1
			}
		}
		sums[r.Record.ID] += v
		counts[r.Record.ID]++
	}
	for id := range sums {
		sums[id] /= counts[id]
	}
	return sums
}

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// IsBaseline reports whether an arm instance is a single-agent baseline (a0, a1, a2).
func IsBaseline(arm string) bool {
	base, err := ParseArm(arm)
	return err == nil && base != "h1"
}

// BestBaseline picks the baseline with the highest success rate; ties go to the lower
// cost per verified success (dollars when both report them, else tokens).
func BestBaseline(ms []ArmMetrics) string {
	best := -1
	for i, m := range ms {
		if !IsBaseline(m.Arm) {
			continue
		}
		if best < 0 || m.SuccessRate > ms[best].SuccessRate ||
			(m.SuccessRate == ms[best].SuccessRate && cheaper(m, ms[best])) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return ms[best].Arm
}

func cheaper(a, b ArmMetrics) bool {
	if a.USDPerSuccess != nil && b.USDPerSuccess != nil {
		return *a.USDPerSuccess < *b.USDPerSuccess
	}
	if a.TokensPerSuccess != nil && b.TokensPerSuccess != nil {
		return *a.TokensPerSuccess < *b.TokensPerSuccess
	}
	return a.TokensPerSuccess != nil && b.TokensPerSuccess == nil
}

// ThresholdRow is one ARCHITECTURE.md §16 / Appendix D criterion for a non-baseline arm.
type ThresholdRow struct {
	Arm       string `json:"arm"`
	Criterion string `json:"criterion"`
	Value     string `json:"value"`
	Met       string `json:"met"` // yes | no | indistinguishable | n/a
}

// Thresholds applies the go/no-go criteria to arm x against the best baseline b. The
// decision and its ADR belong to Feature 002 (SC-006); this only reports the numbers.
func Thresholds(x, b ArmMetrics, success Comparison) []ThresholdRow {
	yn := func(ok bool) string {
		if ok {
			return "yes"
		}
		return "no"
	}
	rows := make([]ThresholdRow, 0, 5)
	diff := x.SuccessRate - b.SuccessRate
	met := yn(diff >= 0.10)
	if success.Indistinguishable {
		met = "indistinguishable"
	}
	rows = append(rows, ThresholdRow{x.Arm, "success ≥ baseline + 10 points, interval excluding 0",
		fmt.Sprintf("%+.0f points (95%% CI %+.0f..%+.0f)", diff*100, success.CI95[0]*100, success.CI95[1]*100), met})
	rows = append(rows, ThresholdRow{x.Arm, "false-success rate ≤ ½ × baseline's",
		fmt.Sprintf("%.0f%% vs %.0f%%", x.FalseSuccessRate*100, b.FalseSuccessRate*100), yn(x.FalseSuccessRate <= b.FalseSuccessRate/2)})
	switch {
	case x.USDPerSuccess != nil && b.USDPerSuccess != nil:
		rows = append(rows, ThresholdRow{x.Arm, "cost per verified success ≤ 2 × baseline's",
			fmt.Sprintf("$%.2f vs $%.2f", *x.USDPerSuccess, *b.USDPerSuccess), yn(*x.USDPerSuccess <= 2**b.USDPerSuccess)})
	case x.TokensPerSuccess != nil && b.TokensPerSuccess != nil:
		rows = append(rows, ThresholdRow{x.Arm, "cost per verified success ≤ 2 × baseline's",
			fmt.Sprintf("%.0f vs %.0f tokens", *x.TokensPerSuccess, *b.TokensPerSuccess), yn(*x.TokensPerSuccess <= 2**b.TokensPerSuccess)})
	default:
		rows = append(rows, ThresholdRow{x.Arm, "cost per verified success ≤ 2 × baseline's", "no successes to price", "n/a"})
	}
	if x.RegressionRate != nil && b.RegressionRate != nil {
		rows = append(rows, ThresholdRow{x.Arm, "regression rate ≤ baseline's",
			fmt.Sprintf("%.0f%% vs %.0f%%", *x.RegressionRate*100, *b.RegressionRate*100), yn(*x.RegressionRate <= *b.RegressionRate)})
	} else {
		rows = append(rows, ThresholdRow{x.Arm, "regression rate ≤ baseline's", "no full-suite data", "n/a"})
	}
	rows = append(rows, ThresholdRow{x.Arm, "equal success with ≥ 30% fewer rescues", "not measurable by replay", "n/a"})
	return rows
}

// Spend is what a set of runs consumed; cache hits cost nothing (FR-016).
type Spend struct {
	Tokens    int64         `json:"tokens"`
	USD       *float64      `json:"usd"`
	Wall      time.Duration `json:"-"`
	WallS     float64       `json:"wall_s"`
	Runs      int           `json:"runs"`
	CacheHits int           `json:"cache_hits"`
}

// SpendOf totals the runs that actually ran.
func SpendOf(rows []RunRow) Spend {
	var s Spend
	var u Usage
	for _, r := range rows {
		if r.Cached {
			s.CacheHits++
			continue
		}
		s.Runs++
		u = u.Add(r.Run.Usage)
		s.Wall += r.Run.Wall
	}
	s.Tokens, s.USD, s.WallS = u.Billable(), u.USD, s.Wall.Seconds()
	return s
}
