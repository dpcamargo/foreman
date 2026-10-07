package eval

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrRunInProgress means a run with the same cache key is running in a live process.
var ErrRunInProgress = errors.New("a run with this key is already in progress")

// RunStatus is a replay run's state (data-model.md, "Replay run state machine").
type RunStatus string

const (
	StatusRunning        RunStatus = "running"
	StatusSucceeded      RunStatus = "succeeded" // graded as a success (or, for an attempt, "finished")
	StatusFailed         RunStatus = "failed"
	StatusTimedOut       RunStatus = "timed_out"
	StatusBudgetExceeded RunStatus = "budget_exceeded"
	StatusContaminated   RunStatus = "contaminated" // leak scan hit
	StatusInfraError     RunStatus = "infra_error"  // excluded from denominators; retried on the next request
)

// CountsAsFailure reports whether a finished run counts as a failed attempt.
func (s RunStatus) CountsAsFailure() bool {
	switch s {
	case StatusFailed, StatusTimedOut, StatusBudgetExceeded, StatusContaminated:
		return true
	}
	return false
}

// Graded reports whether a run counts in success-rate denominators.
func (s RunStatus) Graded() bool { return s == StatusSucceeded || s.CountsAsFailure() }

// Claim is what the arm said about its own work, which is not what's true.
type Claim string

const (
	ClaimDone          Claim = "done"
	ClaimClarification Claim = "needs_clarification"
	ClaimNone          Claim = "none"
)

// Usage is one run's token use, normalized across CLIs: Input is uncached input only.
type Usage struct {
	Input      int64    // uncached input tokens
	Cached     int64    // cache-read input tokens
	CacheWrite int64    // cache-write input tokens
	Output     int64    // output tokens (including reasoning, where the CLI counts it there)
	Reasoning  int64    // reasoning tokens, when reported separately
	USD        *float64 // API-equivalent dollars, when the CLI reports them
	CostSource string   // e.g. "hermes-estimate", "none"
}

// Billable is what token caps compare against (research R8).
func (u Usage) Billable() int64 { return u.Input + u.Output + u.CacheWrite }

// Add sums two usages (dollars only when both are known or one is zero-valued).
func (u Usage) Add(v Usage) Usage {
	out := Usage{
		Input: u.Input + v.Input, Cached: u.Cached + v.Cached, CacheWrite: u.CacheWrite + v.CacheWrite,
		Output: u.Output + v.Output, Reasoning: u.Reasoning + v.Reasoning,
	}
	if u.USD != nil || v.USD != nil {
		var sum float64
		if u.USD != nil {
			sum += *u.USD
		}
		if v.USD != nil {
			sum += *v.USD
		}
		out.USD = &sum
	}
	return out
}

// RunKey is the baseline cache key (spec FR-015): a finished run with the same key is
// reused instead of re-run.
type RunKey struct {
	RecordID   string
	Arm        string // instance name, e.g. "a1" or "a1#2"
	ArmVersion string // <cli version>/<model>/<prompt sha256[:12]>
	Rep        int
	Base       string
}

// PIDFingerprint identifies a process across PID reuse (constitution VII).
type PIDFingerprint struct {
	PID       int
	StartedAt string
}

// ReplayRun is one attempt of (record, arm, repetition).
type ReplayRun struct {
	ID        string
	Key       RunKey
	Status    RunStatus
	Claimed   Claim
	Usage     Usage
	Wall      time.Duration
	PID       PIDFingerprint
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
}

var armName = regexp.MustCompile(`^(a0|a1|a2|h1)(#[1-9][0-9]*)?$`)

// ParseArm validates an arm instance name ("a1", "a1#2") and returns its base arm.
// The suffix runs the same arm under its own cache key, to measure noise (SC-004).
func ParseArm(name string) (string, error) {
	m := armName.FindStringSubmatch(name)
	if m == nil {
		return "", fmt.Errorf("unknown arm %q: want a0, a1, a2 or h1, optionally with an instance suffix like a1#2", name)
	}
	return m[1], nil
}
