# Research: Evaluation Harness (Measure As We Go)

Decisions behind `plan.md`. Each has a real alternative that was rejected on purpose. Facts
marked **verified** were checked on this machine on 2026-10-07 (macOS 26.6.2, Go 1.27.1,
codex-cli 0.160.0, Hermes v0.21.5, Docker 29.8.2 on Colima, default profile 4 CPU / 6 GiB).

## R1. Keeping hidden tests away from the arms

**Decision**: Hidden tests live in foreman's state directory (`$FOREMAN_HOME/state`), and each
arm is confined so it can't read that directory:

- **Codex arms**: a Codex permissions profile passed with `-c` flags. It allows read-only
  `:minimal` plus GOROOT, write access to the run's workspace, and nothing else.
- **Hermes arms**: a dedicated Hermes profile using the Docker terminal backend, which mounts
  only the workspace (plus a read-only module cache) and runs with no network.

As a backstop, the grader scans each transcript for the state path and the hidden test file
names. A match marks the run `contaminated`, which counts as a failure.

**Why**:
- **verified**: under Codex's default `workspace-write` sandbox, a command can read any file
  the user owns outside the workspace, including a 0700 directory and `~/.ssh`. It cannot
  reach the Docker socket. So "stored outside the workspace" (ARCHITECTURE.md §10) is not
  enough on its own while arms run as the same macOS user.
- **verified**: with the allowlist profile below, reads of `~/.ssh`, `$HOME` and a hidden-test
  dir are all denied, while `go test ./...` still passes in the workspace:
  `codex sandbox -c 'default_permissions="fmarm"' -c 'permissions.fmarm.filesystem={":minimal"="read", "<ws>"="write", "<GOROOT>"="read"}'`.
  This needs `TMPDIR`, `GOCACHE` and `GOPATH` pointed inside the workspace and
  `GOTOOLCHAIN=local`. Go prints a harmless telemetry warning.
- **verified** (Hermes source and docs): the Docker backend routes every terminal, file and
  `execute_code` call through `docker exec` into one container per Hermes process. Settings:
  `terminal.docker_volumes` takes `host:container[:ro]`, `docker_mount_cwd_to_workspace`
  mounts the cwd at `/workspace`, and `docker_network: false` adds `--network=none`.
- **verified** (T-codex-deny, live, 2026-10-07): `codex exec` applies the same profile. The
  model `cat`s a random nonce from the workspace (proof that commands really ran), and its
  `ls` of the state directory gets `Operation not permitted`.
- Three findings from the live run (codex-cli 0.160.0):
  1. Codex's own sandbox helper re-executes the `codex` binary under the profile. Its install
     root (`/opt/homebrew`) must therefore be readable, or the session fails to start
     ("failed to load AGENTS.md instructions … execvp() … Operation not permitted"). The
     adapter's `ToolRoots` adds software roots: the Homebrew prefix plus the real install
     directory of the CLI. They hold programs, not secrets.
  2. `--output-schema` uses OpenAI strict mode: every property must be in `required`, so
     `question` is required and nullable (`["string","null"]`).
  3. `exec --json` emits no command-execution items, only agent messages and turn usage.
     Command output is visible only through what the model reports, which is why
     T-codex-deny proves execution with a nonce rather than trusting the transcript.

**Alternatives considered**:
- *Hidden tests only inside a Docker volume in the Colima VM*: Codex can't reach Docker
  (verified), but a Hermes arm on the local backend could, and the grader would still need a
  host copy to fill the volume. Rejected as the main defence: it's more moving parts, and the
  real fix for Hermes is the Docker backend anyway.
- *Encrypting hidden tests at rest*: the key has to live somewhere the same user can read.
  Rejected.
- *A separate macOS user for the grader*: strong, but it's the Feature 002/008 isolation
  track (ADR 0001). Rejected for 001; the deny profile and the leak scan cover it until then.

## R2. Where state lives

**Decision**:
- `$FOREMAN_HOME` defaults to `~/.foreman`.
- `state/` holds the SQLite database (`foreman.db`), record files (`records/<id>/`), run
  artifacts (`runs/<id>/`) and reports (`reports/<id>.{md,json}`). Arms can never read it.
- `work/` holds the per-run workspaces, each run's `io/` directory, and the shared read-only
  module cache. `work/runs/<id>/io/` holds every file an arm's CLI is handed or writes (prompt,
  structured result). fm moves it into `state/runs/<id>/` after the run, so no path under
  `state/` ever appears in an arm's argv, input or output (it would also trip the leak scan,
  R1). Arms see only their own workspace (plus the cache, read-only).

In Feature 002 the same layout moves under the `foreman` macOS user, and `work/` becomes the
only path its Colima profile mounts (ADR 0001).

