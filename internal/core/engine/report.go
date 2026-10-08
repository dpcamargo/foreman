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
	// Patch/DiffHash are the verified diff, surfaced for the daemon's delivery gate
	// (T041/T043: deliveries.diff_sha256 binding and the /diff attachment). Not written to
	// any report file — reports stay human-readable summaries (GenerateMarkdownReport).
	Patch    []byte `json:"-"`
	DiffHash string `json:"diff_hash,omitempty"`
	// Redact, if non-nil, is applied to every free-text field before a report is rendered
	// to disk (FR-017: no unredacted copy of a secret is persisted). Set by the caller that
	// owns the Redactor (cmd/fm, cmd/foreman); never mutates this TaskReport in memory.
	Redact    func(string) string `json:"-"`
	CreatedAt time.Time           `json:"created_at"`
}

// Redact, if non-nil, is applied to every string field before rendering (FR-017: no
// unredacted copy of a secret reaches disk). Set by the caller (cmd/fm, cmd/foreman) which
// owns the Redactor; nil means no redaction (tests, callers with no secrets in scope).
// Redact never mutates the in-memory TaskReport (engine.Engine still needs the verbatim
// Patch for delivery/merge hashing) — it only affects what Generate*Report renders.
type reportRedactor struct {
	redact func(string) string
}

func (rr reportRedactor) apply(s string) string {
	if rr.redact == nil {
		return s
	}
	return rr.redact(s)
}

// GenerateMarkdownReport renders r as markdown. Strings that could carry a secret
// (RequestText, SpecSummary, Findings) are passed through r.Redact first (FR-017).
func GenerateMarkdownReport(r TaskReport) string {
	rr := reportRedactor{redact: r.Redact}
	var b strings.Builder
	fmt.Fprintf(&b, "# Task Report %s\n\n", r.TaskID)
	fmt.Fprintf(&b, "- **Status**: %s\n", r.Status)
	fmt.Fprintf(&b, "- **Repository**: %s\n", r.RepoURL)
	fmt.Fprintf(&b, "- **Base Commit**: %s\n", r.BaseCommit)
	fmt.Fprintf(&b, "- **Attempts**: %d (Ladder Rung %d)\n", r.Attempts, r.LadderRung)
	fmt.Fprintf(&b, "- **Total Tokens**: %d\n", r.TokensTotal)
	fmt.Fprintf(&b, "- **Wall Duration**: %s\n", r.WallTotal.Round(time.Millisecond))
	fmt.Fprintf(&b, "- **Created**: %s\n\n", r.CreatedAt.Format(time.RFC3339))

	fmt.Fprintf(&b, "## Request\n\n%s\n\n", rr.apply(strings.TrimSpace(r.RequestText)))
	fmt.Fprintf(&b, "## Specification Summary\n\n%s\n\n", rr.apply(strings.TrimSpace(r.SpecSummary)))
	fmt.Fprintf(&b, "## Review\n\n- **Status**: %s\n", r.ReviewStatus)
	if len(r.Findings) > 0 {
		fmt.Fprintf(&b, "\n### Findings\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "- [%s] %s:%d - %s\n", strings.ToUpper(f.Severity), f.File, f.Line, rr.apply(f.Message))
			if f.Evidence != "" {
				fmt.Fprintf(&b, "  - Evidence: %s\n", rr.apply(f.Evidence))
			}
			if f.Repro != "" {
				fmt.Fprintf(&b, "  - Repro: %s\n", rr.apply(f.Repro))
			}
		}
	}
	return b.String()
}

// GenerateJSONReport renders r as JSON. Every free-text string field that could carry a
// secret is passed through r.Redact first (FR-017); the in-memory r is never mutated.
func GenerateJSONReport(r TaskReport) ([]byte, error) {
	rr := reportRedactor{redact: r.Redact}
	redacted := r
	redacted.RequestText = rr.apply(r.RequestText)
	redacted.SpecSummary = rr.apply(r.SpecSummary)
	redacted.Findings = make([]ReviewFinding, len(r.Findings))
	copy(redacted.Findings, r.Findings)
	for i := range redacted.Findings {
		redacted.Findings[i].Message = rr.apply(redacted.Findings[i].Message)
		redacted.Findings[i].Evidence = rr.apply(redacted.Findings[i].Evidence)
		redacted.Findings[i].Repro = rr.apply(redacted.Findings[i].Repro)
	}
	return json.MarshalIndent(redacted, "", "  ")
}
