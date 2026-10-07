package artifacts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func redactSecret(s string) string { return strings.ReplaceAll(s, "s3cr3t-value", "[REDACTED]") }

func newTest(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	state, work := filepath.Join(root, "state"), filepath.Join(root, "work")
	return New(state, work, redactSecret), state, work
}

func TestIODirIsOutsideState(t *testing.T) {
	a, state, work := newTest(t)
	dir, err := a.IODir(context.Background(), "run_1")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(work, "runs", "run_1", "io") {
		t.Errorf("IODir = %s", dir)
	}
	if strings.HasPrefix(dir, state) {
		t.Error("io dir is under state/")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("io dir not created: %v", err)
	}
}

func TestTranscriptRedacts(t *testing.T) {
	a, state, _ := newTest(t)
	w, err := a.Transcript(context.Background(), "run_1")
	if err != nil {
		t.Fatal(err)
	}
	// Writes split mid-line and mid-secret must still be redacted.
	io.WriteString(w, `{"text":"token s3cr3t-`)
	io.WriteString(w, "value\"}\n{\"text\":\"ok\"}\n{\"tail\":\"s3cr3t-value")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(state, "runs", "run_1", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cr3t-value") || strings.Count(string(b), "[REDACTED]") != 2 {
		t.Errorf("transcript = %q", b)
	}
}

func TestCollect(t *testing.T) {
	ctx := context.Background()
	a, state, work := newTest(t)
	dir, _ := a.IODir(ctx, "run_1")
	os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("do it"), 0o644)
	os.WriteFile(filepath.Join(dir, "last.json"), []byte(`{"status":"done","summary":"used s3cr3t-value"}`), 0o644)

	for i := 0; i < 2; i++ { // idempotent
		if err := a.Collect(ctx, "run_1"); err != nil {
			t.Fatalf("Collect #%d: %v", i+1, err)
		}
	}
	runDir := filepath.Join(state, "runs", "run_1")
	b, err := os.ReadFile(filepath.Join(runDir, "last.json"))
	if err != nil || strings.Contains(string(b), "s3cr3t-value") {
		t.Errorf("last.json = %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(runDir, "prompt.md")); string(b) != "do it" {
		t.Errorf("prompt.md = %q", b)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("io dir not removed")
	}
	// Nothing outside state/runs/run_1 and work/runs/run_1.
	filepath.Walk(filepath.Dir(state), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if !strings.HasPrefix(p, runDir) && !strings.HasPrefix(p, filepath.Join(work, "runs", "run_1")) {
			t.Errorf("unexpected file %s", p)
		}
		return nil
	})
}

func TestSavePatchIsVerbatim(t *testing.T) {
	a, state, _ := newTest(t)
	patch := []byte("diff --git a/x b/x\n+s3cr3t-value\n")
	if err := a.SavePatch(context.Background(), "run_1", patch); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(state, "runs", "run_1", "patch.diff"))
	if string(b) != string(patch) {
		t.Errorf("patch changed: %q (a redacted patch would no longer apply)", b)
	}
}

func TestRejectsBadRunID(t *testing.T) {
	a, _, _ := newTest(t)
	for _, id := range []string{"", "..", "a/b"} {
		if _, err := a.IODir(context.Background(), id); err == nil {
			t.Errorf("IODir accepted %q", id)
		}
	}
}
