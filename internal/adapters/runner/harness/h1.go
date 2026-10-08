// Package harness adapts the durable execution stages to Feature 001's eval.Arm.
package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// H1Config supplies the model-backed stages used by the H1 evaluation arm.
type H1Config struct {
	Specifier   engine.Specifier
	Implementer engine.Implementer
	Reviewer    engine.Reviewer
}

// H1 runs specify, implement, and cross-family review inside an eval workspace.
// The eval harness remains responsible for hidden-test container grading so hidden
// sources never enter this arm.
type H1 struct{ cfg H1Config }

var _ eval.Arm = (*H1)(nil)

func NewH1(cfg H1Config) *H1 { return &H1{cfg: cfg} }

func (*H1) Name() string { return "h1" }

func (h *H1) Version(context.Context) (string, error) {
	if err := h.validate(); err != nil {
		return "", err
	}
	return "h1/v1/" + h.cfg.Implementer.Route() + "/review-" + h.cfg.Reviewer.Route(), nil
}

func (h *H1) Attempt(ctx context.Context, in eval.AttemptInput) (eval.AttemptResult, error) {
	if err := h.validate(); err != nil {
		return eval.AttemptResult{}, err
	}
	spec, err := h.cfg.Specifier.Specify(ctx, engine.SpecInput{
		RequestText: in.Prompt,
		RepoURL:     in.Workspace.RepoURL,
		BaseCommit:  in.Workspace.Base,
		Workspace:   in.Workspace.Dir,
	})
	if err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "specify: " + err.Error()}, nil
	}
	if err := engine.ValidateTaskSpec(spec, func(path string) bool {
		if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return false
		}
		_, err := os.Stat(filepath.Join(in.Workspace.Dir, filepath.FromSlash(path)))
		return err == nil
	}); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "validate spec: " + err.Error()}, nil
	}

	implemented, err := h.cfg.Implementer.Implement(ctx, engine.ImplementInput{
		TaskID:      in.Workspace.ID,
		NodeID:      "eval",
		Attempt:     1,
		LadderRung:  1,
		Spec:        spec,
		Workspace:   in.Workspace.Dir,
		TokenBudget: int(in.TokenCap),
		WallTimeout: in.WallCap,
	})
	if err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "implement: " + err.Error()}, nil
	}
	result := eval.AttemptResult{
		Status: engineStatus(implemented.Status),
		Usage: eval.Usage{
			Input:      int64(implemented.TokensInput),
			Output:     int64(implemented.TokensOutput),
			Cached:     int64(implemented.TokensCached),
			CostSource: "worker-reported",
			USD:        implemented.USD,
		},
		Error: implemented.Error,
	}
	if result.Status != eval.StatusSucceeded {
		return result, nil
	}
	if err := engine.ValidateReviewerFamily(h.cfg.Implementer.Family(), h.cfg.Reviewer.Family()); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: result.Usage, Error: err.Error()}, nil
	}
	reviewed, err := h.cfg.Reviewer.Review(ctx, engine.ReviewInput{TaskID: in.Workspace.ID, Spec: spec, Patch: implemented.Patch})
	if err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: result.Usage, Error: "review: " + err.Error()}, nil
	}
	reviewed = engine.GateReviewFindings(reviewed)
	if reviewed.Approved {
		result.Claimed = eval.ClaimDone
	}
	return result, nil
}

func (h *H1) validate() error {
	if h.cfg.Specifier == nil || h.cfg.Implementer == nil || h.cfg.Reviewer == nil {
		return errors.New("h1 requires specifier, implementer, and reviewer")
	}
	return nil
}

func engineStatus(status task.RunStatus) eval.RunStatus {
	switch status {
	case task.RunSucceeded:
		return eval.StatusSucceeded
	case task.RunTimedOut:
		return eval.StatusTimedOut
	case task.RunBudgetExceeded:
		return eval.StatusBudgetExceeded
	case task.RunInfraError:
		return eval.StatusInfraError
	default:
		return eval.StatusSucceeded
	}
}

func (h *H1) String() string {
	version, err := h.Version(context.Background())
	if err != nil {
		return fmt.Sprintf("h1(invalid: %v)", err)
	}
	return version
}
