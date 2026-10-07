# Contract: arm invocations and their checks

Exact invocations per arm (research R7). `<ws>` is the run's workspace. `<io>` is
`work/runs/<id>/io/`, which holds every file a CLI is handed or writes; fm moves it into
`state/runs/<id>/` (redacted) after the run, so no `state/` path ever reaches an arm. `<cache>`
is `work/gomodcache` (read-only to arms). Transcripts are captured from the CLI's stdout by fm
and written straight to `state/`.

## Prompt (`prompts/arm_baseline.md`, hashed into `arm_version`)

The same text for every arm:
- the record's request verbatim;
- the list of visible test files already in the workspace;
- "make the change in this repository; run the tests you can see";
- "if the request is ambiguous or impossible, do not guess: stop and ask";
- the required final answer: JSON matching `schemas/arm_result.json`.

The prompt never mentions hidden tests, foreman, or any state path.

## Final result schema (`schemas/arm_result.json`)

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "summary", "question"],
  "properties": {
    "status":   {"type": "string", "enum": ["done", "needs_clarification"]},
    "summary":  {"type": "string", "maxLength": 2000},
    "question": {"type": ["string", "null"], "maxLength": 1000}
  }
}
```

## A1: Codex

```text
env -i HOME=$HOME PATH=$PATH TMPDIR=<ws>/.fm/tmp GOCACHE=<ws>/.fm/gocache GOPATH=<ws>/.fm/gopath \
    GOMODCACHE=<cache> GOFLAGS=-mod=mod GOPROXY=off GOTOOLCHAIN=local \
  codex exec --ephemeral --json -C <ws> \
    -c 'default_permissions="fmarm"' \
    -c 'permissions.fmarm.filesystem={":minimal"="read", "<ws>"="write", "<GOROOT>"="read", "<cache>"="read", "/opt/homebrew"="read"}' \
    --output-schema schemas/arm_result.json -o <io>/last.json  -  < <io>/prompt.md
```

- stdout (JSONL) goes to `state/runs/<id>/transcript.jsonl`, redacted.
- Usage comes from the last `turn.completed.usage`.
- `<ws>/.fm/` is excluded from the exported patch.
- `codex --version` and the configured model form `arm_version`.

## A0: Hermes single shot

Profile `FOREMAN_HERMES_HOME` (created once; see quickstart.md):
- `terminal.backend: docker` with the grader's base image;
- `terminal.docker_mount_cwd_to_workspace: true`;
- `terminal.docker_volumes: ["<cache>:/go/pkg/mod:ro"]`;
- `terminal.docker_network: false`;
- `terminal.container_persistent: false`;
- no `mcp_servers`, no memory provider, no skills;
- model: the Claude model the operator chooses for baselines.

```text
HERMES_HOME=<profile> hermes chat --oneshot --format stream-json --query-file <io>/prompt.md \
  --in <ws> -t file,terminal --max-turns 200 --run-budget <wall cap s> --source tool --yolo
```

- stdout goes to `state/runs/<id>/transcript.jsonl`, redacted.
- The session id comes from the stream's final event. Usage comes from
  `hermes sessions export --session-id <id> -` (fields per research R7).
- The final assistant message is parsed as `arm_result` JSON (fallback rule in R7).

## A2: Hermes goal loop (Kanban `--goal`)

```text
HERMES_HOME=<profile> hermes kanban --board foreman-eval create "<record id> rep <n>" \
  --body-file <io>/prompt.md --assignee foreman-arm --workspace dir:<ws> \
  --goal --goal-max-turns 20 --max-runtime <wall cap s> --max-retries 1 --json
HERMES_HOME=<profile> hermes kanban --board foreman-eval dispatch --max 1
# poll every 10 s:  hermes kanban --board foreman-eval show <card> --json   until done|blocked
#   and each poll:  hermes kanban runs <card> --json → session id → sessions export → usage;
#                   usage over the token cap → reclaim + archive the card → budget_exceeded
```

- A card that ends `blocked` because its turn budget ran out counts as `claimed=none`.
- A card that is still running at the wall cap is reclaimed and archived, and the run is
  marked `timed_out`.

## Contract tests (`internal/adapters/runner/*/contract_test.go`, build tag `live`)

These run real CLIs. They are excluded from `go test ./...` and run with
`go test -tags live ./internal/adapters/runner/...`. They are operator-triggered, because
the Hermes arms use the Claude subscription.

| Test | Proves |
|---|---|
| T-codex-deny | `codex exec` with the A1 profile cannot read a canary file under a temp `state/`. The prompt asks for `cat proof.txt; ls -la <state dir>`: the workspace nonce must appear (proof the command ran), the canary must not, and the listing must show "Operation not permitted". **Must pass before any A1 result is trusted** (research R1). Passed 2026-10-07 |
| T-codex-usage | `turn.completed.usage` parses into `Usage` with non-zero input and output |
| T-hermes-confined | A0's container cannot see `$HOME` or the canary: `ls /Users` and `cat` of the canary path fail inside the container |
| T-hermes-usage | session export yields non-zero tokens and an `estimated_cost_usd` |
| T-kanban-goal | an A2 card on a trivial fixture task reaches `done` and leaves a non-empty diff in the workspace |
| T-leak-scan | a transcript containing the canary path is graded `contaminated` |
