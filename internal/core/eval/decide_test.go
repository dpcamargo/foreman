package eval

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// --- extra fakes for US3 ---

func (f *fakeRuns) FindRun(_ context.Context, k RunKey) (*ReplayRun, error) {
	if r, ok := f.rows[k]; ok {
		return &r, nil
	}
	return nil, nil
}

func (f *fakeRuns) Run(_ context.Context, id string) (ReplayRun, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return ReplayRun{}, ErrNotFound
}

type fakeReports struct {
	saved []Report
}

func (f *fakeReports) SaveReport(_ context.Context, r Report) (Report, error) {
	r.ID = fmt.Sprintf("rpt_%d", len(f.saved)+1)
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Date(2026, 10, 7, 0, 0, len(f.saved), 0, time.UTC)
	}
	f.saved = append(f.saved, r)
	return r, nil
}

func (f *fakeReports) Report(_ context.Context, id string) (Report, error) {
	for _, r := range f.saved {
		if r.ID == id {
			return r, nil
		}
	}
	return Report{}, ErrNotFound
}

func (f *fakeReports) FindReport(_ context.Context, mode ReportMode, seed int64, arms, sample []string) (*Report, error) {
	for i := range f.saved {
		r := f.saved[i]
		if r.Mode == mode && r.Seed == seed && slices.Equal(r.Arms, arms) && slices.Equal(r.Sample, sample) {
			return &f.saved[i], nil
		}
	}
	return nil, nil
}

func (f *fakeReports) SetUsageChecked(_ context.Context, id string) error {
	for i := range f.saved {
		if f.saved[i].ID == id {
			f.saved[i].UsageChecked = true
		}
	}
	return nil
}

func (f *fakeReports) LastSpotCheck(context.Context) (*Report, error) {
	var last *Report
	for i := range f.saved {
		if f.saved[i].Mode == ModeSpot && (last == nil || f.saved[i].CreatedAt.After(last.CreatedAt)) {
			last = &f.saved[i]
		}
	}
	return last, nil
}

func (f *fakeReports) byMode(m ReportMode) []Report {
	var out []Report
	for _, r := range f.saved {
		if r.Mode == m {
			out = append(out, r)
		}
	}
	return out
}

// evalHarness has n replayable records and one fake arm per name.
func evalHarness(t *testing.T, n int) (*harness, Evaluator, *fakeReports) {
	t.Helper()
	h := newHarness(t)
	ctx := context.Background()
	h.store = newMemStore()
	h.p.Records = h.store
	for i := 0; i < n; i++ {
		r := validRecord()
		r.RepoURL = fmt.Sprintf("/r/repo%d", i%2)
		r.CreatedAt = time.Date(2026, 10, 1, i, 0, 0, 0, time.UTC)
		added, _ := h.store.AddRecord(ctx, r, RecordFiles{Hidden: fstest.MapFS{"sub_test.go": {Data: []byte("h")}}})
		h.store.SetReplayable(ctx, added.ID, Validation{Replayable: true})
	}
	reports := &fakeReports{}
	return h, Evaluator{Replayer: h.p, Reports: reports}, reports
}

