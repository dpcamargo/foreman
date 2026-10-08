package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestGenerateTaskReports(t *testing.T) {
	report := TaskReport{
		TaskID: "tsk_1", Status: task.StatusSucceeded, RepoURL: "/repo", BaseCommit: "abc",
		RequestText: "Add subtraction", Attempts: 2, LadderRung: 1, TokensTotal: 42,
		WallTotal: 1500 * time.Millisecond, SpecSummary: "Implement Sub", ReviewStatus: "approved",
		Findings:  []ReviewFinding{{Severity: "minor", File: "add.go", Line: 3, Message: "rename"}},
		CreatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}

	markdown := GenerateMarkdownReport(report)
	for _, want := range []string{"# Task Report tsk_1", "Status**: succeeded", "Attempts**: 2", "[MINOR] add.go:3 - rename"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}

	data, err := GenerateJSONReport(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TaskReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TaskID != report.TaskID || decoded.Status != report.Status || decoded.Attempts != report.Attempts {
		t.Fatalf("decoded report = %+v", decoded)
	}
}
