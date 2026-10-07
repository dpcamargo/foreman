// Package codex implements the A1 baseline arm: one `codex exec` session, confined by a
// read-allowlist permissions profile (specs/001-evaluation-harness/contracts/arms.md).
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/runner/gomod"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/schemas"
)

// Config configures the Codex arm.
type Config struct {
	Binary    string // path to codex; "codex" uses PATH
	Model     string // "" = the CLI's configured default
	GoRoot    string // `go env GOROOT`; readable by the arm
	ModCache  string // $FOREMAN_HOME/work/gomodcache; readable by the arm
	Home      string // operator HOME (codex reads its own auth there, outside the sandbox)
	Path      string // PATH for codex and the commands it runs
	CodexHome string // optional CODEX_HOME
	// ToolRoots are software install roots the arm may read, e.g. /opt/homebrew. Codex's
	// own sandbox helper re-executes the codex binary under the profile, so its install
	// root must be readable (verified 2026-10-07, codex-cli 0.160.0).
	ToolRoots []string
}

// Arm is the A1 arm.
type Arm struct{ cfg Config }

// New returns the A1 arm.
func New(cfg Config) *Arm {
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}
	return &Arm{cfg: cfg}
}

// Name implements eval.Arm.
func (a *Arm) Name() string { return "a1" }

// Version implements eval.Arm: "<codex --version>/<model>".
func (a *Arm) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, a.cfg.Binary, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("codex --version: %w", err)
	}
	model := a.cfg.Model
	if model == "" {
		model = "default"
	}
	return strings.TrimSpace(string(out)) + "/" + model, nil
}

// args builds the exact invocation from contracts/arms.md.
func (a *Arm) args(ws, io string) []string {
	entries := []string{`":minimal"="read"`, strconv.Quote(ws) + `="write"`,
		strconv.Quote(a.cfg.GoRoot) + `="read"`, strconv.Quote(a.cfg.ModCache) + `="read"`}
	for _, r := range a.cfg.ToolRoots {
		entries = append(entries, strconv.Quote(r)+`="read"`)
	}
	perm := "permissions.fmarm.filesystem={" + strings.Join(entries, ", ") + "}"
	args := []string{
		"exec", "--ephemeral", "--json", "-C", ws,
		"-c", `default_permissions="fmarm"`, "-c", perm,
		"--output-schema", filepath.Join(io, "arm_result.schema.json"),
		"-o", filepath.Join(io, "last.json"),
	}
	if a.cfg.Model != "" {
		args = append(args, "-m", a.cfg.Model)
	}
	return append(args, "-")
}

// env is the arm's whole environment: nothing else from the operator's is passed on.
func (a *Arm) env(ws string) []string {
	fm := filepath.Join(ws, ".fm")
	env := []string{
		"HOME=" + a.cfg.Home, "PATH=" + a.cfg.Path, "LANG=en_US.UTF-8",
		"TMPDIR=" + filepath.Join(fm, "tmp"), "GOCACHE=" + filepath.Join(fm, "gocache"),
		"GOPATH=" + filepath.Join(fm, "gopath"), "GOMODCACHE=" + a.cfg.ModCache,
		"GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local",
	}
	if a.cfg.CodexHome != "" {
		env = append(env, "CODEX_HOME="+a.cfg.CodexHome)
	}
	return env
}

