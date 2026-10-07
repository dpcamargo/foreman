package eval

import "testing"

func TestRules(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name            string
		attempt         RunStatus
		claimed         Claim
		passed, failed  int
		regressed       *bool
		expectsClarify  bool
		contaminated    bool
		wantSuccess     bool
		wantFalseSucces bool
	}{
		{"all hidden pass", StatusSucceeded, ClaimDone, 3, 0, nil, false, false, true, false},
		{"hidden fail but claimed done", StatusSucceeded, ClaimDone, 2, 1, nil, false, false, false, true},
		{"hidden fail, no claim", StatusSucceeded, ClaimNone, 2, 1, nil, false, false, false, false},
		{"nothing ran", StatusSucceeded, ClaimDone, 0, 0, nil, false, false, false, true},
		{"full suite regressed", StatusSucceeded, ClaimDone, 3, 0, &yes, false, false, false, true},
		{"full suite fine", StatusSucceeded, ClaimDone, 3, 0, &no, false, false, true, false},
		{"timed out even if tests pass", StatusTimedOut, ClaimNone, 3, 0, nil, false, false, false, false},
		{"over budget even if tests pass", StatusBudgetExceeded, ClaimDone, 3, 0, nil, false, false, false, true},
		{"contaminated even if tests pass", StatusSucceeded, ClaimDone, 3, 0, nil, false, true, false, true},
		{"asked when it should ask", StatusSucceeded, ClaimClarification, 0, 1, nil, true, false, true, false},
		{"guessed when it should ask", StatusSucceeded, ClaimDone, 1, 0, nil, true, false, false, true},
		{"asked when it shouldn't", StatusSucceeded, ClaimClarification, 0, 1, nil, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Record{ExpectsClarification: tt.expectsClarify}
			g := Grade{HiddenPassed: tt.passed, HiddenFailed: tt.failed, FullSuiteRegressed: tt.regressed, Contaminated: tt.contaminated}
			success, falseSuccess := Rules(tt.attempt, tt.claimed, g, r)
			if success != tt.wantSuccess || falseSuccess != tt.wantFalseSucces {
				t.Errorf("Rules = (%v, %v), want (%v, %v)", success, falseSuccess, tt.wantSuccess, tt.wantFalseSucces)
			}
		})
	}
}

func TestFinalStatus(t *testing.T) {
	tests := []struct {
		attempt      RunStatus
		success      bool
		contaminated bool
		want         RunStatus
	}{
		{StatusSucceeded, true, false, StatusSucceeded},
		{StatusSucceeded, false, false, StatusFailed},
		{StatusTimedOut, false, false, StatusTimedOut},
		{StatusBudgetExceeded, false, false, StatusBudgetExceeded},
		{StatusSucceeded, false, true, StatusContaminated},
		{StatusTimedOut, false, true, StatusContaminated},
	}
	for _, tt := range tests {
		if got := finalStatus(tt.attempt, tt.success, tt.contaminated); got != tt.want {
			t.Errorf("finalStatus(%s, %v, %v) = %s, want %s", tt.attempt, tt.success, tt.contaminated, got, tt.want)
		}
	}
}

func TestLeakScan(t *testing.T) {
	state := "/Users/op/.foreman/state"
	hidden := []string{"sub_test.go", "pkg/fizz_hidden_test.go"}
	tests := []struct {
		name, transcript string
		want             bool
	}{
		{"clean", `{"type":"item","text":"ran go test ./... ok"}`, false},
		{"mentions test generically", "I'll write a test for Sub in add_test.go", false},
		{"state path", `cat /Users/op/.foreman/state/records/rec_1/hidden/sub_test.go`, true},
		{"hidden base name", `ls shows sub_test.go`, true},
		{"nested hidden base name", `found fizz_hidden_test.go`, true},
		{"case differs", "SUB_TEST.GO", false},
		{"longer name containing it is still a hit", "xsub_test.go", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LeakScan(tt.transcript, state, hidden); got != tt.want {
				t.Errorf("LeakScan = %v, want %v", got, tt.want)
			}
		})
	}
	if LeakScan("anything", "", nil) {
		t.Error("empty state dir and no hidden names must never match")
	}
}
