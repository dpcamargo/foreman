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
			name:    "reject with id and reason",
			text:    "/reject tsk_123 tests still failing",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "reject", Args: []string{"tsk_123", "tests still failing"}},
		},
		{
			name:    "reject with id but no reason",
			text:    "/reject tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "reject", Args: []string{"tsk_123"}},
		},
		{
			name:    "answer with id and text",
			text:    "/answer tsk_123 use the v2 API",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "answer", Args: []string{"tsk_123", "use the v2 API"}},
		},
		{
			name:    "pause with id",
			text:    "/pause tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "pause", Args: []string{"tsk_123"}},
		},
		{
			name:    "pause with id and now",
			text:    "/pause tsk_123 now",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "pause", Args: []string{"tsk_123", "now"}},
		},
		{
			name:    "pause all",
			text:    "/pause all",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "pause", Args: []string{"all"}},
		},
		{
			name:    "pause all now",
			text:    "/pause all now",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "pause", Args: []string{"all", "now"}},
		},
		{
			name:    "resume with id",
			text:    "/resume tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "resume", Args: []string{"tsk_123"}},
		},
		{
			name:    "cancel with id",
			text:    "/cancel tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "cancel", Args: []string{"tsk_123"}},
		},
		{
			name:    "logs with id and node",
			text:    "/logs tsk_123 impl",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "logs", Args: []string{"tsk_123", "impl"}},
		},
		{
			name:    "logs with id only",
			text:    "/logs tsk_123",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "logs", Args: []string{"tsk_123"}},
		},
		{
			name:    "budget",
			text:    "/budget",
			wantOK:  true,
			wantCmd: telegram.Command{Name: "budget", Args: nil},
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

// TestFreeTextReplyIsTheFixedContractString locks in the exact wording
// contracts/telegram-commands.md specifies: free text is never parsed for intent, and the
// reply never varies by phrasing (FR-006).
func TestFreeTextReplyIsTheFixedContractString(t *testing.T) {
	if telegram.FreeTextReply != "chat isn't enabled yet; use /help" {
		t.Errorf("unexpected FreeTextReply: %q", telegram.FreeTextReply)
	}
	if _, ok := telegram.ParseCommand("approve everything"); ok {
		t.Error("free text must never parse as a command, no matter the phrasing")
	}
}
