# Quickstart: validating the Evaluation Harness

Runnable checks, one per major behaviour. Details are in `contracts/` and `data-model.md`.
Run from the repo root as the operator (the `foreman` macOS user arrives with Feature 002).

## 0. Prerequisites (once)

```sh
go version                          # go1.27.x
docker context show && docker info  # Colima running (4 CPU / 6 GiB)
codex --version                     # logged in (ChatGPT plan)
go build -o bin/fm ./cmd/fm
```

Create the confined Hermes profile used by A0/A2. This is done once, by hand: Hermes config is
changed only through `hermes config set`.

```sh
hermes profile create foreman-arm --no-skills --no-alias \
  --description "foreman baseline arm: confined, no MCP, no memory"   # blank: no --clone
export FOREMAN_HERMES_HOME=~/.hermes/profiles/foreman-arm
# No login needed: a profile with no Anthropic entries of its own inherits the root
# credential pool read-only (hermes_cli/auth.py read_credential_pool).
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set model.provider anthropic
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set model.default <baseline Claude model>
HERMES_HOME=$FOREMAN_HERMES_HOME hermes auth list             # anthropic entries shown (inherited)
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.backend docker
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.docker_image "$FOREMAN_GRADER_IMAGE"
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.docker_mount_cwd_to_workspace true
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.docker_network false
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.container_persistent false
HERMES_HOME=$FOREMAN_HERMES_HOME hermes config set terminal.docker_volumes '["'$HOME'/.foreman/work/gomodcache:/go/pkg/mod:ro"]'
HERMES_HOME=$FOREMAN_HERMES_HOME hermes mcp list             # must be empty
HERMES_HOME=$FOREMAN_HERMES_HOME hermes kanban boards create foreman-eval
```

## 1. Unit and architecture tests (no network, no containers)

```sh
gofmt -l . ; go vet ./... ; go test ./...
```

Expected: no gofmt output, and everything passes. That includes `internal/archtest`, which
fails if `internal/core` imports an adapter or a third-party module.

## 2. Record a task, and reject a broken one (User Story 1)

The fixture is `testdata/fixtures/addrepo`: a tiny Go repo with a base commit missing a
feature, a hidden test for it, and a reference patch. `scripts/fixtures.sh` builds it as a local
git repo under `work/fixtures/addrepo.git`.

```sh
scripts/fixtures.sh
bin/fm eval record --repo work/fixtures/addrepo.git --base $(cat work/fixtures/addrepo.base) \
  --request-file testdata/fixtures/addrepo/request.md --hidden testdata/fixtures/addrepo/hidden \
  --reference testdata/fixtures/addrepo/reference.patch --test-cmd '["go","test","-json","./..."]'
```

Expected: `rec_… replayable`. The hidden test failed on base and passed with the reference.

```sh
bin/fm eval record … --hidden testdata/fixtures/addrepo/hidden-passes-on-base …
```

Expected: exit 1, `not replayable: hidden tests pass on base`.

```sh
bin/fm eval record … --hidden testdata/fixtures/addrepo/hidden-escape …   # contains ../x_test.go
```

Expected: exit 2, a path-escape error, and no record created.

## 3. The grader is offline and clean (FR-006, SC-002)

```sh
go test -tags container ./internal/adapters/container/...
```

Expected: these pass:
- `TestTestStageHasNoNetwork`: a hidden test dials `1.1.1.1:443` and must get a network error;
- `TestNoHostMounts`: `/Users` is absent in the container;
- `TestNoHostEnv`: the host `HOME` and `PATH` are not visible.

## 4. Arms are confined (FR-007; research R1)

```sh
go test -tags live -run 'T_codex_deny|T_hermes_confined|T_leak_scan' ./internal/adapters/runner/...
```

Expected: all pass. **If T-codex-deny fails, A1 results are not trusted:** fix the profile
before step 5. The Hermes test uses a few Claude subscription tokens, so run it deliberately.

## 5. Replay through a baseline (User Story 2, SC-001)

```sh
bin/fm eval replay --record rec_… --arm a1 --k 1
bin/fm eval replay --record rec_… --arm a1 --k 1      # second time
```

Expected:
- first run: graded, with tokens and time printed;
- second run: `cached`, zero spend (FR-015, SC-005).

Then lower the cap to force a failure:

```sh
bin/fm eval replay --record rec_… --arm a1 --k 1 --max-tokens 1000
```

Expected: a new run (the cap is not part of the cache key, so pass `--no-cache` for this
check). Its status is `budget_exceeded` and it counts as failed.

## 6. Spot check and decision replay (User Story 3)

Record at least 3 fixture tasks first (`scripts/fixtures.sh` provides `addrepo`, `fizzrepo`, and
`ambiguous`, which uses `--expects-clarification`).

```sh
bin/fm eval spot --arm a1
bin/fm eval decide --arms 'a1,a1#2' --n 3 --k 3 --seed 7 --budget-tokens 3000000
```

Expected:
- the spot report header says "TREND ONLY";
- `a1#2` is a second instance of A1 with its own cache key (see `contracts/cli.md`): the
  same-arm comparison is reported `indistinguishable` (SC-004);
- the sample section lists repos and categories, and warns about single-repository majority;
- no Claude arm is involved, so `--usage-checked` is not needed.

With a Claude arm:

```sh
bin/fm eval decide --arms a0,a1 --n 3 --k 1 --seed 7 --budget-tokens 3000000
```

Expected: the smoke pass runs, then exit 1 with "check the Anthropic usage page". After
checking, re-run with `--usage-checked`. The smoke runs are cache hits, and the decision
report is written.

## 7. Crash safety (constitution VII)

```sh
bin/fm eval replay --record rec_… --arm a1 --k 1 --no-cache & sleep 20; kill -9 $!
bin/fm eval list
```

Expected: the killed run shows `infra_error (lost)` after the next command's startup sweep.
Re-running replays it from a fresh clone.

## Rollout notes

- Nothing in this feature runs on a schedule, and nothing touches the operator's repos: arms
  work in fresh clones under `~/.foreman/work`, and no push credentials are used anywhere.
- `--budget-tokens` is mandatory for `decide`. There is deliberately no default budget.
- The first real decision replay waits until Feature 002 has produced at least 15 replayable
  records from real work. The fixtures are only for validating this feature, never for the
  go/no-go.
