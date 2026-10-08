package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
)

// messageSender is the subset of *telegram.Client handleUpdate needs, declared locally so
// tests can use a fake instead of a real Bot API call.
type messageSender interface {
	SendMessage(ctx context.Context, req telegram.SendMessageRequest) (telegram.Message, error)
}

// handleUpdate routes one Telegram update to the matching command, replying through
// sender. Commands from a non-private chat or a non-allowlisted sender are silently
// dropped before this function is ever called (Dispatcher.Allowed, FR-003); this function
// assumes the caller already checked that.
func handleUpdate(ctx context.Context, store taskStore, sup dispatcher, sender messageSender, u telegram.Update) {
	if u.Message == nil {
		return // callback queries are handled separately once approvals land (T029)
	}
	chatID := u.Message.Chat.ID
	cmd, ok := telegram.ParseCommand(u.Message.Text)
	if !ok {
		reply(ctx, sender, chatID, "not a command; try /help")
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
		reply(ctx, sender, chatID, formatTaskList(tasks))

	case "help":
		reply(ctx, sender, chatID, helpText)

	default:
		reply(ctx, sender, chatID, "unknown command; try /help")
	}
}

const helpText = `commands:
/task <repo> <request text>   create a task
/status [id]                  show a task's progress (omit id for the most recent)
/tasks                        list recent tasks
/help                         show this help`

func reply(ctx context.Context, sender messageSender, chatID int64, text string) {
	if _, err := sender.SendMessage(ctx, telegram.SendMessageRequest{ChatID: chatID, Text: text}); err != nil {
		slog.Error("telegram: send message failed", "chat_id", chatID, "error", err)
	}
}
