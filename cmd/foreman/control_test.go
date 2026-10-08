package main

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func newRunningTestTask(t *testing.T, store *fakeTaskStore, id string) task.Task {
	t.Helper()
	now := time.Now().UTC()
	tsk := task.Task{
		ID: id, TaskType: task.TaskTypeCode, RepoURL: "/r.git",
		BaseCommit: "0123456789abcdef0123456789abcdef01234567", RequestText: "x",
		Status: task.StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(context.Background(), tsk, nil); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return tsk
}

func TestPauseTaskGraceful(t *testing.T) {
	sup := &fakeDispatcher{}
	pauseTaskGraceful(sup, "tsk_1")
	if len(sup.pausedGraceful) != 1 || sup.pausedGraceful[0] != "tsk_1" {
		t.Errorf("expected PauseGraceful called with tsk_1, got %+v", sup.pausedGraceful)
	}
}

func TestPauseTaskNowCancelsAndMarksPaused(t *testing.T) {
	store := newFakeTaskStore()
	newRunningTestTask(t, store, "tsk_1")
	sup := &fakeDispatcher{}

	if err := pauseTaskNow(context.Background(), store, sup, "tsk_1"); err != nil {
		t.Fatalf("pauseTaskNow: %v", err)
	}
	if len(sup.cancelled) != 1 || sup.cancelled[0] != "tsk_1" {
		t.Errorf("expected Cancel called, got %+v", sup.cancelled)
	}
	got, _ := store.Task(context.Background(), "tsk_1")
	if got.Status != task.StatusPaused {
		t.Errorf("expected status paused, got %s", got.Status)
	}
}

func TestPauseTaskNowRejectsNonRunningTask(t *testing.T) {
	store := newFakeTaskStore()
	tsk := newRunningTestTask(t, store, "tsk_1")
	tsk.Status = task.StatusSucceeded
	store.tasks[0] = tsk
	sup := &fakeDispatcher{}

	if err := pauseTaskNow(context.Background(), store, sup, "tsk_1"); err == nil {
		t.Error("expected an error pausing an already-succeeded task")
	}
}

func TestCancelTaskCancelsAndMarksCancelled(t *testing.T) {
	store := newFakeTaskStore()
	newRunningTestTask(t, store, "tsk_1")
	sup := &fakeDispatcher{}

	if err := cancelTask(context.Background(), store, sup, "tsk_1"); err != nil {
		t.Fatalf("cancelTask: %v", err)
	}
	if len(sup.cancelled) != 1 || sup.cancelled[0] != "tsk_1" {
		t.Errorf("expected Cancel called, got %+v", sup.cancelled)
	}
	got, _ := store.Task(context.Background(), "tsk_1")
	if got.Status != task.StatusCancelled {
		t.Errorf("expected status cancelled, got %s", got.Status)
	}
}

func TestResumeTaskClearsFlagAndRedispatches(t *testing.T) {
	store := newFakeTaskStore()
	now := time.Now().UTC()
	tsk := task.Task{
		ID: "tsk_1", TaskType: task.TaskTypeCode, RepoURL: "/r.git",
		BaseCommit: "0123456789abcdef0123456789abcdef01234567", RequestText: "x",
		Status: task.StatusPaused, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.CreateTask(context.Background(), tsk, nil); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	disp := &fakeDispatcher{}

	if err := resumeTask(context.Background(), store, disp, "tsk_1"); err != nil {
		t.Fatalf("resumeTask: %v", err)
	}
	if len(disp.resumed) != 1 || disp.resumed[0] != "tsk_1" {
		t.Errorf("expected ResumeGraceful called, got %+v", disp.resumed)
	}
	got, _ := store.Task(context.Background(), "tsk_1")
	if got.Status != task.StatusRunning {
		t.Errorf("expected status running after resume, got %s", got.Status)
	}
	if len(disp.dispatched) != 1 || disp.dispatched[0].Task.ID != "tsk_1" {
		t.Fatalf("expected the task to be re-dispatched, got %+v", disp.dispatched)
	}
}

func TestResumeTaskRejectsNonPausedTask(t *testing.T) {
	store := newFakeTaskStore()
	newRunningTestTask(t, store, "tsk_1") // already running, not paused
	disp := &fakeDispatcher{}

	if err := resumeTask(context.Background(), store, disp, "tsk_1"); err == nil {
		t.Error("expected an error resuming a task that isn't paused")
	}
	if len(disp.dispatched) != 0 {
		t.Error("expected no re-dispatch for a rejected resume")
	}
}
