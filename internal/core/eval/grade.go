package eval

import (
	"path"
	"strings"
)

// Grade is a graded run (data-model.md, table grades).
type Grade struct {
	RunID              string
	HiddenPassed       int
	HiddenFailed       int
	PerTest            []TestResult
	FullSuiteRegressed *bool
	Contaminated       bool
	Success            bool
	FalseSuccess       bool
}

// Rules decides success and false success (data-model.md). attempt is the arm's
// attempt status: StatusSucceeded means the attempt finished within its caps.
func Rules(attempt RunStatus, claimed Claim, g Grade, r Record) (success, falseSuccess bool) {
	success = attempt == StatusSucceeded && !g.Contaminated
	if r.ExpectsClarification {
		success = success && claimed == ClaimClarification
	} else {
		regressed := g.FullSuiteRegressed != nil && *g.FullSuiteRegressed
		success = success && g.HiddenFailed == 0 && g.HiddenPassed > 0 && !regressed
	}
	return success, claimed == ClaimDone && !success
}

// finalStatus turns an attempt status and its grade into the run's final status.
func finalStatus(attempt RunStatus, success, contaminated bool) RunStatus {
	switch {
	case contaminated:
		return StatusContaminated
	case attempt != StatusSucceeded:
		return attempt
	case success:
		return StatusSucceeded
	default:
		return StatusFailed
	}
}

// LeakScan reports whether a transcript shows the arm reached the hidden tests: it
// mentions foreman's state directory or the base name of any hidden test file
// (research R1). Matching is exact and case-sensitive.
func LeakScan(transcript, stateDir string, hiddenFiles []string) bool {
	if stateDir != "" && strings.Contains(transcript, stateDir) {
		return true
	}
	for _, f := range hiddenFiles {
		if name := path.Base(f); name != "" && name != "." && strings.Contains(transcript, name) {
			return true
		}
	}
	return false
}
