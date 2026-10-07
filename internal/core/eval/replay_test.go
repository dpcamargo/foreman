package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// --- fakes ---

type fakeRuns struct {
	rows     map[RunKey]ReplayRun
	grades   map[string]*Grade
	finished []ReplayRun
	n        int
}

func newFakeRuns() *fakeRuns {
	return &fakeRuns{rows: map[RunKey]ReplayRun{}, grades: map[string]*Grade{}}
}

func (f *fakeRuns) StartRun(_ context.Context, k RunKey, pid PIDFingerprint) (ReplayRun, bool, error) {
	if r, ok := f.rows[k]; ok {
		switch r.Status {
		case StatusInfraError:
			r.Status, r.Error = StatusRunning, ""
			f.rows[k] = r
			return r, false, nil
		case StatusRunning:
			return ReplayRun{}, false, ErrRunInProgress
		default:
			return r, true, nil
		}
	}
	f.n++
	r := ReplayRun{ID: fmt.Sprintf("run_%d", f.n), Key: k, Status: StatusRunning, PID: pid}
	f.rows[k] = r
	return r, false, nil
}

func (f *fakeRuns) FinishRun(_ context.Context, r ReplayRun, g *Grade) error {
	f.rows[r.Key] = r
	f.grades[r.ID] = g
	f.finished = append(f.finished, r)
	return nil
}

func (f *fakeRuns) SweepLostRuns(context.Context, func(PIDFingerprint) bool) (int, error) {
	return 0, nil
}

func (f *fakeRuns) RunGrade(_ context.Context, id string) (*Grade, error) { return f.grades[id], nil }

type fakeWorkspaces struct {
	prepareErr error
	patch      []byte
	exportErr  error
	log        *[]string
	destroyed  int
	overlay    []string
}

func (w *fakeWorkspaces) Prepare(_ context.Context, id, repo, base string, overlay fs.FS) (Workspace, error) {
	*w.log = append(*w.log, "prepare")
	if w.prepareErr != nil {
		return Workspace{}, w.prepareErr
	}
	w.overlay, _ = ListFiles(overlay)
	return Workspace{ID: id, Dir: "/work/runs/" + id + "/ws", RepoURL: repo, Base: base}, nil
}

func (w *fakeWorkspaces) Export(context.Context, Workspace) ([]byte, error) {
	*w.log = append(*w.log, "export")
	return w.patch, w.exportErr
}

func (w *fakeWorkspaces) Destroy(context.Context, Workspace) error {
	*w.log = append(*w.log, "destroy")
	w.destroyed++
	return nil
}

type fakeArtifacts struct {
	log     *[]string
	saved   []byte
	written strings.Builder
}

func (a *fakeArtifacts) IODir(_ context.Context, id string) (string, error) {
	*a.log = append(*a.log, "iodir")
	return "/work/runs/" + id + "/io", nil
}

func (a *fakeArtifacts) Transcript(context.Context, string) (io.WriteCloser, error) {
	return nopCloser{&a.written}, nil
}

func (a *fakeArtifacts) SavePatch(_ context.Context, _ string, p []byte) error {
	a.saved = p
	return nil
}

