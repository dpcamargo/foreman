# Contract: core ports (`internal/core/eval`)

The interfaces the eval use cases need, declared in the core package that uses them
(constitution X). Each has exactly the adapters listed; none exists without a current user. All
methods take `context.Context` first.

```go
// RecordStore persists records, runs, grades and reports. Adapter: internal/adapters/sqlite.
type RecordStore interface {
    AddRecord(ctx context.Context, r Record, files RecordFiles) (Record, error)
    Record(ctx context.Context, id string) (Record, error)
    Records(ctx context.Context, f RecordFilter) ([]Record, error)
    SetReplayable(ctx context.Context, id string, ok bool, reason string) error

    // StartRun returns the existing run when the cache key already has a finished one
    // (reused=true). Otherwise it inserts a running row with the caller's PID fingerprint.
    StartRun(ctx context.Context, key RunKey, pid PIDFingerprint) (run ReplayRun, reused bool, err error)
    FinishRun(ctx context.Context, run ReplayRun, g *Grade) error // one transaction
    SweepLostRuns(ctx context.Context, alive func(PIDFingerprint) bool) (int, error)

    SaveReport(ctx context.Context, rep Report) error
    Report(ctx context.Context, id string) (Report, error)
    LastSpotCheck(ctx context.Context) (*Report, error)
}

// Workspaces makes fresh clones. Adapter: internal/adapters/sandbox.
type Workspaces interface {
    // Prepare: fresh clone of repo at base, hooks disabled, overlay applied (visible tests).
    Prepare(ctx context.Context, repoURL, base string, overlay fs.FS) (Workspace, error)
    // Export: the diff from base to the workspace's state (including untracked files),
    // with the overlay excluded, written to dst.
    Export(ctx context.Context, ws Workspace, dst string) error
    Destroy(ctx context.Context, ws Workspace) error
}

// Grader grades a patch against hidden tests in a clean container. Adapter:
// internal/adapters/container. Feature 002's verifier wraps this adapter (spec FR-006).
type Grader interface {
    Grade(ctx context.Context, in GradeInput) (GradeOutput, error)
}

type GradeInput struct {
    RepoURL, Base string
    PatchPath     string   // "" = grade the unpatched base (validation)
    Visible       fs.FS    // copied in before the patch is applied
    Hidden        fs.FS    // mounted read-only, copied over the tree only inside the container
    TestCommand   []string
    TestFormat    TestFormat
    FullTest      []string // optional
    Timeout       time.Duration
}

type GradeOutput struct {
    HiddenPassed, HiddenFailed int
    PerTest                    []TestResult
    FullSuiteRegressed         *bool
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
    RunDir    string // where the adapter writes transcript.jsonl and last.json
}

type AttemptResult struct {
    Claimed    Claim // done | needs_clarification | none
    Question   string
    Usage      Usage
    Status     RunStatus // succeeded (attempt finished) | timed_out | budget_exceeded | infra_error
    Transcript string    // path
}
```

## Semantics every adapter must honour

- **Workspaces**: never a linked worktree. Clone from the mirror with `--no-hardlinks`. Every
  git call gets `-c core.hooksPath=/dev/null`. `Destroy` is idempotent.
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
- **RecordStore**: `FinishRun` writes the run's final status and its grade in one transaction
  (constitution VII).

## Architecture test

`internal/archtest/archtest_test.go` runs `go list -deps -json ./internal/core/...`. It fails
if any core package imports a package under `internal/adapters`, `cmd`, or any non-standard-
library module (constitution X). It runs in `go test ./...`.
