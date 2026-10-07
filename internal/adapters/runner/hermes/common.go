// Package hermes implements the Hermes baseline arms: A0, one single-shot session
// (`hermes chat --oneshot`), and A2, a Kanban card in a `--goal` loop. Both run under
// a dedicated profile whose tools execute in a Docker container that sees only the
// workspace (specs/001-evaluation-harness/research.md R1, R7).
package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// Config configures both Hermes arms.
type Config struct {
	Binary       string        // "hermes" uses PATH
	HermesHome   string        // the foreman-arm profile
	ModCache     string        // $FOREMAN_HOME/work/gomodcache (the profile mounts it read-only)
	Board        string        // A2 kanban board; default "foreman-eval"
	Assignee     string        // A2 worker profile; default "foreman-arm"
	MaxTurns     int           // A0; default 200
	GoalMaxTurns int           // A2; default 20
	PollInterval time.Duration // usage and card polling; default 10s
}

func (c Config) withDefaults() Config {
	if c.Binary == "" {
		c.Binary = "hermes"
	}
	if c.Board == "" {
		c.Board = "foreman-eval"
	}
	if c.Assignee == "" {
		c.Assignee = "foreman-arm"
	}
	if c.MaxTurns == 0 {
		c.MaxTurns = 200
	}
	if c.GoalMaxTurns == 0 {
		c.GoalMaxTurns = 20
	}
	if c.PollInterval == 0 {
		c.PollInterval = 10 * time.Second
	}
	return c
}

// env is the operator's environment with HERMES_HOME pointing at the arm profile and
// nothing of foreman's (no FOREMAN_* variables, so no state path).
func (c Config) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FOREMAN_") || strings.HasPrefix(kv, "HERMES_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HERMES_HOME="+c.HermesHome)
}

func (c Config) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Env = c.env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("hermes %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// version is "<hermes --version first line>/<profile model>".
func (c Config) version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	model := "unknown"
	if cfg, err := c.run(ctx, "config", "get", "model"); err == nil {
		for _, line := range strings.Split(string(cfg), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "default:"); ok {
				model = strings.TrimSpace(v)
			}
		}
	}
	return strings.TrimSpace(first) + "/" + model, nil
}

// sessionUsage sums every session the profile ran in ws since start. Each run has its
// own workspace path, so the cwd filter isolates exactly this run's sessions.
func (c Config) sessionUsage(ctx context.Context, ws string, start time.Time) (eval.Usage, error) {
	out, err := c.run(ctx, "sessions", "export", "-", "--format", "jsonl",
		"--cwd", ws, "--after", start.UTC().Add(-5*time.Second).Format(time.RFC3339), "--yes")
	if err != nil {
		return eval.Usage{}, err
	}
	u := eval.Usage{CostSource: "hermes-estimate"}
	var usd float64
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		var s struct {
			Input      int64    `json:"input_tokens"`
			Output     int64    `json:"output_tokens"`
			CacheRead  int64    `json:"cache_read_tokens"`
			CacheWrite int64    `json:"cache_write_tokens"`
			Reasoning  int64    `json:"reasoning_tokens"`
			Cost       *float64 `json:"estimated_cost_usd"`
		}
		if !bytes.HasPrefix(bytes.TrimSpace(sc.Bytes()), []byte("{")) || json.Unmarshal(sc.Bytes(), &s) != nil {
			continue
		}
		// Anthropic-style counts: input already excludes cache reads and writes.
		u.Input += s.Input
		u.Output += s.Output
		u.Cached += s.CacheRead
		u.CacheWrite += s.CacheWrite
		u.Reasoning += s.Reasoning
		if s.Cost != nil {
			usd += *s.Cost
		}
	}
	u.USD = &usd
	return u, nil
}

// parseClaim finds the last JSON object in text that is a valid arm_result.
func parseClaim(text string) (eval.Claim, string) {
	for i := strings.LastIndex(text, "{"); i >= 0; i = strings.LastIndex(text[:i], "{") {
		var out struct {
			Status   string  `json:"status"`
			Question *string `json:"question"`
		}
		if json.NewDecoder(strings.NewReader(text[i:])).Decode(&out) != nil {
			continue
		}
		switch out.Status {
		case string(eval.ClaimDone):
			return eval.ClaimDone, ""
		case string(eval.ClaimClarification):
			q := ""
			if out.Question != nil {
				q = *out.Question
			}
			return eval.ClaimClarification, q
		}
		if out.Status != "" {
			return "", ""
		}
	}
	return "", ""
}

// claimOrFallback applies research R7: an answer that isn't arm_result JSON counts as
// "done" if the workspace changed, and "none" otherwise.
func claimOrFallback(ctx context.Context, text string, ws eval.Workspace) (eval.Claim, string) {
	if c, q := parseClaim(text); c != "" {
		return c, q
	}
	if changed(ctx, ws) {
		return eval.ClaimDone, ""
	}
	return eval.ClaimNone, ""
}

// changed reports whether the workspace differs from base, asking only the trusted git
// dir: the workspace's own .git is arm-written and never trusted (research R6).
func changed(ctx context.Context, ws eval.Workspace) bool {
	args := []string{"--git-dir=" + ws.TrustedGitDir, "--work-tree=" + ws.Dir,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null",
		"status", "--porcelain", "--", ".", ":(exclude).fm", ":(exclude).git"}
	for _, f := range ws.Overlay {
		args = append(args, ":(exclude,literal)"+f)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = ws.Dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

func seconds(d time.Duration) string { return strconv.Itoa(int(d.Round(time.Second) / time.Second)) }

// VerifyProfile refuses to run an arm whose profile isn't confined (research R1;
// tasks T055): it must exist, run its tools in Docker with no network, and have no MCP
// servers. The existence check comes first: pointing hermes at a missing profile makes
// it build a new data root.
func VerifyProfile(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if _, err := os.Stat(filepath.Join(cfg.HermesHome, "config.yaml")); err != nil {
		return fmt.Errorf("hermes profile %s not found; create it first (quickstart step 0): %w", cfg.HermesHome, err)
	}
	get := func(key string) (string, error) {
		out, err := cfg.run(ctx, "config", "get", key)
		if err != nil && strings.Contains(err.Error(), "Config key not set") {
			return "", nil // hermes exits 1 for an unset key (verified, Hermes v0.21.5)
		}
		return strings.TrimSpace(string(out)), err
	}
	checks := []struct{ key, want string }{
		{"terminal.backend", "docker"},
		{"terminal.docker_network", "false"},
	}
	for _, c := range checks {
		got, err := get(c.key)
		if err != nil {
			return err
		}
		if got != c.want {
			return fmt.Errorf("hermes profile %s: %s is %q, want %q (quickstart step 0)", cfg.HermesHome, c.key, got, c.want)
		}
	}
	mcp, err := get("mcp_servers")
	if err != nil {
		return err
	}
	switch mcp {
	case "", "{}", "null", "None", "~":
	default:
		return fmt.Errorf("hermes profile %s has MCP servers configured; the arm profile must have none:\n%s", cfg.HermesHome, mcp)
	}
	return nil
}