func (a *fakeArtifacts) Collect(context.Context, string) error {
	*a.log = append(*a.log, "collect")
	return nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type fakeArm struct {
	name       string
	version    string
	result     AttemptResult
	err        error
	transcript string
	calls      []AttemptInput
	log        *[]string
}

func (a *fakeArm) Name() string                            { return a.name }
func (a *fakeArm) Version(context.Context) (string, error) { return a.version, nil }
func (a *fakeArm) Attempt(_ context.Context, in AttemptInput) (AttemptResult, error) {
	*a.log = append(*a.log, "attempt")
	a.calls = append(a.calls, in)
	io.WriteString(in.Transcript, a.transcript)
	return a.result, a.err
}

type gradeRecorder struct {
	*fakeGrader
	log *[]string
}

func (g gradeRecorder) Grade(ctx context.Context, in GradeInput) (GradeOutput, error) {
	*g.log = append(*g.log, "grade")
	return g.fakeGrader.Grade(ctx, in)
}

type harness struct {
	p     Replayer
	runs  *fakeRuns
	store *memStore
	ws    *fakeWorkspaces
	art   *fakeArtifacts
	arm   *fakeArm
	g     *fakeGrader
	log   []string
	rec   Record
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{runs: newFakeRuns(), store: newMemStore()}
	h.ws = &fakeWorkspaces{log: &h.log, patch: []byte("diff --git a/sub.go b/sub.go\n")}
	h.art = &fakeArtifacts{log: &h.log}
	h.arm = &fakeArm{name: "a1", version: "codex-cli 0.160.0/default", log: &h.log,
		result: AttemptResult{Status: StatusSucceeded, Claimed: ClaimDone, Usage: Usage{Input: 10, Output: 5}}}
	h.g = &fakeGrader{ref: GradeOutput{HiddenPassed: 3}}
	r := validRecord()
	r.WallBudget = 20 * time.Minute
	files := RecordFiles{
		Visible: fstest.MapFS{"v_test.go": {Data: []byte("v")}},
		Hidden:  fstest.MapFS{"sub_test.go": {Data: []byte("h")}},
	}
	h.rec, _ = h.store.AddRecord(context.Background(), r, files)
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	h.p = Replayer{
		Records: h.store, Runs: h.runs, Workspaces: h.ws, Artifacts: h.art,
		Grader:         gradeRecorder{h.g, &h.log},
		PromptTemplate: "Task:\n{{REQUEST}}\nVisible tests:\n{{VISIBLE_TESTS}}\n",
		StateDir:       "/Users/op/.foreman/state",
		PID:            PIDFingerprint{PID: 42, StartedAt: "x"},
		TokenCap:       1000,
		Now:            func() time.Time { clock = clock.Add(time.Minute); return clock },
	}
	return h
}

func (h *harness) replay(t *testing.T, req ReplayRequest) ReplayResult {
	t.Helper()
	if req.Record.ID == "" {
		req.Record = h.rec
	}
	if req.Arm == nil {
		req.Arm = h.arm
	}
	if req.ArmName == "" {
		req.ArmName = "a1"
	}
	if req.Rep == 0 {
		req.Rep = 1
	}
	res, err := h.p.ReplayOnce(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// --- tests ---

func TestReplaySuccess(t *testing.T) {
	h := newHarness(t)
	res := h.replay(t, ReplayRequest{})
	if res.Reused {
		t.Error("first run reported as reused")
	}
	if res.Run.Status != StatusSucceeded || res.Grade == nil || !res.Grade.Success || res.Grade.FalseSuccess {
		t.Errorf("run = %+v grade = %+v", res.Run, res.Grade)
	}
	if res.Run.Usage.Input != 10 || res.Run.Claimed != ClaimDone || res.Run.Wall <= 0 {
		t.Errorf("run did not carry the attempt result: %+v", res.Run)
	}
	want := []string{"prepare", "iodir", "attempt", "export", "collect", "grade", "destroy"}
	if !slices.Equal(h.log, want) {
		t.Errorf("call order = %v, want %v", h.log, want)
	}
	in := h.arm.calls[0]
	if in.RunDir != "/work/runs/"+res.Run.ID+"/io" || in.WallCap != 20*time.Minute || in.TokenCap != 1000 {
		t.Errorf("attempt input = %+v", in)
	}
	if !strings.Contains(in.Prompt, h.rec.Request) || !strings.Contains(in.Prompt, "- v_test.go") {
		t.Errorf("prompt = %q", in.Prompt)
	}
	if strings.Contains(in.Prompt, "sub_test.go") || strings.Contains(in.Prompt, h.p.StateDir) {
		t.Errorf("prompt leaks hidden tests or state: %q", in.Prompt)
	}
	if string(h.art.saved) != string(h.ws.patch) {
		t.Errorf("patch not saved")
	}
	if got := h.g.calls[0]; string(got.Patch) != string(h.ws.patch) || got.Hidden == nil || got.Visible == nil {
		t.Errorf("grader input = %+v", got)
	}
	if len(h.runs.finished) != 1 {
		t.Errorf("FinishRun called %d times, want 1", len(h.runs.finished))
	}
	if !strings.HasPrefix(res.Run.Key.ArmVersion, "codex-cli 0.160.0/default/") || len(res.Run.Key.ArmVersion) != len("codex-cli 0.160.0/default/")+12 {
		t.Errorf("arm version = %q, want <cli>/<prompt sha256[:12]>", res.Run.Key.ArmVersion)
	}
}

func TestReplayCacheHit(t *testing.T) {
	h := newHarness(t)
	first := h.replay(t, ReplayRequest{})
	h.log = nil
	second := h.replay(t, ReplayRequest{})
	if !second.Reused || second.Run.ID != first.Run.ID {
		t.Errorf("second run = %+v, want reuse of %s", second, first.Run.ID)
	}
	if len(h.arm.calls) != 1 || len(h.log) != 0 {
		t.Errorf("cache hit called the arm or other ports: %v", h.log)
	}
	if second.Grade == nil || !second.Grade.Success {
		t.Errorf("cache hit lost its grade: %+v", second.Grade)
	}
}

func TestReplayNoCacheIsNeverReused(t *testing.T) {
	h := newHarness(t)
	a := h.replay(t, ReplayRequest{NoCacheNonce: "n1"})
	b := h.replay(t, ReplayRequest{NoCacheNonce: "n2"})
	if a.Run.ID == b.Run.ID || b.Reused || !strings.HasSuffix(b.Run.Key.ArmVersion, "/nocache-n2") {
		t.Errorf("no-cache runs: %s %s reused=%v version=%s", a.Run.ID, b.Run.ID, b.Reused, b.Run.Key.ArmVersion)
	}
}

func TestReplayInfraErrorIsRetried(t *testing.T) {
	h := newHarness(t)
	h.ws.prepareErr = errors.New("git: network down")
	first := h.replay(t, ReplayRequest{})
	if first.Run.Status != StatusInfraError || first.Grade != nil || !strings.Contains(first.Run.Error, "network down") {
		t.Fatalf("first = %+v", first)
	}
	h.ws.prepareErr = nil
	second := h.replay(t, ReplayRequest{})
	if second.Reused || second.Run.ID != first.Run.ID || second.Run.Status != StatusSucceeded {
		t.Errorf("infra_error row not retried: %+v", second)
	}
}

func TestReplayBaseNoLongerFetchable(t *testing.T) {
	h := newHarness(t)
	ok := true
	h.store.SetReplayable(context.Background(), h.rec.ID, Validation{Replayable: ok})
	h.ws.prepareErr = fmt.Errorf("mirror: %w", ErrBaseNotFound)
	res := h.replay(t, ReplayRequest{})
	if res.Run.Status != StatusInfraError {
		t.Errorf("status = %s, want infra_error", res.Run.Status)
	}
	got := h.store.records[h.rec.ID]
	if got.Replayable == nil || *got.Replayable || got.NotReplayableReason != ReasonBaseGone {
		t.Errorf("record = %v %q, want not replayable: %s", got.Replayable, got.NotReplayableReason, ReasonBaseGone)
	}
	if h.ws.destroyed != 0 {
		t.Error("Destroy called for a workspace that was never prepared")
	}
}

func TestReplayCapsStillGradeForEvidence(t *testing.T) {
	for _, status := range []RunStatus{StatusTimedOut, StatusBudgetExceeded} {
		t.Run(string(status), func(t *testing.T) {
			h := newHarness(t)
			h.arm.result.Status = status
			res := h.replay(t, ReplayRequest{})
			if res.Run.Status != status || res.Grade == nil || res.Grade.Success {
				t.Errorf("run = %+v grade = %+v", res.Run, res.Grade)
			}
			want := []string{"prepare", "iodir", "attempt", "export", "collect", "grade", "destroy"}
			if !slices.Equal(h.log, want) {
				t.Errorf("call order = %v, want %v", h.log, want)
			}
		})
	}
}

func TestReplayArmInfraError(t *testing.T) {
	h := newHarness(t)
	h.arm.result = AttemptResult{Status: StatusInfraError, Error: "rate limited until 13:00"}
	res := h.replay(t, ReplayRequest{})
	if res.Run.Status != StatusInfraError || res.Grade != nil || !strings.Contains(res.Run.Error, "rate limited") {
		t.Errorf("run = %+v", res.Run)
	}
	if slices.Contains(h.log, "grade") || !slices.Contains(h.log, "collect") || !slices.Contains(h.log, "destroy") {
		t.Errorf("calls = %v", h.log)
	}
}

func TestReplayGraderError(t *testing.T) {
	h := newHarness(t)
	h.g.refErr = errors.New("docker: daemon not running")
	res := h.replay(t, ReplayRequest{})
	if res.Run.Status != StatusInfraError || res.Grade != nil {
		t.Errorf("run = %+v grade = %+v", res.Run, res.Grade)
	}
	if h.ws.destroyed != 1 {
		t.Error("Destroy not called")
	}
}

func TestReplayContaminated(t *testing.T) {
	h := newHarness(t)
	h.arm.transcript = `{"cmd":"cat ../sub_test.go"}`
	res := h.replay(t, ReplayRequest{})
	if res.Run.Status != StatusContaminated || !res.Grade.Contaminated || res.Grade.Success {
		t.Errorf("run = %+v grade = %+v", res.Run, res.Grade)
	}
}

func TestReplayEmptyPatchGradesBase(t *testing.T) {
	h := newHarness(t)
	h.ws.patch = nil
	h.g.base = GradeOutput{HiddenFailed: 3}
	res := h.replay(t, ReplayRequest{})
	if h.g.calls[0].Patch != nil || res.Run.Status != StatusFailed || !res.Grade.FalseSuccess {
		t.Errorf("empty patch: grader got %q, run %+v grade %+v", h.g.calls[0].Patch, res.Run, res.Grade)
	}
}

func TestReplayRegressionUsesBaseFullSuite(t *testing.T) {
	h := newHarness(t)
	passed := true
	rec := h.store.records[h.rec.ID]
	rec.FullTestCommand, rec.BaseFullSuitePassed = []string{"go", "test", "./..."}, &passed
	h.store.records[h.rec.ID] = rec
	fails := false
	h.g.ref = GradeOutput{HiddenPassed: 3, FullSuitePassed: &fails}
	res := h.replay(t, ReplayRequest{Record: rec})
	if h.g.calls[0].FullTest == nil || res.Grade.FullSuiteRegressed == nil || !*res.Grade.FullSuiteRegressed || res.Grade.Success {
		t.Errorf("regression not detected: grader full=%v grade=%+v", h.g.calls[0].FullTest, res.Grade)
	}

	// A suite that was already red on base is never run, so it can't count as a regression.
	h2 := newHarness(t)
	red := false
	rec2 := h2.store.records[h2.rec.ID]
	rec2.FullTestCommand, rec2.BaseFullSuitePassed = []string{"go", "test", "./..."}, &red
	res2 := h2.replay(t, ReplayRequest{Record: rec2})
	if h2.g.calls[0].FullTest != nil || res2.Grade.FullSuiteRegressed != nil {
		t.Errorf("red base suite: grader full=%v regressed=%v", h2.g.calls[0].FullTest, res2.Grade.FullSuiteRegressed)
	}
}

func TestReplayRunInProgress(t *testing.T) {
	h := newHarness(t)
	h.arm.version = "v"
	key := RunKey{RecordID: h.rec.ID, Arm: "a1", ArmVersion: "v/" + promptHash(h.p.PromptTemplate), Rep: 1, Base: h.rec.BaseCommit}
	h.runs.rows[key] = ReplayRun{ID: "run_x", Key: key, Status: StatusRunning}
	_, err := h.p.ReplayOnce(context.Background(), ReplayRequest{Record: h.rec, Arm: h.arm, ArmName: "a1", Rep: 1})
	if !errors.Is(err, ErrRunInProgress) {
		t.Errorf("err = %v, want ErrRunInProgress", err)
	}
}

func TestRenderPrompt(t *testing.T) {
	got := RenderPrompt("R: {{REQUEST}}\nV:\n{{VISIBLE_TESTS}}", "Add Sub\n", nil)
	if got != "R: Add Sub\nV:\n(none)" {
		t.Errorf("RenderPrompt = %q", got)
	}
	got = RenderPrompt("{{VISIBLE_TESTS}}", "x", []string{"a_test.go", "p/b_test.go"})
	if got != "- a_test.go\n- p/b_test.go" {
		t.Errorf("RenderPrompt list = %q", got)
	}
}
