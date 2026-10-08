package codex

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

// setup copies the fake CLI and its data into a temp dir and returns an Arm using it.
func setup(t *testing.T, mode string, lastJSON string) (*Arm, string, eval.AttemptInput, *bytes.Buffer) {
	t.Helper()
	bin := t.TempDir()
	for _, f := range []string{"fakecodex", "events.jsonl"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(bin, f), b, 0o755)
	}
	if mode != "" {
		os.WriteFile(filepath.Join(bin, "mode"), []byte(mode), 0o644)
	}
	if lastJSON != "" {
		os.WriteFile(filepath.Join(bin, "last.json"), []byte(lastJSON), 0o644)
	}
	root := t.TempDir()
	ws := filepath.Join(root, "work", "runs", "run_1", "ws")
	io := filepath.Join(root, "work", "runs", "run_1", "io")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(io, 0o755)
	arm := New(Config{
		Binary: filepath.Join(bin, "fakecodex"), GoRoot: "/opt/go", ModCache: filepath.Join(root, "work", "gomodcache"),
		Home: "/Users/op", Path: "/usr/bin:/bin", ToolRoots: []string{"/opt/homebrew"},
	})
	var transcript bytes.Buffer
	in := eval.AttemptInput{
		Workspace: eval.Workspace{ID: "run_1", Dir: ws}, Prompt: "Add Sub", WallCap: time.Minute,
		TokenCap: 1_000_000, RunDir: io, Transcript: &transcript,
	}
	return arm, bin, in, &transcript
}

func TestVersion(t *testing.T) {
	arm, _, _, _ := setup(t, "", "")
	v, err := arm.Version(context.Background())
	if err != nil || v != "codex-cli 9.9.9/default" {
		t.Errorf("Version = %q, %v", v, err)
	}
	if arm.Name() != "a1" {
		t.Errorf("Name = %q", arm.Name())
	}
	if arm.Family() != "openai" || arm.Route() != "codex/default" {
		t.Errorf("family/route = %q/%q", arm.Family(), arm.Route())
	}
}

func TestSpecifyStructuredOutput(t *testing.T) {
	arm, _, in, _ := setup(t, "", `{"summary":"Add Sub","acceptance_criteria":["works"],"verify_commands":[["go","test","./..."]],"cited_files":["add.go"]}`)
	spec, err := arm.Specify(context.Background(), engine.SpecInput{RequestText: "add subtraction", Workspace: in.Workspace.Dir})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Summary != "Add Sub" || len(spec.VerifyCommands) != 1 || spec.CitedFiles[0] != "add.go" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestReviewStructuredOutput(t *testing.T) {
	arm, _, _, _ := setup(t, "", `{"status":"approved","summary":"looks good","findings":[]}`)
	res, err := arm.Review(context.Background(), engine.ReviewInput{Spec: engine.TaskSpec{Summary: "Add Sub"}, Patch: []byte("diff")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Approved || res.Summary != "looks good" {
		t.Fatalf("review = %+v", res)
	}
}

func TestAttemptInvocation(t *testing.T) {
	arm, bin, in, transcript := setup(t, "", `{"status":"done","summary":"added Sub"}`)
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimDone {
		t.Errorf("result = %+v", res)
	}
	argv, _ := os.ReadFile(filepath.Join(bin, "argv"))
	args := strings.Split(strings.TrimSpace(string(argv)), "\n")
	ws, io := in.Workspace.Dir, in.RunDir
	want := []string{
		"exec", "--ephemeral", "--json", "-C", ws,
		"-c", `default_permissions="fmarm"`,
		"-c", `permissions.fmarm.filesystem={":minimal"="read", ` + strconv.Quote(ws) + `="write", "/opt/go"="read", ` +
			strconv.Quote(arm.cfg.ModCache) + `="read", "/opt/homebrew"="read"}`,
		"--output-schema", filepath.Join(io, "arm_result.schema.json"),
		"-o", filepath.Join(io, "last.json"),
		"-",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Errorf("argv:\n%s\nwant:\n%s", strings.Join(args, "\n"), strings.Join(want, "\n"))
	}
	if stdin, _ := os.ReadFile(filepath.Join(bin, "stdin")); string(stdin) != "Add Sub" {
		t.Errorf("stdin = %q", stdin)
	}
	if p, _ := os.ReadFile(filepath.Join(io, "prompt.md")); string(p) != "Add Sub" {
		t.Errorf("prompt.md = %q", p)
	}
	env, _ := os.ReadFile(filepath.Join(bin, "env"))
	for _, kv := range []string{
		"HOME=/Users/op", "GOPROXY=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local",
		"TMPDIR=" + filepath.Join(ws, ".fm", "tmp"), "GOCACHE=" + filepath.Join(ws, ".fm", "gocache"),
		"GOPATH=" + filepath.Join(ws, ".fm", "gopath"), "GOMODCACHE=" + arm.cfg.ModCache,
	} {
		if !strings.Contains(string(env), kv+"\n") {
			t.Errorf("env lacks %s", kv)
		}
	}
	// Nothing from the operator's environment beyond what's listed, and no state path.
	for _, bad := range []string{"FOREMAN_", "/state", "SSH_AUTH_SOCK", "GITHUB_TOKEN"} {
		if strings.Contains(string(env)+string(argv), bad) {
			t.Errorf("arm environment or argv contains %q", bad)
		}
	}
	if !strings.Contains(transcript.String(), `"turn.completed"`) {
		t.Errorf("transcript not streamed: %q", transcript.String())
	}
	// Real usage from codex-cli 0.160.0: input includes cached tokens.
	u := res.Usage
	if u.Input != 27517-24064 || u.Cached != 24064 || u.Output != 78 || u.USD != nil || u.CostSource != "none" {
		t.Errorf("usage = %+v", u)
	}
}

func TestAttemptClarification(t *testing.T) {
	arm, _, in, _ := setup(t, "", `{"status":"needs_clarification","summary":"contradictory","question":"product or sum?"}`)
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Claimed != eval.ClaimClarification || res.Question != "product or sum?" {
		t.Errorf("result = %+v", res)
	}
}

func TestAttemptNoFinalAnswer(t *testing.T) {
	arm, _, in, _ := setup(t, "", "")
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusSucceeded || res.Claimed != eval.ClaimNone {
		t.Errorf("result = %+v", res)
	}
}

func TestWallCapKillsProcessGroup(t *testing.T) {
	arm, bin, in, _ := setup(t, "sleep", "")
	in.WallCap = time.Second
	start := time.Now()
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusTimedOut {
		t.Errorf("status = %s, want timed_out", res.Status)
	}
	if time.Since(start) > 15*time.Second {
		t.Error("wall cap did not stop the run promptly")
	}
	b, err := os.ReadFile(filepath.Join(bin, "child"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	time.Sleep(200 * time.Millisecond)
	if exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil {
		t.Errorf("grandchild %d survived the wall cap", pid)
		exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
}

func TestTokenCap(t *testing.T) {
	arm, _, in, _ := setup(t, "", `{"status":"done","summary":"x"}`)
	in.TokenCap = 1000 // the recorded run used 3453 uncached + 78 output
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusBudgetExceeded {
		t.Errorf("status = %s, want budget_exceeded", res.Status)
	}
}

func TestProviderErrorIsInfra(t *testing.T) {
	arm, _, in, _ := setup(t, "fail", "")
	res, err := arm.Attempt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != eval.StatusInfraError || !strings.Contains(res.Error, "401") {
		t.Errorf("result = %+v", res)
	}
}
