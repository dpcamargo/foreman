package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/control"
)

// messageSender is the subset of *telegram.Client handleUpdate needs, declared locally so
// tests can use a fake instead of a real Bot API call.
type messageSender interface {
	SendMessage(ctx context.Context, req telegram.SendMessageRequest) (telegram.Message, error)
	SendDocument(ctx context.Context, chatID int64, filename string, content []byte) (telegram.Message, error)
}

// fullStore is everything handleUpdate needs from the store: task CRUD/listing plus
// approval and delivery lookup (for /diff and merge callbacks).
type fullStore interface {
	taskStore
	deliverApprovalStore
	approvalResolver
}

// dispatcherApprovals is everything handleUpdate needs to dispatch and resolve approvals.
type dispatcherApprovals interface {
	dispatcher
	approvalWaiter
	supervisorControl
}

// approvalResolver is the subset approvals.go's resolveApproval needs, used by both
// Telegram and the local API — declared here so handleUpdate's switch can call it directly.
type approvalResolver interface {
	PendingApprovalForTask(ctx context.Context, taskID string) (control.Approval, error)
}

// handleUpdate routes one Telegram update to the matching command, replying through
// sender. Commands from a non-private chat or a non-allowlisted sender are silently
// dropped before this function is ever called (Dispatcher.Allowed, FR-003); this function
// assumes the caller already checked that.
func handleUpdate(ctx context.Context, store fullStore, sup dispatcherApprovals, sender messageSender, u telegram.Update) {
	if u.CallbackQuery != nil {
		handleCallbackQuery(ctx, store, sup, sender, u.CallbackQuery)
		return
	}
	if u.Message == nil {
		return
	}
	chatID := u.Message.Chat.ID
	cmd, ok := telegram.ParseCommand(u.Message.Text)
	if !ok {
		reply(ctx, sender, chatID, telegram.FreeTextReply)
		return
	}

	switch cmd.Name {
	case "task":
		if len(cmd.Args) < 2 {
			reply(ctx, sender, chatID, "usage: /task <repo> <request text>")
			return
		}
		tsk, err := createTask(ctx, store, sup, cmd.Args[0], cmd.Args[1])
		if err != nil {
			reply(ctx, sender, chatID, "could not create task: "+err.Error())
			return
		}
		reply(ctx, sender, chatID, fmt.Sprintf("created %s (pending)", tsk.ID))

	case "status":
		id := ""
		if len(cmd.Args) > 0 {
			id = cmd.Args[0]
		} else if latest, err := store.RecentTasks(ctx, 1); err == nil && len(latest) == 1 {
			id = latest[0].ID
		}
		if id == "" {
			reply(ctx, sender, chatID, "no tasks yet")
			return
		}
		tsk, err := store.Task(ctx, id)
		if err != nil {
			reply(ctx, sender, chatID, "unknown task: "+id)
			return
		}
		nodes, _ := store.NodesForTask(ctx, id)
		reply(ctx, sender, chatID, formatTaskStatus(tsk, nodes))

	case "tasks":
		tasks, err := store.RecentTasks(ctx, 20)
		if err != nil {
			reply(ctx, sender, chatID, "could not list tasks: "+err.Error())
			return
		}
		reply(ctx, sender, chatID, formatTaskList(nonTerminalTasks(tasks)))

	case "pause":
		if len(cmd.Args) == 0 {
			reply(ctx, sender, chatID, "usage: /pause <id|all> [now]")
			return
		}
		now := len(cmd.Args) > 1 && cmd.Args[1] == "now"
		target := cmd.Args[0]
		ids := []string{target}
		if target == "all" {
			all, err := store.RecentTasks(ctx, 0)
			if err != nil {
				reply(ctx, sender, chatID, "could not list tasks: "+err.Error())
				return
			}
			ids = ids[:0]
			for _, t := range nonTerminalTasks(all) {
				ids = append(ids, t.ID)
			}
		}
		for _, id := range ids {
			if now {
				if err := pauseTaskNow(ctx, store, sup, id); err != nil {
					reply(ctx, sender, chatID, fmt.Sprintf("could not pause %s: %v", id, err))
					continue
				}
			} else {
				pauseTaskGraceful(sup, id)
			}
		}
		reply(ctx, sender, chatID, fmt.Sprintf("paused %d task(s)", len(ids)))

	case "resume":
		if len(cmd.Args) == 0 {
			reply(ctx, sender, chatID, "usage: /resume <id>")
			return
		}
		if err := resumeTask(ctx, store, sup, cmd.Args[0]); err != nil {
			reply(ctx, sender, chatID, "could not resume: "+err.Error())
			return
		}
		reply(ctx, sender, chatID, "resumed "+cmd.Args[0])

	case "cancel":
		if len(cmd.Args) == 0 {
			reply(ctx, sender, chatID, "usage: /cancel <id>")
			return
		}
		if err := cancelTask(ctx, store, sup, cmd.Args[0]); err != nil {
			reply(ctx, sender, chatID, "could not cancel: "+err.Error())
			return
		}
		reply(ctx, sender, chatID, "cancelled "+cmd.Args[0])

	case "reject":
		if len(cmd.Args) == 0 {
			reply(ctx, sender, chatID, "usage: /reject <id> <reason>")
			return
		}
		if len(cmd.Args) < 2 {
			reply(ctx, sender, chatID, "a reason is required: /reject "+cmd.Args[0]+" <reason>")
			return
		}
		taskID, reason := cmd.Args[0], cmd.Args[1]
		a, err := store.PendingApprovalForTask(ctx, taskID)
		if err != nil {
			reply(ctx, sender, chatID, "no pending approval for "+taskID)
			return
		}
		if err := resolveApproval(ctx, store, sup, a.ID, a.Nonce, "reject", reason); err != nil {
			reply(ctx, sender, chatID, "could not reject: "+err.Error())
			return
		}
		reply(ctx, sender, chatID, "rejected "+taskID)

	case "answer":
		if len(cmd.Args) < 2 {
			reply(ctx, sender, chatID, "usage: /answer <id> <text>")
			return
		}
		// Feature 002's engine has no mechanic yet for resuming a blocked_human task with
		// new evidence and re-dispatching it — that is a gap for a later feature, not
		// something this command can honestly satisfy today.
		reply(ctx, sender, chatID, "answer recorded, but automatic resume isn't wired yet; use /task to start a corrected attempt")

	case "diff":
		if len(cmd.Args) == 0 {
			reply(ctx, sender, chatID, "usage: /diff <id>")
			return
		}
		d, err := store.Delivery(ctx, cmd.Args[0])
		if err != nil {
			reply(ctx, sender, chatID, "no delivery for "+cmd.Args[0])
			return
		}
		if _, err := sender.SendDocument(ctx, chatID, cmd.Args[0]+".patch", d.Patch); err != nil {
			reply(ctx, sender, chatID, "could not send diff: "+err.Error())
		}

	case "help":
		reply(ctx, sender, chatID, helpText)

	default:
		reply(ctx, sender, chatID, "unknown command; try /help")
	}
}

