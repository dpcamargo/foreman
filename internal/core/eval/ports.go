package eval

import (
	"context"
	"errors"
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
	// Export writes the diff from base to the workspace's current tree (untracked files
	// included; overlay, .fm/ and .git/ excluded) using only the trusted git dir.
	Export(ctx context.Context, ws Workspace, dst string) error
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
