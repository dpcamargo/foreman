package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// sourceRepo creates a one-commit repo and returns its path and HEAD.
func sourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/s\n")
	write(t, filepath.Join(dir, "main.go"), "package s\n")
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	return dir, strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

var overlay = fstest.MapFS{"visible_test.go": {Data: []byte("package s\n")}}

func TestPrepare(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	m := New(t.TempDir())

	ws, err := m.Prepare(ctx, "run_1", src, base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(ws.Dir, ".git"))
	if err != nil || !fi.IsDir() {
		t.Fatalf(".git must be a real directory (no worktree gitfile): %v", err)
	}
	if head := strings.TrimSpace(gitT(t, ws.Dir, "rev-parse", "HEAD")); head != base {
		t.Errorf("HEAD = %s, want %s", head, base)
	}
	if !exists(filepath.Join(ws.Dir, "main.go")) {
		t.Error("tracked file missing from workspace")
	}
	if !exists(filepath.Join(ws.Dir, "visible_test.go")) {
		t.Error("overlay file not applied")
	}
	if ws.TrustedGitDir != filepath.Join(m.work, "runs", "run_1", "trusted.git") {
		t.Errorf("TrustedGitDir = %s", ws.TrustedGitDir)
	}
	if strings.HasPrefix(ws.TrustedGitDir, ws.Dir+string(filepath.Separator)) || !exists(filepath.Join(ws.TrustedGitDir, "HEAD")) {
		t.Errorf("trusted git dir must exist outside the workspace: %s", ws.TrustedGitDir)
	}
	if strings.Join(ws.Overlay, ",") != "visible_test.go" {
		t.Errorf("Overlay = %v", ws.Overlay)
	}
	// No hardlinks into the mirror: object files have link count 1.
	objs, _ := filepath.Glob(filepath.Join(ws.Dir, ".git", "objects", "pack", "*"))
	loose, _ := filepath.Glob(filepath.Join(ws.Dir, ".git", "objects", "??", "*"))
	for _, p := range append(objs, loose...) {
		if n := linkCount(t, p); n > 1 {
			t.Errorf("%s has %d hard links; clone must use --no-hardlinks", p, n)
		}
	}
}

func TestPrepareDoesNotRunMirrorHooks(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	m := New(t.TempDir())
	ws, err := m.Prepare(ctx, "run_a", src, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(m.mirrorPath(src), "hooks", "post-checkout")
	write(t, hook, "#!/bin/sh\ntouch "+sentinel+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(ctx, "run_b", src, base, nil); err != nil {
		t.Fatal(err)
	}
	if exists(sentinel) {
		t.Error("a hook planted in the mirror ran during Prepare")
	}
}

func TestExport(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	m := New(t.TempDir())
	ws, err := m.Prepare(ctx, "run_1", src, base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws.Dir, "main.go"), "package s\n\nfunc New() {}\n")  // modified
	write(t, filepath.Join(ws.Dir, "pkg", "added.go"), "package pkg\n")         // untracked
	write(t, filepath.Join(ws.Dir, ".fm", "tmp", "scratch"), "x")               // fm scratch
	write(t, filepath.Join(ws.Dir, "visible_test.go"), "package s // edited\n") // overlay, even if edited

	dst := filepath.Join(t.TempDir(), "patch.diff")
	if err := m.Export(ctx, ws, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	diff := string(b)
	for _, want := range []string{"a/main.go", "b/pkg/added.go", "func New()"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	for _, bad := range []string{".fm/", "visible_test.go", ".git/"} {
		if strings.Contains(diff, bad) {
			t.Errorf("diff must not include %q:\n%s", bad, diff)
		}
	}

	// The patch applies cleanly to a fresh checkout of base.
	check := t.TempDir()
	gitT(t, check, "clone", "-q", src, ".")
	gitT(t, check, "checkout", "-q", base)
	gitT(t, check, "apply", "--check", dst)
}

// Constitution VI / research R6: config the agent writes into its workspace .git must
// never execute on the host during Export.
func TestExportIgnoresPlantedGitConfig(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	m := New(t.TempDir())
	ws, err := m.Prepare(ctx, "run_1", src, base, nil)
	if err != nil {
		t.Fatal(err)
	}

	scripts := t.TempDir()
	sentinels := map[string]string{}
	for _, name := range []string{"fsmonitor", "filter", "diffext"} {
		s := filepath.Join(scripts, name+"-ran")
		sentinels[name] = s
		script := filepath.Join(scripts, name+".sh")
		body := "#!/bin/sh\ntouch " + s + "\n"
		if name == "filter" {
			body += "cat\n" // a clean filter must pass content through
		}
		write(t, script, body)
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(ws.Dir, ".git", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[core]\n\tfsmonitor = " + filepath.Join(scripts, "fsmonitor.sh") + "\n" +
		"[filter \"x\"]\n\tclean = " + filepath.Join(scripts, "filter.sh") + "\n" +
		"[diff]\n\texternal = " + filepath.Join(scripts, "diffext.sh") + "\n")
	f.Close()
	write(t, filepath.Join(ws.Dir, ".gitattributes"), "* filter=x\n")
	write(t, filepath.Join(ws.Dir, "main.go"), "package s\n\nvar X = 1\n")

	// Control: the planted config is live for ordinary git commands on the workspace.
	control := t.TempDir()
	gitT(t, ws.Dir, "status", "--porcelain")
	gitT(t, ws.Dir, "add", "-A")
	gitT(t, ws.Dir, "diff", "--cached", "HEAD")
	for name, s := range sentinels {
		if !exists(s) {
			t.Fatalf("control failed: planted %s config did not run; the test proves nothing", name)
		}
		os.Rename(s, filepath.Join(control, name))
	}

	dst := filepath.Join(t.TempDir(), "patch.diff")
	if err := m.Export(ctx, ws, dst); err != nil {
		t.Fatal(err)
	}
	for name, s := range sentinels {
		if exists(s) {
			t.Errorf("planted %s config executed during Export", name)
		}
	}
	b, _ := os.ReadFile(dst)
	if !strings.Contains(string(b), "var X = 1") {
		t.Errorf("export lost the change:\n%s", b)
	}
}

func TestDestroyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	m := New(t.TempDir())
	ws, err := m.Prepare(ctx, "run_1", src, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := m.Destroy(ctx, ws); err != nil {
			t.Fatalf("Destroy #%d: %v", i+1, err)
		}
	}
	if exists(filepath.Join(m.work, "runs", "run_1")) {
		t.Error("run dir (workspace and trusted.git) not removed")
	}
}

func TestPrepareUnknownBase(t *testing.T) {
	src, _ := sourceRepo(t)
	m := New(t.TempDir())
	_, err := m.Prepare(context.Background(), "run_1", src, strings.Repeat("ab", 20), nil)
	if !errors.Is(err, eval.ErrBaseNotFound) {
		t.Errorf("err = %v, want ErrBaseNotFound", err)
	}
	if exists(filepath.Join(m.work, "runs", "run_1")) {
		t.Error("failed Prepare left a run dir behind")
	}
}

func TestPrepareRejectsBadRunID(t *testing.T) {
	src, base := sourceRepo(t)
	m := New(t.TempDir())
	for _, id := range []string{"", "../x", "a/b", "."} {
		if _, err := m.Prepare(context.Background(), id, src, base, nil); err == nil {
			t.Errorf("Prepare accepted run id %q", id)
		}
	}
}
