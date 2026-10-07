package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ReasonBaseGone marks a record whose base commit disappeared after validation.
const ReasonBaseGone = "base commit no longer fetchable"

// Replayer replays recorded tasks through arms (contracts/report.md, replayOnce).
type Replayer struct {
	Records    RecordStore
	Runs       RunStore
	Workspaces Workspaces
	Artifacts  Artifacts
	Grader     Grader

	PromptTemplate string         // prompts/arm_baseline.md, passed in by cmd
	StateDir       string         // for the leak scan
	PID            PIDFingerprint // this process
	TokenCap       int64          // per run, billable tokens
	GradeTimeout   time.Duration
	Now            func() time.Time
}

// ReplayRequest asks for one (record, arm, repetition).
type ReplayRequest struct {
	Record       Record
	Arm          Arm
	ArmName      string // instance name; defaults to Arm.Name()
	Rep          int
	NoCacheNonce string // non-empty: never reuse, and never be reused (validation only)
}

// ReplayResult is a run and its grade. Reused means the cache answered.
type ReplayResult struct {
	Run    ReplayRun
	Grade  *Grade
	Reused bool
}

// ReplayOnce runs or reuses one replay. It returns an error only when the run could
// not be started; every later failure is recorded on the run itself.
func (p Replayer) ReplayOnce(ctx context.Context, req ReplayRequest) (ReplayResult, error) {
	rec := req.Record
	name := req.ArmName
	if name == "" {
		name = req.Arm.Name()
	}
	files, err := p.Records.RecordFiles(ctx, rec.ID)
	if err != nil {
		return ReplayResult{}, fmt.Errorf("files of %s: %w", rec.ID, err)
	}
	visible, err := ListFiles(files.Visible)
	if err != nil {
		return ReplayResult{}, fmt.Errorf("visible files of %s: %w", rec.ID, err)
	}
	hidden, err := ListFiles(files.Hidden)
	if err != nil {
		return ReplayResult{}, fmt.Errorf("hidden files of %s: %w", rec.ID, err)
	}
	key, err := p.Key(ctx, req)
	if err != nil {
		return ReplayResult{}, err
	}

	run, reused, err := p.Runs.StartRun(ctx, key, p.PID)
	if err != nil {
		return ReplayResult{}, err
	}
	if reused {
		g, err := p.Runs.RunGrade(ctx, run.ID)
		if err != nil {
			return ReplayResult{}, fmt.Errorf("grade of %s: %w", run.ID, err)
		}
		return ReplayResult{Run: run, Grade: g, Reused: true}, nil
	}
	run.StartedAt = p.now()
	return p.execute(ctx, rec, files, visible, hidden, req.Arm, run)
}

