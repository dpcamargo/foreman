package gomod

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWarmSkipsNonModules(t *testing.T) {
	if err := Warm(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "cache")); err != nil {
		t.Errorf("Warm on a non-module dir: %v", err)
	}
}

func TestWarmModuleWithoutDeps(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o644)
	cache := filepath.Join(t.TempDir(), "cache")
	if err := Warm(context.Background(), ws, cache); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cache dir not created: %v", err)
	}
}

func TestWarmBrokenModuleFails(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("this is not a go.mod\n"), 0o644)
	if err := Warm(context.Background(), ws, filepath.Join(t.TempDir(), "cache")); err == nil {
		t.Error("Warm accepted a broken go.mod")
	}
}
