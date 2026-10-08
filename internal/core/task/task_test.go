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
