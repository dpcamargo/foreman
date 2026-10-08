package main

import (
	"context"
	"strings"
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
)

type fakeSender struct {
	sent []telegram.SendMessageRequest
}

func (f *fakeSender) SendMessage(ctx context.Context, req telegram.SendMessageRequest) (telegram.Message, error) {
	f.sent = append(f.sent, req)
	return telegram.Message{MessageID: int64(len(f.sent))}, nil
}

func TestHandleUpdateTaskCommand(t *testing.T) {
	origResolve := resolveBaseCommit
	resolveBaseCommit = func(ctx context.Context, repoURL string) (string, error) {
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	defer func() { resolveBaseCommit = origResolve }()

	store := newFakeTaskStore()
	disp := &fakeDispatcher{}
	sender := &fakeSender{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/task /r.git Add a Sub function",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "created") {
		t.Fatalf("expected a 'created tsk_...' reply, got %+v", sender.sent)
	}
	if len(disp.dispatched) != 1 {
		t.Fatalf("expected the task to be dispatched, got %+v", disp.dispatched)
	}
}

func TestHandleUpdateStatusWithNoTasks(t *testing.T) {
	store := newFakeTaskStore()
	sender := &fakeSender{}
	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/status",
	}}
	handleUpdate(context.Background(), store, &fakeDispatcher{}, sender, u)
	if len(sender.sent) != 1 || sender.sent[0].Text != "no tasks yet" {
		t.Fatalf("expected 'no tasks yet', got %+v", sender.sent)
	}
}

func TestHandleUpdateTasksEmpty(t *testing.T) {
	store := newFakeTaskStore()
	sender := &fakeSender{}
	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/tasks",
	}}
	handleUpdate(context.Background(), store, &fakeDispatcher{}, sender, u)
	if len(sender.sent) != 1 || sender.sent[0].Text != "no tasks yet" {
		t.Fatalf("expected 'no tasks yet', got %+v", sender.sent)
	}
}

func TestHandleUpdateHelp(t *testing.T) {
	store := newFakeTaskStore()
	sender := &fakeSender{}
	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/help",
	}}
	handleUpdate(context.Background(), store, &fakeDispatcher{}, sender, u)
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "/task") {
		t.Fatalf("expected help text, got %+v", sender.sent)
	}
}

func TestHandleUpdateNotACommand(t *testing.T) {
	store := newFakeTaskStore()
	sender := &fakeSender{}
	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "hello there",
	}}
	handleUpdate(context.Background(), store, &fakeDispatcher{}, sender, u)
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "/help") {
		t.Fatalf("expected a hint about /help, got %+v", sender.sent)
	}
}