// mergePusher is set once in serve.go alongside dispatchOnDone (same reason: the
// integrator doesn't exist at package-init time). nil means merge callbacks are ignored.
var mergePusher pusher

// handleCallbackQuery applies an inline-button tap: [Approve] or [Reject] on a spec
// approval. The nonce in the callback data is the single enforcement point
// (contracts/approval-nonce.md) — the chat/allowlist check already happened in Dispatcher,
// but the button itself is additionally bound to the exact approval it was issued for.
func handleCallbackQuery(ctx context.Context, store fullStore, sup approvalWaiter, sender messageSender, cq *telegram.CallbackQuery) {
	cb, ok := telegram.ParseCallbackData(cq.Data)
	if !ok {
		return
	}
	if cb.Decision == "merge" {
		handleMergeCallback(ctx, store, sup, mergePusher, cb, sender)
		return
	}
	err := resolveApproval(ctx, store, sup, cb.ApprovalID, cb.Nonce, cb.Decision, "")
	if cq.Message == nil {
		return
	}
	text := "approved"
	if cb.Decision == "reject" {
		text = "rejected"
	}
	if err != nil {
		text = "could not apply: " + err.Error()
	}
	reply(ctx, sender, cq.Message.Chat.ID, text)
}

const helpText = `commands:
/task <repo> <request text>   create a task
/status [id]                  show a task's progress (omit id for the most recent)
/tasks                        list recent tasks
/diff <id>                    send a delivered task's patch as a .patch file
/help                         show this help`

func reply(ctx context.Context, sender messageSender, chatID int64, text string) {
	if _, err := sender.SendMessage(ctx, telegram.SendMessageRequest{ChatID: chatID, Text: text}); err != nil {
		slog.Error("telegram: send message failed", "chat_id", chatID, "error", err)
	}
}
