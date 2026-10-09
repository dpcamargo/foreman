package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// gitCatFile returns the contents of path at commit in repoURL (a local bare/non-bare path
// or a remote URL), mirroring gitResolveHEAD's branching. Used to fetch
// .foreman/project.yaml at the base commit (FR-016).
func gitCatFile(ctx context.Context, repoURL, commit, path string) ([]byte, error) {
	if looksLocal(repoURL) {
		var cmd *exec.Cmd
		if fileExists(filepath.Join(repoURL, "HEAD")) {
			cmd = exec.CommandContext(ctx, "git", "--git-dir="+repoURL, "cat-file", "-p", commit+":"+path)
		} else {
			cmd = exec.CommandContext(ctx, "git", "-C", repoURL, "cat-file", "-p", commit+":"+path)
		}
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", commit, path, err)
		}
		return out, nil
	}

	// Remote URL: try `gh api` for GitHub URLs (git archive --remote is disabled by
	// GitHub server-side). Falls back to git archive --remote for other git servers.
	if strings.Contains(repoURL, "github.com") {
		// Extract owner/repo from the URL.
		trimmed := strings.TrimSuffix(strings.TrimPrefix(repoURL, "https://"), ".git")
		trimmed = strings.TrimPrefix(trimmed, "git@github.com:")
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 2 {
			owner, repo := parts[len(parts)-2], parts[len(parts)-1]
			apiURL := "https://api.github.com/repos/" + owner + "/" + repo + "/contents/" + path + "?ref=" + commit
			req, reqErr := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
			if reqErr == nil {
				req.Header.Set("Accept", "application/vnd.github+json")
				if tok := os.Getenv("FOREMAN_GITHUB_TOKEN"); tok != "" {
					req.Header.Set("Authorization", "Bearer "+tok)
				}
				resp, doErr := http.DefaultClient.Do(req)
				if doErr == nil {
					defer resp.Body.Close()
					if resp.StatusCode == 200 {
						var out struct {
							Content string `json:"content"`
						}
						if jsonErr := json.NewDecoder(resp.Body).Decode(&out); jsonErr == nil && out.Content != "" {
							decoded, decErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
							if decErr == nil {
								return decoded, nil
							}
						}
					}
				}
			}
		}
	}

	out, err := exec.CommandContext(ctx, "git", "archive", "--remote="+repoURL, commit, path).Output()
	if err != nil {
		return nil, fmt.Errorf("git archive --remote %s %s: %w", repoURL, path, err)
	}
	// git archive emits a tar stream even for a single file — extract just the file body.
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("extract %s from git archive: %w", path, err)
		}
		if hdr.Name == path || strings.TrimPrefix(hdr.Name, "./") == path {
			body, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read %s from git archive: %w", path, err)
			}
			return body, nil
		}
	}
}