**Why**: one root that's easy to deny (R1). It keeps the SQLite file canonical (constitution
VII) and fits ADR 0001's "VM mounts only foreman's workspace root".

**Alternatives considered**: records committed into this repo under `eval/tasks/`
(ARCHITECTURE.md §14). Rejected: the repo is cloned into arm workspaces when foreman works on
itself, so hidden tests would leak into later tasks' bases; and records are machine-written,
not hand-authored.

## R3. Record format: SQLite rows plus file blobs, JSON only

**Decision**: the database is the source of truth for every record field. Files under
`state/records/<id>/` hold only blobs: `visible/` and `hidden/` test trees (repo-relative
paths) and an optional `reference.patch`. `fm eval record` takes flags and directories. No
YAML.

**Why**: one source of truth. JSON and `database/sql` are standard library; a YAML parser
would be 001's second third-party dependency for no current need (AGENTS.md rule 4). Feature
002's `.foreman/project.yaml` can justify YAML when it arrives.

**Alternatives considered**: a `record.json` file per record as the canonical copy, with the
database as an index. Rejected: two sources of truth that can drift.

## R4. SQLite driver

**Decision**: `modernc.org/sqlite` v1.60.1 (published 2026-09-29, past the 7-day cooldown), in
WAL mode with `busy_timeout`, and plain `database/sql` (no ORM). `sqlc` is deferred until
there are more than a handful of queries; 001 has about 15.

**Why**: pure Go, with no cgo toolchain needed in the arm and grader images or under launchd.
The constitution's Technology Stack allows either driver.

**Alternatives considered**: `mattn/go-sqlite3`. Rejected because it needs cgo. Generating
query code with `sqlc` from day one was rejected on YAGNI grounds; revisit in 002.

## R5. Grader mechanics

**Decision**: the grader drives the `docker` CLI through `os/exec`; there is no Docker SDK
dependency. For each grading:

1. **Prepare**: make a fresh clone at the base commit (R6), apply the patch with
   `git -c core.hooksPath=/dev/null apply --index`, and copy the visible tests in.
