package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleView(mode ReportMode) ReportView {
	rows := []RunRow{
		row("a1", "r1", 1, true), row("a1#2", "r1", 1, true),
		row("a1", "r2", 1, false, func(r *RunRow) { r.Run.Status = StatusContaminated; r.Grade.Contaminated = true }),
		row("a1#2", "r2", 1, false, func(r *RunRow) { r.Run.Status = StatusInfraError; r.Run.Error = "docker down"; r.Grade = nil }),
	}
	tokens := int64(500)
	rep := Report{ID: "rpt_1", Mode: mode, Seed: 7, Arms: []string{"a1", "a1#2"}, K: 1, Sample: []string{"r1", "r2"},
		BudgetTokens: &tokens, CreatedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	return BuildView(rep, rows, []Record{rows[0].Record, rows[2].Record}, 4)
}

func TestReportJSONShape(t *testing.T) {
	b, err := sampleView(ModeDecision).JSON()
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "mode", "seed", "k", "arms", "sample", "spend", "metrics", "comparisons", "thresholds", "runs", "records_since_last_spot"} {
		if _, ok := v[k]; !ok {
			t.Errorf("report JSON lacks %q", k)
		}
	}
	sample := v["sample"].(map[string]any)
	for _, k := range []string{"records", "repos", "categories", "single_repo_majority"} {
		if _, ok := sample[k]; !ok {
			t.Errorf("sample lacks %q", k)
		}
	}
	runs := v["runs"].([]any)
	if len(runs) != 4 {
		t.Errorf("runs = %d, want one per record × arm × rep (paired data, FR-009)", len(runs))
	}
	first := runs[0].(map[string]any)
	for _, k := range []string{"run_id", "record_id", "arm", "rep", "status", "claimed", "success", "false_success", "tokens_billable", "cached"} {
		if _, ok := first[k]; !ok {
			t.Errorf("run entry lacks %q", k)
		}
	}
	if v["records_since_last_spot"].(float64) != 4 {
		t.Error("records_since_last_spot not carried")
	}
}

func TestReportMarkdown(t *testing.T) {
	md := sampleView(ModeDecision).Markdown()
	sections := []string{"# ", "## Sample", "## Spend", "## Metrics", "## Comparisons", "## Problem runs", "replayable records since the last spot check"}
	last := -1
	for _, s := range sections {
		i := strings.Index(md, s)
		if i < 0 || i < last {
			t.Fatalf("section %q missing or out of order\n%s", s, md)
		}
		last = i
	}
	for _, want := range []string{"contaminated", "docker down", "indistinguishable"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "TREND ONLY") || strings.Contains(md, "SMOKE") {
		t.Error("decision report carries a spot/smoke banner")
	}
	if spot := sampleView(ModeSpot).Markdown(); !strings.Contains(spot, "TREND ONLY") {
		t.Error("spot report lacks the TREND ONLY banner")
	}
	if smoke := sampleView(ModeSmoke).Markdown(); !strings.Contains(smoke, "SMOKE") {
		t.Error("smoke report lacks the SMOKE banner")
	}
}
