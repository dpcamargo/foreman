package hermes

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

type fixture struct {
	bin, root, ws, io, trusted string
	transcript                 *bytes.Buffer
	cfg                        Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{bin: t.TempDir(), root: t.TempDir(), transcript: &bytes.Buffer{}}
	for _, name := range []string{"fakehermes", "result.jsonl", "session.jsonl"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(f.bin, name), b, 0o755)
	}
	f.ws = filepath.Join(f.root, "work", "runs", "run_1", "ws")
	f.io = filepath.Join(f.root, "work", "runs", "run_1", "io")
	f.trusted = filepath.Join(f.root, "work", "runs", "run_1", "trusted.git")
	os.MkdirAll(f.io, 0o755)
	// A real repo, with the trusted git dir copied out as sandbox.Prepare does.
	os.MkdirAll(f.ws, 0o755)
	os.WriteFile(filepath.Join(f.ws, "a.go"), []byte("package a\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = f.ws
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.CopyFS(f.trusted, os.DirFS(filepath.Join(f.ws, ".git"))); err != nil {
		t.Fatal(err)
	}
	f.cfg = Config{
		Binary: filepath.Join(f.bin, "fakehermes"), HermesHome: "/Users/op/.hermes/profiles/foreman-arm",
		ModCache: filepath.Join(f.root, "work", "gomodcache"), PollInterval: 50 * time.Millisecond,
	}
	return f
}

func (f *fixture) set(name, content string) {
	os.WriteFile(filepath.Join(f.bin, name), []byte(content), 0o644)
}

func (f *fixture) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.bin, name))
	return string(b)
}

func (f *fixture) input() eval.AttemptInput {
	return eval.AttemptInput{
		Workspace: eval.Workspace{ID: "run_1", Dir: f.ws, TrustedGitDir: f.trusted},
		Prompt:    "Add Sub", WallCap: time.Minute, TokenCap: 1_000_000, RunDir: f.io, Transcript: f.transcript,
	}
}

func overCap() string {
	return `{"id": "sess_1", "input_tokens": 5000000, "output_tokens": 1, "cache_read_tokens": 0, "cache_write_tokens": 0, "reasoning_tokens": 0, "estimated_cost_usd": 9.0}` + "\n"
}

func TestVersionAndNames(t *testing.T) {
	f := newFixture(t)
	for _, arm := range []eval.Arm{NewOneshot(f.cfg), NewKanbanGoal(f.cfg)} {
		v, err := arm.Version(context.Background())
		if err != nil || v != "Hermes Agent v9.9.9 (2026.10.1)/claude-test" {
			t.Errorf("%s Version = %q, %v", arm.Name(), v, err)
		}
	}
	oneshoot := NewOneshot(f.cfg)
	if oneshoot.Name() != "a0" || NewKanbanGoal(f.cfg).Name() != "a2" {
		t.Error("arm names must be a0 and a2")
	}
	if oneshoot.Family() != "anthropic" || oneshoot.Route() != "hermes/claude" {
		t.Errorf("family/route = %q/%q", oneshoot.Family(), oneshoot.Route())
	}
}

