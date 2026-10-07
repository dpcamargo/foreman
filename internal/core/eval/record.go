// Package eval is the core of foreman's evaluation harness: task records, replays of
// those records through single-agent baselines, and the comparison of results
// (specs/001-evaluation-harness). It imports only the standard library and does no I/O;
// everything that touches the outside world sits behind the interfaces in ports.go.
package eval

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

// ErrInvalidRecord marks input that can never form a usable record (CLI exit code 2).
var ErrInvalidRecord = errors.New("invalid record")

// ErrNotFound is returned by stores for an unknown id.
var ErrNotFound = errors.New("not found")

// TaskTypeCode is the only task type with a verifier in Feature 001.
const TaskTypeCode = "code"

// recordFormatVersion is the current task-record format (spec FR-003).
const recordFormatVersion = 1

// defaultWallBudget applies when a record doesn't set one (data-model.md).
const defaultWallBudget = 1800 * time.Second

// Source says where a record came from.
type Source string

const (
	SourceManual Source = "manual" // fm eval record
	SourceFmRun  Source = "fm-run" // written by Feature 002's fm run
)

// Category labels the kind of task (spec FR-001).
type Category string

const (
	CategoryBugFix     Category = "bug_fix"
	CategoryFeature    Category = "feature"
	CategoryRefactor   Category = "refactor"
	CategoryDependency Category = "dependency"
	CategoryResearch   Category = "research"
	CategoryAmbiguous  Category = "ambiguous"
)

var categories = []Category{
	CategoryBugFix, CategoryFeature, CategoryRefactor,
	CategoryDependency, CategoryResearch, CategoryAmbiguous,
}

// TestFormat says how to read the output of a record's test command.
type TestFormat string

const (
	FormatGoTestJSON TestFormat = "go-test-json" // per-test results from `go test -json`
	FormatExitCode   TestFormat = "exit-code"    // pass/fail from the exit status alone
)

// Outcome is how a task ended when it was run live.
type Outcome string

const (
	OutcomeVerifiedSuccess Outcome = "verified_success"
	OutcomeFailure         Outcome = "failure"
	OutcomeBlocked         Outcome = "blocked"
	OutcomeRescued         Outcome = "rescued"
	OutcomeClarification   Outcome = "clarification"
)

var outcomes = []Outcome{
	OutcomeVerifiedSuccess, OutcomeFailure, OutcomeBlocked, OutcomeRescued, OutcomeClarification,
}

// LiveOutcome is what happened when the task was run for real (spec FR-001).
type LiveOutcome struct {
	Outcome        Outcome
	Attempts       int
	TokensBillable int64
	USD            float64
	Wall           time.Duration
	Interventions  int // human rescues; planned approvals are not counted
}

// Record is one task record (data-model.md, table records).
type Record struct {
	ID                   string
	FormatVersion        int
	Source               Source
	TaskType             string
	Category             Category
	Request              string
	RepoURL              string
	BaseCommit           string
	TestCommand          []string // nil only when there are no hidden tests
	TestFormat           TestFormat
	FullTestCommand      []string
	BaseFullSuitePassed  *bool // set by validation when FullTestCommand exists
	WallBudget           time.Duration
	ExpectsClarification bool
	Live                 *LiveOutcome // nil for manual records without one
	Replayable           *bool        // nil = not validated yet
	NotReplayableReason  string
	ValidatedAt          time.Time
	CreatedAt            time.Time
}

// RecordFiles are a record's file blobs. Hidden may be nil or empty for a record
// without acceptance tests.
type RecordFiles struct {
	Visible        fs.FS // copied into the arm's workspace before it starts
	Hidden         fs.FS // never visible to an arm
	ReferencePatch []byte
}

// RecordFilter selects records. Zero values mean "no constraint".
type RecordFilter struct {
	IDs            []string
	ReplayableOnly bool
	PendingOnly    bool // never validated
	RepoURL        string
	CreatedSince   time.Time
}

// ValidateNew checks a new record and its files, and returns the record with defaults
// applied. Every error wraps ErrInvalidRecord.
func ValidateNew(r Record, f RecordFiles) (Record, error) {
	r.BaseCommit = strings.ToLower(strings.TrimSpace(r.BaseCommit))
	if r.TaskType == "" {
		r.TaskType = TaskTypeCode
	}
	if r.WallBudget == 0 {
		r.WallBudget = defaultWallBudget
	}
	r.FormatVersion = recordFormatVersion
	if len(r.TestCommand) > 0 && r.TestFormat == "" {
		r.TestFormat = FormatGoTestJSON
	}

	if err := checkFields(r); err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}
	hidden, err := ListFiles(f.Hidden)
	if err != nil {
		return Record{}, fmt.Errorf("%w: hidden: %w", ErrInvalidRecord, err)
	}
	if _, err := ListFiles(f.Visible); err != nil {
		return Record{}, fmt.Errorf("%w: visible: %w", ErrInvalidRecord, err)
	}
	if len(hidden) > 0 && len(r.TestCommand) == 0 {
		return Record{}, fmt.Errorf("%w: hidden tests need a test_command", ErrInvalidRecord)
	}
	return r, nil
}

func checkFields(r Record) error {
	switch {
	case !isSHA1Hex(r.BaseCommit):
		return fmt.Errorf("base_commit must be 40 hex characters, got %q", r.BaseCommit)
	case strings.TrimSpace(r.Request) == "":
		return errors.New("request must not be empty")
	case strings.TrimSpace(r.RepoURL) == "":
		return errors.New("repo must not be empty")
	case r.Source != SourceManual && r.Source != SourceFmRun:
		return fmt.Errorf("unknown source %q", r.Source)
	case !slices.Contains(categories, r.Category):
		return fmt.Errorf("unknown category %q", r.Category)
	case r.WallBudget < 0:
		return errors.New("wall budget must be positive")
	}
	if err := checkArgv("test_command", r.TestCommand); err != nil {
		return err
	}
	if err := checkArgv("full_test_command", r.FullTestCommand); err != nil {
		return err
	}
	if len(r.TestCommand) > 0 && r.TestFormat != FormatGoTestJSON && r.TestFormat != FormatExitCode {
		return fmt.Errorf("unknown test_format %q", r.TestFormat)
	}
	if r.Live != nil && !slices.Contains(outcomes, r.Live.Outcome) {
		return fmt.Errorf("unknown live outcome %q", r.Live.Outcome)
	}
	return nil
}

func checkArgv(field string, argv []string) error {
	for i, a := range argv {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("%s element %d is empty", field, i)
		}
	}
	return nil
}

func isSHA1Hex(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ListFiles returns the regular files in fsys, sorted, as slash-separated paths. It
// rejects symlinks and any entry whose name would escape the tree. A nil fsys is an
// empty tree.
func ListFiles(fsys fs.FS) ([]string, error) {
	if fsys == nil {
		return nil, nil
	}
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		name := d.Name()
		if !fs.ValidPath(p) || !fs.ValidPath(name) || strings.Contains(name, "/") || path.Base(p) != name {
			return fmt.Errorf("path %q escapes the repository", name)
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; symlinks are not allowed", p)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", p)
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}
