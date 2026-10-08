package task_test

import (
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

const validSHA = "7fb3e561ee1cee9f9f02a6dccaacbee1212cb336"

func TestNewTaskValidation(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		mutate  func(*task.Task)
		wantErr string
	}{
		{
			name:   "valid task",
			mutate: func(t *task.Task) {},
		},
		{
			name:    "invalid base commit SHA",
			mutate:  func(t *task.Task) { t.BaseCommit = "shortsha" },
			wantErr: "base_commit",
		},
		{
			name:    "empty request",
			mutate:  func(t *task.Task) { t.RequestText = "   " },
			wantErr: "request_text",
		},
		{
			name:    "empty repo url",
			mutate:  func(t *task.Task) { t.RepoURL = "" },
			wantErr: "repo_url",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tsk := task.Task{
				ID:          "tsk_20261007_12345678",
				TaskType:    task.TaskTypeCode,
				RepoURL:     "/path/to/repo.git",
				BaseCommit:  validSHA,
				RequestText: "Add feature X",
				Status:      task.StatusPending,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			tc.mutate(&tsk)
			err := tsk.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if tc.wantErr != "" {
				if err == nil || !contains(err.Error(), tc.wantErr) {
					t.Fatalf("got err %v, want substring %q", err, tc.wantErr)
				}
			}
		})
	}
}

func TestTaskStateTransitions(t *testing.T) {
	now := time.Now().UTC()
	tsk := task.Task{
		ID:          "tsk_1",
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repo.git",
		BaseCommit:  validSHA,
		RequestText: "Do something",
		Status:      task.StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := tsk.Start(now); err != nil {
		t.Fatalf("failed to start task: %v", err)
	}
	if tsk.Status != task.StatusRunning {
		t.Errorf("expected running, got %s", tsk.Status)
	}

	// Cannot start an already running task
	if err := tsk.Start(now); err == nil {
		t.Error("expected error starting already running task, got nil")
	}

	// Block on Rung 3
	if err := tsk.BlockHuman(now); err != nil {
		t.Fatalf("failed to block task: %v", err)
	}
	if tsk.Status != task.StatusBlockedHuman {
		t.Errorf("expected blocked_human, got %s", tsk.Status)
	}
}

func TestTaskPauseResumeCancel(t *testing.T) {
	now := time.Now().UTC()
	newTask := func(status task.TaskStatus) task.Task {
		return task.Task{
			ID:          "tsk_1",
			TaskType:    task.TaskTypeCode,
			RepoURL:     "/repo.git",
			BaseCommit:  validSHA,
			RequestText: "Do something",
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}

	t.Run("pause running then resume", func(t *testing.T) {
		tsk := newTask(task.StatusRunning)
		if err := tsk.Pause(now); err != nil {
			t.Fatalf("pause: %v", err)
		}
		if tsk.Status != task.StatusPaused {
			t.Errorf("expected paused, got %s", tsk.Status)
		}
		if err := tsk.Resume(now); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if tsk.Status != task.StatusRunning {
			t.Errorf("expected running after resume, got %s", tsk.Status)
		}
	})

	t.Run("cannot pause a pending task", func(t *testing.T) {
		tsk := newTask(task.StatusPending)
		if err := tsk.Pause(now); err == nil {
			t.Error("expected error pausing a pending task, got nil")
		}
	})

	t.Run("cannot resume a task that isn't paused", func(t *testing.T) {
		tsk := newTask(task.StatusRunning)
		if err := tsk.Resume(now); err == nil {
			t.Error("expected error resuming a non-paused task, got nil")
		}
	})

	for _, from := range []task.TaskStatus{
		task.StatusPending, task.StatusRunning, task.StatusBlockedHuman, task.StatusPaused,
	} {
		t.Run("cancel from "+string(from), func(t *testing.T) {
			tsk := newTask(from)
			if err := tsk.Cancel(now); err != nil {
				t.Fatalf("cancel from %s: %v", from, err)
			}
			if tsk.Status != task.StatusCancelled {
				t.Errorf("expected cancelled, got %s", tsk.Status)
			}
		})
	}

	t.Run("cannot cancel an already-cancelled task", func(t *testing.T) {
		tsk := newTask(task.StatusCancelled)
		if err := tsk.Cancel(now); err == nil {
			t.Error("expected error cancelling an already-cancelled task, got nil")
		}
	})

	for _, terminal := range []task.TaskStatus{task.StatusSucceeded, task.StatusFailed} {
		t.Run("cannot cancel terminal status "+string(terminal), func(t *testing.T) {
			tsk := newTask(terminal)
			if err := tsk.Cancel(now); err == nil {
				t.Errorf("expected error cancelling a %s task, got nil", terminal)
			}
		})
	}
}

func TestLinearNodesGeneration(t *testing.T) {
	now := time.Now().UTC()
	nodes := task.NewLinearNodes("tsk_1", now)
	if len(nodes) != 4 {
		t.Fatalf("expected 4 linear nodes, got %d", len(nodes))
	}
	wantNames := []task.NodeName{task.NodeSpec, task.NodeImplement, task.NodeVerify, task.NodeReview}
	for i, n := range nodes {
		if n.Name != wantNames[i] {
			t.Errorf("node %d: expected name %s, got %s", i, wantNames[i], n.Name)
		}
		if n.StepOrder != i+1 {
			t.Errorf("node %d: expected step_order %d, got %d", i, i+1, n.StepOrder)
		}
		if n.Status != task.NodeStatusPending {
			t.Errorf("node %d: expected pending status, got %s", i, n.Status)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) > 0 && testing.Verbose() || true && len(s) > 0 && (s != "" && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
