package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// ClassifyFailure categorizes an attempt outcome into infra, provider, attempt, or policy (FR-009).
func ClassifyFailure(report VerifyReport, errStr string) task.FailureClass {
	if !report.PolicyPassed && len(report.PolicyViolations) > 0 {
		return task.FailurePolicy
	}
	lowerErr := strings.ToLower(errStr)
	if strings.Contains(lowerErr, "docker") ||
		strings.Contains(lowerErr, "cannot connect") ||
		strings.Contains(lowerErr, "lost pid") ||
		strings.Contains(lowerErr, "disk full") ||
		strings.Contains(lowerErr, "context canceled") {
		return task.FailureInfra
	}
	if strings.Contains(lowerErr, "rate limit") ||
		strings.Contains(lowerErr, "429") ||
		strings.Contains(lowerErr, "503") ||
		strings.Contains(lowerErr, "timeout") {
		return task.FailureProvider
	}
	return task.FailureAttempt
}

// ComputeFailureSignature hashes sorted failing tests and the primary error line (FR-011).
func ComputeFailureSignature(failingTests []string, errorSummary string) string {
	sorted := make([]string, len(failingTests))
	copy(sorted, failingTests)
	sort.Strings(sorted)
	h := sha256.New()
	for _, t := range sorted {
		h.Write([]byte(t))
		h.Write([]byte{})
	}
	h.Write([]byte(strings.TrimSpace(errorSummary)))
	return hex.EncodeToString(h.Sum(nil))
}

// LoopGuard tracks historical signatures, diff hashes, and test pass counts to prevent loops.
type LoopGuard struct {
	sigs       []string
	diffHashes []string
	passCounts []int
}

func NewLoopGuard() *LoopGuard {
	return &LoopGuard{}
}

func (g *LoopGuard) Record(sig, diffHash string, passCount int) {
	g.sigs = append(g.sigs, sig)
	g.diffHashes = append(g.diffHashes, diffHash)
	g.passCounts = append(g.passCounts, passCount)
}

func (g *LoopGuard) Check(sig, diffHash string, passCount int) (bool, string) {
	n := len(g.sigs)
	if n == 0 {
		return false, ""
	}
	// 1. Signature repetition (immediate consecutive match)
	if g.sigs[n-1] != "" && g.sigs[n-1] == sig {
		return true, "repeated_failure_signature"
	}
	// 2. Diff hash oscillation (match against any prior diff)
	if diffHash != "" {
		for _, prev := range g.diffHashes {
			if prev == diffHash {
				return true, "diff_oscillation"
			}
		}
	}
	// 3. Pass count flatline (passing tests did not strictly increase)
	if passCount <= g.passCounts[n-1] {
		return true, "pass_count_flatline"
	}
	return false, ""
}

type LadderDecision struct {
	NextRung     int
	SwitchFamily bool
	TargetFamily string
	BlockedHuman bool
}

// DecideLadderTransition calculates the next rung and action per FR-008 and FR-011.
func DecideLadderTransition(currentRung int, cls task.FailureClass, loopGuardTriggered bool, currentFamily, currentRoute string) LadderDecision {
	switch cls {
	case task.FailurePolicy:
		// Policy violations skip directly to Rung 3 blocked_human
		return LadderDecision{NextRung: 3, BlockedHuman: true}

	case task.FailureInfra, task.FailureProvider:
		// Retry cleanly on same rung
		return LadderDecision{NextRung: currentRung, SwitchFamily: false}

	case task.FailureAttempt:
		if currentRung == 1 {
			if loopGuardTriggered {
				// Escalate to Rung 2 with family switch
				target := otherFamily(currentFamily)
				return LadderDecision{NextRung: 2, SwitchFamily: true, TargetFamily: target}
			}
			// Retry on Rung 1 with evidence
			return LadderDecision{NextRung: 1, SwitchFamily: false}
		}
		if currentRung == 2 {
			// Escalation from Rung 2 reaches Rung 3
			return LadderDecision{NextRung: 3, BlockedHuman: true}
		}
		return LadderDecision{NextRung: 3, BlockedHuman: true}
	default:
		return LadderDecision{NextRung: 3, BlockedHuman: true}
	}
}

func otherFamily(fam string) string {
	if fam == "openai" {
		return "anthropic"
	}
	return "openai"
}
