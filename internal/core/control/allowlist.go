package control

import "context"

// AllowlistStore is the persistence subset an Allowlist needs. Adapter:
// internal/adapters/sqlite.
type AllowlistStore interface {
	IsAllowedUser(ctx context.Context, userID int64) (bool, error)
}

// Allowlist checks a numeric Telegram user id against the authorized operator list
// (spec FR-003).
type Allowlist struct {
	Store AllowlistStore
}

// IsAllowed reports whether userID may issue commands. A zero id is never allowed.
func (a Allowlist) IsAllowed(ctx context.Context, userID int64) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	return a.Store.IsAllowedUser(ctx, userID)
}
