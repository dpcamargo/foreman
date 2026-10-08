# Contract: Core Ports (`internal/core/task` and `internal/core/engine`)

Following Principle X and AGENTS.md rule 2, ports are small interfaces declared in the core package that uses them. All methods take `context.Context` first.

```go
package task

import (
	"context"
	"time"
)

type TaskStore interface {
	CreateTask(ctx context.Context, t Task, nodes []Node) (Task, error)
	Task(ctx context.Context, id string) (Task, error)
	UpdateTaskStatus(ctx context.Context, id string, status TaskStatus, currentNodeID string) error
	Node(ctx context.Context, id string) (Node, error)
	NodesForTask(ctx context.Context, taskID string) ([]Node, error)
	UpdateNodeStatus(ctx context.Context, id string, status NodeStatus) error
	RecordRun(ctx context.Context, r Run) error
	FinishRun(ctx context.Context, runID string, status RunStatus, failureClass string, failureSig string) error
}
```

```go
package engine

import (
	"context"
	"io/fs"
	"time"
)

// Specifier produces a structured TaskSpec from request and base commit context.
type Specifier interface {
	Specify(ctx context.Context, in SpecInput) (TaskSpec, error)
}

type SpecInput struct {
	RequestText string
	RepoURL     string
	BaseCommit  string
	Category    string
}

type TaskSpec struct {
	Summary            string     `json:"summary"`
	AcceptanceCriteria []string   `json:"acceptance_criteria"`
	VerifyCommands     [][]string `json:"verify_commands"`
	CitedFiles         []string   `json:"cited_files"`
}

// Implementer produces a code patch for a node.
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
	Evidence    *FailureEvidence // nil on first attempt or clean rung 2
	TokenBudget int
	WallTimeout time.Duration
}

type ImplementResult struct {
	Status    string
	Patch     []byte
	DiffHash  string
	Tokens    int
	USD       *float64
	Wall      time.Duration
	Claimed   string
	Error     string
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
	Passed          bool
	PolicyPassed    bool
	PolicyViolations []string
	BuildPassed     bool
	VisiblePassed   bool
	HiddenPassed    bool
	FailingTests    []string
	ErrorSummary    string
	FullOutput      string
}

// Reviewer performs cross-family code review.
type Reviewer interface {
	Family() string
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
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Evidence string `json:"evidence"`
	Repro    string `json:"repro"`
}
```
