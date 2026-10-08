# Quickstart: Validating the Durable Execution Loop (Feature 002)

Runnable checks proving Feature 002 functionality end-to-end. Run from the repository root.

---

## 0. Prerequisites

```sh
go version                          # go1.27.x
docker context show && docker info  # Colima running (4 CPU / 6 GiB)
codex --version                     # logged in
go build -o bin/fm ./cmd/fm
```

Ensure test fixture repos exist:
```sh
./scripts/fixtures.sh
```

---
## 1. Unit & Architecture Tests (no network, no containers)

```sh
gofmt -l . ; go vet ./... ; go test ./...
```

Expected: zero gofmt output, all tests pass, including `internal/archtest` proving `internal/core/task` and `internal/core/engine` import no adapters.

---
## 2. Run a Task End-to-End (User Story 1, SC-001)

Run a task with acceptance criteria on `addrepo`:

```sh
bin/fm run testdata/fixtures/addrepo/task.yaml
```

Expected:
- Produces a `TaskSpec` validated against base commit.
- Executes implementer attempt in fresh disposable clone.
- Runs offline containerized verification pass with hidden tests.
- Runs cross-family review pass.
- Emits task report `state/reports/rpt_<id>.{md,json}` and records task in `records` table.
- Exits 0 with verified success.

---
## 3. Trust "Done" Only via Container Verification (User Story 2, SC-003)

Run a task where the implementation passes visible tests but fails a hidden test:

```sh
bin/fm run testdata/fixtures/addrepo/task_failing_hidden.yaml
```

Expected:
- Hidden test failure is recorded in `VerifyReport`.
- Node does NOT advance to succeeded.
- The engine triggers Rung 1 escalation with failure evidence without leaking hidden test code.

---
## 4. Diff Policy Violation Rejection (FR-006, SC-003)

Run verification against a patch that deletes an existing test or edits a protected path (`.foreman/project.yaml`):

```sh
bin/fm run testdata/fixtures/addrepo/task_policy_violation.yaml
```

Expected:
- Fails before running tests on diff-policy ground.
- State records `failure_class: policy`.
- Does not loop; escalates directly or exits 1.

---
## 5. Escalation Ladder Progression (FR-008, FR-011)

Run a difficult task that fails initial attempt:

```sh
bin/fm run testdata/fixtures/ambiguous/task.yaml
```

Expected:
- Attempt 1 fails on Rung 1.
- Rung 1 retries same route with evidence.
- If loop guard triggers (signature repeated or pass count flat), escalates immediately to Rung 2 (different model family, no prior diff).
- If Rung 2 fails, marks task `blocked_human` (Rung 3) with full diagnostic report.

---
## 6. Crash Safety & Lost-Run Recovery (User Story 3, SC-002)

Simulate a crash mid-run:

```sh
bin/fm run testdata/fixtures/addrepo/task.yaml & PID=$!; sleep 8; kill -9 $PID
bin/fm run --resume
```

Expected:
- Reconciler sweep detects dead PID via process start time fingerprint.
- Marks previous run as lost (`infra_error`).
- Retries the interrupted attempt cleanly from a fresh clone without corrupting SQLite state.

---
## 7. Replay Through Arm H1 (FR-022, SC-004)

Replay a recorded task using foreman's durable loop arm (`H1`):

```sh
bin/fm eval replay --record rec_... --arm h1 --k 1
```

Expected:
- Replays the task through the full execution loop.
- Produces standardized grade and usage metrics directly comparable to baseline arms A0, A1, A2.
