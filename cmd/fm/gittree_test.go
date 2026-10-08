package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitTreeFileExists must check the target repo's tree at a commit, not the CLI's cwd
// (spec FR-004: cited files must exist in the base commit's git tree).
func TestGitTreeFileExists(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte("package addrepo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := string(out[:len(out)-1])

	// Check from a working directory that does NOT contain add.go.
	t.Chdir(t.TempDir())

	exists := gitTreeFileExists(context.Background(), dir, commit)
	if !exists("add.go") {
		t.Error("add.go should exist in the base commit's tree")
	}
	if exists("missing.go") {
		t.Error("missing.go should not exist in the base commit's tree")
	}
	if exists("../add.go") {
		t.Error("path escaping the tree root should not resolve")
	}
}
