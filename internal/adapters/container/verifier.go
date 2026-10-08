package container

import (
	"context"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/verify"
)

// GradeRunner is the subset of the Feature 001 grader used by the task verifier.
type GradeRunner interface {
	Grade(ctx context.Context, in eval.GradeInput) (eval.GradeOutput, error)
}

// Verifier adds project-config and diff-policy checks around the Feature 001 grader.
type Verifier struct {
	grader  GradeRunner
	secrets []string
}

// NewVerifier returns an engine verifier backed by the existing clean-room grader.
func NewVerifier(grader GradeRunner, secrets []string) *Verifier {
	return &Verifier{grader: grader, secrets: append([]string(nil), secrets...)}
}

// Verify rejects policy violations before invoking the offline container grader.
func (v *Verifier) Verify(ctx context.Context, in engine.VerifyInput) (engine.VerifyReport, error) {
	cfg, err := verify.ParseProjectConfig(in.ProjectYAML)
	if err != nil {
		return engine.VerifyReport{}, err
	}
	policy := verify.CheckDiffPolicy(in.Patch, cfg, v.secrets)
	if !policy.Passed {
		return engine.VerifyReport{
			PolicyPassed:     false,
			PolicyViolations: policy.Violations,
			ErrorSummary:     strings.Join(policy.Violations, "; "),
		}, nil
	}

	command := cfg.Verify[0]
	out, err := v.grader.Grade(ctx, eval.GradeInput{
		RepoURL:     in.RepoURL,
		Base:        in.BaseCommit,
		Patch:       in.Patch,
		Visible:     in.Visible,
		Hidden:      in.Hidden,
		FullTest:    command,
		TestCommand: command,
		TestFormat:  eval.FormatGoTestJSON,
	})
	if err != nil {
		return engine.VerifyReport{}, err
	}

	visiblePassed := out.FullSuitePassed != nil && *out.FullSuitePassed
	hiddenPassed := out.HiddenFailed == 0 && out.HiddenPassed > 0
	failing := make([]string, 0, out.HiddenFailed)
	for _, result := range out.PerTest {
		if result.Result == "fail" {
			failing = append(failing, result.Test)
		}
	}
	errorSummary := out.ApplyError
	if errorSummary == "" && (!visiblePassed || !hiddenPassed) {
		errorSummary = "verification command failed"
	}
	return engine.VerifyReport{
		Passed:        visiblePassed && hiddenPassed && out.ApplyError == "",
		PolicyPassed:  true,
		BuildPassed:   visiblePassed,
		VisiblePassed: visiblePassed,
		HiddenPassed:  hiddenPassed,
		FailingTests:  failing,
		ErrorSummary:  errorSummary,
		FullOutput:    out.Log,
	}, nil
}
