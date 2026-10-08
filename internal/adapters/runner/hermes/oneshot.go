package hermes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/runner/gomod"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// Oneshot is arm A0 and the Anthropic-family engine worker.
type Oneshot struct{ cfg Config }

var (
	_ eval.Arm           = (*Oneshot)(nil)
	_ engine.Implementer = (*Oneshot)(nil)
	_ engine.Reviewer    = (*Oneshot)(nil)
)

// NewOneshot returns arm A0.
func NewOneshot(cfg Config) *Oneshot { return &Oneshot{cfg: cfg.withDefaults()} }

// Name implements eval.Arm.
func (a *Oneshot) Name() string { return "a0" }

// Version implements eval.Arm.
func (a *Oneshot) Version(ctx context.Context) (string, error) { return a.cfg.version(ctx) }

// Family identifies the provider family for cross-family routing.
func (a *Oneshot) Family() string { return "anthropic" }

// Route identifies this worker route in persisted run records.
func (a *Oneshot) Route() string { return "hermes/claude" }

// Implement executes one engine attempt in the supplied disposable workspace.
func (a *Oneshot) Implement(ctx context.Context, in engine.ImplementInput) (engine.ImplementResult, error) {
	wall := in.WallTimeout
	if wall <= 0 {
		wall = 30 * time.Minute
	}
	runDir := filepath.Join(in.Workspace, ".fm", fmt.Sprintf("engine-%d", in.Attempt))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return engine.ImplementResult{}, fmt.Errorf("prepare hermes run directory: %w", err)
	}
	prompt, err := json.Marshal(struct {
		Instruction string                  `json:"instruction"`
		Spec        engine.TaskSpec         `json:"spec"`
		Evidence    *engine.FailureEvidence `json:"failure_evidence,omitempty"`
	}{Instruction: "Implement the specification in /workspace. Do not alter or delete tests. Return done only after running the verification commands.", Spec: in.Spec, Evidence: in.Evidence})
	if err != nil {
		return engine.ImplementResult{}, fmt.Errorf("encode implement prompt: %w", err)
	}
	attempt, err := a.Attempt(ctx, eval.AttemptInput{
		Workspace:  eval.Workspace{ID: in.TaskID, Dir: in.Workspace},
		Prompt:     string(prompt),
		WallCap:    wall,
		TokenCap:   int64(in.TokenBudget),
		RunDir:     runDir,
		Transcript: io.Discard,
	})
	if err != nil {
		return engine.ImplementResult{}, err
	}
	patch, err := hermesWorkspacePatch(ctx, in.Workspace)
	if err != nil {
		return engine.ImplementResult{}, err
	}
	hash := sha256.Sum256(patch)
	return engine.ImplementResult{
		Status:         hermesTaskRunStatus(attempt.Status),
		Patch:          patch,
		DiffHash:       hex.EncodeToString(hash[:]),
		TokensInput:    int(attempt.Usage.Input),
		TokensOutput:   int(attempt.Usage.Output),
		TokensCached:   int(attempt.Usage.Cached),
		TokensBillable: int(attempt.Usage.Billable()),
		USD:            attempt.Usage.USD,
		Claimed:        string(attempt.Claimed),
		Error:          attempt.Error,
	}, nil
}

// Review performs a cross-family structured review without exposing hidden tests.
func (a *Oneshot) Review(ctx context.Context, in engine.ReviewInput) (engine.ReviewResult, error) {
	ws, err := os.MkdirTemp("", "fm-hermes-review-")
	if err != nil {
		return engine.ReviewResult{}, fmt.Errorf("create review workspace: %w", err)
	}
	defer os.RemoveAll(ws)
	promptPath := filepath.Join(ws, "review.md")
	payload, err := json.Marshal(struct {
		Instruction string          `json:"instruction"`
		Spec        engine.TaskSpec `json:"spec"`
		Patch       string          `json:"patch"`
	}{Instruction: "Return only JSON matching review_findings.json. Blocking findings require concrete evidence and a repro.", Spec: in.Spec, Patch: string(in.Patch)})
	if err != nil {
		return engine.ReviewResult{}, fmt.Errorf("encode review prompt: %w", err)
	}
	if err := os.WriteFile(promptPath, payload, 0o600); err != nil {
		return engine.ReviewResult{}, fmt.Errorf("write review prompt: %w", err)
	}
	cmd := exec.CommandContext(ctx, a.cfg.Binary, "chat", "--oneshot", "--format", "stream-json",
		"--query-file", promptPath, "--in", ws, "-t", "file,terminal",
		"--max-turns", fmt.Sprint(a.cfg.MaxTurns), "--source", "tool", "--yolo")
	cmd.Env = a.cfg.runEnv(ws)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return engine.ReviewResult{}, fmt.Errorf("review stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return engine.ReviewResult{}, fmt.Errorf("start hermes review: %w", err)
	}
	result, saw := readStream(stdout, io.Discard, func(string) {})
	if err := cmd.Wait(); err != nil || !saw || result.ExitCode != 0 {
		return engine.ReviewResult{}, fmt.Errorf("hermes review failed: %v: %s %s", err, result.Error, strings.TrimSpace(stderr.String()))
	}
	var raw struct {
		Status   string                 `json:"status"`
		Summary  string                 `json:"summary"`
		Findings []engine.ReviewFinding `json:"findings"`
	}
	if err := decodeJSONObject(result.Text, &raw); err != nil {
		return engine.ReviewResult{}, fmt.Errorf("decode hermes review: %w", err)
	}
	return engine.ReviewResult{Approved: raw.Status == "approved", Summary: raw.Summary, Findings: raw.Findings}, nil
}