func TestOneshotReviewStructuredOutput(t *testing.T) {
	f := newFixture(t)
	f.set("result.jsonl", `{"type":"result","session_id":"sess_1","exit_code":0,"text":"{\"status\":\"approved\",\"summary\":\"looks good\",\"findings\":[]}","tokens":{}}`+"\n")
	res, err := NewOneshot(f.cfg).Review(context.Background(), engine.ReviewInput{Spec: engine.TaskSpec{Summary: "Add Sub"}, Patch: []byte("diff")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Approved || res.Summary != "looks good" {
		t.Fatalf("review = %+v", res)
	}
}

func TestOneshotInvocation(t *testing.T) {
	t.Setenv("FOREMAN_HOME", "/Users/op/.foreman")
	f := newFixture(t)
	res, err := NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimDone {
		t.Errorf("result = %+v", res)
	}
	want := []string{"chat", "--oneshot", "--format", "stream-json", "--query-file", filepath.Join(f.io, "prompt.md"),
		"--in", f.ws, "-t", "file,terminal", "--max-turns", "200", "--run-budget", "60", "--source", "tool", "--yolo"}
	if got := strings.Fields(f.read("chat_argv")); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v\nwant %v", got, want)
	}
	env := f.read("chat_env")
	if !strings.Contains(env, "HERMES_HOME="+f.cfg.HermesHome+"\n") {
		t.Error("HERMES_HOME not set to the arm profile")
	}
	if strings.Contains(env, "FOREMAN_") || strings.Contains(f.read("calls"), "/state") {
		t.Error("foreman's environment or a state path reached hermes")
	}
	if p, _ := os.ReadFile(filepath.Join(f.io, "prompt.md")); string(p) != "Add Sub" {
		t.Errorf("prompt.md = %q", p)
	}
	u := res.Usage
	if u.Input != 12 || u.Output != 2475 || u.Cached != 90812 || u.CacheWrite != 92553 || u.USD == nil || *u.USD != 0.2743189 || u.CostSource != "hermes-estimate" {
		t.Errorf("usage = %+v", u)
	}
	if !strings.Contains(f.read("calls"), "--session-id sess_1") {
		t.Error("usage was not looked up by the run's session id")
	}
	if !strings.Contains(env, `TERMINAL_DOCKER_VOLUMES=["`+f.ws+`:/workspace","`+f.cfg.ModCache+`:/go/pkg/mod:ro"]`) {
		t.Errorf("run did not pass its own workspace mount:\n%s", env)
	}
	if !strings.Contains(f.transcript.String(), `"type": "result"`) {
		t.Error("stream not copied to the transcript")
	}
}

func TestOneshotFallbackClaim(t *testing.T) {
	f := newFixture(t)
	f.set("result.jsonl", `{"type":"result","session_id":"s","exit_code":0,"text":"I made the change.","tokens":{"input":1,"output":1}}`+"\n")
	res, _ := NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if res.Claimed != eval.ClaimNone {
		t.Errorf("no diff, no JSON: claimed = %s, want none", res.Claimed)
	}
	os.WriteFile(filepath.Join(f.ws, "sub.go"), []byte("package a\n"), 0o644) // a change
	res, _ = NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if res.Claimed != eval.ClaimDone {
		t.Errorf("diff, no JSON: claimed = %s, want done (research R7)", res.Claimed)
	}
}

func TestOneshotClarification(t *testing.T) {
	f := newFixture(t)
	f.set("result.jsonl", `{"type":"result","exit_code":0,"text":"{\"status\": \"needs_clarification\", \"summary\": \"contradictory\", \"question\": \"sum or product?\"}","tokens":{}}`+"\n")
	res, _ := NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if res.Claimed != eval.ClaimClarification || res.Question != "sum or product?" {
		t.Errorf("result = %+v", res)
	}
}

func TestOneshotWallCapKillsGroup(t *testing.T) {
	f := newFixture(t)
	f.set("chat_mode", "sleep")
	in := f.input()
	in.WallCap = time.Second
	res, _ := NewOneshot(f.cfg).Attempt(context.Background(), in)
	if res.Status != eval.StatusTimedOut {
		t.Errorf("status = %s, want timed_out", res.Status)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(f.read("child")))
	time.Sleep(200 * time.Millisecond)
	if pid > 0 && exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil {
		t.Errorf("grandchild %d survived", pid)
		exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
}

func TestOneshotTokenCapStopsMidRun(t *testing.T) {
	f := newFixture(t)
	f.set("chat_mode", "sleep")
	f.set("session.jsonl", overCap())
	start := time.Now()
	res, _ := NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if res.Status != eval.StatusBudgetExceeded || time.Since(start) > 20*time.Second {
		t.Errorf("status = %s after %s, want budget_exceeded quickly", res.Status, time.Since(start))
	}
	if pid, _ := strconv.Atoi(strings.TrimSpace(f.read("child"))); pid > 0 {
		exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
}

func TestOneshotProviderErrorIsInfra(t *testing.T) {
	f := newFixture(t)
	f.set("chat_mode", "provider")
	res, _ := NewOneshot(f.cfg).Attempt(context.Background(), f.input())
	if res.Status != eval.StatusInfraError || !strings.Contains(res.Error, "429") {
		t.Errorf("result = %+v", res)
	}
}

func TestKanbanDone(t *testing.T) {
	f := newFixture(t)
	f.set("summary.json", `"{\"status\": \"done\", \"summary\": \"added Sub\", \"question\": null}"`)
	res, err := NewKanbanGoal(f.cfg).Attempt(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimDone || res.Usage.Output != 2475 {
		t.Errorf("result = %+v", res)
	}
	want := []string{"create", "run_1", "--body-file", filepath.Join(f.io, "prompt.md"), "--assignee", "foreman-arm",
		"--workspace", "dir:" + f.ws, "--goal", "--goal-max-turns", "20", "--max-runtime", "60", "--max-retries", "1", "--json"}
	if got := strings.Split(strings.TrimSpace(f.read("create_argv")), "\n"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("create argv = %v\nwant %v", got, want)
	}
	calls := f.read("calls")
	for _, w := range []string{"kanban --board foreman-eval dispatch --max 1", "kanban --board foreman-eval show t_abc --json", "kanban --board foreman-eval log t_abc"} {
		if !strings.Contains(calls, w) {
			t.Errorf("calls lack %q:\n%s", w, calls)
		}
	}
	if !strings.Contains(f.transcript.String(), "worker: ran go test") {
		t.Error("worker log not copied to the transcript")
	}
	if !strings.Contains(calls, "--source kanban --title run_1") {
		t.Error("worker usage not looked up by source and card title")
	}
	if !strings.Contains(f.read("dispatch_env"), "TERMINAL_DOCKER_VOLUMES=") {
		t.Error("dispatch (whose env the worker inherits) lacks the run's mounts")
	}
}

func TestKanbanNotSpawnedByUsIsInfra(t *testing.T) {
	f := newFixture(t)
	f.set("dispatch_none", "1")
	res, _ := NewKanbanGoal(f.cfg).Attempt(context.Background(), f.input())
	if res.Status != eval.StatusInfraError || !strings.Contains(res.Error, "not spawned by this dispatch") {
		t.Errorf("result = %+v", res)
	}
	if calls := f.read("calls"); !strings.Contains(calls, "archive t_abc") {
		t.Error("card not stopped")
	}
}

func TestKanbanBlockedIsNoClaim(t *testing.T) {
	f := newFixture(t)
	f.set("kanban_status", "blocked")
	res, _ := NewKanbanGoal(f.cfg).Attempt(context.Background(), f.input())
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimNone {
		t.Errorf("result = %+v", res)
	}
}

func TestKanbanWallCap(t *testing.T) {
	f := newFixture(t)
	f.set("kanban_status", "running")
	in := f.input()
	in.WallCap = 300 * time.Millisecond
	res, _ := NewKanbanGoal(f.cfg).Attempt(context.Background(), in)
	if res.Status != eval.StatusTimedOut {
		t.Errorf("status = %s, want timed_out", res.Status)
	}
	calls := f.read("calls")
	if !strings.Contains(calls, "reclaim t_abc") || !strings.Contains(calls, "archive t_abc") {
		t.Errorf("card not reclaimed and archived:\n%s", calls)
	}
}

func TestKanbanTokenCap(t *testing.T) {
	f := newFixture(t)
	f.set("kanban_status", "running")
	f.set("session.jsonl", overCap())
	res, _ := NewKanbanGoal(f.cfg).Attempt(context.Background(), f.input())
	if res.Status != eval.StatusBudgetExceeded {
		t.Errorf("status = %s, want budget_exceeded", res.Status)
	}
	if calls := f.read("calls"); !strings.Contains(calls, "reclaim t_abc") || !strings.Contains(calls, "archive t_abc") {
		t.Errorf("card not reclaimed and archived:\n%s", calls)
	}
}

func TestParseClaim(t *testing.T) {
	tests := []struct {
		text  string
		claim eval.Claim
	}{
		{`{"status":"done","summary":"x","question":null}`, eval.ClaimDone},
		{"Summary first.\n```json\n{\"status\": \"needs_clarification\", \"summary\": \"y\", \"question\": \"q?\"}\n```", eval.ClaimClarification},
		{`{"status":"maybe"}`, ""},
		{"no json here", ""},
	}
	for _, tt := range tests {
		if c, _ := parseClaim(tt.text); c != tt.claim {
			t.Errorf("parseClaim(%q) = %q, want %q", tt.text, c, tt.claim)
		}
	}
}

func TestVerifyProfile(t *testing.T) {
	f := newFixture(t)
	profile := t.TempDir()
	f.cfg.HermesHome = profile
	ctx := context.Background()
	if err := VerifyProfile(ctx, f.cfg); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing profile: err = %v", err)
	}
	if strings.Contains(f.read("calls"), "config") {
		t.Error("hermes was invoked for a missing profile")
	}
	os.WriteFile(filepath.Join(profile, "config.yaml"), []byte("x: 1\n"), 0o644)
	f.set("config_terminal.backend", "docker\n")
	f.set("config_terminal.docker_network", "false\n")
	if err := VerifyProfile(ctx, f.cfg); err != nil { // mcp_servers unset: hermes exits 1, like a fresh profile
		t.Errorf("confined profile with mcp_servers unset rejected: %v", err)
	}
	f.set("config_mcp_servers", "\n")
	if err := VerifyProfile(ctx, f.cfg); err != nil {
		t.Errorf("confined profile rejected: %v", err)
	}
	f.set("config_terminal.docker_volumes", `["/x:/workspace"]`+"\n") // a profile-level mount would override per-run mounts
	if err := VerifyProfile(ctx, f.cfg); err == nil {
		t.Error("profile with terminal.docker_volumes accepted")
	}
	f.set("config_terminal.docker_volumes", "[]\n")
	if err := VerifyProfile(ctx, f.cfg); err != nil {
		t.Errorf("docker_volumes [] rejected: %v", err)
	}
	os.Remove(filepath.Join(f.bin, "config_terminal.backend")) // unset backend must fail
	if err := VerifyProfile(ctx, f.cfg); err == nil {
		t.Error("unset terminal.backend accepted")
	}
	f.set("config_terminal.backend", "docker\n")
	for key, bad := range map[string]string{
		"terminal.backend":        "local\n",
		"terminal.docker_network": "true\n",
		"mcp_servers":             "github:\n  command: x\n",
	} {
		orig := f.read("config_" + key)
		f.set("config_"+key, bad)
		if err := VerifyProfile(ctx, f.cfg); err == nil {
			t.Errorf("%s = %q accepted", key, bad)
		}
		f.set("config_"+key, orig)
	}
}
