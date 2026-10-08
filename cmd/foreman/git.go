package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitResolveHEAD resolves repoURL's current HEAD commit: a local bare/non-bare path is
// read directly; anything else is treated as a remote URL via `git ls-remote`.
func gitResolveHEAD(ctx context.Context, repoURL string) (string, error) {
	if looksLocal(repoURL) {
		var cmd *exec.Cmd
		if fileExists(filepath.Join(repoURL, "HEAD")) {
			cmd = exec.CommandContext(ctx, "git", "--git-dir="+repoURL, "rev-parse", "HEAD")
		} else {
			cmd = exec.CommandContext(ctx, "git", "-C", repoURL, "rev-parse", "HEAD")
		}
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	out, err := exec.CommandContext(ctx, "git", "ls-remote", repoURL, "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s HEAD: %w", repoURL, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("git ls-remote %s HEAD: empty result", repoURL)
	}
	return fields[0], nil
}

func looksLocal(repoURL string) bool {
	if strings.Contains(repoURL, "://") || strings.HasPrefix(repoURL, "git@") {
		return false
	}
	return filepath.IsAbs(repoURL) || strings.HasPrefix(repoURL, "./") || strings.HasPrefix(repoURL, "../") || strings.HasPrefix(repoURL, "~/")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