func TestDecideHappyPath(t *testing.T) {
	h, e, reports := evalHarness(t, 4)
	res, err := e.Decide(context.Background(), DecideRequest{
		Arms: map[string]Arm{"a1": h.arm, "a1#2": h.arm}, ArmOrder: []string{"a1", "a1#2"},
		N: 3, K: 2, Seed: 7, BudgetTokens: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Smoke: 3 records × 2 arms × rep 1. Decision: 3 × 2 × 2, where rep 1 reuses the smoke runs.
	if got := len(h.arm.calls); got != 12 {
		t.Errorf("arm calls = %d, want 12", got)
	}
	if len(reports.byMode(ModeSmoke)) != 1 || len(reports.byMode(ModeDecision)) != 1 {
		t.Fatalf("reports = %+v", reports.saved)
	}
	dec := reports.byMode(ModeDecision)[0]
	if len(dec.RunIDs) != 12 || dec.K != 2 || len(dec.Sample) != 3 || dec.ParentID != reports.byMode(ModeSmoke)[0].ID {
		t.Errorf("decision report = %+v", dec)
	}
	if res.Decision == nil || res.Projected.Tokens != 6*15 {
		t.Errorf("projection = %+v, want 6 remaining runs × 15 tokens", res.Projected)
	}
	if dec.ProjectedTokens == nil || *dec.ProjectedTokens != 90 {
		t.Errorf("projected tokens not stored: %v", dec.ProjectedTokens)
	}
}

func TestDecideNotEnoughRecords(t *testing.T) {
	h, e, _ := evalHarness(t, 2)
	_, err := e.Decide(context.Background(), DecideRequest{Arms: map[string]Arm{"a1": h.arm}, ArmOrder: []string{"a1"}, N: 3, K: 1, BudgetTokens: 100})
	if !errors.Is(err, ErrNotEnoughRecords) {
		t.Errorf("err = %v", err)
	}
}

func TestDecideSmokeInfraStops(t *testing.T) {
	h, e, reports := evalHarness(t, 4)
	h.arm.result = AttemptResult{Status: StatusInfraError, Error: "rate limited"}
	_, err := e.Decide(context.Background(), DecideRequest{Arms: map[string]Arm{"a1": h.arm}, ArmOrder: []string{"a1"}, N: 3, K: 3, BudgetTokens: 1_000_000})
	if !errors.Is(err, ErrSmokeNotClean) {
		t.Errorf("err = %v, want ErrSmokeNotClean", err)
	}
	if len(reports.byMode(ModeDecision)) != 0 {
		t.Error("a decision report was written after a dirty smoke pass")
	}
}

func TestDecideUsageGate(t *testing.T) {
	h, e, reports := evalHarness(t, 4)
	h.arm.name = "a0"
	req := DecideRequest{Arms: map[string]Arm{"a0": h.arm}, ArmOrder: []string{"a0"}, N: 3, K: 2, Seed: 3, BudgetTokens: 1_000_000}
	_, err := e.Decide(context.Background(), req)
	if !errors.Is(err, ErrUsageCheckNeeded) {
		t.Fatalf("err = %v, want ErrUsageCheckNeeded", err)
	}
	if len(h.arm.calls) != 3 || len(reports.byMode(ModeDecision)) != 0 {
		t.Fatalf("calls = %d, decision reports = %d", len(h.arm.calls), len(reports.byMode(ModeDecision)))
	}
	req.UsageChecked = true
	if _, err := e.Decide(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(h.arm.calls) != 6 { // only rep 2; the smoke runs are cache hits
		t.Errorf("calls after usage check = %d, want 6", len(h.arm.calls))
	}
	smokes := reports.byMode(ModeSmoke)
	if len(smokes) != 1 || !smokes[0].UsageChecked {
		t.Errorf("smoke report not reused and marked usage-checked: %+v", smokes)
	}
}

func TestDecideOverBudget(t *testing.T) {
	h, e, reports := evalHarness(t, 4)
	_, err := e.Decide(context.Background(), DecideRequest{Arms: map[string]Arm{"a1": h.arm}, ArmOrder: []string{"a1"}, N: 3, K: 3, BudgetTokens: 50})
	if !errors.Is(err, ErrOverBudget) || !strings.Contains(err.Error(), "90") {
		t.Errorf("err = %v, want ErrOverBudget naming the projected 90 tokens", err)
	}
	if len(h.arm.calls) != 3 || len(reports.byMode(ModeDecision)) != 0 {
		t.Errorf("calls = %d; decision runs started despite the budget", len(h.arm.calls))
	}
}

func TestDecideResumesFromCache(t *testing.T) {
	h, e, _ := evalHarness(t, 4)
	req := DecideRequest{Arms: map[string]Arm{"a1": h.arm}, ArmOrder: []string{"a1"}, N: 3, K: 2, Seed: 9, BudgetTokens: 1_000_000}
	if _, err := e.Decide(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before := len(h.arm.calls)
	res, err := e.Decide(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.arm.calls) != before || res.Projected.Tokens != 0 {
		t.Errorf("rerun made %d new calls, projected %d", len(h.arm.calls)-before, res.Projected.Tokens)
	}
}

func TestSpot(t *testing.T) {
	h, e, reports := evalHarness(t, 5)
	reports.saved = append(reports.saved, Report{ID: "rpt_old", Mode: ModeSpot, CreatedAt: time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC)})
	rep, rows, err := e.Spot(context.Background(), SpotRequest{Arm: h.arm, ArmName: "a1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Records were created at hours 0..4; the last spot was at 1:30, so 2, 3 and 4 are new; newest first, limit 2.
	var got []string
	for _, r := range rows {
		got = append(got, r.Record.CreatedAt.Format("15"))
	}
	if strings.Join(got, ",") != "04,03" {
		t.Errorf("spot picked hours %v, want 04,03", got)
	}
	if rep.Mode != ModeSpot || rep.K != 1 || len(rep.RunIDs) != 2 || !slices.Equal(rep.Arms, []string{"a1"}) {
		t.Errorf("spot report = %+v", rep)
	}
	n, err := e.RecordsSinceLastSpot(context.Background())
	if err != nil || n != 0 {
		t.Errorf("records since last spot = %d, %v; want 0 right after a spot check", n, err)
	}
}
