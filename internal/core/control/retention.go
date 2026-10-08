package control

import "time"

// RetentionKind identifies what kind of on-disk artifact a retention decision is about
// (data-model.md's Retention section). Structured SQLite rows (tasks, nodes, runs,
// approvals, deliveries) are deliberately absent — this package never selects them for
// deletion (FR-018).
type RetentionKind string

const (
	// KindTranscript: full transcripts, prompts-as-sent, context packs, verifier logs.
	KindTranscript RetentionKind = "transcript"
	// KindContainerOutput: container/verifier output kept 7d (configurable to 14).
	KindContainerOutput RetentionKind = "container-output"
	// KindSuccessfulWorkspace: deleted immediately on success.
	KindSuccessfulWorkspace RetentionKind = "successful-workspace"
	// KindCancelledWorkspace: kept 7d (configurable to 14) for forensics (FR-009).
	KindCancelledWorkspace RetentionKind = "cancelled-workspace"
)

// IsFileArtifact reports whether this kind refers to on-disk files subject to the sweep
// (as opposed to a structured record, which this function never selects).
func (k RetentionKind) IsFileArtifact() bool {
	switch k {
	case KindTranscript, KindContainerOutput, KindSuccessfulWorkspace, KindCancelledWorkspace:
		return true
	}
	return false
}

// AllKinds lists every RetentionKind, for tests that iterate over the full set.
func AllKinds() []RetentionKind {
	return []RetentionKind{
		KindTranscript,
		KindContainerOutput,
		KindSuccessfulWorkspace,
		KindCancelledWorkspace,
	}
}

// Retention holds configurable retention windows (spec.md FR-018: 7 days, configurable up
// to 14). The transcript window is fixed at 90 days and the successful-workspace window is
// 0 (deleted on success) — neither is operator-configurable.
type Retention struct {
	// ContainerOutputDays: 0 means "use DefaultRetention's value".
	ContainerOutputDays int
	// CancelledWorkspaceDays: 0 means "use DefaultRetention's value".
	CancelledWorkspaceDays int
}

// DefaultRetention matches data-model.md's Retention section defaults (7d for both
// configurable windows).
var DefaultRetention = Retention{
	ContainerOutputDays:    7,
	CancelledWorkspaceDays: 7,
}

const (
	transcriptDays = 90
	day            = 24 * time.Hour
)

// ShouldSweep reports whether an artifact of the given kind and age should be deleted by
// the daemon's housekeeping tick (FR-018). It is pure: it decides, it never touches disk
// or any store — adapters do the actual deletion, and only ever for IsFileArtifact kinds.
func ShouldSweep(kind RetentionKind, age time.Duration, cfg Retention) bool {
	if !kind.IsFileArtifact() {
		return false // structured records are never selected for deletion
	}
	switch kind {
	case KindSuccessfulWorkspace:
		return true // deleted immediately on success, regardless of age
	case KindTranscript:
		return age >= transcriptDays*day
	case KindContainerOutput:
		days := cfg.ContainerOutputDays
		if days <= 0 {
			days = DefaultRetention.ContainerOutputDays
		}
		return age >= time.Duration(days)*day
	case KindCancelledWorkspace:
		days := cfg.CancelledWorkspaceDays
		if days <= 0 {
			days = DefaultRetention.CancelledWorkspaceDays
		}
		return age >= time.Duration(days)*day
	}
	return false
}
