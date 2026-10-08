package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// deliverApprovalStore is the persistence subset deliver.go needs: approval CRUD plus
// delivery rows (sqlite.Store implements all of it).
type deliverApprovalStore interface {
	approvalStore
	CreateDelivery(ctx context.Context, taskID, branch, diffSHA256 string, patch []byte) error
	Delivery(ctx context.Context, taskID string) (sqlite.Delivery, error)
	UpdateDeliveryStatus(ctx context.Context, taskID, status, prURL string) error
}

// pusher is the integrator subset deliver.go needs.
type pusher interface {
	OpenDraftPR(ctx context.Context, taskID string, patch []byte) (string, error)
	MergePullRequest(ctx context.Context, taskID string, pullNumber int) error
}

// pendingMerge tracks approvals awaiting T042's second confirmation tap (FR-016: merging
// is never a single tap — the first [Merge] only arms this flag, /confirm or a second tap
// actually merges).
var pendingMerge = map[string]bool{}

// diffSHA256 mirrors integrator.DiffSHA256 (cmd/ can't import an adapter's helper for a
// one-liner; kept byte-identical so it hashes to the same value).
func diffSHA256(patch []byte) string {
	sum := sha256.Sum256(patch)
	return hex.EncodeToString(sum[:])
}

// requestDeliveryApproval runs after RunTask succeeds (T041): it creates the Delivery row
// and a deliver-kind Approval, then calls the integrator to open a draft PR on approval,
// and finally opens a merge-kind Approval bound to the same diff hash (T039/SC-002).
func requestDeliveryApproval(ctx context.Context, store deliverApprovalStore, sup approvalWaiter, sender messageSender, ig pusher, report engine.TaskReport) error {
	if report.Status != task.StatusSucceeded || len(report.Patch) == 0 {
		return nil // only successful runs with a verified patch get delivered
	}
	branch := "agent/" + report.TaskID
	dSHA := diffSHA256(report.Patch)
	if err := store.CreateDelivery(ctx, report.TaskID, branch, dSHA, report.Patch); err != nil {
		return fmt.Errorf("create delivery: %w", err)
	}

	nonce, err := control.NewNonce()
	if err != nil {
		return err
	}
	created, err := store.CreateApproval(ctx, control.Approval{
		TaskID:        report.TaskID,
		Kind:          "deliver",
		Question:      fmt.Sprintf("Deliver %s as a draft PR?", report.TaskID),
		Options:       []string{"approve", "reject"},
		SubjectSHA256: dSHA,
		Nonce:         nonce,
		Status:        "pending",
	})
	if err != nil {
		return fmt.Errorf("create delivery approval: %w", err)
	}

	approved, err := waitApproval(ctx, store, sup, sender, created)
	if err != nil {
		return err
	}
	if !approved {
		_ = store.UpdateDeliveryStatus(ctx, report.TaskID, "failed", "")
		return nil
	}

	prURL, err := ig.OpenDraftPR(ctx, report.TaskID, report.Patch)
	if err != nil {
		_ = store.UpdateDeliveryStatus(ctx, report.TaskID, "failed", "")
		return fmt.Errorf("open draft PR: %w", err)
	}
	if err := store.UpdateDeliveryStatus(ctx, report.TaskID, "pr_open", prURL); err != nil {
		return fmt.Errorf("update delivery status: %w", err)
	}
	if sender != nil {
		_, _ = sender.SendMessage(ctx, telegram.SendMessageRequest{
			Text: "draft PR opened for " + report.TaskID + ": " + prURL,
		})
	}

	// Merge gate (T039/SC-002): bound to the delivered diff's hash, not the task id.
	mergeNonce, err := control.NewNonce()
	if err != nil {
		return err
	}
	mergeApproval, err := store.CreateApproval(ctx, control.Approval{
		TaskID:        report.TaskID,
		Kind:          "merge",
		Question:      fmt.Sprintf("Merge %s?", report.TaskID),
		Options:       []string{"merge"},
		SubjectSHA256: dSHA,
		Nonce:         mergeNonce,
		Status:        "pending",
	})
	if err != nil {
		return fmt.Errorf("create merge approval: %w", err)
	}
	if sender != nil {
		kb := telegram.NewMergeKeyboard(mergeApproval.ID, mergeApproval.Nonce)
		_, _ = sender.SendMessage(ctx, telegram.SendMessageRequest{
			Text:        mergeApproval.Question,
			ReplyMarkup: kb,
		})
	}
	return nil
}

// waitApproval broadcasts an approval request and blocks until resolved — shared by the
// spec and delivery gates (same shape as requestSpecApproval's body).
func waitApproval(ctx context.Context, store approvalStore, sup approvalWaiter, sender messageSender, a control.Approval) (bool, error) {
	if sender != nil {
		ids, err := store.AllowedUserIDs(ctx)
		if err == nil {
			kb := telegram.NewApprovalKeyboard(a.ID, a.Nonce)
			for _, chatID := range ids {
				_, _ = sender.SendMessage(ctx, telegram.SendMessageRequest{
					ChatID:      chatID,
					Text:        a.Question,
					ReplyMarkup: kb,
				})
			}
		}
	}
	return sup.WaitForApproval(ctx, a.ID)
}

// handleMergeCallback handles a [Merge] tap. FR-016: never a single tap — the first tap
// validates the nonce and arms pendingMerge; only a second tap (or /confirm) merges.
func handleMergeCallback(ctx context.Context, store deliverApprovalStore, sup approvalWaiter, ig pusher, cb telegram.CallbackData, sender messageSender) {
	a, err := store.Approval(ctx, cb.ApprovalID)
	if err != nil {
		return
	}
	delivery, err := store.Delivery(ctx, a.TaskID)
	if err != nil {
		return
	}
	if err := control.ValidateApproval(a, cb.Nonce, delivery.DiffSHA256); err != nil {
		return
	}
	if !pendingMerge[a.ID] {
		pendingMerge[a.ID] = true
		if sender != nil {
			_, _ = sender.SendMessage(ctx, telegram.SendMessageRequest{
				Text: "tap [Merge] again (or /confirm " + a.ID + ") to merge " + a.TaskID,
			})
		}
		return
	}
	delete(pendingMerge, a.ID)
	pullNumber, err := pullNumberFromURL(delivery.PRURL)
	if err != nil {
		return
	}
	if err := ig.MergePullRequest(ctx, a.TaskID, pullNumber); err != nil {
		return
	}
	_ = store.ResolveApproval(ctx, a.ID, "approved", "merge", "")
	sup.ResolveApproval(a.ID, true)
}

// pullNumberFromURL extracts the trailing PR number from a GitHub PR html_url.
func pullNumberFromURL(prURL string) (int, error) {
	i := strings.LastIndex(prURL, "/")
	if i < 0 {
		return 0, fmt.Errorf("no PR number in %q", prURL)
	}
	return strconv.Atoi(prURL[i+1:])
}
