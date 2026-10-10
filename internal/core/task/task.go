// Package task defines the domain models and state transitions for task
// orchestration in Feature 002 (specs/002-durable-execution-loop). It imports only
// the standard library and performs no I/O.
package task

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const TaskTypeCode = "code"

var (
	ErrInvalidTask       = errors.New("invalid task")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrNotFound          = errors.New("not found")
)

type TaskStatus string

const (
	StatusPending      TaskStatus = "pending"
	StatusRunning      TaskStatus = "running"
	StatusSucceeded    TaskStatus = "succeeded"
	StatusFailed       TaskStatus = "failed"
	StatusBlockedHuman TaskStatus = "blocked_human"
	StatusPaused       TaskStatus = "paused"
	StatusCancelled    TaskStatus = "cancelled"
)

type Task struct {
	ID            string
	TaskType      string
	RepoURL       string
	BaseCommit    string
	RequestText   string
	Status        TaskStatus
	CurrentNodeID string
	Arm           string
	RecordID      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (t *Task) Validate() error {
	if strings.TrimSpace(t.RepoURL) == "" {
		return fmt.Errorf("%w: repo_url cannot be empty", ErrInvalidTask)
	}
	if len(t.BaseCommit) != 40 || !isHex(t.BaseCommit) {
		return fmt.Errorf("%w: base_commit must be a 40-character hex SHA", ErrInvalidTask)
	}
	if strings.TrimSpace(t.RequestText) == "" {
		return fmt.Errorf("%w: request_text cannot be empty", ErrInvalidTask)
	}
	if t.TaskType == "" {
		t.TaskType = TaskTypeCode
	}
	return nil
}

func (t *Task) Start(now time.Time) error {
	if t.Status != StatusPending {
		return fmt.Errorf("%w: cannot start task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusRunning
	t.UpdatedAt = now
	return nil
}

func (t *Task) Complete(now time.Time) error {
	if t.Status != StatusRunning {
		return fmt.Errorf("%w: cannot complete task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusSucceeded
	t.UpdatedAt = now
	return nil
}

func (t *Task) Fail(now time.Time) error {
	if t.Status != StatusRunning {
		return fmt.Errorf("%w: cannot fail task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusFailed
	t.UpdatedAt = now
	return nil
}

func (t *Task) BlockHuman(now time.Time) error {
	if t.Status != StatusRunning {
		return fmt.Errorf("%w: cannot block task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusBlockedHuman
	t.UpdatedAt = now
	return nil
}

// Pause implements Feature 003's "/pause <id>": stop new dispatch, let the running attempt
// finish (FR-008). Only a running task can be paused.
func (t *Task) Pause(now time.Time) error {
	if t.Status != StatusRunning {
		return fmt.Errorf("%w: cannot pause task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusPaused
	t.UpdatedAt = now
	return nil
}

// Resume implements Feature 003's "/resume <id>": the dispatch loop picks the task back up.
func (t *Task) Resume(now time.Time) error {
	if t.Status != StatusPaused {
		return fmt.Errorf("%w: cannot resume task from status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusRunning
	t.UpdatedAt = now
	return nil
}

// Cancel implements Feature 003's "/cancel <id>": kill process groups/containers for this
// task and mark it cancelled (FR-009). Any non-terminal status may be cancelled.
func (t *Task) Cancel(now time.Time) error {
	switch t.Status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return fmt.Errorf("%w: cannot cancel task from terminal status %s", ErrInvalidTransition, t.Status)
	}
	t.Status = StatusCancelled
	t.UpdatedAt = now
	return nil
}

type NodeName string

const (
	NodeSpec      NodeName = "spec"
	NodeImplement NodeName = "implement"
	NodeVerify    NodeName = "verify"
	NodeReview    NodeName = "review"
)

type NodeStatus string

const (
	NodeStatusPending   NodeStatus = "pending"
	NodeStatusRunning   NodeStatus = "running"
	NodeStatusSucceeded NodeStatus = "succeeded"
	NodeStatusFailed    NodeStatus = "failed"
	NodeStatusSkipped   NodeStatus = "skipped"
)

type Node struct {
	ID        string
	TaskID    string
	Name      NodeName
	StepOrder int
	Status    NodeStatus
	StartedAt *time.Time
	EndedAt   *time.Time
	// NotBefore is set when the node is parked because all routes are cooling
	// down (FR-005). The node stays pending and is retried after this time.
	NotBefore *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Park sets the node's not_before timestamp without counting an attempt (FR-005).
func (n *Node) Park(until time.Time) {
	n.NotBefore = &until
	n.UpdatedAt = until
}

func (n *Node) Start(now time.Time) error {
	if n.Status != NodeStatusPending {
		return fmt.Errorf("%w: cannot start node from status %s", ErrInvalidTransition, n.Status)
	}
	n.Status = NodeStatusRunning
	n.StartedAt = &now
	n.UpdatedAt = now
	return nil
}

func (n *Node) Complete(now time.Time) error {
	if n.Status != NodeStatusRunning {
		return fmt.Errorf("%w: cannot complete node from status %s", ErrInvalidTransition, n.Status)
	}
	n.Status = NodeStatusSucceeded
	n.EndedAt = &now
	n.UpdatedAt = now
	return nil
}

func (n *Node) Fail(now time.Time) error {
	if n.Status != NodeStatusRunning {
		return fmt.Errorf("%w: cannot fail node from status %s", ErrInvalidTransition, n.Status)
	}
	n.Status = NodeStatusFailed
	n.EndedAt = &now
	n.UpdatedAt = now
	return nil
}

func NewLinearNodes(taskID string, now time.Time) []Node {
	names := []NodeName{NodeSpec, NodeImplement, NodeVerify, NodeReview}
	nodes := make([]Node, len(names))
	for i, name := range names {
		nodes[i] = Node{
			ID:        NewID("nod"),
			TaskID:    taskID,
			Name:      name,
			StepOrder: i + 1,
			Status:    NodeStatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	return nodes
}

type RunStatus string

const (
	RunRunning        RunStatus = "running"
	RunSucceeded      RunStatus = "succeeded"
	RunFailed         RunStatus = "failed"
	RunInfraError     RunStatus = "infra_error"
	RunTimedOut       RunStatus = "timed_out"
	RunBudgetExceeded RunStatus = "budget_exceeded"
)

type FailureClass string

const (
	FailureInfra    FailureClass = "infra"
	FailureProvider FailureClass = "provider"
	FailureAttempt  FailureClass = "attempt"
	FailurePolicy   FailureClass = "policy"
)

type Run struct {
	ID             string
	TaskID         string
	NodeID         string
	AttemptNum     int
	LadderRung     int
	Route          string
	ModelFamily    string
	Status         RunStatus
	FailureClass   FailureClass
	FailureSig     string
	DiffHash       string
	TokensInput    int
	TokensOutput   int
	TokensCached   int
	TokensBillable int
	Wall           time.Duration
	PID            int
	PIDStartedAt   string
	Error          string
	StartedAt      time.Time
	EndedAt        *time.Time
}

func NewID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().UTC().Format("20060102150405"), hex.EncodeToString(b[:]))
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
