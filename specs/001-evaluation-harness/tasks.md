---

description: "Task list for Feature 001: Evaluation Harness (Measure As We Go)"

---

# Tasks: Evaluation Harness (Measure As We Go)

**Input**: Design documents from `specs/001-evaluation-harness/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ (cli, ports, arms,
report), quickstart.md

**Tests**: Required. The constitution (Principle III) and `AGENTS.md` ("Write tests first")
make tests mandatory. Within each phase, test tasks come first and MUST fail before the
implementation tasks that follow them.

Test tiers:
- **Plain**: `go test ./...` runs hermetically, with no network and no containers. Git is used
  only on local temp repos.
- **`container`**: `go test -tags container ./...` uses Docker.
- **`live`**: `go test -tags live ./internal/adapters/runner/...` uses the real CLIs. It is
  operator-triggered, because the Hermes tests use Claude subscription tokens.

**Organization**: one phase per user story, in spec order. US2 needs records from US1, and US3
needs replays from US2. These dependencies are real and stated below; each story still ends at
a checkpoint you can test by itself.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2 or US3 (spec.md user stories)
- Paths are relative to the repo root, following the plan.md Project Structure

## Conventions every task follows

- Code rules come from `AGENTS.md`:
  - core imports only the standard library;
  - interfaces are declared in `internal/core/eval/ports.go`;
  - errors are wrapped with `%w`;
  - `ctx` comes first;
  - tests are table-driven.
- "Done" for any task means the commands it names pass on this machine, plus `gofmt -l .`
  (empty output) and `go vet ./...`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: module skeleton, the `fm` entry point, and the architecture guard.

- [X] T001 Create `go.mod` with module `github.com/dpcamargo/foreman` and `go 1.27`, and create `.gitignore` covering `bin/`, `*.test` and `coverage.out`
- [X] T002 Create `cmd/fm/main.go`: top-level dispatch with one subcommand, `eval` (sub-subcommands are added by later tasks); `fm`, `fm help` and unknown commands print usage; exit codes 0/1/2/3 per `specs/001-evaluation-harness/contracts/cli.md`
- [X] T003 [P] Write `internal/archtest/archtest_test.go`. It runs `go list -deps -json ./internal/core/...` and fails if any core package imports `internal/adapters/...`, `cmd/...` or a non-standard-library module. Include a table-driven test of the checking function on synthetic import lists, so the guard is proven to fail on a violation. While `internal/core` is empty, the test is skipped with a clear message (contracts/ports.md, "Architecture test")
- [X] T004 [P] Create `cmd/fm/env.go`. It resolves `FOREMAN_HOME` (default `~/.foreman`), creates `state/` with mode 0700 and `work/` with 0755, and reads `FOREMAN_DOCKER_CONTEXT`, `FOREMAN_HERMES_HOME` and `FOREMAN_GRADER_IMAGE` with their defaults (contracts/cli.md, "Environment"; research R2, R12)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the record type, the database, and the store's record methods. Every story reads
or writes records.

**⚠️ CRITICAL**: no user-story work starts until this phase is complete.

### Tests (write first, must fail)

- [X] T005 [P] Write `internal/core/eval/record_test.go` covering input validation for a new record. Cases:
  - accept a well-formed record;
  - reject a `base_commit` that isn't 40 hex characters;
  - reject a `test_command` that isn't a non-empty argv;
  - reject an unknown `category` or `test_format`;
  - accept an empty hidden file set without a `test_command` (a metrics-only record, spec US1 scenario 3);
  - reject hidden files given without a `test_command`;
  - reject any hidden or visible path that is absolute, contains `..`, or is a symlink (via an `fs.FS` fixture built with `testing/fstest`);
  - default the wall budget to 1800 s (data-model.md, "Validation rules").
- [X] T006 [P] Write `internal/adapters/sqlite/store_test.go` (record methods) against a real temp database:
  - `AddRecord` assigns a `rec_<YYYYMMDDHHMMSS>_<8 hex>` id and copies the files under `state/records/<id>/{visible,hidden}`, plus `reference.patch` when given;
  - `Record`/`Records` round-trip every column;
  - the `RecordFilter` cases work (`replayable` only, by repo, created since);
  - `SetReplayable` updates only `replayable`, `not_replayable_reason` and `validated_at`;
  - `AddRecord` and `SetReplayable` each insert their `events` row in the same transaction. Inject a failing event insert and assert the record change rolled back (constitution VII);
  - WAL mode and `foreign_keys` are on.

### Implementation

- [X] T007 Implement `internal/core/eval/record.go`. Types: `Record`, `Category`, `TestFormat`, `LiveOutcome`, `RecordFiles` (`Visible`, `Hidden fs.FS`; `ReferencePatch []byte`) and `RecordFilter`. Implement `ValidateNew(r Record, f RecordFiles) error` to pass T005. No I/O beyond walking the given `fs.FS`.
- [X] T008 Declare the record half of the `RecordStore` port in `internal/core/eval/ports.go`: `AddRecord`, `Record`, `Records`, `SetReplayable` (contracts/ports.md). The run and report methods are added in US2 and US3.
- [X] T009 Write `migrations/0001_eval.sql` with tables `records`, `replay_runs`, `grades`, `reports`, `report_runs` and `events`, plus indexes and the unique cache-key constraint, exactly as specified in data-model.md.
- [X] T010 Implement `internal/adapters/sqlite/migrate.go`. Embed `migrations/*.sql` with `embed.FS`, apply each pending file in its own transaction, and track them in `schema_migrations`. Add `modernc.org/sqlite v1.60.1` to `go.mod` (`go get modernc.org/sqlite@v1.60.1`). Open the database with `_pragma=journal_mode(WAL)`, `_pragma=foreign_keys(1)` and `_pragma=busy_timeout(5000)`.
- [X] T011 Implement the record methods in `internal/adapters/sqlite/store.go` so T006 passes: id generation with `crypto/rand`, blob copying that refuses symlinks, RFC 3339 UTC timestamps, and an `events` insert inside the same transaction as each change (one unexported `withTx` helper).

**Checkpoint**: `go test ./...` passes (T003, T005, T006). `archtest` now checks real core packages.

---

## Phase 3: User Story 1 - Every task leaves a record (Priority: P1) 🎯 MVP

**Goal**: `fm eval record`, `fm eval validate` and `fm eval list`. A record becomes replayable
only after the clean, offline grader shows that its hidden tests fail on the base commit and
pass with the reference patch.

**Independent Test**: quickstart step 2. Record the `addrepo` fixture and see it come out
`replayable`; `hidden-passes-on-base` comes out "not replayable: hidden tests pass on base";
`hidden-escape` is rejected with exit 2. Quickstart step 3: the grader's container tests pass.
No foreman orchestrator code is involved.

### Fixtures

- [X] T012 [P] [US1] Create `testdata/fixtures/addrepo/`:
  - `src/`: a minimal Go module at its base state, with no `Sub` function;
  - `request.md`: "Add `Sub(a, b int) int`";
  - `hidden/sub_test.go`: fails on base;
  - no `visible/` directory (git can't track an empty one, and `--visible` is optional);
  - `reference.patch`: adds `Sub`;
  - `hidden-passes-on-base/add_hidden_test.go`: tests the existing `Add` (not `add_test.go`, which would overwrite the repo's own test when hidden tests are copied in);
  - `hidden-escape/outside_link`: a symlink to `../../outside_test.go` (no `.go` suffix, so `gofmt -l .` doesn't trip on the dangling link).
- [X] T013 [P] [US1] Create `testdata/fixtures/fizzrepo/`, with the same layout as addrepo and a FizzBuzz feature, and `testdata/fixtures/ambiguous/`. The ambiguous request is deliberately contradictory ("make `Add` return the product, but keep its existing tests passing"); its `hidden/` test fails on base, and it is recorded with `--expects-clarification` later.
- [X] T014 [US1] Write `scripts/fixtures.sh`. For each fixture it builds a local git repo from `src/` (one commit), writes `${FOREMAN_HOME:-$HOME/.foreman}/work/fixtures/<name>.git` (bare) and writes `<name>.base` containing that commit's SHA. It must be idempotent, use `set -euo pipefail`, and print what it created.

### Tests (write first, must fail)

- [X] T015 [P] [US1] Write `internal/adapters/container/gotest_test.go` (plain). It parses recorded `go test -json` streams from `testdata/gotest/*.jsonl` (pass, fail, build failure, panic) into per-test results and pass/fail counts. A build failure gives zero passed and at least one failed, never an empty "all passed".
- [X] T016 [P] [US1] Write `internal/adapters/sandbox/git_test.go` (plain; local temp repos only):
  - `Prepare` makes a fresh clone at the base commit, from a bare mirror, with `--no-hardlinks`;
  - `.git` is a real directory, not a gitfile (no worktree);
  - hooks don't run: a `post-checkout` hook planted in the mirror writes a sentinel that must not appear;
  - the overlay files are applied;
  - `Prepare` creates `work/runs/<id>/trusted.git` outside the workspace;
  - `Export` writes a diff that includes untracked files and excludes the overlay, `.fm/` and the workspace's `.git/`;
  - **planted config does not run**: after `Prepare`, write into the *workspace's* `.git/config` a `core.fsmonitor` command, a `diff.external` command, and a `filter.x.clean` driver (with a matching `.gitattributes`), each of which would create a sentinel file. Run `Export` and assert no sentinel exists and the diff is still correct (research R6, constitution VI);
  - `Destroy` is idempotent and removes `trusted.git`;
  - an unknown base commit returns an error that matches `ErrBaseNotFound`.
- [X] T017 [P] [US1] Write `internal/adapters/container/grader_test.go` (build tag `container`):
  - `TestTestStageHasNoNetwork`: a hidden test dials `1.1.1.1:443` and must get a network error;
  - `TestNoHostMounts`: `/Users` is absent;
  - `TestNoHostEnv`: the host `HOME` and `PATH` values are not visible;
  - `TestHiddenReadOnly`: a write to `/hidden` fails;
  - `TestFailOnBase`: addrepo's base gives `HiddenFailed > 0`;
  - `TestPassWithReference`: base plus `reference.patch` gives `HiddenFailed == 0` and `HiddenPassed > 0`;
  - `TestBadImageIsInfraError`: a nonexistent image returns an error, never a zero-pass `GradeOutput`;
  - `TestFullSuite`: with a `full_test_command`, base gives pass, and a patch that breaks an unrelated existing test gives full-suite fail;
  - `TestFetchEnv`: the dependency-fetch container gets exactly `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `GOFLAGS=-mod=readonly`, and only the clone mounted read-only.
- [X] T018 [P] [US1] Write the validation use case tests in `internal/core/eval/validate_test.go` (plain), with fake `Workspaces`, `Grader` and `RecordStore`. They cover the replayability rules 0–4 in order (no acceptance tests, no verifier for task type, base not fetchable, passes on base, reference fails), each failure reason string, storing `base_full_suite_passed` when a `full_test_command` exists, and that `--all` revalidates while `--pending` skips validated records (data-model.md, "Validation rules").
- [X] T019 [P] [US1] Write `internal/core/eval/redact_test.go`. It covers:
  - the values of given env vars whose names contain `TOKEN`, `KEY`, `SECRET` or `PASSWORD` replaced by `[REDACTED]`;
  - each token pattern from research R5 (`gh[opusr]_…`, `github_pat_…`, `sk-…`, `xox[abpr]-…`, `AKIA…`, PEM private-key blocks) replaced;
  - ordinary text, and short values under 8 characters, left untouched;
  - truncation to a byte limit on a UTF-8 boundary.

### Implementation

- [X] T020 [US1] Declare the `Workspaces` and `Grader` ports and their types (`Workspace`, `GradeInput`, `GradeOutput`, `TestResult`, `ErrBaseNotFound`) in `internal/core/eval/ports.go`, per contracts/ports.md.
- [X] T021 [US1] Implement `internal/adapters/sandbox/git.go` to pass T016:
  - keep mirrors in `work/mirrors/<sha256(url)[:16]>.git`, updated with `git fetch --prune`;
  - pass `-c core.hooksPath=/dev/null` on every git call;
  - clone with `git clone --no-hardlinks --no-checkout`, then checkout the base;
  - copy the `.git` directory to `work/runs/<id>/trusted.git` right after checkout, then copy the overlay in;
  - export through the trusted git dir only: `GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git --git-dir=<trusted.git> --work-tree=<ws> -c core.hooksPath=/dev/null -c core.fsmonitor=false -c diff.external= -c core.attributesFile=/dev/null add -A`, then `diff --cached --binary --no-ext-diff --no-textconv <base>`, excluding overlay paths, `.fm/` and `.git/` (research R6).
- [X] T022 [P] [US1] Implement `internal/adapters/container/gotest.go`, the `go test -json` parser, to pass T015.
- [X] T023 [P] [US1] Implement `internal/core/eval/redact.go` (`NewRedactor(env map[string]string)` with `Redact(string) string` and `Truncate(string, int) string`) to pass T019. It is pure: the environment is passed in by `cmd`.
- [X] T024 [US1] Implement `internal/adapters/container/grader.go` to pass T017. Stages per research R5:
  - **prepare**: the sandbox clone, plus the patch applied with hooks disabled;
  - **dependency fetch**: `go mod download` into the named volume `fm-gomod-<sha256(go.sum)[:16]>`, network on, only the clone mounted read-only, with `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org` and `GOFLAGS=-mod=readonly`. Skipped when the volume exists;
  - **test**: `--network=none --read-only --tmpfs /tmp --cap-drop=ALL --security-opt no-new-privileges --pids-limit 512 --memory 2g --cpus 2`; mount the clone read-write at `/src`, the hidden tests read-only at `/hidden`, and the module volume read-only; copy hidden over src, then run the test command;
  - **full suite**: optional, with the same isolation as the test stage; reports pass or fail;
  - pass `--context` from `FOREMAN_DOCKER_CONTEXT`, and no host environment variables;
  - redact and truncate the log to 64 KiB with the core redactor (T023).
- [X] T025 [US1] Pin `FOREMAN_GRADER_IMAGE` in `cmd/fm/env.go` to `golang:1.27.1-bookworm@sha256:<digest>`, choosing the newest build at least 7 days old. Record the digest and its date in a comment, and run T017 against it.
- [X] T026 [US1] Implement the validation use case in `internal/core/eval/validate.go` (`Validate(ctx, ids []string, mode)`) to pass T018. Rule 1 rejects a non-`code` task type with reason `no verifier for task type <t>` (research R11; data-model.md).
- [X] T027 [US1] Implement `fm eval record` in `cmd/fm/eval.go`, with every flag from contracts/cli.md:
  - read the `--hidden` and `--visible` directories with `os.DirFS`;
  - call `ValidateNew`, then `AddRecord`, then `Validate` (unless `--no-validate`);
  - print the id and the replayable state or reason;
  - exit 1 when not replayable, 2 on input errors, 3 on infrastructure errors.
- [X] T028 [US1] Implement `fm eval validate [ids... | --pending | --all]` and `fm eval list [--replayable] [--repo] [--since] [--json]` in `cmd/fm/eval.go`. The list footer's "since last spot check" count is added in T065.
- [X] T029 [US1] Run quickstart steps 1–3 for real and fix whatever fails: `scripts/fixtures.sh`, recording addrepo and the two broken variants, and `go test -tags container ./internal/adapters/container/...`.

**Checkpoint**: US1 is complete. Records exist and are validated by the offline grader. This is
the MVP: measurement can start, even with tasks done by hand outside foreman.

---

## Phase 4: User Story 2 - Replay a recorded task through a baseline arm (Priority: P1)

**Goal**: `fm eval replay`. A replay runs one recorded task through A1 (Codex), A0 (Hermes
single shot) or A2 (Hermes Kanban `--goal`) in a fresh clone, with caps and the baseline
cache. The arms are confined away from the hidden tests, and every run is graded by the US1
grader.

**Independent Test**: quickstart steps 4, 5 and 7.
- T-codex-deny, T-hermes-confined and T-leak-scan pass.
- Replaying addrepo through A1 gives a graded run with tokens and time.
- A second replay is `cached`.
- `--max-tokens 1000 --no-cache` gives `budget_exceeded`.
- `kill -9` mid-run shows up as `infra_error (lost)` after the next command starts.

Depends on US1 (records and the grader).

### Tests: core and store (write first, must fail)

- [X] T030 [P] [US2] Write `internal/core/eval/run_test.go`:
  - `RunKey` equality (record, arm, arm version, rep, base);
  - `Usage.Billable()` = uncached input + output + cache writes;
  - which statuses count as failure (`timed_out`, `budget_exceeded`, `contaminated`, `failed`) and which are excluded from the denominator (`infra_error`);
  - arm-name parsing accepts `a0`, `a1`, `a2`, `h1` and the instance suffix (`a1#2` → base `a1`, instance name `a1#2`) and rejects anything else (contracts/cli.md, "Arm names").
- [X] T031 [P] [US2] Write `internal/core/eval/grade_test.go`, covering the `success` and `false_success` rules as a table over (status, claimed, hidden passed/failed, regression, `expects_clarification`, contaminated), from data-model.md. Leak scan cases:
  - the state-dir path, or any hidden file's base name, in the transcript gives contaminated;
  - an unrelated mention of "test" does not;
  - matching is exact on path and base name, and case-sensitive.
- [X] T032 [P] [US2] Write `internal/core/eval/replay_test.go` for `ReplayOnce`, using fakes for every port:
  - a cached finished run is returned without calling the arm;
  - an `infra_error` row is retried;
  - a `Prepare` error becomes `infra_error`;
  - a `Prepare` error matching `ErrBaseNotFound` also calls `SetReplayable(id, false, "base commit no longer fetchable")` (spec FR-004);
  - the arm gets `RunDir` from `Artifacts.IODir`, and `Collect` is called after `Export` and before grading, even when the arm timed out;
  - arm `timed_out` or `budget_exceeded` is recorded as a failure, and the patch is still exported and graded for evidence;
  - a grader error becomes `infra_error`;
  - a leak-scan hit becomes `contaminated`;
  - `FinishRun` is called exactly once, with the run and its grade;
  - `Destroy` is always called;
  - `--no-cache` creates a new row whose `arm_version` carries a `nocache-<id>` suffix, so it is never reused (contracts/cli.md).
- [X] T033 [P] [US2] Add the run-method tests to `internal/adapters/sqlite/store_test.go`:
  - `StartRun` inserts a `running` row, or returns the finished row with `reused=true` for the same key;
  - `FinishRun` writes the status, the grade and the `events` row in one transaction (inject a grade or event insert failure and assert the status is unchanged);
  - `StartRun` (new row) and every sweep update insert an `events` row in the same transaction;
  - `SweepLostRuns` marks `running` rows whose fingerprint the `alive` func rejects as `infra_error` with error `lost`, and leaves live ones alone.
- [X] T034 [P] [US2] Write `internal/adapters/artifacts/artifacts_test.go`:
  - `IODir` creates `work/runs/<id>/io`, outside `state/`;
  - a `Transcript` writer redacts as it writes, and the file ends up at `state/runs/<id>/transcript.jsonl`;
  - `Collect` moves `io/*` into `state/runs/<id>/` redacted and removes `io/`;
  - `Collect` is idempotent;
  - nothing is written outside `work/runs/<id>` and `state/runs/<id>`.
- [X] T035 [P] [US2] Write `internal/adapters/proc/proc_test.go`: the current process's fingerprint is alive; a fingerprint with the same PID but a different start time is not alive; a dead PID is not alive.

### Implementation: core, store, proc

- [X] T036 [US2] Implement `internal/core/eval/run.go` (`ReplayRun`, `RunStatus`, `Claim`, `Usage`, `RunKey`, `PIDFingerprint`, arm-name parsing) to pass T030.
- [X] T037 [US2] Implement `internal/core/eval/grade.go` (`Rules(run, grade, record) (success, falseSuccess bool)` and `LeakScan(transcript string, stateDir string, hiddenNames []string) bool`) to pass T031.
- [X] T038 [US2] Add the `Arm` and `Artifacts` ports and their types (`AttemptInput`, `AttemptResult`) to `internal/core/eval/ports.go`, together with the run methods of `RecordStore` (`StartRun`, `FinishRun`, `SweepLostRuns`), per contracts/ports.md.
- [X] T039 [US2] Implement `ReplayOnce` and its prompt rendering in `internal/core/eval/replay.go` to pass T032:
  - the prompt template text is passed in from `cmd`, not read by core;
  - `arm_version` = `Arm.Version()` plus the prompt's sha256[:12];
  - the algorithm follows contracts/report.md, `replayOnce`.
- [X] T040 [US2] Implement the run methods in `internal/adapters/sqlite/store.go` to pass T033.
- [X] T041 [P] [US2] Implement `internal/adapters/proc/proc.go` to pass T035. The PID start time comes from `ps -o lstart= -p <pid>` (standard library only; no `golang.org/x/sys`). The sweep runs once per command, so its cost doesn't matter.
- [X] T042 [P] [US2] Implement `internal/adapters/artifacts/artifacts.go` to pass T034, using the core redactor (T023) injected by `cmd`.
- [X] T043 [P] [US2] Create `schemas/arm_result.json` exactly as in contracts/arms.md.
- [X] T044 [P] [US2] Write `prompts/arm_baseline.md` per contracts/arms.md, "Prompt". It contains the request (verbatim), the visible test list, the instructions to run the visible tests and to ask rather than guess, and the JSON final-answer instruction. It never mentions hidden tests, foreman, or any state path.

### A1: Codex

- [X] T045 [P] [US2] Write `internal/adapters/runner/codex/codex_test.go` (plain), using a fake `codex` executable (a shell script in `testdata/` placed first on `PATH`):
  - the argv and environment match contracts/arms.md A1 exactly, including the inline `permissions.fmarm.filesystem` table with `:minimal`, the workspace, GOROOT and the cache, `-o <io>/last.json`, the prompt read from `<io>/prompt.md`, and no path under `state/` anywhere in argv, environment or stdin;
  - usage is parsed from `testdata/codex/turn_completed.jsonl`;
  - the wall cap kills the whole process group (the fake starts a grandchild `sleep`, which must be dead afterwards);
  - billable tokens over the cap give `budget_exceeded`;
  - a provider error exit gives `infra_error`.
- [X] T046 [US2] Implement `internal/adapters/runner/codex/codex.go` to pass T045:
  - `Version` = `codex --version` plus the configured model;
  - the prompt is sent on stdin;
  - `Setpgid` is set and the group is killed on cap or cancel;
  - the adapter writes `<io>/prompt.md` itself, streams stdout into the `Transcript` writer it was given, has the CLI write `<io>/last.json`, and parses the claim from it.
- [X] T047 [US2] Write `internal/adapters/runner/codex/contract_test.go` (build tag `live`):
  - **T-codex-deny**: plant a canary file under a temp `state/`. The prompt asks for a harmless `ls` of the canary's directory and nothing else. Assert that the canary content is absent from the transcript and that "Operation not permitted" appears in a command result. If the model never ran the command, the test fails as "inconclusive", never passes.
  - **T-codex-usage**: non-zero input and output tokens.
  - Run it, and record the result and date in a comment.
- [X] T048 [US2] Implement `fm eval replay --record --arm --k --max-tokens --no-cache --json` in `cmd/fm/eval.go`:
  - wire sqlite, sandbox, container, proc and codex;
  - run `SweepLostRuns` at startup;
  - print each run's grade and the command's total spend (FR-016).
- [X] T049 [US2] Write `internal/adapters/runner/codex/leak_contract_test.go` (build tag `live`), **T-leak-scan**: replay a fixture with a prompt override that makes the arm `echo` the canary path, then assert the run is graded `contaminated`.
- [X] T050 [US2] Run quickstart steps 4 (Codex part), 5 and 7 for real and fix whatever fails.

### A0 and A2: Hermes

- [X] T051 [P] [US2] Write `internal/adapters/runner/hermes/hermes_test.go` (plain), using a fake `hermes` executable:
  - A0's argv matches contracts/arms.md (`chat --oneshot --format stream-json --query-file … --in <ws> -t file,terminal --max-turns 200 --run-budget <s> --source tool --yolo`) with `HERMES_HOME=<profile>` and no state path anywhere;
  - the session id comes from the stream's final event;
  - usage comes from a recorded `sessions export` line (`testdata/hermes/session.jsonl`);
  - the final message is parsed as `arm_result`, with the fallback rule from research R7;
  - A2 creates the card with every flag (`--workspace dir:<ws> --goal --goal-max-turns 20 --max-runtime --max-retries 1 --json`), dispatches `--max 1`, polls `show --json` until `done` or `blocked`, and treats `blocked` as `claimed=none`;
  - at the wall cap the card is reclaimed and archived, and the run is `timed_out`;
  - **A2 token cap**: when the session usage read on a poll is over the cap, the card is reclaimed and archived, and the run is `budget_exceeded` (research R8);
  - `--query-file` and `--body-file` point into `<io>/`, never `state/`.
- [X] T052 [US2] Implement `internal/adapters/runner/hermes/oneshot.go` (arm `a0`) to pass the A0 cases of T051. It stops the process as soon as streamed usage passes the token cap (research R8).
- [X] T053 [US2] Implement `internal/adapters/runner/hermes/kanban.go` (arm `a2`) to pass the A2 cases of T051. Board `foreman-eval`, polling every 10 s. On each poll, read usage from `kanban runs --json` plus `sessions export` and enforce the token cap.
- [ ] T054 [US2] Write `internal/adapters/runner/hermes/contract_test.go` (build tag `live`):
  - **T-hermes-confined**: inside the arm's container, `ls /Users` and a `cat` of the canary path both fail;
  - **T-hermes-usage**: non-zero tokens and an `estimated_cost_usd`;
  - **T-kanban-goal**: an A2 card on addrepo reaches `done` with a non-empty diff.

  It requires the `foreman-arm` profile from quickstart step 0.
- [X] T055 [US2] Wire `a0` and `a2` into `fm eval replay` in `cmd/fm/eval.go`. Before the first call, verify the profile: `HERMES_HOME=$FOREMAN_HERMES_HOME hermes mcp list` is empty, `terminal.backend` is `docker`, and `docker_network` is false. If not, refuse with exit 3 and say which setting is wrong.
- [ ] T056 [US2] Operator step: create the `foreman-arm` profile (quickstart step 0; it inherits the root Anthropic credentials, so no login is needed). Then run `go test -tags live ./internal/adapters/runner/hermes/...` deliberately, followed by quickstart step 5 with `--arm a0` and `--arm a2`.

**Checkpoint**: US2 is complete. Any replayable record can be replayed through A0, A1 or A2,
confined, capped, cached, and graded.

---

## Phase 5: User Story 3 - Check that foreman still earns its overhead (Priority: P1)

**Goal**:
- `fm eval spot`: k=1, a trend only.
- `fm eval decide`: seeded sample, smoke pass, Claude usage gate, budget projection, k=3,
  paired bootstrap, thresholds.
- `fm eval report`: re-renders a stored report.
- Markdown and JSON reports.

**Independent Test**: quickstart step 6, with the three fixtures recorded.
- The spot report says "TREND ONLY".
- `decide --arms 'a1,a1#2'` reports `indistinguishable` (SC-004), and the sample section lists
  repos and categories with the single-repository warning.
- `decide` with `a0` stops after the smoke pass until `--usage-checked` is given.

Depends on US2 (replays).

### Tests (write first, must fail)

- [ ] T057 [P] [US3] Write `internal/core/eval/sample_test.go`:
  - a seeded draw is deterministic and in a stable order;
  - the pool ignores live outcome (failed, blocked and rescued records are drawn);
  - n greater than the pool size is an error;
  - the repository and category mix is counted;
  - `single_repo_majority` is true when one repository is more than half the sample (FR-012).
- [ ] T058 [P] [US3] Write `internal/core/eval/stats_test.go`:
  - each metric's formula, on a hand-computed table (contracts/report.md, "Metrics");
  - pass^k counts only records where all reps succeeded;
  - `infra_error` is excluded from denominators;
  - rescues show as n/a for replays;
  - the paired bootstrap is deterministic for a given seed;
  - an identical-distribution pair gives an interval containing 0 (`indistinguishable`);
  - a 10-out-of-10 versus 0-out-of-10 pair gives an interval excluding 0;
  - best-baseline selection breaks ties on cost per verified success;
  - the threshold table is computed for non-baseline arms only, with the rescue row "not measurable by replay".
- [ ] T059 [P] [US3] Write `internal/core/eval/report_test.go`:
  - JSON output matches the shape in contracts/report.md (golden file `testdata/report/decision.json`);
  - markdown has the sections in order, with the "TREND ONLY" or "SMOKE" banner by mode;
  - contaminated runs are listed;
  - the footer shows the records-since-last-spot count (golden file `testdata/report/decision.md`).
- [ ] T060 [P] [US3] Write `internal/core/eval/decide_test.go`, which tests `Decide` and `Spot` with fakes:
  - the smoke pass runs k=1 on the first 3 sampled records for every arm;
  - an infrastructure error in the smoke pass stops the run with exit-class infra;
  - a Claude arm without usage-checked stops after the smoke pass, and re-running with it set reuses the cached smoke runs (no arm calls);
  - the projection uses only non-cached smoke runs, and a projection over budget is refused before any decision run;
  - an interrupted decide, run again, only executes the missing runs;
  - spot uses k=1 on records since the last spot check, newest first, up to the limit.
- [ ] T061 [P] [US3] Add the report-method tests to `internal/adapters/sqlite/store_test.go`: `SaveReport` with its `report_runs` links and its `events` row in the same transaction, `Report` round-trip, and `LastSpotCheck` returning the newest `spot` report.

### Implementation

- [ ] T062 [US3] Implement `internal/core/eval/sample.go` to pass T057, using `math/rand/v2` with a PCG source seeded from the report seed.
- [ ] T063 [US3] Implement `internal/core/eval/stats.go` to pass T058 (metrics, paired bootstrap with 10,000 resamples, best baseline, thresholds per contracts/report.md).
- [ ] T064 [US3] Implement `internal/core/eval/report.go` to pass T059 (a `Report` type plus pure `JSON()` and `Markdown()` renderers). Add the report methods of `RecordStore` to `ports.go`.
- [ ] T065 [US3] Implement the report methods in `internal/adapters/sqlite/store.go` to pass T061. Add the "replayable records since last spot check" footer to `fm eval list` in `cmd/fm/eval.go` (FR-017).
- [ ] T066 [US3] Implement `Decide` and `Spot` in `internal/core/eval/decide.go` to pass T060, following contracts/report.md, "Decision replay" and "Spot check".
- [ ] T067 [US3] Implement `fm eval spot`, `fm eval decide` (with `--budget-tokens` required and `--usage-checked`) and `fm eval report <id>` in `cmd/fm/eval.go`, writing `state/reports/<id>.md` and `.json`. Arm instance suffixes (`a1#2`) map to the base arm's adapter, under the instance name.
- [ ] T068 [US3] Run quickstart step 6 for real (fixtures, spot, `decide --arms 'a1,a1#2'`, then `decide --arms a0,a1` up to the usage gate). Check the Anthropic usage page after the first smoke pass with A0, then re-run with `--usage-checked`.

**Checkpoint**: all three stories work. foreman can now be measured against the baselines as
soon as Feature 002 starts producing records.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T069 [P] Run the secret-redaction check over every artifact written during T029, T050, T056 and T068: grep `$FOREMAN_HOME/state/runs/` and `$FOREMAN_HOME/state/reports/` for configured secret values and the token patterns from research R5 (the same list as `internal/core/eval/redact.go`). Fix any leaks found in the writers (`internal/adapters/artifacts/`, `internal/adapters/container/grader.go`). (This anticipates spec 002's FR-017; it's cheap to verify now.)
- [ ] T070 [P] Update `specs/ROADMAP.md`: 001 status set to "implemented", with the actual grader image digest and the T-codex-deny result and date under "Carry-overs".
- [ ] T071 Do a final pass of every quickstart step from a clean `FOREMAN_HOME` (`FOREMAN_HOME=$(mktemp -d)`), then `gofmt -l .`, `go vet ./...`, `go test ./...` and `go test -tags container ./...`. Measure the per-replay overhead (clone + export + grade, excluding the arm) on a warm cache against plan.md's 60 s goal. Record the outcome in the PR description.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: needs Setup. Blocks every story.
- **US1 (Phase 3)**: needs Foundational.
- **US2 (Phase 4)**: needs US1, because replays need records and the grader.
- **US3 (Phase 5)**: needs US2, because comparisons need replay runs.
- **Polish (Phase 6)**: needs the stories you intend to ship.

### Within phases

- Tests before implementation: T005–T006 before T007–T011; T015–T019 before T020–T028;
  T030–T035 before T036–T042; T045 before T046; T051 before T052–T053; T057–T061 before
  T062–T067.
- Ports before adapters: T020 before T021 and T024; T038 before T040, T042 and T046.
- The redactor (T023) comes before the grader (T024) and the artifacts adapter (T042).
- `cmd/fm/eval.go` is touched by T027, T028, T048, T055, T065 and T067. These are sequential,
  never [P].
- `internal/core/eval/ports.go` is touched by T008, T020, T038 and T064. Sequential.
- `internal/adapters/sqlite/store.go` and `store_test.go` grow in T006/T011, T033/T040 and
  T061/T065. Sequential.

### Delivery mapping (plan.md, Delivery)

| PR | Tasks |
|---|---|
| 1 | T001–T011 |
| 2 | T012–T029 |
| 3 | T030–T050 |
| 4 | T051–T056 |
| 5 | T057–T068 |
| Polish | T069–T071 (folded into PR 5 or a small PR 6) |

## Parallel Examples

### Phase 1

```text
T003 internal/archtest/archtest_test.go
T004 cmd/fm/env.go
```

### US1 (after T011)

```text
T012 testdata/fixtures/addrepo/           T015 internal/adapters/container/gotest_test.go
T013 testdata/fixtures/{fizzrepo,ambiguous}/   T016 internal/adapters/sandbox/git_test.go
T017 internal/adapters/container/grader_test.go   T018 internal/core/eval/validate_test.go
T019 internal/core/eval/redact_test.go
then: T022 gotest.go and T023 redact.go in parallel with T021 git.go (after T020)
```

### US2 (after US1)

```text
T030 run_test.go   T031 grade_test.go   T032 replay_test.go   T033 store_test.go (runs)
T034 artifacts_test.go   T035 proc_test.go
then: T041 proc.go, T042 artifacts.go, T043 schemas/arm_result.json, T044 prompts/arm_baseline.md, T045 codex_test.go, T051 hermes_test.go
```

### US3 (after US2)

```text
T057 sample_test.go   T058 stats_test.go   T059 report_test.go   T060 decide_test.go   T061 store_test.go (reports)
```

## Implementation Strategy

### MVP: User Story 1

1. Phases 1–2, then Phase 3.
2. **Stop and validate**: quickstart steps 1–3.
3. Usable right away: start recording tasks done by hand (`fm eval record`) on any repo with
   tests. Every record made now is a replayable eval task later.

### Incremental delivery

1. US1 (PR 1 and 2): the ledger and the grader.
2. US2 with A1 only (PR 3): the cheapest baseline. Codex on a ChatGPT plan may run without the
   operator present. **T-codex-deny must pass before A1 numbers are trusted.**
3. US2 with A0 and A2 (PR 4): Claude-family baselines, run only when the operator starts them.
4. US3 (PR 5): spot checks and decision replays. The first *real* decision replay waits for 15
   or more records from Feature 002; the fixtures only validate the mechanism.

## Notes

- [P] means different files and no dependency on an incomplete task.
- Live tests spend real tokens (Codex quota and Claude subscription). Run them on purpose,
  never in a loop.
- Never weaken a failing test to make a task pass (AGENTS.md). If a contract test shows an
  assumption in research.md is wrong, update research.md and the plan first.
- Commit after each task or logical group, with the task ID in the message.
