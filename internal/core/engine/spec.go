package engine

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidSpec = errors.New("invalid task spec")

// ValidateTaskSpec performs grounding and structural checks on a generated TaskSpec (FR-004).
// fileExists is a callback checking if a path exists in the base commit's git tree.
func ValidateTaskSpec(spec TaskSpec, fileExists func(path string) bool) error {
	if strings.TrimSpace(spec.Summary) == "" {
		return fmt.Errorf("%w: summary cannot be empty", ErrInvalidSpec)
	}
	if len(spec.AcceptanceCriteria) == 0 {
		return fmt.Errorf("%w: acceptance criteria cannot be empty", ErrInvalidSpec)
	}
	for i, ac := range spec.AcceptanceCriteria {
		if strings.TrimSpace(ac) == "" {
			return fmt.Errorf("%w: acceptance criterion %d cannot be empty", ErrInvalidSpec, i)
		}
	}
	if len(spec.VerifyCommands) == 0 {
		return fmt.Errorf("%w: verify commands cannot be empty", ErrInvalidSpec)
	}
	for i, cmd := range spec.VerifyCommands {
		if len(cmd) == 0 || strings.TrimSpace(cmd[0]) == "" {
			return fmt.Errorf("%w: verify command %d cannot be empty", ErrInvalidSpec, i)
		}
	}
	if fileExists != nil {
		for _, f := range spec.CitedFiles {
			clean := strings.TrimSpace(f)
			if clean == "" {
				continue
			}
			if !fileExists(clean) {
				return fmt.Errorf("%w: cited file not found in tree: %s", ErrInvalidSpec, clean)
			}
		}
	}
	return nil
}
