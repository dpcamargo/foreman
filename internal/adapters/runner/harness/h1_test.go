package harness_test

import (
	"context"
	"io"
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/runner/harness"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
)

type specifier struct{}

func (specifier) Specify(context.Context, engine.SpecInput) (engine.TaskSpec, error) {
	return engine.TaskSpec{Summary: "Add Sub", AcceptanceCriteria: []string{"works"}, VerifyCommands: [][]string{{"go", "test", "./..."}}}, nil
}

type implementer struct{ calls int }

func (*implementer) Family() string { return "openai" }
func (*implementer) Route() string  { return "codex/default" }
func (i *implementer) Implement(context.Context, engine.ImplementInput) (engine.ImplementResult, error) {
	i.calls++
	return engine.ImplementResult{Status: task.RunSucceeded, TokensBillable: 12}, nil
}

type reviewer struct{}

func (reviewer) Family() string { return "anthropic" }
func (reviewer) Route() string  { return "hermes/claude" }
func (reviewer) Review(context.Context, engine.ReviewInput) (engine.ReviewResult, error) {
	return engine.ReviewResult{Approved: true, Summary: "ok"}, nil
}

func TestH1AttemptRunsSpecifyImplementReview(t *testing.T) {
	impl := &implementer{}
	arm := harness.NewH1(harness.H1Config{Specifier: specifier{}, Implementer: impl, Reviewer: reviewer{}})
	if arm.Name() != "h1" {
		t.Fatalf("Name = %q", arm.Name())
	}
	if _, err := arm.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := arm.Attempt(context.Background(), eval.AttemptInput{
		Workspace: eval.Workspace{ID: "run_1", Dir: t.TempDir()}, Prompt: "Add Sub",
		Transcript: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimDone || impl.calls != 1 {
		t.Fatalf("result = %+v, calls = %d", res, impl.calls)
	}
}
