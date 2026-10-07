# Contract: core ports (`internal/core/eval`)

The interfaces the eval use cases need, declared in the core package that uses them
(constitution X). Each has exactly the adapters listed; none exists without a current user. All
methods take `context.Context` first.

```go
// The store is split into three small interfaces (AGENTS.md rule 2); the sqlite adapter
// implements all of them.

// RecordStore persists records. Adapter: internal/adapters/sqlite.
type RecordStore interface {
    AddRecord(ctx context.Context, r Record, files RecordFiles) (Record, error)
    Record(ctx context.Context, id string) (Record, error)
    Records(ctx context.Context, f RecordFilter) ([]Record, error)
    RecordFiles(ctx context.Context, id string) (RecordFiles, error) // read-only views of the blobs
    // SetReplayable stores a validation verdict (replayable, reason, base full-suite result).
    SetReplayable(ctx context.Context, id string, v Validation) error

}

// RunStore persists replay runs and grades.
type RunStore interface {
    // StartRun returns the existing run when the cache key already has a finished one
    // (reused=true). Otherwise it inserts a running row with the caller's PID fingerprint.
    StartRun(ctx context.Context, key RunKey, pid PIDFingerprint) (run ReplayRun, reused bool, err error)
    FinishRun(ctx context.Context, run ReplayRun, g *Grade) error // one transaction
    // SweepLostRuns returns the ids it marked lost, so the caller can remove their workspaces.
    SweepLostRuns(ctx context.Context, alive func(PIDFingerprint) bool) ([]string, error)
    RunGrade(ctx context.Context, runID string) (*Grade, error)
    FindRun(ctx context.Context, key RunKey) (*ReplayRun, error) // nil when none
    Run(ctx context.Context, id string) (ReplayRun, error)
}

// ReportStore persists reports.
type ReportStore interface {
    SaveReport(ctx context.Context, r Report) (Report, error) // assigns id; links runs; one tx with its event
    Report(ctx context.Context, id string) (Report, error)
    FindReport(ctx context.Context, mode ReportMode, seed int64, arms, sample []string) (*Report, error)
    SetUsageChecked(ctx context.Context, id string) error
    LastSpotCheck(ctx context.Context) (*Report, error)
}

// Workspaces makes fresh clones. Adapter: internal/adapters/sandbox.
type Workspaces interface {
    // Prepare: fresh clone of repo at base, hooks disabled, overlay applied (visible tests).
    // id names work/runs/<id>/; ErrBaseNotFound when base can't be fetched.
    Prepare(ctx context.Context, id, repoURL, base string, overlay fs.FS) (Workspace, error)
    // Export: the diff from base to the workspace's state (including untracked files),
    // with the overlay excluded, written to dst. Computed with the trusted git dir captured
    // at Prepare time, never with the workspace's own .git (research R6).
    Export(ctx context.Context, ws Workspace, dst string) error
    Destroy(ctx context.Context, ws Workspace) error
}

// Artifacts owns run files. Adapter: internal/adapters/artifacts. It keeps every file an arm
// touches out of state/ (research R2) and redacts on the way in (research R5, Redaction).
type Artifacts interface {
    IODir(ctx context.Context, runID string) (string, error)              // creates work/runs/<id>/io
    Transcript(ctx context.Context, runID string) (io.WriteCloser, error) // redacting writer → state/runs/<id>/transcript.jsonl
    PatchPath(runID string) string                                        // state/runs/<id>/patch.diff (written by fm, not the arm)
    Collect(ctx context.Context, runID string) error                      // move io/* → state/runs/<id>/ redacted; remove io/
}

// Grader grades a patch against hidden tests in a clean container. Adapter:
// internal/adapters/container. Feature 002's verifier wraps this adapter (spec FR-006).
type Grader interface {
    Grade(ctx context.Context, in GradeInput) (GradeOutput, error)
}

type GradeInput struct {
    RepoURL, Base string
    Patch         []byte   // nil = grade the unpatched base (validation)
    Visible       fs.FS    // copied in before the patch is applied
    Hidden        fs.FS    // mounted read-only, copied over the tree only inside the container
    TestCommand   []string
    TestFormat    TestFormat
    FullTest      []string // optional; run before the hidden tests are copied in
    Timeout       time.Duration
}

type GradeOutput struct {
    HiddenPassed, HiddenFailed int
    PerTest                    []TestResult
    FullSuitePassed            *bool  // nil without a full test command; core derives regressions
    ApplyError                 string // patch (or its go.mod) unusable: an attempt failure, not infra
    Log                        string // redacted, truncated
}

// Arm attempts one task. Adapters: internal/adapters/runner/codex (a1),
// internal/adapters/runner/hermes (a0 single shot, a2 kanban goal). Feature 002 adds h1.
type Arm interface {
    Name() string
    Version(ctx context.Context) (string, error) // "<cli version>/<model>/<prompt hash>"
    Attempt(ctx context.Context, in AttemptInput) (AttemptResult, error)
}

type AttemptInput struct {
    Workspace Workspace
    Prompt    string // rendered from prompts/arm_baseline.md
    WallCap   time.Duration
    TokenCap  int64  // billable tokens (research R8)
    RunDir    string // from Artifacts.IODir: the adapter writes prompt.md here and the CLI writes last.json; never under state/
    Transcript io.Writer // fm-owned sink (redacting) that ends in state/runs/<id>/transcript.jsonl
}

type AttemptResult struct {
    Claimed    Claim // done | needs_clarification | none
    Question   string
    Usage      Usage
    Status     RunStatus // succeeded (attempt finished) | timed_out | budget_exceeded | infra_error
}
```