func decodeJSONObject(text string, dst any) error {
	for start := strings.Index(text, "{"); start >= 0; {
		if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(dst); err == nil {
			return nil
		}
		next := strings.Index(text[start+1:], "{")
		if next < 0 {
			break
		}
		start += next + 1
	}
	return errors.New("no JSON object found")
}

func hermesTaskRunStatus(status eval.RunStatus) task.RunStatus {
	switch status {
	case eval.StatusSucceeded:
		return task.RunSucceeded
	case eval.StatusTimedOut:
		return task.RunTimedOut
	case eval.StatusBudgetExceeded:
		return task.RunBudgetExceeded
	case eval.StatusInfraError:
		return task.RunInfraError
	default:
		return task.RunFailed
	}
}

func hermesWorkspacePatch(ctx context.Context, ws string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "diff", "--binary", "HEAD", "--", ".", ":(exclude).fm")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("export hermes patch: %w", err)
	}
	return out, nil
}

type resultEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
	Text      string `json:"text"`
	Error     string `json:"error"`
	Tokens    struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cache_read"`
		CacheWrite int64 `json:"cache_write"`
	} `json:"tokens"`
}

// Attempt implements eval.Arm.
func (a *Oneshot) Attempt(ctx context.Context, in eval.AttemptInput) (eval.AttemptResult, error) {
	ws := in.Workspace
	prompt := filepath.Join(in.RunDir, "prompt.md")
	if err := os.WriteFile(prompt, []byte(in.Prompt), 0o644); err != nil {
		return eval.AttemptResult{}, fmt.Errorf("write prompt: %w", err)
	}
	if err := gomod.Warm(ctx, ws.Dir, a.cfg.ModCache); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "warm module cache: " + err.Error()}, nil
	}
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, in.WallCap)
	defer cancel()

	cmd := exec.CommandContext(runCtx, a.cfg.Binary, "chat", "--oneshot", "--format", "stream-json",
		"--query-file", prompt, "--in", ws.Dir, "-t", "file,terminal",
		"--max-turns", fmt.Sprint(a.cfg.MaxTurns), "--run-budget", seconds(in.WallCap),
		"--source", "tool", "--yolo")
	cmd.Env = a.cfg.runEnv(ws.Dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return eval.AttemptResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "start hermes: " + err.Error()}, nil
	}

	// The stream reports tokens only at the end, so the token cap is enforced by
	// polling the session's usage (research R8).
	var overBudget atomic.Bool
	var sessionID atomic.Value // from the stream's init event
	sessionID.Store("")
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		t := time.NewTicker(a.cfg.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				id := sessionID.Load().(string)
				if id == "" || in.TokenCap <= 0 {
					continue
				}
				if u, err := a.cfg.sessionUsage(runCtx, []string{"--session-id", id}, start); err == nil && u.Billable() > in.TokenCap {
					overBudget.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	result, sawResult := readStream(stdout, in.Transcript, func(id string) { sessionID.Store(id) })
	waitErr := cmd.Wait()
	cancel()
	<-pollDone

	id := result.SessionID
	if id == "" {
		id = sessionID.Load().(string)
	}
	usage, uerr := eval.Usage{}, fmt.Errorf("no session id")
	if id != "" {
		usage, uerr = a.cfg.sessionUsage(context.WithoutCancel(ctx), []string{"--session-id", id}, start)
	}
	if uerr != nil { // fall back to the stream's own totals
		usage = eval.Usage{Input: result.Tokens.Input, Output: result.Tokens.Output,
			Cached: result.Tokens.CacheRead, CacheWrite: result.Tokens.CacheWrite, CostSource: "none"}
	}
	res := eval.AttemptResult{Status: eval.StatusSucceeded, Usage: usage}
	switch {
	case overBudget.Load():
		res.Status = eval.StatusBudgetExceeded
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.Status = eval.StatusTimedOut
	case ctx.Err() != nil:
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage, Error: "cancelled"}, nil
	case !sawResult:
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage, Error: fmt.Sprintf("hermes exited without a result: %v", waitErr)}, nil
	case result.ExitCode != 0 && result.Error != "":
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage, Error: "hermes: " + result.Error}, nil
	}
	res.Claimed, res.Question = claimOrFallback(context.WithoutCancel(ctx), result.Text, ws)
	if res.Status == eval.StatusSucceeded && in.TokenCap > 0 && usage.Billable() > in.TokenCap {
		res.Status = eval.StatusBudgetExceeded
	}
	return res, nil
}

// readStream copies the JSONL stream to the transcript, reports the session id from the
// init event, and returns the result event.
func readStream(r io.Reader, transcript io.Writer, onInit func(string)) (resultEvent, bool) {
	var res resultEvent
	saw := false
	sc := bufio.NewScanner(io.TeeReader(r, transcript))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if strings.Contains(string(line), `"init"`) {
			var ev struct {
				Subtype   string `json:"subtype"`
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Subtype == "init" && ev.SessionID != "" {
				onInit(ev.SessionID)
			}
		}
		if !strings.Contains(string(line), `"result"`) {
			continue
		}
		var ev resultEvent
		if json.Unmarshal(line, &ev) == nil && ev.Type == "result" {
			res, saw = ev, true
		}
	}
	io.Copy(io.Discard, r)
	return res, saw
}
