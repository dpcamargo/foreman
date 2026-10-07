# Implementation Plan: Evaluation Harness (Measure As We Go)

**Branch**: `001-evaluation-harness` (work happens on `main`, per the repo's current practice) |
**Date**: 2026-10-07 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-evaluation-harness/spec.md`

## Summary

`fm eval` keeps a ledger of tasks and replays the replayable ones through the plain
single-agent baselines.

- **Records**: a record holds the request, the repo at its base commit, visible and hidden
  tests, and live outcome metrics. Records come from `fm eval record` now, and from `fm run`
  once Feature 002 exists.
- **Validation**: a record becomes replayable only when a clean, offline container shows that
  its hidden tests fail on the base commit and pass with the reference result.
- **Replays**: a replay gives an arm a fresh clone with the visible tests applied. The arms are
  A0 (Hermes single shot on Claude), A1 (`codex exec`) and A2 (Hermes Kanban `--goal` card).
  The produced patch is graded by the same container.
- **Two modes**: spot checks (k=1, one arm, a trend) and decision replays (seeded sample, smoke
  pass, budget projection, k=3, paired bootstrap comparison). Both write markdown and JSON
  reports.

The design work went into one problem: keeping the arms away from the hidden tests while they
run as the same macOS user. Codex gets a read-allowlist permissions profile; Hermes runs its
tools inside a Docker container that mounts only the workspace; and a leak scan of every
transcript backs both up (research R1, verified on this machine).

The feature ships as five PRs (see Delivery). The first PR adds the code layout from
`AGENTS.md`, creating only the packages this feature needs.

## Technical Context

**Language/Version**: Go 1.27 (`go 1.27` in `go.mod`; installed toolchain go1.27.1
darwin/arm64)

**Primary Dependencies**:
- `modernc.org/sqlite` v1.60.1: pure-Go SQLite with no cgo. It is the only third-party module
  (research R4).
- Everything else is the standard library: `flag`, `database/sql`, `encoding/json`, `os/exec`,
  `log/slog`, `math/rand/v2`, `io/fs`.
- External CLIs driven via `os/exec`: `git`, `docker` (Colima context), `codex` 0.160.0 and
  `hermes` v0.21.5.

**Storage**:
- SQLite in WAL mode at `$FOREMAN_HOME/state/foreman.db` (default `~/.foreman`), with numbered
  SQL migrations in `migrations/`.
- Blobs (test trees, patches, transcripts, reports) live under `$FOREMAN_HOME/state/`.
- Workspaces and the module cache live under `$FOREMAN_HOME/work/` (data-model.md, research R2).

**Testing**:
- `go test ./...` runs table-driven unit tests: core with fakes, the SQLite adapter against a
  real temporary database, plus `internal/archtest`. It needs no network and no containers.
- `-tags container` runs the grader and sandbox adapters against real Docker and git.
- `-tags live` runs the arm contract tests, which call the real CLIs. They are
  operator-triggered (contracts/arms.md).

**Target Platform**: macOS on Apple Silicon (the operator's M1), with Docker via Colima (4 CPU
/ 6 GiB). Linux should also work, but isn't a target.

**Project Type**: a CLI (`fm`), the first piece of the single-binary orchestrator.

**Performance Goals**: none beyond the arms' own time. Overhead per replay (clone + grade,
excluding the arm) should stay under 60 s on a warm mirror and module cache.

**Constraints**:
- Token economy:
  - `decide` requires `--budget-tokens`;
  - baselines are cached;
  - spot checks are k=1;
  - every command prints its own spend.
- Claude arms run only when the operator invokes them; `decide` requires `--usage-checked`
  after the first smoke pass that included a Claude arm.
- The grader test stage runs fully offline.
- Hidden tests are never readable by an arm.
- Records are never stored in a repository an arm can clone.

**Scale/Scope**:
- Tens to low hundreds of records; decision replays of about 15 records × 3 arms × k=3, so
  about 135 runs.
- One replay at a time.
- Roughly 1,500–2,500 lines of Go, including tests.

## Constitution Check

*Gate: must pass before Phase 0 research; re-checked after Phase 1 design. Result: **PASS** before
and after design, with the deviations listed under Complexity Tracking.*

| Principle | How this feature satisfies it | Enforced by / proven by |
|---|---|---|
| I. Deterministic orchestration | `fm eval` is plain Go. The only LLM calls are the arms under measurement. No model decides control flow, sampling or grading | `internal/core/eval` has no LLM port. Sampling and bootstrap are seeded and deterministic (unit tests with fixed seeds) |
| II. Evidence decides "done" | A run succeeds only if the hidden tests pass in the clean container (or, for `expects_clarification` records, the arm asked). The arm's own "done" claim is stored separately and drives only the false-success metric | `success`/`false_success` rules in data-model.md; unit tests in `core/eval/grade_test.go` |
| III. Acceptance criteria first | A record is replayable only if its hidden tests fail on the base commit, and pass with the reference when one exists. Hidden tests never reach the arm | `fm eval validate`; T-codex-deny, T-hermes-confined, T-leak-scan (contracts/arms.md) |
| IV. Single-threaded writes | Each run gets its own fresh clone. Runs are sequential, with no parallel writers anywhere | Sandbox adapter creates one workspace per run ID; no `--parallel` flag exists |
| V. Cross-family review | Not applicable: 001 has no review step. The rubric grader that must be cross-family is deferred to the first judgment-graded task type (research R11) | Records needing a rubric are stored as not replayable |
| VI. Isolation by default | Fresh clones from a bare mirror, no worktrees, hooks disabled. The grader runs `--network=none --read-only --cap-drop=ALL` with explicit mounts only. Arms are confined: Codex has a read allowlist and no network; Hermes runs in Docker with no network. Patches leave as diffs | `contracts/ports.md` adapter semantics; quickstart steps 3–4; container-tagged tests. **Deviation**: arms run as the operator user, not the `foreman` user (Complexity Tracking) |
| VII. Durable & resumable state | All state is in SQLite (WAL). A run's final status and its grade are committed in one transaction. A startup sweep marks runs lost when their PID start-time fingerprint no longer matches. Re-running `decide` resumes through the cache | `RecordStore.FinishRun` and `SweepLostRuns`; quickstart step 7 |
| VIII. Gated deliberation and approval | Not applicable: no deliberation, no approvals, no external side effects. Arms work only in disposable clones | none needed |
| IX. Minimal infrastructure | No new service. One third-party module (pure-Go SQLite); no Docker SDK, YAML, CLI framework or ORM. Vendor CLIs are used through their own sandbox and structured-output flags, with no custom agent loop | `go.mod` has one `require`; research R3–R5, R7 |
| X. Hexagonal, minimal structure | Core is `internal/core/eval` (stdlib only, no I/O). Adapters are `sqlite`, `sandbox`, `container`, `runner/codex` and `runner/hermes`. `cmd/fm` only wires. Ports are declared in core, each with a current implementation | `internal/archtest` fails the build on violations; contracts/ports.md |
| Measure as we go | This feature *is* the mechanism: records, replays, spot checks and decision replays. Baseline arms exist before Feature 002 builds the loop | Spec SC-001…SC-007; quickstart steps 2–6 |
| Token economy | Caps on every arm run; baseline cache; required decision budget; spend printed per command and stored per report | FR-008, FR-015, FR-016; quickstart step 5 |
| Continuous evaluation | Prompt versions are hashed into `arm_version`, so a prompt change is a new cache key and gets re-measured | `Arm.Version` contract |

## Project Structure

### Documentation (this feature)

```text
specs/001-evaluation-harness/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   ├── cli.md           # fm eval commands, flags, exit codes, environment
│   ├── ports.md         # core interfaces and adapter semantics
│   ├── arms.md          # exact A0/A1/A2 invocations, result schema, contract tests
│   └── report.md        # replay algorithms, metrics, comparison, report formats
└── tasks.md             # Phase 2 (/speckit-tasks; not created here)
```

### Source Code (repository root)

Every path below is created by this feature. Nothing else from the `AGENTS.md` target tree is
created yet: no `core/task`, `core/engine`, `core/verify` or `cmd/foreman`.

```text
go.mod                                   # module github.com/dpcamargo/foreman, go 1.27
cmd/fm/
├── main.go                              # top-level dispatch: "eval" only for now
└── eval.go                              # flag parsing + wiring adapters into core use cases
internal/core/eval/
├── record.go       record_test.go       # Record, validation of inputs, replayability rules
├── run.go          run_test.go          # ReplayRun, statuses, RunKey, cache semantics
├── grade.go        grade_test.go        # success / false_success rules, leak scan (pure: takes text)
├── validate.go     validate_test.go     # use case: Validate (replayability rules)
├── replay.go       replay_test.go       # use case: ReplayOnce + prompt rendering
├── decide.go       decide_test.go       # use cases: Spot, Decide (smoke, usage gate, projection)
├── sample.go       sample_test.go       # seeded sampling, repo/category mix, majority flag
├── stats.go        stats_test.go        # metrics, paired bootstrap, thresholds
├── report.go       report_test.go       # Report type, markdown + JSON rendering (pure)
└── ports.go                             # RecordStore, Workspaces, Grader, Arm (contracts/ports.md)
internal/adapters/
├── sqlite/         store.go store_test.go migrate.go
├── sandbox/        git.go git_test.go             # mirrors, fresh clones, overlay, export
├── container/      grader.go grader_test.go gotest.go   # docker CLI, go test -json parsing
├── proc/           proc.go proc_test.go            # PID + start-time fingerprint for lost-run sweep
└── runner/
    ├── codex/      codex.go codex_test.go contract_test.go leak_contract_test.go
    └── hermes/     oneshot.go kanban.go hermes_test.go contract_test.go
internal/archtest/  archtest_test.go             # core imports stdlib + core only
migrations/         0001_eval.sql
prompts/            arm_baseline.md
schemas/            arm_result.json
scripts/            fixtures.sh                  # builds local fixture repos for tests/quickstart
testdata/fixtures/  addrepo/ fizzrepo/ ambiguous/  # request.md, hidden/, visible/, reference.patch, src/
```

**Structure Decision**: a single Go module following the `AGENTS.md` layout, with one core
package because 001 has one cohesive domain. Unit tests sit next to the code, as Go
convention prescribes. Tests that need Docker or real CLIs are separated by build tags, not
directories, so `go test ./...` stays hermetic.

## Delivery

| PR | Contents | User story | Operator action |
|---|---|---|---|
| 1 | `go.mod`, `cmd/fm` skeleton, `core/eval` record and run types and input validation, `adapters/sqlite` + migration 0001, `archtest`, `fm eval record --no-validate` and `fm eval list` | US1 (storage half) | none |
| 2 | `adapters/sandbox`, `adapters/container` grader, `fm eval validate`, fixtures, container-tagged tests (offline, no host mounts, no host env) | US1 complete | pin the grader image digest; run quickstart 2–3 |
| 3 | `Arm` port, `runner/codex` (A1) with the permissions profile, `ReplayOnce` with caps, cache and lost-run sweep, `fm eval replay`, T-codex-deny, T-codex-usage, T-leak-scan | US2 (A1) | run `go test -tags live` for Codex; quickstart 4–5, 7 |
| 4 | `runner/hermes` single shot (A0) and Kanban goal (A2), T-hermes-confined, T-hermes-usage, T-kanban-goal | US2 complete | create the `foreman-arm` profile (quickstart 0; no login, it inherits the root Anthropic credentials); run the Hermes live tests deliberately (Claude tokens) |
| 5 | `sample.go`, `stats.go`, `report.go`; `fm eval spot`, `fm eval decide` (smoke, usage gate, budget projection), `fm eval report` | US3 | quickstart 6; check the Anthropic usage page after the first smoke pass with A0 |

Each PR passes `gofmt -l .` (empty), `go vet ./...` and `go test ./...` before merge. PRs 2–5
also pass their tagged suites, run by the operator.

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Arms run as the operator's macOS user, not the dedicated `foreman` user (constitution VI, ADR 0001) | The `foreman` user is a manual, sudo-requiring prerequisite for Feature 002, and isn't created yet (ROADMAP prerequisites). 001 must be testable before it exists | Waiting for the `foreman` user would block baseline measurement for no gain: the read allowlist (Codex) and container (Hermes) keep arms away from secrets and hidden tests now, and the same adapters run unchanged under the `foreman` user later |
| The grader uses the operator's current Docker context, not a `foreman`-owned Colima profile (constitution Technology Stack, spec 002 FR-019) | Same reason. Grader containers get only explicit mounts, so Colima's home mount isn't exposed to them | `FOREMAN_DOCKER_CONTEXT` makes the switch a configuration change in 002, with no code change |
| Codex's token cap is checked at the end of the run, not enforced mid-run (spec FR-008 as amended) | `codex exec` 0.160.0 reports usage only at `turn.completed` and has no token-limit flag (research R8) | Killing on estimated tokens from transcript length would be guesswork; the wall-clock cap still bounds every run |
| One third-party module (`modernc.org/sqlite`) | Principle VII requires SQLite; the standard library has no driver | `mattn/go-sqlite3` needs cgo (research R4) |
