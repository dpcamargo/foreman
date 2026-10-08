# Contract: Approval Nonce Binding

`internal/core/control.ValidateApproval` is the single function both the Telegram adapter's
inline-button handler and the local API's `/v1/approvals/{id}/respond` endpoint call — one
enforcement point for FR-005/SC-002, not two adapters each reimplementing the check.

```go
// ValidateApproval reports whether a response to approval id, carrying nonce, against the
// subject's current sha256, may be applied. It never mutates state itself.
func ValidateApproval(a Approval, suppliedNonce, currentSubjectSHA256 string) error
```

Rejects (returns a non-nil, specifically-typed error) when:

1. `a.Status != "pending"` — an already-resolved or expired approval can't be resolved twice
   (double-tap / race protection, per spec edge case "two Telegram messages race to approve the
   same gate").
2. `suppliedNonce != a.Nonce` — wrong or replayed nonce (`subtle.ConstantTimeCompare`, not `==`,
   so this isn't a timing side-channel for forging a nonce byte-by-byte).
3. `currentSubjectSHA256 != a.SubjectSHA256` — the artifact changed since the approval was
   requested (a replan produced a new spec, or the diff was regenerated) — this is SC-002's
   exact scenario, caught even though the approval row's `task_id`/`node_id` foreign keys still
   point at the "same" task.

On success, the caller (adapter) is responsible for the actual state transition (mark task
executing, mark delivery merged, etc.) inside the same SQLite transaction that updates
`approvals.status` — `ValidateApproval` itself does no I/O, per Principle X.

**Expiry**: an approval older than the configured unanswered-approval threshold (default 4
hours, per spec Assumptions) does not auto-approve or auto-reject; the daemon's housekeeping
tick instead pings a secondary channel (ntfy) and leaves the approval `pending` indefinitely —
`ValidateApproval` has no time-based rejection branch, because the spec explicitly forbids
auto-resolution on a timeout (FR-006 edge case, constitution Principle VIII).
