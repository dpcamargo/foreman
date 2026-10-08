# Quickstart: Validating the Daemon + Telegram Control Plane (Feature 003)

Runnable checks proving Feature 003 functionality end-to-end. Run from the repository root.
Unlike Features 001/002's quickstarts, several steps here need a real Telegram bot token and a
real GitHub fine-grained token — those are marked **(operator action)** and can't be automated
in CI.

---

## 0. Prerequisites

```sh
go version                          # go1.27.x
```

**(operator action)** Before step 1:
1. Create a Telegram bot via `@BotFather`, get its token.
2. Find your own numeric Telegram user id (e.g. via `@userinfobot`) and add it to the
   `allowlist` table (or a `--allow <id>` bootstrap flag on first run).
3. Create a GitHub fine-grained PAT scoped to the test repo only, "Contents: write" and
   "Pull requests: write", no other permissions.
4. **If testing unattended dispatch** (not just attended manual testing): create the dedicated
   `foreman` macOS user per ADR 0001, and run the rest of this quickstart as that user. Running
   as the operator's own account is fine for attended testing (ADR 0001 Consequences) but is
   not the target configuration.

```sh
export TELEGRAM_BOT_TOKEN=...
export FOREMAN_GITHUB_TOKEN=...
go build -o bin/foreman ./cmd/foreman
go build -o bin/fm ./cmd/fm
```

---

## 1. Unit & Architecture Tests (no network, no containers, no live Telegram/GitHub)

```sh
gofmt -l . ; go vet ./... ; go test ./...
```

Expected: zero `gofmt` output, all tests pass, including `internal/archtest` proving
`internal/core/control` (like every other core package) imports no adapters.

---

## 2. Create and Track a Task from Telegram (User Story 1, SC-001)

```sh
bin/foreman &
```

From the allowlisted Telegram chat:
```text
/task addrepo Add a function Sub(a, b int) int to package addrepo that returns a - b.
```

Expected:
- Immediate reply with a task id, within a few seconds.
- A `TaskCreated` event is recorded (`sqlite3 $FOREMAN_HOME/state/foreman.db "select * from
  events where entity='task' order by id desc limit 1;"`).
- `/status <id>` returns the task's current phase.

---

## 3. Approve or Reject with a Nonce-Bound Button (User Story 2, SC-002, SC-005)

Trigger a spec approval gate (any task reaching the hard-coded "all lane S/D specs require
approval" stub rule — see spec.md Assumptions).

Expected:
- Telegram shows **[Approve] [Reject] [Edit]** bound to the spec's sha256.
- Sending a free-text message instead of tapping a button gets the fixed
  "chat isn't enabled yet; use /commands" reply, and does **not** approve, reject, or change
  anything — try phrasings like "yes approved" and "go ahead" explicitly (SC-005).
- Simulate a race: after the approval request is sent, force a replan (or manually update the
  spec row) so the artifact's sha256 changes, then tap the now-stale button. Expected: rejected
  with a nonce/hash mismatch, not silently applied to the new spec (SC-002).
- Tap **[Reject]** without typing a reason first: the bot prompts for one; the rejection is not
  recorded until a reason is supplied (FR-011).

---

## 4. Pause, Resume, Cancel, and Crash Recovery (User Story 3, SC-003)

```sh
# Pause: new dispatch stops, running nodes finish.
```
Send `/pause <id>` while a node is in flight; confirm (via `/status <id>` or logs) that the
in-flight attempt completes normally and no new attempt starts until `/resume <id>`.

```sh
# Pause now: kill in-flight work immediately.
```
Send `/pause <id> now`; confirm the run is recorded `infra_error`/cancelled (eligible for retry
on resume), not as an attempt failure.

```sh
# Crash recovery.
kill -9 $(pgrep -f 'bin/foreman')
bin/foreman &
```
Expected: the reconciler (Feature 002's, reused) expires stale leases and detects the dead PID
via start-time fingerprint before any new Telegram-triggered dispatch is accepted (FR-010); the
paused-but-in-flight task resumes or cleanly restarts from a fresh clone, with zero operator
action beyond the restart itself (SC-003).

```sh
# Cancel.
```
Send `/cancel <id>`; confirm process groups/containers are killed, the task is marked
`cancelled`, and its workspace is retained (check `$FOREMAN_HOME/work/runs/<id>/` still exists
immediately after cancellation — the 7-day sweep is step 6).

---

## 5. Telegram Outage and Notification Coalescing (SC-004)

```sh
# Simulate an outage: block outbound HTTPS to api.telegram.org, or revoke the bot token
# temporarily, while a task (or several) complete.
```

Expected: tasks continue running to completion regardless of Telegram reachability. Once
connectivity is restored, queued notifications arrive as a single coalesced digest, not one
message per event — and `outbox_cursors.last_event_id` has advanced past every event that was
actually delivered (`sqlite3 ... "select * from outbox_cursors;"`).

---

## 6. End-to-End Delivery: Draft PR, Then Merge Only on a Bound Approval (User Story 4, SC-006)

**Rollout/deployment-sequencing scenario** — this is the first run that touches real GitHub
state, so verify each sub-step before the next:

1. Run a task with acceptance tests end to end from Telegram (steps 2–3 above, same chat) on a
   **disposable test repo**, not a repo you care about yet.
2. **Given** the task's result passed verification and review, confirm the integrator applied
   the patch to `agent/<task_id>` in the host-side mirror, pushed that branch, and opened a
   **draft** PR containing the spec, review findings, verifier evidence, and cost — and that
   the repo's default branch is untouched (`git log origin/main` shows no new commit).
3. Confirm `deliveries.status = 'pr_open'` and `pr_url` is set.
4. **Only then**, tap **[Merge]**: confirm it requires the bound nonce (same mismatch test as
   step 3 above, now against `deliveries.diff_sha256`) **and** a second confirmation step before
   the merge actually proceeds (FR-016).
5. Confirm a push/PR-creation failure (temporarily revoke `FOREMAN_GITHUB_TOKEN` and retry) is
   classified as an infra/provider failure with backoff, and that the verified patch is neither
   lost nor regenerated — re-run after restoring the token and confirm the same patch is pushed,
   not a freshly re-implemented one.

---

## 7. Retention Sweep (FR-018, SC-007)

```sh
# After a task completes successfully, confirm its run workspace is gone immediately
# (Feature 002 behavior, now also exercised via the daemon's dispatch path):
ls $FOREMAN_HOME/work/runs/<successful-task-run-id>/   # expect: not found

# After the configured retention window elapses (or with a shortened test window),
# confirm a failed-run workspace and a cancelled task's workspace are swept, and that
# structured SQLite rows (tasks, nodes, runs, approvals, deliveries) are NOT deleted.
```
