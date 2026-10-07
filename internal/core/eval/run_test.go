package eval

import (
	"testing"
)

func TestRunKeyEquality(t *testing.T) {
	a := RunKey{RecordID: "rec_1", Arm: "a1", ArmVersion: "v1", Rep: 1, Base: goodSHA}
	b := a
	if a != b {
		t.Error("identical keys differ")
	}
	for name, mutate := range map[string]func(*RunKey){
		"record": func(k *RunKey) { k.RecordID = "rec_2" },
		"arm":    func(k *RunKey) { k.Arm = "a1#2" },
		"ver":    func(k *RunKey) { k.ArmVersion = "v2" },
		"rep":    func(k *RunKey) { k.Rep = 2 },
		"base":   func(k *RunKey) { k.Base = "ffffffffffffffffffffffffffffffffffffffff" },
	} {
		c := a
		mutate(&c)
		if c == a {
			t.Errorf("changing %s did not change the key", name)
		}
	}
}

func TestUsageBillable(t *testing.T) {
	u := Usage{Input: 100, Cached: 5000, CacheWrite: 300, Output: 40, Reasoning: 10}
	if got := u.Billable(); got != 440 {
		t.Errorf("Billable = %d, want 440 (uncached input + output + cache writes)", got)
	}
}

func TestRunStatusClasses(t *testing.T) {
	tests := []struct {
		s               RunStatus
		failure, graded bool
	}{
		{StatusSucceeded, false, true},
		{StatusFailed, true, true},
		{StatusTimedOut, true, true},
		{StatusBudgetExceeded, true, true},
		{StatusContaminated, true, true},
		{StatusInfraError, false, false},
		{StatusRunning, false, false},
	}
	for _, tt := range tests {
		if got := tt.s.CountsAsFailure(); got != tt.failure {
			t.Errorf("%s.CountsAsFailure() = %v, want %v", tt.s, got, tt.failure)
		}
		if got := tt.s.Graded(); got != tt.graded {
			t.Errorf("%s.Graded() = %v, want %v", tt.s, got, tt.graded)
		}
	}
}

func TestParseArm(t *testing.T) {
	tests := []struct {
		in, base string
		ok       bool
	}{
		{"a0", "a0", true}, {"a1", "a1", true}, {"a2", "a2", true}, {"h1", "h1", true},
		{"a1#2", "a1", true}, {"a0#10", "a0", true},
		{"a3", "", false}, {"A1", "", false}, {"a1#", "", false}, {"a1#0", "", false},
		{"a1#x", "", false}, {"", "", false}, {"a1 ", "", false},
	}
	for _, tt := range tests {
		base, err := ParseArm(tt.in)
		if tt.ok != (err == nil) {
			t.Errorf("ParseArm(%q) err = %v, want ok=%v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && base != tt.base {
			t.Errorf("ParseArm(%q) base = %q, want %q", tt.in, base, tt.base)
		}
	}
}
