package prompts

import (
	"strings"
	"testing"
)

// The arm prompt must never point an arm at the hidden tests or at foreman itself.
func TestArmBaselineSaysNothingItShouldnt(t *testing.T) {
	lower := strings.ToLower(ArmBaseline)
	for _, banned := range []string{"hidden", "foreman", "state", ".foreman", "grader", "replay", "eval"} {
		if strings.Contains(lower, banned) {
			t.Errorf("arm prompt mentions %q", banned)
		}
	}
	for _, want := range []string{"{{REQUEST}}", "{{VISIBLE_TESTS}}", "needs_clarification", `"status"`} {
		if !strings.Contains(ArmBaseline, want) {
			t.Errorf("arm prompt lacks %q", want)
		}
	}
}
