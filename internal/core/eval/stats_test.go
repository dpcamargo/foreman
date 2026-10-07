package eval

import (
	"math"
	"testing"
	"time"
)

// row builds a graded run row for record id.
func row(arm, id string, rep int, success bool, opts ...func(*RunRow)) RunRow {
	st := StatusFailed
	if success {
		st = StatusSucceeded
	}
	r := RunRow{
		Record: Record{ID: id, RepoURL: "/r/" + id},
		Run:    ReplayRun{ID: arm + id + string(rune('0'+rep)), Key: RunKey{RecordID: id, Arm: arm, Rep: rep}, Status: st, Claimed: ClaimDone, Usage: Usage{Input: 100}, Wall: time.Duration(rep) * time.Minute},
		Grade:  &Grade{Success: success, FalseSuccess: !success},
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeMetrics(t *testing.T) {
	yes := true
	no := false
	rows := []RunRow{
		row("a1", "r1", 1, true), row("a1", "r1", 2, true), row("a1", "r1", 3, true), // pass^3
		row("a1", "r2", 1, true), row("a1", "r2", 2, false), row("a1", "r2", 3, true),
		row("a1", "r3", 1, false, func(r *RunRow) { r.Run.Status = StatusInfraError; r.Grade = nil }), // excluded
		row("a1", "r3", 2, false, func(r *RunRow) { r.Run.Status = StatusContaminated }),
		row("a1", "r3", 3, false, func(r *RunRow) {
			r.Record.FullTestCommand, r.Record.BaseFullSuitePassed = []string{"go"}, &yes
			r.Grade.FullSuiteRegressed = &yes
		}),
		row("a1", "r4", 1, true, func(r *RunRow) {
			r.Record.FullTestCommand, r.Record.BaseFullSuitePassed = []string{"go"}, &yes
			r.Grade.FullSuiteRegressed = &no
		}),
	}
	m := ComputeMetrics("a1", rows, 3)
	if m.Graded != 9 || m.Successes != 6 || m.InfraErrors != 1 || m.Contaminated != 1 {
		t.Fatalf("counts = %+v", m)
	}
	if !approx(m.SuccessRate, 6.0/9) || !approx(m.FalseSuccessRate, 3.0/9) {
		t.Errorf("rates = %v %v", m.SuccessRate, m.FalseSuccessRate)
	}
	if m.PassK == nil || !approx(*m.PassK, 1.0/4) {
		t.Errorf("pass^k = %v, want 1/4 (only r1 had all reps succeed)", m.PassK)
	}
	if m.RegressionRate == nil || !approx(*m.RegressionRate, 0.5) {
		t.Errorf("regression = %v, want 0.5", m.RegressionRate)
	}
	if m.TokensPerSuccess == nil || !approx(*m.TokensPerSuccess, 900.0/6) {
		t.Errorf("tokens/success = %v", m.TokensPerSuccess)
	}
	if m.USDPerSuccess != nil {
		t.Errorf("usd/success = %v, want nil when no arm run reported dollars", *m.USDPerSuccess)
	}
	if m.Rescues != "n/a" {
		t.Errorf("rescues = %q, want n/a for replays", m.Rescues)
	}
	if k1 := ComputeMetrics("a1", rows, 1); k1.PassK != nil {
		t.Error("pass^k must be nil at k=1")
	}
}

func TestClarificationAccuracy(t *testing.T) {
	ask := func(r *RunRow) { r.Record.ExpectsClarification = true; r.Run.Claimed = ClaimClarification }
	guess := func(r *RunRow) { r.Record.ExpectsClarification = true }
	m := ComputeMetrics("a1", []RunRow{row("a1", "x", 1, true, ask), row("a1", "x", 2, false, guess)}, 2)
	if m.ClarificationAccuracy == nil || !approx(*m.ClarificationAccuracy, 0.5) {
		t.Errorf("clarification accuracy = %v", m.ClarificationAccuracy)
	}
}

func perRecord(arm string, successes map[string][]bool) []RunRow {
	var rows []RunRow
	for id, reps := range successes {
		for i, s := range reps {
			rows = append(rows, row(arm, id, i+1, s))
		}
	}
	return rows
}

func TestPairedComparison(t *testing.T) {
	same := map[string][]bool{}
	allA, allB := map[string][]bool{}, map[string][]bool{}
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		same[id] = []bool{i%2 == 0, i%3 == 0, true}
		allA[id], allB[id] = []bool{true}, []bool{false}
	}
	c := Compare(perRecord("a1", same), perRecord("a1#2", same), "a1#2", "a1", "success", 7)
	if !c.Indistinguishable || c.Estimate != 0 {
		t.Errorf("identical arms: %+v, want indistinguishable, estimate 0", c)
	}
	d := Compare(perRecord("h1", allA), perRecord("a1", allB), "h1", "a1", "success", 7)
	if d.Indistinguishable || !approx(d.Estimate, 1) || d.CI95[0] <= 0 {
		t.Errorf("10/10 vs 0/10: %+v, want a clear difference", d)
	}
	again := Compare(perRecord("h1", allA), perRecord("a1", allB), "h1", "a1", "success", 7)
	if again != d {
		t.Error("bootstrap is not deterministic for a fixed seed")
	}
}

func TestBestBaselineAndThresholds(t *testing.T) {
	usd := func(v float64) *float64 { return &v }
	ms := []ArmMetrics{
		{Arm: "a0", SuccessRate: 0.6, USDPerSuccess: usd(2)},
		{Arm: "a1", SuccessRate: 0.6, USDPerSuccess: usd(1)}, // tie, cheaper
		{Arm: "a2", SuccessRate: 0.4},
		{Arm: "h1", SuccessRate: 0.9, USDPerSuccess: usd(1.5), FalseSuccessRate: 0.05},
	}
	if b := BestBaseline(ms); b != "a1" {
		t.Errorf("best baseline = %s, want a1 (tie broken on cost)", b)
	}
	ms[1].FalseSuccessRate = 0.2
	cmp := Comparison{Arm: "h1", Vs: "a1", Metric: "success", Estimate: 0.3, CI95: [2]float64{0.1, 0.5}}
	rows := Thresholds(ms[3], ms[1], cmp)
	if len(rows) != 5 {
		t.Fatalf("thresholds = %d rows, want 5", len(rows))
	}
	want := []string{"yes", "yes", "yes", "n/a", "n/a"}
	for i, r := range rows {
		if r.Met != want[i] {
			t.Errorf("row %d (%s) = %s, want %s", i, r.Criterion, r.Met, want[i])
		}
	}
	if rows[4].Value != "not measurable by replay" {
		t.Errorf("rescue row = %+v", rows[4])
	}
	if got := Thresholds(ms[3], ms[1], Comparison{Estimate: 0.3, CI95: [2]float64{-0.1, 0.5}, Indistinguishable: true}); got[0].Met != "indistinguishable" {
		t.Errorf("success row with CI across 0 = %s", got[0].Met)
	}
}