## Semantics every adapter must honour

- **Workspaces**:
  - never a linked worktree; clone from the mirror with `--no-hardlinks`;
  - every git call gets `-c core.hooksPath=/dev/null`;
  - `Prepare` copies the fresh `.git` to `work/runs/<id>/trusted.git` before the arm starts;
  - `Export`, and any other host-side git call on an arm-written tree, uses
    `--git-dir=<trusted.git> --work-tree=<ws>` with `GIT_CONFIG_NOSYSTEM=1`,
    `GIT_CONFIG_GLOBAL=/dev/null`, `core.fsmonitor=false`, empty `diff.external`,
    `--no-ext-diff` and `--no-textconv`. A planted `core.fsmonitor`, external diff or filter
    driver in the workspace's `.git/config` MUST NOT execute (test T016);
  - `Prepare` returns an error matching `ErrBaseNotFound` when the base commit can't be fetched;
  - `Destroy` is idempotent and also removes `trusted.git`.
- **Grader**:
  - the test stage runs with `--network=none` and no host mounts except the patched clone,
    the hidden tests (read-only) and the module volume (read-only);
  - no environment variables from the host;
  - a container that fails to start returns an error (an infrastructure failure), never a
    zero-pass `GradeOutput`.
- **Arm**:
  - the adapter alone decides how to confine the CLI (research R1). It MUST NOT pass any path
    under `$FOREMAN_HOME/state` to the CLI or into its environment;
  - it MUST kill the CLI's whole process group on cap or context cancel;
  - a CLI that exits non-zero for provider reasons (rate limit, auth) returns
    `Status=infra_error` with the provider message, not an attempt failure.
- **RecordStore**: every method that changes state writes its `events` row in the same
  transaction (data-model.md, `events`; constitution VII). `FinishRun` writes the run's final
  status, its grade and its event in one transaction.

## Architecture test

`internal/archtest/archtest_test.go` runs `go list -deps -json ./internal/core/...`. It fails
if any core package imports a package under `internal/adapters`, `cmd`, or any non-standard-
library module (constitution X). It runs in `go test ./...`.
