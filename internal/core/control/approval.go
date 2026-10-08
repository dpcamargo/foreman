// Package control holds Feature 003's daemon control-plane domain types: approvals bound
// to a nonce and an artifact's sha256, the Telegram allowlist check, and outbox cursor
// advancement. Like every other core package, it imports only the standard library and
// performs no I/O (AGENTS.md Principle X); adapters implement the ports declared here.
package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrApprovalRejected marks a response that must not be applied.
var ErrApprovalRejected = errors.New("approval rejected")

// Approval is one gate that needs a human decision (data-model.md).
type Approval struct {
	ID            string
	TaskID        string
	NodeID        string
	Kind          string // "spec", "deliver", "merge"
	Question      string
	Options       []string
	SubjectSHA256 string // sha256 of the exact artifact being approved
	Nonce         string
	Status        string // "pending", "approved", "rejected", "expired"
	Response      string
	Reason        string
	CreatedAt     time.Time
	RespondedAt   *time.Time
}

// NewNonce returns a 32-byte, hex-encoded random nonce for a new Approval, independent of
// the approval's own id (research.md R5).
func NewNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate approval nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ValidateApproval reports whether a response to approval a, carrying suppliedNonce, against
// the subject's currentSubjectSHA256, may be applied. It never mutates state itself
// (contracts/approval-nonce.md) — the caller performs the actual transition.
func ValidateApproval(a Approval, suppliedNonce, currentSubjectSHA256 string) error {
	if a.Status != "pending" {
		return fmt.Errorf("%w: approval %s is already %s", ErrApprovalRejected, a.ID, a.Status)
	}
	if a.Nonce == "" || suppliedNonce == "" ||
		subtle.ConstantTimeCompare([]byte(a.Nonce), []byte(suppliedNonce)) != 1 {
		return fmt.Errorf("%w: nonce mismatch for approval %s", ErrApprovalRejected, a.ID)
	}
	if a.SubjectSHA256 != currentSubjectSHA256 {
		return fmt.Errorf("%w: artifact changed since approval %s was requested", ErrApprovalRejected, a.ID)
	}
	return nil
}
