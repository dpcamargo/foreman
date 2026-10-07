package eval

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"time"
)

// ErrBaseNotFound means a record's base commit can't be fetched from its repository.
var ErrBaseNotFound = errors.New("base commit not found")

// Ports: the interfaces the eval use cases need, declared where they are used
// (constitution X; contracts/ports.md). Each has one current adapter.

// Validation is the outcome of checking whether a record can be replayed.
type Validation struct {
	Replayable          bool
	Reason              string // why not, when Replayable is false
	BaseFullSuitePassed *bool  // set when the record has a full test command
}

// RecordStore persists records. Adapter: internal/adapters/sqlite. Every method that
// changes state writes its events row in the same transaction (constitution VII).
type RecordStore interface {
	// AddRecord assigns an id, stores the record and copies its files.
	AddRecord(ctx context.Context, r Record, f RecordFiles) (Record, error)
	Record(ctx context.Context, id string) (Record, error)
	Records(ctx context.Context, f RecordFilter) ([]Record, error)
	// RecordFiles returns a record's stored blobs (read-only views).
	RecordFiles(ctx context.Context, id string) (RecordFiles, error)
	// SetReplayable records a validation result. Nothing else on a record ever changes.
	SetReplayable(ctx context.Context, id string, v Validation) error
}

// Workspace is a fresh, disposable clone pinned to a base commit.
type Workspace struct {
	ID            string
	Dir           string // the clone the arm works in
	TrustedGitDir string // fm-owned copy of the clone's .git, outside Dir (research R6)
	RepoURL       string
	Base          string
	Overlay       []string // files copied in on top of the base (visible tests)
}

// Workspaces makes fresh clones. Adapter: internal/adapters/sandbox.
type Workspaces interface {
	// Prepare clones repoURL at base (hooks disabled, no linked worktrees), captures the
	// trusted git dir, then applies overlay. It returns ErrBaseNotFound when base can't
	// be fetched.
	Prepare(ctx context.Context, id, repoURL, base string, overlay fs.FS) (Workspace, error)
	// Export returns the diff from base to the workspace's current tree (untracked files
	// included; overlay, .fm/ and .git/ excluded), computed with the trusted git dir only.
	Export(ctx context.Context, ws Workspace) ([]byte, error)
	// Destroy removes the workspace and its trusted git dir. It is idempotent.
	Destroy(ctx context.Context, ws Workspace) error
}

// TestResult is one test's outcome: Result is "pass", "fail" or "skip".
type TestResult struct {
	Package string
	Test    string
	Result  string
}

// GradeInput is what the grader needs to grade one patch (or the unpatched base).
type GradeInput struct {
	RepoURL     string
	Base        string
	Patch       []byte // nil grades the unpatched base
	Visible     fs.FS  // copied in before the patch is applied
	Hidden      fs.FS  // mounted read-only; copied over the tree only inside the container
	TestCommand []string
	TestFormat  TestFormat
	FullTest    []string // optional; run without the hidden tests
	Timeout     time.Duration
}

// GradeOutput is a grading result. A non-nil error from Grade means an infrastructure
// failure, and the output then carries no grade.
type GradeOutput struct {
	HiddenPassed    int
	HiddenFailed    int
	PerTest         []TestResult
	FullSuitePassed *bool  // nil when there is no full test command
	ApplyError      string // the patch did not apply: an attempt failure, not infra
	Log             string // redacted, truncated
}

// Grader grades a patch against hidden tests in a clean, offline container. Adapter:
// internal/adapters/container. Feature 002's verifier wraps it (spec FR-006).
type Grader interface {
	Grade(ctx context.Context, in GradeInput) (GradeOutput, error)
}

// RunStore persists replay runs and their grades. Adapter: internal/adapters/sqlite.
// Every status change writes its events row in the same transaction.
type RunStore interface {
	// StartRun inserts a running row for key, or returns the finished run for key with
	// reused=true. An infra_error row is reset to running (a retry). A running row owned
	// by a live process returns ErrRunInProgress.
	StartRun(ctx context.Context, key RunKey, pid PIDFingerprint) (run ReplayRun, reused bool, err error)
	// FinishRun writes the final status, usage and grade (nil for infra errors) in one
	// transaction.
	FinishRun(ctx context.Context, run ReplayRun, g *Grade) error
	// SweepLostRuns marks running rows whose process is gone as infra_error ("lost")
	// and returns their ids, so the caller can remove their workspaces.
	SweepLostRuns(ctx context.Context, alive func(PIDFingerprint) bool) ([]string, error)
	RunGrade(ctx context.Context, runID string) (*Grade, error)
}

// Artifacts owns run files. Adapter: internal/adapters/artifacts. Files an arm reads
// or writes live under work/runs/<id>/io and are moved into state/ only after the run,
// so no state/ path ever reaches an arm (research R2).
type Artifacts interface {
	IODir(ctx context.Context, runID string) (string, error)
	// Transcript returns a redacting writer that ends in state/runs/<id>/transcript.jsonl.
	Transcript(ctx context.Context, runID string) (io.WriteCloser, error)
	SavePatch(ctx context.Context, runID string, patch []byte) error
	// Collect moves io/* into state/runs/<id>/ (redacted) and removes io/. Idempotent.
	Collect(ctx context.Context, runID string) error
}

// AttemptInput is what an arm gets for one attempt.
type AttemptInput struct {
	Workspace  Workspace
	Prompt     string
	WallCap    time.Duration
	TokenCap   int64     // billable tokens (research R8)
	RunDir     string    // from Artifacts.IODir; the adapter writes prompt.md here
	Transcript io.Writer // the CLI's event stream goes here
}

// AttemptResult is an arm's report. Status is StatusSucceeded (the attempt finished
// within its caps), StatusTimedOut, StatusBudgetExceeded or StatusInfraError.
type AttemptResult struct {
	Status   RunStatus
	Claimed  Claim
	Question string
	Usage    Usage
	Error    string // set with StatusInfraError
}

// Arm attempts tasks. Adapters: internal/adapters/runner/codex (a1),
// internal/adapters/runner/hermes (a0, a2); Feature 002 adds h1.
type Arm interface {
	Name() string
	// Version identifies the CLI and model; the prompt hash is appended by the caller.
	Version(ctx context.Context) (string, error)
	Attempt(ctx context.Context, in AttemptInput) (AttemptResult, error)
}
