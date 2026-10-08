package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// T048's regression guard: a canary secret in any free-text field must never reach the
// rendered report (FR-017: "redact secrets at write time ... from every transcript, prompt,
// context, verifier log, and report it persists"). This is the same class of check
// Feature 002's T035 ran as a grep — permanent here so a future field addition can't
// silently regress it.
func TestGenerateReportsRedactsCanarySecret(t *testing.T) {
	canary := "CANARY-s3cr3t-value-1234567890"
	redact := func(s string) string {
		return strings.ReplaceAll(s, canary, "[REDACTED]")
	}
	report := TaskReport{
		TaskID: "tsk_1", Status: task.StatusSucceeded, RepoURL: "/repo", BaseCommit: "abc",
		RequestText:  "fix the bug, token " + canary + " leaked",
		SpecSummary:  "scrub " + canary + " from logs",
		ReviewStatus: "changes_requested",
		Findings: []ReviewFinding{{
			Severity: "major", File: "auth.go", Line: 10,
			Message:  "hardcoded secret " + canary,
			Evidence: "line 10: apiKey = \"" + canary + "\"",
			Repro:    "grep " + canary + " auth.go",
		}},
		CreatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		Redact:    redact,
	}

	md := GenerateMarkdownReport(report)
	if strings.Contains(md, canary) {
		t.Errorf("markdown report leaked the canary secret:\n%s", md)
	}
	if !strings.Contains(md, "[REDACTED]") {
		t.Errorf("markdown report has no [REDACTED] marker, redaction never ran:\n%s", md)
	}

	js, err := GenerateJSONReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), canary) {
		t.Errorf("JSON report leaked the canary secret:\n%s", js)
	}
	if !strings.Contains(string(js), "[REDACTED]") {
		t.Errorf("JSON report has no [REDACTED] marker, redaction never ran:\n%s", js)
	}

	// The in-memory report must not be mutated (engine.RunTask's Patch/DiffHash are still
	// needed verbatim for delivery/merge hashing after the report is written).
	if !strings.Contains(report.RequestText, canary) {
		t.Error("GenerateJSONReport mutated TaskReport.RequestText in place")
	}
}

// A nil Redact (tests, callers with no secrets in scope) must not panic or alter output.
func TestGenerateReportsNilRedact(t *testing.T) {
	report := TaskReport{
		TaskID: "tsk_1", RequestText: "Add subtraction", SpecSummary: "Implement Sub",
	}
	md := GenerateMarkdownReport(report)
	if !strings.Contains(md, "Add subtraction") {
		t.Errorf("nil Redact altered markdown output:\n%s", md)
	}
	js, err := GenerateJSONReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "Add subtraction") {
		t.Errorf("nil Redact altered JSON output:\n%s", js)
	}
}

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