2. **Fetch dependencies** (network on, no secrets, no host mounts except the clone, read-only):
   run `go mod download` into a named volume keyed by `go.sum`, with
   `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `GONOSUMDB` and `GOPRIVATE`
   empty, and `GOFLAGS=-mod=readonly`. Every module is therefore checksum-verified, and the
   step only downloads: Go modules run no code at download time, and nothing from the patch is
   executed here. Skipped when the volume exists. This is the one network-on step in grading;
   it is logged as a deviation in plan.md Complexity Tracking (constitution VI).
3. **Test** (`--network=none --read-only --tmpfs /tmp --cap-drop=ALL
   --security-opt no-new-privileges --pids-limit 512 --memory 2g --cpus 2`):
   - mount the clone read-write at `/src`, the hidden tests read-only at `/hidden`, and the
     module volume read-only;
   - copy the hidden tests over `/src`;
   - run the record's test command with `go test -json`;
   - parse the per-test results.
4. **Full suite** (optional, if the record has `full_test_command`): same isolation as the
   test step. Validation runs it once on the unpatched base and stores
   `base_full_suite_passed` on the record. A graded run has `full_suite_regressed = 1` only
   when the base passed and the patched tree fails, so a suite that was already red never
   counts as a regression.

The image is `golang:1.27.1-bookworm`, pinned by digest at implementation time to a build at
least 7 days old.

**Redaction** (grader logs and arm transcripts, before anything is written to `state/`):
- replace configured secret values with `[REDACTED]`. These are the values of fm's own
  environment variables whose names contain `TOKEN`, `KEY`, `SECRET` or `PASSWORD`. fm reads no
  other tool's credential files;
- replace common token patterns (`gh[opusr]_[A-Za-z0-9]{36,}`, `github_pat_[A-Za-z0-9_]{50,}`,
  `sk-[A-Za-z0-9-_]{20,}`, `xox[abpr]-[A-Za-z0-9-]{10,}`, `AKIA[0-9A-Z]{16}`, and PEM private-key
  blocks) the same way;
- truncate grader logs to 64 KiB.

One redactor in `internal/core/eval` (pure: values and patterns in, text out), used by every
writer.

**Why**: the dependency fetch has to happen somewhere. Doing it in its own step, with no
secrets and no access to hidden tests, keeps the test step fully offline (FR-006: "read-only
dependency caches"). `go test -json` gives per-test pass/fail with only the standard library
needed to parse it.

**Alternatives considered**:
- *Vendoring dependencies into the clone*: changes the repo under test. Rejected.
- *A network-on test step*: violates constitution VI. Rejected.
- *The Docker Go SDK*: a large dependency tree for a few `docker run` calls. Rejected.

## R6. Fresh clones and trusted export

**Decision**:
- Keep one bare mirror per repo URL in `work/mirrors/` (fetched with `git fetch --prune`).
- Each run gets `git clone --no-hardlinks --no-checkout <mirror> <ws>`, then a checkout of
  the base.
- Hooks are disabled everywhere, and there are no linked worktrees (constitution VI).
- Right after checkout, fm copies the clone's `.git` to `work/runs/<id>/trusted.git`, outside
  the workspace and not in the arm's allowlist. The arm keeps its own `.git` in the workspace
  for `git status` and `git diff`, and fm never uses it again.
- **Export** runs with the trusted git dir and a neutral environment:
  `GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git --git-dir=<trusted.git>
  --work-tree=<ws> -c core.hooksPath=/dev/null -c core.fsmonitor=false -c diff.external=
  -c core.attributesFile=/dev/null add -A`, then `diff --cached --binary --no-ext-diff
  --no-textconv <base>`. The workspace's `.git/` and `.fm/` are excluded.
- Every other host-side git call that touches an arm-written tree follows the same rules.

**Why**: the agent can write anything in its workspace, including `.git/config`.
`core.fsmonitor`, `diff.external`, and filter or textconv drivers defined there run commands
when git runs. Hooks being disabled doesn't stop them (constitution VI: "hooks and config are
not a security boundary"). Driver definitions live only in config, and the trusted git dir's
config was written by fm before the arm started. So a `.gitattributes` that names a driver
finds no definition and runs nothing. A missing base commit is detected at fetch time, and
the record is marked not replayable (spec FR-004).

**Alternatives considered**: computing the diff inside a throwaway container. Rejected: as
safe, but slower per run, and another moving part. Copying the workspace tree (minus `.git`)
into a second fresh clone and diffing there was also rejected: correct, but it doubles the
checkout I/O for no extra safety over the trusted git dir.

## R7. Arm invocations

**Decision**: one `Arm` port in core with three adapters.

- **A1, Codex**:
  - Command: `codex exec --ephemeral --json -C <ws> --output-schema schemas/arm_result.json
    -o <io>/last.json` with the R1 permissions profile, reading the prompt from stdin
    (`<io>` = `work/runs/<id>/io`, R2).
  - Environment: `TMPDIR`, `GOCACHE` and `GOPATH` under the workspace, `GOMODCACHE` set to the
    read-only cache, `GOFLAGS=-mod=mod`, `GOPROXY=off`, `GOTOOLCHAIN=local`.
  - Usage: from the `turn.completed` event's `usage` field (**verified** shape:
    `input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`,
    `reasoning_output_tokens`).
- **A0, Hermes single shot**:
  - Command: `HERMES_HOME=<foreman-arm profile> hermes chat --oneshot --format stream-json
    --query-file <io>/prompt.md --in <ws> -t file,terminal --max-turns <n>
    --run-budget <seconds> --source tool --yolo`.
  - The toolset names `file` and `terminal` are **verified** (`hermes tools list`).
  - The profile uses the Docker terminal backend (R1) with `docker_network: false` and no MCP
    servers. `--yolo` is acceptable only because every command runs in that container.
  - Usage: from the session record (`hermes sessions export --session-id <id>`, **verified**
    fields `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
    `estimated_cost_usd`).
- **A2, Hermes goal loop**:
  - `/goal` is an interactive-only slash command (**verified** in source). The headless
    equivalent is a Kanban card with `--goal`, which runs the same continuation engine in one
    worker session.
  - Command: `hermes kanban --board foreman-eval create <title> --body-file <io>/prompt.md
    --assignee foreman-arm --workspace dir:<ws> --goal --goal-max-turns <n>
    --max-runtime <s> --max-retries 1 --json`, then `hermes kanban --board foreman-eval
    dispatch --max 1`.
  - Poll `hermes kanban show <id> --json` until the card is done or blocked. Usage comes from
    the worker's session (`kanban runs <id> --json` gives the session id).

All arms end with the same structured result: `status` (`done` or `needs_clarification`),
`summary`, and an optional `question`. A Hermes run whose last message doesn't parse as that
JSON counts as `done` if it left a non-empty diff, and `none` otherwise. Codex enforces the
schema itself.

**Why**: these are each vendor's own headless paths (constitution IX: no custom agent loop).
Kanban `--goal` is the route ARCHITECTURE.md §16 already names for A2.

**Where Hermes itself runs**: on the host. Only its tools run in the container (Docker
*terminal backend*, not Hermes-in-Docker). Credentials therefore stay in the host process. The
`foreman-arm` profile inherits the root Anthropic login read-only, and nothing credential-like
is mounted or forwarded into the container (`docker_forward_env` is left empty). Running Hermes
itself inside a container (Feature 008's always-on track, or Hermes's own Docker image) would
change two things:

