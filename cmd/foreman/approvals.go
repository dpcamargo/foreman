package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/engine"
)

// approvalStore is the persistence subset approvals.go needs.
type approvalStore interface {
	CreateApproval(ctx context.Context, a control.Approval) (control.Approval, error)
	Approval(ctx context.Context, id string) (control.Approval, error)
	ResolveApproval(ctx context.Context, id, status, response, reason string) error
	AllowedUserIDs(ctx context.Context) ([]int64, error)
}

// approvalWaiter is the Supervisor subset the spec-approval gate needs.
type approvalWaiter interface {
	WaitForApproval(ctx context.Context, approvalID string) (bool, error)
	ResolveApproval(approvalID string, approved bool)
}

// specSubjectSHA256 hashes a TaskSpec's canonical JSON form, so a later resolution attempt
// can be checked against exactly what was approved (contracts/approval-nonce.md).
func specSubjectSHA256(spec engine.TaskSpec) (string, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal spec for approval hash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// requestSpecApproval is engine.Config.ApprovalGate's body (research.md R7): it creates the
// Approval row, notifies every allowlisted Telegram user with bound Approve/Reject buttons
// (sender may be nil if Telegram isn't configured — the approval can still be resolved
// through the local API), and blocks on sup.WaitForApproval until a Telegram callback or a
// local API respond call resolves it.
//
// serve.go wires this into a closure rather than passing sup by value directly: the
// Supervisor doesn't exist yet when engine.Config is built (NewSupervisor builds the Engine
// from it immediately), so the closure captures a *engine.Supervisor variable that is only
// assigned after NewSupervisor returns — by the time this function is actually called
// (during a later Dispatch), that variable already holds the real Supervisor.
//
// spec.md's Assumptions make every Telegram/local-API-created task go through this gate —
// Feature 002 has no concept of "lanes" that would decide otherwise yet.
func requestSpecApproval(ctx context.Context, store approvalStore, sup approvalWaiter, sender messageSender, taskID string, spec engine.TaskSpec) (bool, error) {
	subjectSHA, err := specSubjectSHA256(spec)
	if err != nil {
		return false, err
	}
	nonce, err := control.NewNonce()
	if err != nil {
		return false, err
	}
	created, err := store.CreateApproval(ctx, control.Approval{
		TaskID:        taskID,
		Kind:          "spec",
		Question:      fmt.Sprintf("Approve spec for %s?\n\n%s", taskID, spec.Summary),
		Options:       []string{"approve", "reject"},
		SubjectSHA256: subjectSHA,
		Nonce:         nonce,
		Status:        "pending",
	})
	if err != nil {
		return false, fmt.Errorf("create spec approval: %w", err)
	}

	if sender != nil {
		ids, err := store.AllowedUserIDs(ctx)
		if err == nil {
			kb := telegram.NewApprovalKeyboard(created.ID, nonce)
			for _, chatID := range ids {
				_, _ = sender.SendMessage(ctx, telegram.SendMessageRequest{
					ChatID:      chatID,
					Text:        created.Question,
					ReplyMarkup: kb,
				})
			}
		}
	}

	return sup.WaitForApproval(ctx, created.ID)
}

// resolveApproval is the single enforcement point contracts/approval-nonce.md requires:
// both the Telegram callback handler and POST /v1/approvals/{id}/respond call this, and
// only this, to apply a decision.
//
// currentSubjectSHA256 for a spec approval is, today, always the value stored on the
// approval row itself: nothing in this feature keeps a separately mutable copy of the spec
// around to compare against (the TaskSpec only exists on the engine's call stack while
// WaitForApproval blocks). The check still runs — and will have real teeth once a deliver
// approval's current diff hash can differ from what was originally requested (T041, a later
// task) — rather than being skipped for spec approvals specifically.
func resolveApproval(ctx context.Context, store approvalStore, sup approvalWaiter, approvalID, suppliedNonce, decision, reason string) error {
	if decision != "approve" && decision != "reject" {
		return fmt.Errorf("invalid decision %q", decision)
	}
	a, err := store.Approval(ctx, approvalID)
	if err != nil {
		return fmt.Errorf("load approval %s: %w", approvalID, err)
	}
	if err := control.ValidateApproval(a, suppliedNonce, a.SubjectSHA256); err != nil {
		return err
	}

	status := "approved"
	if decision == "reject" {
		status = "rejected"
	}
	if err := store.ResolveApproval(ctx, approvalID, status, decision, reason); err != nil {
		return fmt.Errorf("resolve approval %s: %w", approvalID, err)
	}
	sup.ResolveApproval(approvalID, decision == "approve")
	return nil
}
