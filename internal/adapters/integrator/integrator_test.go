package integrator_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/integrator"
)

// sourceRepo creates a one-commit repo (with a git identity baked in, since GitEnv() strips
// all global/system config) and returns its path and HEAD.
func sourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/s\n")
	write(t, filepath.Join(dir, "main.go"), "package s\n")
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir, strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

func runGit(t *testing.T, dir string, args ...string) string {
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

const testPatch = `diff --git a/x.go b/x.go
new file mode 100644
--- /dev/null
+++ b/x.go
@@ -0,0 +1 @@
+package s
`

// T038: applying a patch to agent/<task> in a test bare mirror uses the hardened git
// invocation (hooks disabled, no system/global config — same env as sandbox.GitEnv()), so a
// hook planted in the mirror must never fire.
func TestApplyPatchHardenedGitDoesNotRunMirrorHooks(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	work := t.TempDir()
	ig := integrator.New(work, "owner", "repo", "token")

	// First apply creates/refreshes the bare mirror (same layout sandbox.Manager uses:
	// <work>/mirrors/<sha256(repoURL)[:16]>.git).
	if err := ig.ApplyAndPush(ctx, "tsk_1", []byte(testPatch), src, base, src); err != nil {
		t.Fatalf("ApplyAndPush: %v", err)
	}

	// Plant a post-checkout hook in the *actual* mirror this integrator uses — the same
	// trick sandbox's own TestPrepareDoesNotRunMirrorHooks does. If the integrator's git
	// invocation is genuinely hardened (hooks disabled via core.hooksPath=/dev/null), this
	// sentinel never appears even though the mirror is reused for a second apply.
	sum := sha256.Sum256([]byte(src))
	mirror := filepath.Join(work, "mirrors", hex.EncodeToString(sum[:])[:16]+".git")
	sentinel := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(mirror, "hooks", "post-checkout")
	write(t, hook, "#!/bin/sh\ntouch "+sentinel+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ig.ApplyAndPush(ctx, "tsk_2", []byte(testPatch), src, base, src); err != nil {
		t.Fatalf("second ApplyAndPush: %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("hook in the reused mirror ran despite hardened git invocation (stat err: %v)", err)
	}
}

// T038: opening a draft PR posts the expected JSON to a fake GitHub HTTP server.
func TestOpenDraftPRPostsExpectedJSON(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)

	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			// defaultBranch() lookup
			_ = json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode PR body: %v", err)
		}
		w.WriteHeader(http.StatusCreated) // GitHub's real POST /pulls returns 201 Created
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/owner/repo/pull/1"})
	}))
	defer srv.Close()

	ig := integrator.New(t.TempDir(), "owner", "repo", "token")
	ig.BaseURL = srv.URL
	if err := ig.ApplyAndPush(ctx, "tsk_1", []byte(testPatch), src, base, src); err != nil {
		t.Fatalf("ApplyAndPush: %v", err)
	}
	prURL, err := ig.OpenDraftPR(ctx, "tsk_1", []byte(testPatch))
	if err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	if prURL != "https://github.com/owner/repo/pull/1" {
		t.Errorf("PR URL = %q, want https://github.com/owner/repo/pull/1", prURL)
	}
	if gotAuth != "Bearer token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer token")
	}
	if !strings.HasSuffix(gotPath, "/repos/owner/repo/pulls") {
		t.Errorf("request path %q, want it to end with /repos/owner/repo/pulls", gotPath)
	}
	if gotBody["draft"] != true {
		t.Errorf("draft = %v, want true", gotBody["draft"])
	}
	if gotBody["head"] != "agent/tsk_1" {
		t.Errorf("head = %v, want agent/tsk_1", gotBody["head"])
	}
	if gotBody["base"] != "main" {
		t.Errorf("base = %v, want main", gotBody["base"])
	}
}

// T038: a push failure is classified as an infra/provider failure, retried with backoff, and
// never discards or regenerates the verified patch.
func TestPushFailureClassifiedAndPatchPreserved(t *testing.T) {
	ctx := context.Background()
	src, base := sourceRepo(t)
	ig := integrator.New(t.TempDir(), "owner", "repo", "token")

	// A push URL that will always fail: a bare repo path that doesn't exist.
	badPush := filepath.Join(t.TempDir(), "nope.git")

	patchCopy := []byte(testPatch)
	err := ig.ApplyAndPush(ctx, "tsk_1", patchCopy, src, base, badPush)
	if err == nil {
		t.Fatal("expected a push failure against a nonexistent remote, got nil")
	}

	// The verified patch must be untouched — never discarded or regenerated (T038).
	if !bytes.Equal(patchCopy, []byte(testPatch)) {
		t.Errorf("patch was mutated by the failed push: got %q", patchCopy)
	}

	// A git push failure against a missing remote is an infrastructure problem, not an
	// attempt/policy one — FR-008/FR-011's ladder treats it as retriable-with-backoff.
	// Use the integrator's own classifier (engine.ClassifyFailure classifies *run* failures,
	// not delivery failures — different domain).
	cls, sig := integrator.ClassifyDeliveryFailure(err.Error())
	if cls != "infra" && cls != "provider" {
		t.Errorf("ClassifyDeliveryFailure = %q, want infra or provider", cls)
	}
	if sig == "" {
		t.Error("expected a non-empty failure signature from ClassifyDeliveryFailure")
	}
}
