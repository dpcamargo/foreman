package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/control"
	"github.com/dpcamargo/foreman/internal/core/task"
)

type fakeSender struct {
	sent []telegram.SendMessageRequest
	docs []struct {
		chatID   int64
		filename string
		content  []byte
	}
}

func (f *fakeSender) SendMessage(ctx context.Context, req telegram.SendMessageRequest) (telegram.Message, error) {
	f.sent = append(f.sent, req)
	return telegram.Message{MessageID: int64(len(f.sent))}, nil
}

func (f *fakeSender) SendDocument(ctx context.Context, chatID int64, filename string, content []byte) (telegram.Message, error) {
	f.docs = append(f.docs, struct {
		chatID   int64
		filename string
		content  []byte
	}{chatID, filename, content})
	return telegram.Message{MessageID: int64(len(f.docs))}, nil
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
	if len(sender.sent) != 1 || sender.sent[0].Text != telegram.FreeTextReply {
		t.Fatalf("expected the fixed free-text reply, got %+v", sender.sent)
	}
}

func TestHandleUpdateRejectRequiresReason(t *testing.T) {
	store := newFakeTaskStore()
	store.approvals = map[string]control.Approval{
		"apv_1": {ID: "apv_1", TaskID: "tsk_1", Nonce: "noncevalue", Status: "pending", SubjectSHA256: "deadbeef"},
	}
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/reject tsk_1",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "reason is required") {
		t.Fatalf("expected a prompt for a reason, got %+v", sender.sent)
	}
	if store.approvals["apv_1"].Status != "pending" {
		t.Error("expected the approval to remain pending without a reason")
	}
}

func TestHandleUpdateRejectWithReasonResolves(t *testing.T) {
	store := newFakeTaskStore()
	store.approvals = map[string]control.Approval{
		"apv_1": {ID: "apv_1", TaskID: "tsk_1", Nonce: "noncevalue", Status: "pending", SubjectSHA256: "deadbeef"},
	}
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/reject tsk_1 flaky test",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "rejected") {
		t.Fatalf("expected a 'rejected' reply, got %+v", sender.sent)
	}
	got := store.approvals["apv_1"]
	if got.Status != "rejected" || got.Reason != "flaky test" {
		t.Errorf("expected a resolved+reasoned approval, got %+v", got)
	}
}

func TestHandleCallbackQueryApprove(t *testing.T) {
	store := newFakeTaskStore()
	store.approvals = map[string]control.Approval{
		"apv_1": {ID: "apv_1", TaskID: "tsk_1", Nonce: "noncevalue", Status: "pending", SubjectSHA256: "deadbeef"},
	}
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID:      "cbq_1",
		Message: &telegram.Message{Chat: telegram.Chat{ID: 42, Type: "private"}},
		Data:    "apv_1|noncevalue|approve",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(sender.sent) != 1 || sender.sent[0].Text != "approved" {
		t.Fatalf("expected an 'approved' reply, got %+v", sender.sent)
	}
	if store.approvals["apv_1"].Status != "approved" {
		t.Errorf("expected the approval resolved, got %+v", store.approvals["apv_1"])
	}
}

func TestHandleCallbackQueryWrongNonceRejected(t *testing.T) {
	store := newFakeTaskStore()
	store.approvals = map[string]control.Approval{
		"apv_1": {ID: "apv_1", TaskID: "tsk_1", Nonce: "noncevalue", Status: "pending", SubjectSHA256: "deadbeef"},
	}
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID:      "cbq_1",
		Message: &telegram.Message{Chat: telegram.Chat{ID: 42, Type: "private"}},
		Data:    "apv_1|wrong-nonce|approve",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if store.approvals["apv_1"].Status != "pending" {
		t.Errorf("expected the approval to remain pending after a wrong-nonce tap, got %+v", store.approvals["apv_1"])
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "could not apply") {
		t.Fatalf("expected an error reply, got %+v", sender.sent)
	}
}

func TestHandleUpdatePauseGraceful(t *testing.T) {
	store := newFakeTaskStore()
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/pause tsk_1",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(disp.pausedGraceful) != 1 || disp.pausedGraceful[0] != "tsk_1" {
		t.Errorf("expected graceful pause called, got %+v", disp.pausedGraceful)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "paused 1 task") {
		t.Fatalf("expected a pause confirmation, got %+v", sender.sent)
	}
}

func TestHandleUpdatePauseAll(t *testing.T) {
	store := newFakeTaskStore()
	now := time.Now().UTC()
	store.tasks = append(store.tasks,
		task.Task{ID: "tsk_1", Status: task.StatusRunning, CreatedAt: now},
		task.Task{ID: "tsk_2", Status: task.StatusPending, CreatedAt: now},
		task.Task{ID: "tsk_3", Status: task.StatusSucceeded, CreatedAt: now}, // terminal, excluded
	)
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/pause all",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(disp.pausedGraceful) != 2 {
		t.Errorf("expected 2 non-terminal tasks paused, got %+v", disp.pausedGraceful)
	}
}

func TestHandleUpdateCancel(t *testing.T) {
	store := newFakeTaskStore()
	now := time.Now().UTC()
	store.tasks = append(store.tasks, task.Task{
		ID: "tsk_1", TaskType: task.TaskTypeCode, RepoURL: "/r.git",
		BaseCommit: "0123456789abcdef0123456789abcdef01234567", RequestText: "x",
		Status: task.StatusRunning, CreatedAt: now, UpdatedAt: now,
	})
	sender := &fakeSender{}
	disp := &fakeDispatcher{}

	u := telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: "private"},
		Text: "/cancel tsk_1",
	}}
	handleUpdate(context.Background(), store, disp, sender, u)

	if len(disp.cancelled) != 1 || disp.cancelled[0] != "tsk_1" {
		t.Errorf("expected Cancel called, got %+v", disp.cancelled)
	}
	got, _ := store.Task(context.Background(), "tsk_1")
	if got.Status != task.StatusCancelled {
		t.Errorf("expected status cancelled, got %s", got.Status)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "cancelled") {
		t.Fatalf("expected a cancellation confirmation, got %+v", sender.sent)
	}
}
