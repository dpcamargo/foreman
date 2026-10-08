package container_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

type fakeGrader struct {
	calls int
	in    eval.GradeInput
	out   eval.GradeOutput
	err   error
}

func (g *fakeGrader) Grade(_ context.Context, in eval.GradeInput) (eval.GradeOutput, error) {
	g.calls++
	g.in = in
	return g.out, g.err
}

func TestVerifierRejectsPolicyViolationBeforeGrading(t *testing.T) {
	grader := &fakeGrader{}
	verifier := container.NewVerifier(grader, nil)

	report, err := verifier.Verify(context.Background(), engine.VerifyInput{
		Patch:       []byte("diff --git a/add_test.go b/add_test.go\ndeleted file mode 100644\n--- a/add_test.go\n+++ /dev/null\n"),
		ProjectYAML: projectYAML(),
	})
	if err != nil {
		t.Fatalf("Verify returned infrastructure error: %v", err)
	}
	if report.Passed || report.PolicyPassed || len(report.PolicyViolations) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if grader.calls != 0 {
		t.Fatalf("grader called %d times after policy failure", grader.calls)
	}
}

func TestVerifierMapsVisibleAndHiddenResults(t *testing.T) {
	fullPassed := true
	grader := &fakeGrader{out: eval.GradeOutput{
		FullSuitePassed: &fullPassed,
		HiddenPassed:    1,
		PerTest:         []eval.TestResult{{Package: "example", Test: "TestHidden", Result: "pass"}},
		Log:             "verification log",
	}}
	verifier := container.NewVerifier(grader, nil)
	visible := fstest.MapFS{"visible_test.go": {Data: []byte("package example")}}
	hidden := fstest.MapFS{"hidden_test.go": {Data: []byte("package example")}}

	report, err := verifier.Verify(context.Background(), engine.VerifyInput{
		TaskID: "tsk_1", RepoURL: "/repo", BaseCommit: "abc", Patch: []byte("patch"),
		Visible: visible, Hidden: hidden, ProjectYAML: projectYAML(),
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !report.Passed || !report.PolicyPassed || !report.VisiblePassed || !report.HiddenPassed {
		t.Fatalf("unexpected report: %+v", report)
	}
	if grader.calls != 1 {
		t.Fatalf("grader calls = %d, want 1", grader.calls)
	}
	if grader.in.RepoURL != "/repo" || grader.in.Base != "abc" || len(grader.in.FullTest) == 0 || len(grader.in.TestCommand) == 0 {
		t.Fatalf("unexpected grader input: %+v", grader.in)
	}
}

func TestVerifierReportsHiddenFailuresWithoutSource(t *testing.T) {
	fullPassed := true
	grader := &fakeGrader{out: eval.GradeOutput{
		FullSuitePassed: &fullPassed,
		HiddenFailed:    1,
		PerTest:         []eval.TestResult{{Package: "example", Test: "TestSecretCase", Result: "fail"}},
		Log:             "assertion failed",
	}}
	verifier := container.NewVerifier(grader, nil)

	report, err := verifier.Verify(context.Background(), engine.VerifyInput{ProjectYAML: projectYAML()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.HiddenPassed || len(report.FailingTests) != 1 || report.FailingTests[0] != "TestSecretCase" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.FullOutput != "assertion failed" {
		t.Fatalf("FullOutput = %q", report.FullOutput)
	}
}

func projectYAML() []byte {
	return []byte("version: 1\nverifier_image: grader@sha256:abc\nverify:\n  - [\"go\", \"test\", \"-json\", \"./...\"]\n")
}
