package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// writeDaemonReport persists a daemon-dispatched task's report to
// $FOREMAN_HOME/state/reports/rpt_<id>.{md,json}, the same contract
// (specs/002-durable-execution-loop/contracts/ladder.md rung 3, cmd/fm/run.go step 5) the
// `fm run` path satisfies. Without this the daemon's quickstart pass left state/reports/
// empty and T048's redaction scan had nothing to scan — the report-writing path was simply
// never exercised by the daemon at all.
//
// Every free-text field is redacted at write time (FR-017) via eval.NewRedactor over the
// daemon's own environment, the same redactor cmd/fm wires into its report writer.
func writeDaemonReport(_ context.Context, e env, report engine.TaskReport) {
	if report.TaskID == "" {
		return
	}
	redactor := eval.NewRedactor(environMap())
	report.Redact = redactor.Redact

	md := engine.GenerateMarkdownReport(report)
	js, _ := engine.GenerateJSONReport(report)
	rptDir := filepath.Join(e.State, "reports")
	rptID := task.NewID("rpt")
	_ = os.MkdirAll(rptDir, 0o700)
	_ = os.WriteFile(filepath.Join(rptDir, rptID+".md"), []byte(md), 0o600)
	_ = os.WriteFile(filepath.Join(rptDir, rptID+".json"), js, 0o600)
}
