package task

import (
	"context"
	"time"
)

// TaskStore persists tasks, nodes, and execution runs. Adapter: internal/adapters/sqlite.
type TaskStore interface {
	CreateTask(ctx context.Context, t Task, nodes []Node) (Task, error)
	Task(ctx context.Context, id string) (Task, error)
	UpdateTaskStatus(ctx context.Context, id string, status TaskStatus, currentNodeID string) error
	Node(ctx context.Context, id string) (Node, error)
	NodesForTask(ctx context.Context, taskID string) ([]Node, error)
	UpdateNodeStatus(ctx context.Context, id string, status NodeStatus) error
	ParkNode(ctx context.Context, id string, until time.Time) error
	RecordTaskRun(ctx context.Context, r Run) error
	FinishTaskRun(ctx context.Context, runID string, status RunStatus, failureClass FailureClass, failureSig string) error
}
