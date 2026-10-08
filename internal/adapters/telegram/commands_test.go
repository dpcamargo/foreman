package telegram_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantOK  bool
		wantCmd telegram.Command
	}{
		{
			name:    "task with repo and request",
			text:    "/task https://github.com/x/y.git Add a Sub function",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "task", Args: []string{"https://github.com/x/y.git", "Add a Sub function"}},
		},
		{
			name:    "status with id",
			text:    "/status tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "status", Args: []string{"tsk_123"}},
		},
		{
			name:    "status with no id",
			text:    "/status",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "status", Args: nil},
		},
		{
			name:    "tasks",
			text:    "/tasks",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "tasks", Args: nil},
		},
		{
			name:    "help",
			text:    "/help",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "help", Args: nil},
		},
		{
			name:    "bot-suffixed command",
			text:    "/status@my_bot tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "status", Args: []string{"tsk_123"}},
		},
		{
			name:   "not a command",
			text:   "hello there",
			wantOK: false,
		},
		{
			name:   "empty text",
			text:   "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok := telegram.ParseCommand(tc.text)
			if ok != tc.wantOK {
				t.Fatalf("ParseCommand(%q) ok = %v, want %v", tc.text, ok, tc.wantOK)
			}
			if ok && !reflect.DeepEqual(cmd, tc.wantCmd) {
				t.Errorf("ParseCommand(%q) = %+v, want %+v", tc.text, cmd, tc.wantCmd)
			}
		})
	}
}

type fakeAllowlist struct{ allowed map[int64]bool }

func (f fakeAllowlist) IsAllowed(ctx context.Context, userID int64) (bool, error) {
	return f.allowed[userID], nil
}

func TestDispatcherRejectsNonPrivateChat(t *testing.T) {
	d := telegram.Dispatcher{Allowlist: fakeAllowlist{allowed: map[int64]bool{42: true}}}
	update := telegram.Update{Message: &telegram.Message{
		From: &telegram.User{ID: 42},
		Chat: telegram.Chat{ID: 100, Type: "group"},
		Text: "/tasks",
	}}
	allowed, err := d.Allowed(context.Background(), update)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected a group-chat sender to be rejected, even if allowlisted")
	}
}

func TestDispatcherRejectsNonAllowlistedSender(t *testing.T) {
	d := telegram.Dispatcher{Allowlist: fakeAllowlist{allowed: map[int64]bool{42: true}}}
	update := telegram.Update{Message: &telegram.Message{
		From: &telegram.User{ID: 999},
		Chat: telegram.Chat{ID: 999, Type: "private"},
		Text: "/tasks",
	}}
	allowed, err := d.Allowed(context.Background(), update)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected a non-allowlisted sender to be rejected")
	}
}

func TestDispatcherAllowsPrivateAllowlistedSender(t *testing.T) {
	d := telegram.Dispatcher{Allowlist: fakeAllowlist{allowed: map[int64]bool{42: true}}}
	update := telegram.Update{Message: &telegram.Message{
		From: &telegram.User{ID: 42},
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/tasks",
	}}
	allowed, err := d.Allowed(context.Background(), update)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected a private, allowlisted sender to be allowed")
	}
}
