package control_test

import (
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
)

// T044: pure tests for the retention decision function — given a kind and an age, returns
// whether it should be swept (data-model.md's Retention section: 90d / 7d-configurable-to-14
// / immediate-on-success / 7d respectively). Structured SQLite rows are never selected for
// deletion by this function.
func TestShouldSweep(t *testing.T) {
	const day = 24 * time.Hour
	tests := []struct {
		name string
		kind control.RetentionKind
		age  time.Duration
		want bool
	}{
		// Transcript: 90 days.
		{"transcript just under 90d is kept", control.KindTranscript, 89 * day, false},
		{"transcript exactly 90d is swept", control.KindTranscript, 90 * day, true},
		{"transcript well past 90d is swept", control.KindTranscript, 120 * day, true},

		// Container output: 7 days by default.
		{"container output just under 7d is kept", control.KindContainerOutput, 6 * day, false},
		{"container output exactly 7d is swept", control.KindContainerOutput, 7 * day, true},

		// Successful workspace: deleted immediately on success (0 days retention).
		{"successful workspace is swept immediately", control.KindSuccessfulWorkspace, 0, true},
		{"successful workspace is swept at any age", control.KindSuccessfulWorkspace, 1 * day, true},

		// Cancelled workspace: 7 days for forensics (FR-009), same as container output.
		{"cancelled workspace just under 7d is kept", control.KindCancelledWorkspace, 6 * day, false},
		{"cancelled workspace exactly 7d is swept", control.KindCancelledWorkspace, 7 * day, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := control.ShouldSweep(tc.kind, tc.age, control.DefaultRetention)
			if got != tc.want {
				t.Errorf("ShouldSweep(%s, %v) = %v, want %v", tc.kind, tc.age, got, tc.want)
			}
		})
	}
}

// T044: container output and failed/cancelled workspaces are configurable up to 14 days.
func TestShouldSweepConfigurableRetention(t *testing.T) {
	const day = 24 * time.Hour
	custom := control.Retention{ContainerOutputDays: 14, CancelledWorkspaceDays: 14}

	if !control.ShouldSweep(control.KindContainerOutput, 7*day, control.DefaultRetention) {
		t.Error("default 7d should sweep container output at 7d")
	}
	if control.ShouldSweep(control.KindContainerOutput, 7*day, custom) {
		t.Error("14d config should NOT sweep container output at 7d")
	}
	if !control.ShouldSweep(control.KindContainerOutput, 14*day, custom) {
		t.Error("14d config should sweep container output at 14d")
	}
	if control.ShouldSweep(control.KindCancelledWorkspace, 7*day, custom) {
		t.Error("14d config should NOT sweep cancelled workspace at 7d")
	}
	if !control.ShouldSweep(control.KindCancelledWorkspace, 15*day, custom) {
		t.Error("14d config should sweep cancelled workspace past 14d")
	}
}

// T044: structured SQLite rows are never selected for deletion by this function — a
// structured-record kind (if ever passed) must never sweep, regardless of age.
func TestShouldSweepNeverSelectsStructuredRecords(t *testing.T) {
	const day = 24 * time.Hour
	for _, kind := range control.AllKinds() {
		if !kind.IsFileArtifact() {
			if control.ShouldSweep(kind, 365*day, control.DefaultRetention) {
				t.Errorf("structured-record kind %s must never be swept", kind)
			}
		}
	}
}
