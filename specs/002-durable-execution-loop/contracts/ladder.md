# Contract: Escalation Ladder & Loop Guards (Feature 002)

Based on ARCHITECTURE.md §7.5 and spec FR-008, FR-009, FR-011.

## Escalation Rungs

### Rung 1: Retry Same Route with Evidence
- **Trigger**: First attempt failure (tests failed, compilation error, or recoverable lint error).
- **Route**: Same model and prompt template as attempt 1.
- **Context**: Spec + Failure Evidence.
  - Failure Evidence contains: failing test names, assertion failure messages, compiler errors.
  - **Redaction Rule**: Hidden test file paths and hidden test source code are strictly withheld from the prompt.
- **Workspace**: Fresh clone from base commit (no dirty state carries over).

### Rung 2: Escalate to Different Model Family
- **Trigger**: Second consecutive failure on Rung 1, OR loop guard triggered during Rung 1.
- **Route**: Alternate model family.
  - If Rung 1 was OpenAI (`codex`), Rung 2 is Anthropic (`claude` via Hermes).
  - If Rung 1 was Anthropic (`claude`), Rung 2 is OpenAI (`codex`).
- **Context**: Fresh start from `TaskSpec` plus a concise high-level failure summary (e.g. "Previous attempt failed on edge cases with negative numbers").
- **No Anchoring Rule**: The patch or diff from Rung 1 is **NEVER** provided to Rung 2.
- **Workspace**: Fresh clone from base commit.

### Rung 3: Human Attention (`blocked_human`)
- **Trigger**: Failure on Rung 2, OR unrecoverable policy failure, OR budget exhausted.
- **Action**: Task transitions to `blocked_human`.
- **Evidence**: Complete run audit, error signatures, diffs, and verification reports saved to `$FOREMAN_HOME/state/reports/rpt_<id>.{md,json}`.

---

## Failure Classification (FR-009)

| Class | Causes | Ladder Action |
|---|---|---|
| `infra` | Docker daemon error, disk space, dead PID / lost process | Do not advance ladder; retry cleanly |
| `provider` | API 429 rate limit, 503 outage, network timeout | Do not advance ladder; exponential backoff & retry |
| `attempt` | Unit test failed, compiler error, syntax error | Advance ladder |
| `policy` | Test file deleted, protected path touched, secret detected | Advance ladder directly to Rung 3 |

---

## Loop Guards (FR-011)

The engine evaluates three loop guards before scheduling a Rung 1 retry:

1. **Signature Repetition**:
   `sig = sha256(sort(failing_test_names) + error_class)`
   If `sig[attempt_N] == sig[attempt_N-1]`, the agent made no diagnostic progress. Skip immediately to Rung 2.

2. **Diff Hash Oscillation**:
   `diff_hash = sha256(patch)`
   If `diff_hash[attempt_N] == diff_hash[attempt_M]` for any prior attempt M, an oscillation loop is detected. Skip immediately to Rung 2.

3. **Pass Count Flatline**:
   If `passed_tests_count[attempt_N] <= passed_tests_count[attempt_N-1]` across two attempts, progress has stalled. Escalate immediately to Rung 2.
