# Contract: Telegram Commands

All commands are parsed by deterministic code in `internal/adapters/telegram/commands.go`.
No LLM call sits between a received update and the action it triggers (FR-002). Every command
is checked against the `allowlist` table and the `chat.type == "private"` requirement before
dispatch (FR-003); a rejected sender gets no reply (not even an error) so the bot's behavior
leaks nothing about whether a task or id exists.

| Command | Action | Notes |
|---|---|---|
| `/task <repo> <text>` | Creates a task row (`status=pending`), pushes it onto the Supervisor's dispatch channel, replies with the new task id | `/start` is reserved by Telegram for bot startup, so task creation uses `/task` (ARCHITECTURE.md §9) |
| `/status [id]` | Replies with the task's current node, status, attempts, tokens/cost so far; omitted `id` means the most recently created task | |
| `/tasks` | Lists non-terminal tasks with id and status | |
| `/pause [id\|all] [now]` | Without `now`: sets a pause flag the Supervisor checks between ladder attempts (running attempt finishes). With `now`: cancels that task's context immediately (FR-008) | `all` pauses every non-terminal task |
| `/resume [id]` | Clears the pause flag; the dispatch loop picks the task back up on its next cycle | |
| `/cancel <id>` | Cancels the task's context, marks `status=cancelled`, retains its workspace 7 days (FR-009) | |
| `/reject <id> <reason>` | Resolves the task's pending approval as rejected; `<reason>` is required (FR-011) — if omitted, the bot prompts for one and does not reject until supplied | |
| `/answer <id> <text>` | Answers a `blocked_human` task's question, moving it back to dispatchable | |
| `/diff <id>` | Sends the task's current patch as a `.patch` file attachment (Telegram caps text messages at 4096 characters) | |
| `/logs <id> [node]` | Sends recent run output for the task, optionally filtered to one node | |
| `/budget` | Replies with spend/quota summary across active tasks | |
| `help` / `/help` | Usage text | matches `cmd/fm`'s existing `help` convention |

**Inline buttons** (`[Approve] [Reject] [Edit]`, `[Merge]`): callback data carries
`{approval_id, nonce}`. The handler calls `control.ValidateApproval` (contract:
`approval-nonce.md`) before applying anything. A **[Reject]** tap without a stored reason
prompts the operator for one via a follow-up message; the rejection is not recorded until the
reason arrives (FR-011).

**Free text** (anything not matching a command or callback): until Feature 007 wires in the
Hermes relay, the bot replies with a fixed string ("chat isn't enabled yet; use /commands") and
parses nothing from it (FR-006). It is never treated as an approval, rejection, or policy
change, in any phrasing — this is enforced structurally (free text never reaches the approval
code path at all), not by a denylist of phrases.