1. The container needs its own Anthropic login: a separate `hermes auth add anthropic` stored
   in that container's data volume. Do not mount or copy the host `auth.json`. Anthropic OAuth
   refresh grants are single-use, so two holders of one grant revoke each other on the first
   refresh. Hermes strips cloned grants for exactly this reason (`hermes_cli/profiles.py`,
   `strip_cloned_single_use_oauth_grants`).
2. Credentials and agent tools would share one container. The agent's commands would then
   need a further sandbox inside it, or the login would be exposed to them. That breaks
   constitution VI unless the tools are confined again.

Neither applies to 001.

**Alternatives considered**: driving `/goal` through a pseudo-terminal (tmux). Rejected as
fragile; the Kanban path is supported and machine-readable. For A0, `hermes -z <prompt>
--usage-file <path>` writes a usage report directly, but prints only the final text. That
leaves no transcript for the leak scan (R1), so A0 uses `chat --format stream-json` plus a
session export.

## R8. Enforcing the caps

**Decision**:
- **Wall-clock**: a `context.WithTimeout` per run kills the process group. Hermes also gets
  `--run-budget` / `--max-runtime` so it can wrap up first.
- **Tokens**:
  - A0 is stopped as soon as its stream reports usage over the cap.
  - A2 has no stream. On every 10-second poll, fm reads the worker session's usage
    (`kanban runs <card> --json` gives the session id, then `sessions export`) and reclaims and
    archives the card once usage is over the cap. It can overshoot by at most one poll interval.
  - Codex reports usage only at `turn.completed`, so its cap is checked when the run ends. A run over
  either cap is recorded `budget_exceeded` or `timed_out` and counts as a failure (spec FR-008
  as amended).
- The cap counts billable tokens: uncached input + output + cache writes. All token fields are
  stored.

**Why**: Codex has no mid-turn usage stream or token-limit flag in 0.160.0, so "stop at the
cap" is not possible there. The spec was amended to say so rather than promise it.

## R9. Statistics and the noise flag

**Decision**:
- **Success rate**: averaged over (task, repetition) pairs; pass^3 is computed per task.
- **Paired comparison** of two arms: the per-task difference in mean success, with a 95%
  bootstrap confidence interval (10,000 resamples of tasks, seeded with the report's recorded
  seed).
- **Noise flag**: the difference is "indistinguishable" when the interval contains 0. Cost and
  time get the same bootstrap.
- **Paired data**: per-(task, repetition) outcomes are stored so McNemar's test can be run
  later (FR-009). The harness doesn't compute it.

**Why**: a bootstrap needs only `math/rand/v2` with a seed, and it's deterministic given that
seed. It gives an honest "can't tell" at 15 tasks instead of a fixed 15–20-point rule of thumb
that only holds at about 40 tasks × 3.

**Alternatives considered**: a fixed threshold from Appendix D. Rejected: it's wrong at the
sizes this feature will actually run.

## R10. Rescues and the go/no-go criteria

**Decision**:
- Replays never involve the operator, so every replayed arm has 0 rescues. Rescue rate is
  reported only from live records.
- Decision replays apply the §16 criteria that replays can measure: verified success,
  false-success rate, cost per verified success, and regression rate.
- The criterion "equal success with ≥30% fewer rescues" is shown as "not measurable by
  replay" until baseline arms have live records too.

**Why**: inventing a rescue count for baselines would bias the comparison.

## R11. Rubric grading

**Decision**: not built in 001. A record whose task type or category needs a rubric is stored
with replayable = false and reason `rubric grader not implemented`. Records keep a nullable
rubric verdict, so adding the grader later doesn't require a schema rewrite.

**Why**: the first decision replay is code-only and graded by hidden tests (spec FR-014).
Building a cross-family rubric grader now would be speculative (YAGNI). It arrives with the
first judgment-graded task type (Feature 010, research).

## R12. Configuration

**Decision**:
- Environment variables, each with a default: `FOREMAN_HOME`, `FOREMAN_DOCKER_CONTEXT`
  (default: the current context) and `FOREMAN_HERMES_HOME` (path to the `foreman-arm` Hermes
  profile).
- Caps and budgets are per-command flags with defaults.
- No config file.

**Why**: three values don't justify a file format (YAGNI). Feature 002 adds
`.foreman/project.yaml` for per-repo settings.

## R13. Claude subscription guard

**Decision**:
- Every `fm eval` command runs only when the operator invokes it; nothing schedules Claude
  arms.
- `fm eval decide` refuses to run with a Claude-family arm (A0, A2) until a smoke pass that
  included that arm has been followed by `--usage-checked`. That flag records that the
  operator looked at the Anthropic account's usage page (spec Assumptions; ARCHITECTURE.md §0).

**Why**: it turns a manual step into a gate the CLI can enforce.
