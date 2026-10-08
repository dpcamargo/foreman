package engine

import (
	"context"
	"io/fs"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// Specifier produces a structured TaskSpec from user request and base commit context.
type Specifier interface {
	Specify(ctx context.Context, in SpecInput) (TaskSpec, error)
}

type SpecInput struct {
	RequestText string
	RepoURL     string
	BaseCommit  string
	Category    string
	Workspace   string
}

type TaskSpec struct {
	Summary            string     `json:"summary"`
	AcceptanceCriteria []string   `json:"acceptance_criteria"`
	VerifyCommands     [][]string `json:"verify_commands"`
	CitedFiles         []string   `json:"cited_files"`
}

// FailureEvidence encapsulates test failures to pass back to the implementer on Rung 1.
type FailureEvidence struct {
	FailingTests []string
	ErrorSummary string
	FullOutput   string
}

// Implementer produces a code patch for an attempt.
type Implementer interface {
	Family() string // "openai", "anthropic"
	Route() string  // "codex/default", "hermes/claude-sonnet-5"
	Implement(ctx context.Context, in ImplementInput) (ImplementResult, error)
}

type ImplementInput struct {
	TaskID      string
	NodeID      string
	Attempt     int
	LadderRung  int
	Spec        TaskSpec
	Workspace   string
	Evidence    *FailureEvidence // nil on first attempt or fresh rung 2
	TokenBudget int
	WallTimeout time.Duration
}

type ImplementResult struct {
	Status         task.RunStatus
	Patch          []byte
	DiffHash       string
	TokensInput    int
	TokensOutput   int
	TokensCached   int
	TokensBillable int
	USD            *float64
	Wall           time.Duration
	PID            int
	PIDStartedAt   string
	Claimed        string
	Error          string
}

// Verifier runs diff-policy and container verification.
type Verifier interface {
	Verify(ctx context.Context, in VerifyInput) (VerifyReport, error)
}

type VerifyInput struct {
	TaskID      string
	RepoURL     string
	BaseCommit  string
	Patch       []byte
	Visible     fs.FS
	Hidden      fs.FS
	ProjectYAML []byte
}

type VerifyReport struct {
	Passed           bool
	PolicyPassed     bool
	PolicyViolations []string
	BuildPassed      bool
	VisiblePassed    bool
	HiddenPassed     bool
	FailingTests     []string
	ErrorSummary     string
	FullOutput       string
}

// Reviewer performs cross-family code review.
type Reviewer interface {
	Family() string
	Route() string
	Review(ctx context.Context, in ReviewInput) (ReviewResult, error)
}

type ReviewInput struct {
	TaskID string
	Spec   TaskSpec
	Patch  []byte
}

type ReviewResult struct {
	Approved bool
	Summary  string
	Findings []ReviewFinding
}

type ReviewFinding struct {
	Severity string `json:"severity"` // "critical", "major", "minor", "advisory"
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Evidence string `json:"evidence"`
	Repro    string `json:"repro"`
}

// WorkspaceCloner provides isolated workspace creation for each attempt.
type WorkspaceCloner interface {
	PrepareWorkspace(ctx context.Context, taskID, runID, repoURL, baseCommit string) (string, error)
	DestroyWorkspace(ctx context.Context, dir string) error
}
