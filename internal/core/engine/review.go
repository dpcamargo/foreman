package engine

import (
	"errors"
	"fmt"
	"strings"
)

var ErrSameFamilyReview = errors.New("cross-family review violation: implementer and reviewer cannot share model family")

// ValidateReviewerFamily ensures the reviewer is from a different model family than the implementer (Principle V, FR-007).
func ValidateReviewerFamily(implementerFamily, reviewerFamily string) error {
	if strings.EqualFold(strings.TrimSpace(implementerFamily), strings.TrimSpace(reviewerFamily)) {
		return fmt.Errorf("%w: implementer=%s, reviewer=%s", ErrSameFamilyReview, implementerFamily, reviewerFamily)
	}
	return nil
}

// GateReviewFindings downgrades any critical or major finding lacking evidence or repro to advisory (FR-007).
// If all blocking findings are downgraded, the review is considered approved.
func GateReviewFindings(res ReviewResult) ReviewResult {
	out := ReviewResult{
		Approved: res.Approved,
		Summary:  res.Summary,
		Findings: make([]ReviewFinding, len(res.Findings)),
	}

	hasBlockingFinding := false
	for i, f := range res.Findings {
		fCopy := f
		sev := strings.ToLower(f.Severity)
		if sev == "critical" || sev == "major" || sev == "blocking" {
			hasEvidence := strings.TrimSpace(f.Evidence) != ""
			hasRepro := strings.TrimSpace(f.Repro) != ""
			if !hasEvidence && !hasRepro {
				fCopy.Severity = "advisory"
			} else {
				hasBlockingFinding = true
			}
		}
		out.Findings[i] = fCopy
	}

	if hasBlockingFinding {
		out.Approved = false
	} else {
		out.Approved = true
	}
	return out
}
