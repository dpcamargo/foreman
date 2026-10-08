package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestTaskStoreCreateAndRetrieve(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC()

	tsk := task.Task{
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repos/addrepo.git",
		BaseCommit:  "0123456789abcdef0123456789abcdef01234567",
		RequestText: "Implement Sub",
		Status:      task.StatusPending,
	}
	nodes := task.NewLinearNodes("", now)

	created, err := s.CreateTask(ctx, tsk, nodes)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty task ID")
	}

	retrieved, err := s.Task(ctx, created.ID)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if retrieved.ID != created.ID || retrieved.RepoURL != tsk.RepoURL || retrieved.Status != task.StatusPending {
		t.Errorf("retrieved task mismatch: %+v vs %+v", retrieved, created)
	}

	gotNodes, err := s.NodesForTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("NodesForTask: %v", err)
	}
	if len(gotNodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(gotNodes))
	}
	if gotNodes[0].Name != task.NodeSpec || gotNodes[1].Name != task.NodeImplement {
		t.Errorf("unexpected node names: %v, %v", gotNodes[0].Name, gotNodes[1].Name)
	}

	// Update task status
	if err := s.UpdateTaskStatus(ctx, created.ID, task.StatusRunning, gotNodes[0].ID); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	updated, _ := s.Task(ctx, created.ID)
	if updated.Status != task.StatusRunning || updated.CurrentNodeID != gotNodes[0].ID {
		t.Errorf("expected running and node %s, got %+v", gotNodes[0].ID, updated)
	}

	// Update node status
	if err := s.UpdateNodeStatus(ctx, gotNodes[0].ID, task.NodeStatusSucceeded); err != nil {
		t.Fatalf("UpdateNodeStatus: %v", err)
	}
	node, err := s.Node(ctx, gotNodes[0].ID)
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	if node.Status != task.NodeStatusSucceeded {
		t.Errorf("expected node status succeeded, got %s", node.Status)
	}
}

func TestTaskStoreRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC()

	tsk, err := s.CreateTask(ctx, task.Task{
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/repos/addrepo.git",
		BaseCommit:  "0123456789abcdef0123456789abcdef01234567",
		RequestText: "Implement Sub",
		Status:      task.StatusPending,
	}, task.NewLinearNodes("", now))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.NodesForTask(ctx, tsk.ID)
	implNode := nodes[1]

	runID := task.NewID("run")
	r := task.Run{
		ID:          runID,
		TaskID:      tsk.ID,
		NodeID:      implNode.ID,
		AttemptNum:  1,
		LadderRung:  1,
		Route:       "codex/default",
		ModelFamily: "openai",
		Status:      task.RunRunning,
		PID:         12345,
		StartedAt:   now,
	}

	if err := s.RecordTaskRun(ctx, r); err != nil {
		t.Fatalf("RecordTaskRun: %v", err)
	}

	if err := s.FinishTaskRun(ctx, runID, task.RunFailed, task.FailureAttempt, "sig_abc123"); err != nil {
		t.Fatalf("FinishTaskRun: %v", err)
	}
}
