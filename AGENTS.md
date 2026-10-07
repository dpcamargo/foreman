# AGENTS.md

Instructions for any coding agent (Codex, Claude, Antigravity, Hermes) working in this
repository. Binding rules come from `.specify/memory/constitution.md`; this file spells out
how its Principle X (code structure) applies day to day. If they conflict, the constitution
wins.

## What this is

foreman is a deterministic Go orchestrator: one daemon (`foreman`) plus a CLI (`fm`). It owns
task state in SQLite and dispatches headless agent CLIs as workers. Specs live in `specs/NNN-*/`
(spec-kit), with the build order in `specs/ROADMAP.md`. Read the spec and plan for the feature
you're working on before writing code.

## Code rules

1. **Dependencies point inward.** `internal/adapters/...` imports `internal/core/...`, never the
   reverse. Core imports only the standard library and other core packages, and does no I/O.
   A test fails the build if any core package imports an adapter.
2. **Small interfaces, declared where they are used.** Each core package declares the
   interface it needs ("accept interfaces, return structs"). Add one only when a second
   implementation or a test fake needs it. No interface for a single implementation "just in
   case".
3. **`cmd/` only wires.** `cmd/fm` and `cmd/foreman` build the adapters, hand them to core, and
   parse flags. No business logic.
4. **Standard library first.** `net/http`, `log/slog`, `database/sql`, `os/exec`, `context`.
   No web frameworks, no dependency-injection containers, no catch-all `util`/`common`/`helpers`
   package. A new third-party dependency needs a justification in the plan's Complexity
   Tracking.
5. **YAGNI.** No package, interface, option, flag or directory before a current task needs it.
   The layout below is where the code ends up, not a scaffold to create on day one.
6. **Clean code.**
   - Short functions; names say what they do.
   - Wrap errors with `%w` at each boundary, adding what was being attempted.
   - Pass `context.Context` first through anything that blocks or does I/O.
   - No package-level mutable state; no `init()` side effects.
   - Table-driven tests. Test core with fakes; test adapters against real SQLite and real
     containers, not mocks of them.

## Target layout (after Features 001 and 002)

```
cmd/fm/              CLI: wiring only
cmd/foreman/         daemon: wiring only (arrives with Feature 003)
internal/core/       domain + use cases; stdlib only, no I/O
  task/              Task, Node, Run, state transitions
  engine/            run loop, retry ladder, reconciler; declares the ports it needs
  verify/            verification and diff-policy rules
  eval/              task records, replay sampling, comparison metrics (Feature 001)
internal/adapters/   everything that touches the outside world
  sqlite/            implements the core store interface
  runner/codex/      implements the core Runner interface
  runner/hermes/     implements the core Runner interface
  sandbox/           fresh clone per run
  container/         verifier and grader on foreman's own Colima profile
migrations/          versioned SQL migrations
schemas/             JSON schemas for structured model output
prompts/             versioned prompt templates (hashed per run)
docs/adr/            architecture decision records
```

The core ports come from ARCHITECTURE.md §15: Runner, Sandbox, Verifier, Memory, Notifier.

## Workflow

- Write tests first. A task isn't done until its tests pass along with `gofmt -l .` (no
  output), `go vet ./...` and `go test ./...`, and you've run them yourself.
- Never delete or weaken an existing test to make a change pass.
- Never put secrets, tokens or personal paths in code, fixtures, logs or test data.
- Keep changes to the task in hand; note unrelated problems instead of fixing them in passing.
