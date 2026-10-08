package control_test

import (
	"context"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/control"
)

type fakeAllowlistStore struct{ allowed map[int64]bool }

func (f fakeAllowlistStore) IsAllowedUser(ctx context.Context, userID int64) (bool, error) {
	return f.allowed[userID], nil
}

func TestAllowlistIsAllowed(t *testing.T) {
	store := fakeAllowlistStore{allowed: map[int64]bool{12345: true}}
	a := control.Allowlist{Store: store}

	allowed, err := a.IsAllowed(context.Background(), 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowlisted id to be allowed")
	}

	allowed, err = a.IsAllowed(context.Background(), 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected unknown id to be rejected")
	}

	allowed, err = a.IsAllowed(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected zero id to be rejected")
	}
}
