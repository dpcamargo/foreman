# Contract: Local Unix-Socket API

Served by `internal/adapters/localapi` at `$FOREMAN_HOME/state/foreman.sock` (mode `0700`,
inside the already-`0700` state directory per Feature 001's `env.ensureDirs`). Every connection
is checked with a `LOCAL_PEERCRED`-equivalent syscall (`golang.org/x/sys/unix`) before any
request is read; a peer whose UID doesn't match the daemon process's own UID gets the
connection closed with no response (FR-013) — not even an HTTP error, to avoid giving an
unauthorized local process a signal to probe further.

This is the same surface Telegram commands use internally — `cmd/fm`'s future daemon-aware
subcommands (not in this feature's scope beyond what's listed) and the Telegram adapter both
call through it, so there is exactly one code path that mutates task state, per Principle I.

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/tasks` | `{repo, base_commit, request, category}` | mirrors `/task <repo> <text>`; returns `{id}` |
| GET | `/v1/tasks/{id}` | — | mirrors `/status <id>` |
| GET | `/v1/tasks` | — | mirrors `/tasks` |
| POST | `/v1/tasks/{id}/pause` | `{now: bool}` | mirrors `/pause <id> [now]` |
| POST | `/v1/tasks/{id}/resume` | — | mirrors `/resume <id>` |
| POST | `/v1/tasks/{id}/cancel` | — | mirrors `/cancel <id>` |
| POST | `/v1/approvals/{id}/respond` | `{nonce, response, reason?}` | same `control.ValidateApproval` path as the Telegram inline buttons |

Every response is JSON. Errors use the same three-tier exit-code mapping as `fm eval`/`fm run`
(usage error, infra error, negative result), translated to HTTP 400/503/200-with-failure-body
respectively, so a future `fm` daemon-aware subcommand can reuse `cmd/fm`'s existing
`exitFor`-style error classification.
