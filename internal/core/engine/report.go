package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

type TaskReport struct {
	TaskID       string          `json:"task_id"`
	Status       task.TaskStatus `json:"status"`
	RepoURL      string          `json:"repo_url"`
	BaseCommit   string          `json:"base_commit"`
	RequestText  string          `json:"request_text"`
	Attempts     int             `json:"attempts"`
	LadderRung   int             `json:"ladder_rung"`
	TokensTotal  int             `json:"tokens_total"`
	WallTotal    time.Duration   `json:"wall_total"`
	SpecSummary  string          `json:"spec_summary"`
	ReviewStatus string          `json:"review_status"`
	Findings     []ReviewFinding `json:"findings,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

func GenerateMarkdownReport(r TaskReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task Report %s\n\n", r.TaskID)
	fmt.Fprintf(&b, "- **Status**: %s\n", r.Status)
	fmt.Fprintf(&b, "- **Repository**: %s\n", r.RepoURL)
	fmt.Fprintf(&b, "- **Base Commit**: %s\n", r.BaseCommit)
	fmt.Fprintf(&b, "- **Attempts**: %d (Ladder Rung %d)\n", r.Attempts, r.LadderRung)
	fmt.Fprintf(&b, "- **Total Tokens**: %d\n", r.TokensTotal)
	fmt.Fprintf(&b, "- **Wall Duration**: %s\n", r.WallTotal.Round(time.Millisecond))
	fmt.Fprintf(&b, "- **Created**: %s\n\n", r.CreatedAt.Format(time.RFC3339))

	fmt.Fprintf(&b, "## Request\n\n%s\n\n", strings.TrimSpace(r.RequestText))
	fmt.Fprintf(&b, "## Specification Summary\n\n%s\n\n", strings.TrimSpace(r.SpecSummary))
	fmt.Fprintf(&b, "## Review\n\n- **Status**: %s\n", r.ReviewStatus)
	if len(r.Findings) > 0 {
		fmt.Fprintf(&b, "\n### Findings\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "- [%s] %s:%d - %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Message)
			if f.Evidence != "" {
				fmt.Fprintf(&b, "  - Evidence: %s\n", f.Evidence)
			}
			if f.Repro != "" {
				fmt.Fprintf(&b, "  - Repro: %s\n", f.Repro)
			}
		}
	}
	return b.String()
}

func GenerateJSONReport(r TaskReport) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