// Attempt implements eval.Arm.
func (a *Arm) Attempt(ctx context.Context, in eval.AttemptInput) (eval.AttemptResult, error) {
	ws, io := in.Workspace.Dir, in.RunDir
	for _, d := range []string{"tmp", "gocache", "gopath"} {
		if err := os.MkdirAll(filepath.Join(ws, ".fm", d), 0o755); err != nil {
			return eval.AttemptResult{}, fmt.Errorf("prepare workspace dirs: %w", err)
		}
	}
	promptPath := filepath.Join(io, "prompt.md")
	if err := os.WriteFile(promptPath, []byte(in.Prompt), 0o644); err != nil {
		return eval.AttemptResult{}, fmt.Errorf("write prompt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(io, "arm_result.schema.json"), schemas.ArmResult, 0o644); err != nil {
		return eval.AttemptResult{}, fmt.Errorf("write schema: %w", err)
	}
	if err := gomod.Warm(ctx, ws, a.cfg.ModCache); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "warm module cache: " + err.Error()}, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, in.WallCap)
	defer cancel()
	stdin, err := os.Open(promptPath)
	if err != nil {
		return eval.AttemptResult{}, fmt.Errorf("open prompt: %w", err)
	}
	defer stdin.Close()

	cmd := exec.CommandContext(runCtx, a.cfg.Binary, a.args(ws, io)...)
	cmd.Dir, cmd.Env, cmd.Stdin = ws, a.env(ws), stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stderr := &tailBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return eval.AttemptResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return eval.AttemptResult{Status: eval.StatusInfraError, Error: "start codex: " + err.Error()}, nil
	}
	usage, completed, lastErr := readEvents(stdout, in.Transcript)
	waitErr := cmd.Wait()

	res := eval.AttemptResult{Status: eval.StatusSucceeded, Usage: usage}
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		res.Status = eval.StatusTimedOut
	case ctx.Err() != nil:
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage, Error: "cancelled"}, nil
	case waitErr != nil && !completed:
		return eval.AttemptResult{Status: eval.StatusInfraError, Usage: usage,
			Error: fmt.Sprintf("codex exited: %v: %s %s", waitErr, lastErr, strings.TrimSpace(stderr.String()))}, nil
	}
	res.Claimed, res.Question = readClaim(filepath.Join(io, "last.json"))
	if res.Status == eval.StatusSucceeded && in.TokenCap > 0 && usage.Billable() > in.TokenCap {
		res.Status = eval.StatusBudgetExceeded // codex reports usage only at turn end (research R8)
	}
	return res, nil
}

type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"` // type "error"
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"` // type "turn.failed"
	Usage *struct {
		Input      int64 `json:"input_tokens"`
		Cached     int64 `json:"cached_input_tokens"`
		CacheWrite int64 `json:"cache_write_input_tokens"`
		Output     int64 `json:"output_tokens"`
		Reasoning  int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

// readEvents copies codex's JSONL stream to the transcript, sums turn usage, and keeps
// the last error event. Codex counts cached tokens inside input_tokens; Usage.Input is
// uncached only.
func readEvents(r io.Reader, transcript io.Writer) (u eval.Usage, completed bool, lastErr string) {
	u = eval.Usage{CostSource: "none"}
	sc := bufio.NewScanner(io.TeeReader(r, transcript))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var ev codexEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "error" && ev.Message != "":
			lastErr = ev.Message
		case ev.Type == "turn.failed" && ev.Error != nil:
			lastErr = ev.Error.Message
		}
		if ev.Type != "turn.completed" || ev.Usage == nil {
			continue
		}
		completed = true
		u.Input += ev.Usage.Input - ev.Usage.Cached
		u.Cached += ev.Usage.Cached
		u.CacheWrite += ev.Usage.CacheWrite
		u.Output += ev.Usage.Output
		u.Reasoning += ev.Usage.Reasoning
	}
	io.Copy(io.Discard, r) // drain anything after a too-long line
	return u, completed, lastErr
}

// readClaim parses the structured final answer; anything else is "no claim".
func readClaim(path string) (eval.Claim, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return eval.ClaimNone, ""
	}
	var out struct {
		Status   string `json:"status"`
		Question string `json:"question"`
	}
	if json.Unmarshal(bytes.TrimSpace(b), &out) != nil {
		return eval.ClaimNone, ""
	}
	switch out.Status {
	case string(eval.ClaimDone):
		return eval.ClaimDone, ""
	case string(eval.ClaimClarification):
		return eval.ClaimClarification, out.Question
	}
	return eval.ClaimNone, ""
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
