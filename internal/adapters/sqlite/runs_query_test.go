package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// seedRunSpec describes one run to insert in a RunsForTask/BudgetSummary test.
type seedRunSpec struct {
	nodeIdx int // index into the task's real NewLinearNodes nodes (0=spec, 1=impl, 2=verify, 3=review)
	tokens  int
	status  task.RunStatus
}

// seedRuns inserts runs for taskID using the task's real node ids (runs.node_id has a
// foreign key to nodes(id) — a made-up node id fails the insert).
func seedRuns(t *testing.T, ctx context.Context, s *Store, taskID string, specs []seedRunSpec) {
	t.Helper()
	nodes, err := s.NodesForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("NodesForTask: %v", err)
	}
	now := time.Now().UTC()
	for i, spec := range specs {
		if spec.nodeIdx >= len(nodes) {
			t.Fatalf("seedRuns: nodeIdx %d out of range (%d nodes)", spec.nodeIdx, len(nodes))
		}
		runID := task.NewID("run")
		r := task.Run{
			ID:             runID,
			TaskID:         taskID,
			NodeID:         nodes[spec.nodeIdx].ID,
			AttemptNum:     i + 1,
			LadderRung:     1,
			Route:          "codex/default",
			ModelFamily:    "openai",
			Status:         task.RunRunning,
			TokensBillable: spec.tokens,
			StartedAt:      now,
		}
		if err := s.RecordTaskRun(ctx, r); err != nil {
			t.Fatalf("RecordTaskRun: %v", err)
		}
		if spec.status != task.RunRunning {
			if err := s.FinishTaskRun(ctx, runID, spec.status, "", ""); err != nil {
				t.Fatalf("FinishTaskRun: %v", err)
			}
		}
	}
}

// T047: /logs <id> [node] needs the run history for a task, optionally filtered to one node.
func TestRunsForTask(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	taskID := newTestTaskForControl(t, ctx, s)

	nodes, err := s.NodesForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("NodesForTask: %v", err)
	}
	seedRuns(t, ctx, s, taskID, []seedRunSpec{
		{nodeIdx: 1, tokens: 100, status: task.RunSucceeded}, // impl
		{nodeIdx: 3, tokens: 50, status: task.RunSucceeded},  // review
		{nodeIdx: 1, tokens: 25, status: task.RunFailed},     // impl again
	})

	all, err := s.RunsForTask(ctx, taskID, "")
	if err != nil {
		t.Fatalf("RunsForTask: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("RunsForTask(all) = %d runs, want 3", len(all))
	}

	implNodeID := nodes[1].ID
	implOnly, err := s.RunsForTask(ctx, taskID, implNodeID)
	if err != nil {
		t.Fatalf("RunsForTask(impl): %v", err)
	}
	if len(implOnly) != 2 {
		t.Errorf("RunsForTask(impl) = %d runs, want 2", len(implOnly))
	}
	for _, r := range implOnly {
		if r.NodeID != implNodeID {
			t.Errorf("node filter leaked run from %s", r.NodeID)
		}
	}
}

// T047: /budget replies with spend/quota summary across active tasks.
func TestBudgetSummary(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	taskID := newTestTaskForControl(t, ctx, s)

	seedRuns(t, ctx, s, taskID, []seedRunSpec{
		{nodeIdx: 1, tokens: 100, status: task.RunSucceeded},
		{nodeIdx: 3, tokens: 50, status: task.RunSucceeded},
		{nodeIdx: 1, tokens: 25, status: task.RunRunning},
	})

	summary, err := s.BudgetSummary(ctx)
	if err != nil {
		t.Fatalf("BudgetSummary: %v", err)
	}
	if summary.TotalTokensBillable != 175 {
		t.Errorf("TotalTokensBillable = %d, want 175", summary.TotalTokensBillable)
	}
	if len(summary.ByTask) != 1 {
		t.Fatalf("ByTask = %d entries, want 1", len(summary.ByTask))
	}
	got := summary.ByTask[0]
	if got.TaskID != taskID || got.TokensBillable != 175 || got.Runs != 3 {
		t.Errorf("ByTask[0] = %+v, want {TaskID:%s TokensBillable:175 Runs:3}", got, taskID)
	}
}

// T047: /budget on an empty store reports zero, not an error.
func TestBudgetSummaryEmpty(t *testing.T) {
	s := openTest(t)
	summary, err := s.BudgetSummary(context.Background())
	if err != nil {
		t.Fatalf("BudgetSummary: %v", err)
	}
	if summary.TotalTokensBillable != 0 || len(summary.ByTask) != 0 {
		t.Errorf("empty summary = %+v, want zeros", summary)
	}
}
