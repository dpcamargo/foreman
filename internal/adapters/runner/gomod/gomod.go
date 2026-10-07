// Package gomod fills the read-only module cache an arm builds against.
package gomod

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Warm downloads wsDir's module dependencies into cache, before the arm starts.
//
// It runs on the host, with network, on the fm-created clone (the arm hasn't touched it
// yet). `go mod download` only downloads and checksum-verifies; it runs no code from the
// repository. Arms then get the cache read-only and GOPROXY=off.
func Warm(ctx context.Context, wsDir, cache string) error {
	if _, err := os.Stat(filepath.Join(wsDir, "go.mod")); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fmt.Errorf("create module cache: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "download")
	cmd.Dir = wsDir
	cmd.Env = append(cleanEnv(),
		"GOMODCACHE="+cache, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local",
		"GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOPRIVATE=", "GONOSUMDB=",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// cleanEnv keeps the host environment minus Go settings that would redirect the download.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GO") || strings.HasPrefix(kv, "CGO_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}