// execute runs a started replay to completion and calls FinishRun exactly once.
func (p Replayer) execute(ctx context.Context, rec Record, files RecordFiles, visible, hidden []string, arm Arm, run ReplayRun) (ReplayResult, error) {
	ws, err := p.Workspaces.Prepare(ctx, run.ID, rec.RepoURL, rec.BaseCommit, files.Visible)
	if errors.Is(err, ErrBaseNotFound) {
		if serr := p.Records.SetReplayable(ctx, rec.ID, Validation{Replayable: false, Reason: ReasonBaseGone}); serr != nil {
			err = errors.Join(err, serr)
		}
	}
	if err != nil {
		return p.finishInfra(ctx, run, "prepare workspace: "+err.Error())
	}
	defer p.Workspaces.Destroy(context.WithoutCancel(ctx), ws)

	ioDir, err := p.Artifacts.IODir(ctx, run.ID)
	if err != nil {
		return p.finishInfra(ctx, run, "io dir: "+err.Error())
	}
	sink, err := p.Artifacts.Transcript(ctx, run.ID)
	if err != nil {
		return p.finishInfra(ctx, run, "transcript: "+err.Error())
	}
	var transcript bytes.Buffer
	res, err := arm.Attempt(ctx, AttemptInput{
		Workspace: ws, Prompt: RenderPrompt(p.PromptTemplate, rec.Request, visible),
		WallCap: rec.WallBudget, TokenCap: p.TokenCap, RunDir: ioDir,
		Transcript: io.MultiWriter(sink, &transcript),
	})
	sink.Close()
	if err != nil {
		res = AttemptResult{Status: StatusInfraError, Error: err.Error()}
	}
	run.Claimed, run.Usage = res.Claimed, res.Usage

	patch, exportErr := p.Workspaces.Export(ctx, ws)
	if exportErr == nil {
		exportErr = p.Artifacts.SavePatch(ctx, run.ID, patch)
	}
	if cerr := p.Artifacts.Collect(ctx, run.ID); cerr != nil && exportErr == nil {
		exportErr = cerr
	}
	switch {
	case res.Status == StatusInfraError:
		return p.finishInfra(ctx, run, "arm: "+res.Error)
	case exportErr != nil:
		return p.finishInfra(ctx, run, "export: "+exportErr.Error())
	}

	in := GradeInput{
		RepoURL: rec.RepoURL, Base: rec.BaseCommit, Visible: files.Visible, Hidden: files.Hidden,
		TestCommand: rec.TestCommand, TestFormat: rec.TestFormat, Timeout: p.gradeTimeout(),
	}
	if len(patch) > 0 {
		in.Patch = patch
	}
	baseSuitePassed := rec.BaseFullSuitePassed != nil && *rec.BaseFullSuitePassed
	if baseSuitePassed {
		in.FullTest = rec.FullTestCommand
	}
	out, err := p.Grader.Grade(ctx, in)
	if err != nil {
		return p.finishInfra(ctx, run, "grade: "+err.Error())
	}

	g := Grade{
		RunID: run.ID, HiddenPassed: out.HiddenPassed, HiddenFailed: out.HiddenFailed, PerTest: out.PerTest,
		Contaminated: LeakScan(transcript.String(), p.StateDir, hidden),
	}
	if baseSuitePassed && out.FullSuitePassed != nil {
		regressed := !*out.FullSuitePassed
		g.FullSuiteRegressed = &regressed
	}
	g.Success, g.FalseSuccess = Rules(res.Status, res.Claimed, g, rec)
	run.Status = finalStatus(res.Status, g.Success, g.Contaminated)
	run.EndedAt = p.now()
	run.Wall = run.EndedAt.Sub(run.StartedAt)
	if err := p.Runs.FinishRun(ctx, run, &g); err != nil {
		return ReplayResult{}, fmt.Errorf("finish %s: %w", run.ID, err)
	}
	return ReplayResult{Run: run, Grade: &g}, nil
}

func (p Replayer) finishInfra(ctx context.Context, run ReplayRun, msg string) (ReplayResult, error) {
	run.Status, run.Error = StatusInfraError, msg
	run.EndedAt = p.now()
	run.Wall = run.EndedAt.Sub(run.StartedAt)
	if err := p.Runs.FinishRun(ctx, run, nil); err != nil {
		return ReplayResult{}, fmt.Errorf("finish %s: %w", run.ID, err)
	}
	return ReplayResult{Run: run}, nil
}

func (p Replayer) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p Replayer) gradeTimeout() time.Duration {
	if p.GradeTimeout > 0 {
		return p.GradeTimeout
	}
	return DefaultGradeTimeout
}

// RenderPrompt fills the arm prompt template: {{REQUEST}} is the record's request,
// verbatim; {{VISIBLE_TESTS}} lists the visible test files.
func RenderPrompt(template, request string, visible []string) string {
	list := "(none)"
	if len(visible) > 0 {
		list = "- " + strings.Join(visible, "\n- ")
	}
	r := strings.NewReplacer("{{REQUEST}}", strings.TrimRight(request, "\n"), "{{VISIBLE_TESTS}}", list)
	return r.Replace(template)
}

// promptHash versions the prompt template (constitution: prompt versions are hashed).
func promptHash(template string) string {
	sum := sha256.Sum256([]byte(template))
	return hex.EncodeToString(sum[:])[:12]
}
