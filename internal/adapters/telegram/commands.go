package telegram

import (
	"context"
	"strings"
)

// Command is a parsed Telegram bot command: "/task <repo> <text>" becomes
// Command{Name: "task", Args: []string{"<repo>", "<text>"}}, where the last arg absorbs
// the rest of the line verbatim (a task request, a rejection reason, etc. may contain
// spaces).
type Command struct {
	Name string
	Args []string
}

// commandsWithFreeformLastArg absorbs everything after their first argument into one
// string, instead of splitting on every space (e.g. "/task <repo> fix the thing please").
var commandsWithFreeformLastArg = map[string]bool{
	"task":   true,
	"reject": true,
	"answer": true,
}

// FreeTextReply is the fixed reply for anything that doesn't parse as a command
// (contracts/telegram-commands.md: until Feature 007 wires in the Hermes relay, free text
// is never interpreted as an approval, rejection, or policy change — enforced structurally,
// since it never reaches ParseCommand's "ok" branch at all, not by a denylist of phrases).
const FreeTextReply = "chat isn't enabled yet; use /help"

// ParseCommand parses text as a Telegram bot command. It returns ok=false for anything that
// doesn't start with "/" (FR-002: no LLM between an update and the action it takes — a
// plain text message is never interpreted as a command).
func ParseCommand(text string) (Command, bool) {
	if !strings.HasPrefix(text, "/") {
		return Command{}, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, false
	}
	name := strings.TrimPrefix(fields[0], "/")
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at] // "/status@my_bot" -> "status"
	}
	if name == "" {
		return Command{}, false
	}

	var args []string
	if len(fields) > 1 {
		if commandsWithFreeformLastArg[name] && len(fields) > 2 {
			args = []string{fields[1], strings.Join(fields[2:], " ")}
		} else {
			args = fields[1:]
		}
	}
	return Command{Name: name, Args: args}, true
}

// AllowlistChecker is the subset Dispatcher needs from internal/core/control.Allowlist
// (declared here, not imported, so this package stays a pure adapter over the Bot API —
// control's own package already owns the IsAllowed rule).
type AllowlistChecker interface {
	IsAllowed(ctx context.Context, userID int64) (bool, error)
}

// Dispatcher gates which updates may reach a command handler (spec FR-003).
type Dispatcher struct {
	Allowlist AllowlistChecker
}

// Allowed reports whether update's sender may issue commands: the chat must be private and
// the sender must be allowlisted. A rejected sender gets no reply at all (FR-003) — the
// caller must not send anything when Allowed returns false.
func (d Dispatcher) Allowed(ctx context.Context, u Update) (bool, error) {
	if u.Message == nil || u.Message.From == nil {
		return false, nil
	}
	if u.Message.Chat.Type != "private" {
		return false, nil
	}
	return d.Allowlist.IsAllowed(ctx, u.Message.From.ID)
}
