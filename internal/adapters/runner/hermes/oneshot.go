package hermes

import (
	"bufio"
	"context"
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
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// Oneshot is arm A0: one single-shot Hermes session on Claude.
type Oneshot struct{ cfg Config }

// NewOneshot returns arm A0.
func NewOneshot(cfg Config) *Oneshot { return &Oneshot{cfg: cfg.withDefaults()} }

// Name implements eval.Arm.
func (a *Oneshot) Name() string { return "a0" }

// Version implements eval.Arm.
func (a *Oneshot) Version(ctx context.Context) (string, error) { return a.cfg.version(ctx) }

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
	cmd.Env = a.cfg.env()
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
				if u, err := a.cfg.sessionUsage(runCtx, ws.Dir, start); err == nil && in.TokenCap > 0 && u.Billable() > in.TokenCap {
					overBudget.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	result, sawResult := readStream(stdout, in.Transcript)
	waitErr := cmd.Wait()
	cancel()
	<-pollDone

	usage, uerr := a.cfg.sessionUsage(context.WithoutCancel(ctx), ws.Dir, start)
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

// readStream copies the JSONL stream to the transcript and returns the result event.
func readStream(r io.Reader, transcript io.Writer) (resultEvent, bool) {
	var res resultEvent
	saw := false
	sc := bufio.NewScanner(io.TeeReader(r, transcript))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
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
