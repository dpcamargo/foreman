// Package integrator delivers a verified patch to GitHub: applies it to an `agent/<task>`
// branch in the per-repo bare mirror (sandbox's hardened git invocation, FR-014), pushes
// that branch, and opens a draft PR via the GitHub REST API. The GitHub token is read from
// this (the daemon/`foreman` OS user's) environment only — never from the workspace or the
// verifier (FR-014) — and is never logged.
package integrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/core/task"
)

// Integrator pushes branches and opens PRs against one GitHub repo.
type Integrator struct {
	Work      string // $FOREMAN_HOME/work — bare mirrors live here (sandbox.Manager's layout)
	Owner     string
	Repo      string
	Token     string // GitHub PAT, from the daemon's own environment (FR-014)
	BaseURL   string // overridable for tests; defaults to api.github.com
	HTTP      *http.Client
	sandbox   *sandbox.Manager
	retryBase time.Duration
}

// New returns an Integrator for owner/repo, using work for bare mirrors.
func New(work, owner, repo, token string) *Integrator {
	return &Integrator{
		Work:      work,
		Owner:     owner,
		Repo:      repo,
		Token:     token,
		BaseURL:   "https://api.github.com",
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		sandbox:   sandbox.New(work),
		retryBase: time.Second,
	}
}

// DiffSHA256 is the canonical hash of a verified patch — what a merge Approval's
// SubjectSHA256 must match (T039/SC-002), never the task id.
func DiffSHA256(patch []byte) string {
	sum := sha256.Sum256(patch)
	return hex.EncodeToString(sum[:])
}

// PushBranch pushes the verified patch as agent/<taskID> to the GitHub repo this Integrator
// is configured for. It constructs the authenticated push URL from the configured token and
// delegates to PushWithBackoff. This is the missing step before OpenDraftPR — without it
// GitHub's POST /pulls fails because the head branch was never pushed.
func (ig *Integrator) PushBranch(ctx context.Context, taskID string, patch []byte, repoURL, baseCommit string) error {
	pushURL := "https://x-access-token:" + ig.Token + "@github.com/" + ig.Owner + "/" + ig.Repo + ".git"
	return ig.PushWithBackoff(ctx, taskID, patch, repoURL, baseCommit, pushURL)
}

// ApplyAndPush applies patch (a verified export diff) to a fresh agent/<taskID> branch off
// baseCommit in the bare mirror of repoURL, and pushes it to pushURL. Any failure — push
// included — leaves patch exactly as passed in (T038: never discard or regenerate the
// verified patch).
func (ig *Integrator) ApplyAndPush(ctx context.Context, taskID string, patch []byte, repoURL, baseCommit, pushURL string) error {
	if taskID == "" || len(patch) == 0 {
		return errors.New("task id and non-empty patch are required")
	}
	if _, err := ig.sandbox.PrepareWorkspace(ctx, taskID, "", repoURL, baseCommit); err != nil {
		return fmt.Errorf("prepare mirror workspace for %s: %w", taskID, err)
	}
	mirror, err := ig.ensureMirror(ctx, repoURL, baseCommit)
	if err != nil {
		return err
	}
	clone := filepath.Join(ig.Work, "deliver", taskID)
	if err := os.RemoveAll(clone); err != nil {
		return fmt.Errorf("clear delivery clone: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		return fmt.Errorf("create delivery dir: %w", err)
	}
	defer os.RemoveAll(clone)
	if _, err := sandbox.Git(ctx, "", "clone", "-q", "--no-hardlinks", "--no-checkout", mirror, clone); err != nil {
		return fmt.Errorf("clone mirror: %w", err)
	}
	branch := "agent/" + taskID
	if _, err := sandbox.Git(ctx, clone, "checkout", "-q", "-b", branch, baseCommit); err != nil {
		return fmt.Errorf("checkout %s: %w", branch, err)
	}
	// sandbox.Git doesn't pipe stdin (it's built for argument-only git calls), so write the
	// patch to a temp file and apply from there — same hardened invocation either way.
	patchFile := filepath.Join(clone, ".git", "foreman.patch")
	if err := os.WriteFile(patchFile, patch, 0o600); err != nil {
		return fmt.Errorf("stage patch for apply: %w", err)
	}
	defer os.Remove(patchFile)
	if _, err := sandbox.Git(ctx, clone, "apply", "--whitespace=nowarn", patchFile); err != nil {
		return fmt.Errorf("apply patch to %s: %w", branch, err)
	}
	// A commit identity is required since GitEnv() strips all global/system config
	// (a hardened invocation can't inherit the operator's name/email).
	if _, err := sandbox.Git(ctx, clone, "config", "user.email", "foreman@localhost"); err != nil {
		return fmt.Errorf("set commit identity: %w", err)
	}
	if _, err := sandbox.Git(ctx, clone, "config", "user.name", "foreman"); err != nil {
		return fmt.Errorf("set commit identity: %w", err)
	}
	if _, err := sandbox.Git(ctx, clone, "add", "-A"); err != nil {
		return fmt.Errorf("stage patch: %w", err)
	}
	if _, err := sandbox.Git(ctx, clone, "commit", "-q", "-m", "foreman: "+taskID); err != nil {
		return fmt.Errorf("commit patch: %w", err)
	}
	if _, err := sandbox.Git(ctx, clone, "push", "-q", pushURL, branch+":"+branch); err != nil {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	return nil
}

// PushWithBackoff retries ApplyAndPush up to 3 attempts with exponential backoff
// (retryBase, doubling, capped at 30s — same shape as telegram.Client.Poll's backoff),
// matching T038's "a push failure is classified as an infra/provider failure, retried with
// backoff, and never discards or regenerates the verified patch." patch is passed through
// to each attempt untouched — the verified patch is never regenerated on retry.
func (ig *Integrator) PushWithBackoff(ctx context.Context, taskID string, patch []byte, repoURL, baseCommit, pushURL string) error {
	backoff := ig.retryBase
	if backoff <= 0 {
		backoff = time.Second
	}
	const maxBackoff = 30 * time.Second
	const maxAttempts = 3

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = ig.ApplyAndPush(ctx, taskID, patch, repoURL, baseCommit, pushURL)
		if err == nil {
			return nil
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return err
}

// ensureMirror reuses sandbox.Manager's mirror layout (fresh fetch/refresh) so the
// integrator and the run workspace share one mirror per repoURL (ARCHITECTURE.md §5).
func (ig *Integrator) ensureMirror(ctx context.Context, repoURL, baseCommit string) (string, error) {
	// PrepareWorkspace above already created/refreshed the mirror; this just resolves its
	// path (same sha256-keyed scheme sandbox.Manager uses internally).
	sum := sha256.Sum256([]byte(repoURL))
	return filepath.Join(ig.Work, "mirrors", hex.EncodeToString(sum[:])[:16]+".git"), nil
}

// OpenDraftPR opens a draft PR from agent/<taskID> into the repo's default branch, with the
// verified patch inlined in the body (human review of the exact same diff T039's hash binds
// to). Returns the PR's html_url.
func (ig *Integrator) OpenDraftPR(ctx context.Context, taskID string, patch []byte) (string, error) {
	baseBranch, err := ig.defaultBranch(ctx)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"title": "foreman: " + taskID,
		"head":  "agent/" + taskID,
		"base":  baseBranch,
		"body":  "Verified patch (sha256 " + DiffSHA256(patch) + "):\n\n```diff\n" + string(patch) + "\n```",
		"draft": true,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal PR request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ig.BaseURL+"/repos/"+ig.Owner+"/"+ig.Repo+"/pulls", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build PR request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ig.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ig.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("open draft PR: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("open draft PR: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode PR response: %w", err)
	}
	return out.HTMLURL, nil
}

// MergePullRequest merges an already-approved (non-draft) PR via GitHub's REST merge
// endpoint — only ever called after T042's two-round-trip merge confirmation (FR-016:
// never a single tap).
func (ig *Integrator) MergePullRequest(ctx context.Context, taskID string, pullNumber int) error {
	body := map[string]any{"merge_method": "squash"}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal merge request: %w", err)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/merge", ig.BaseURL, ig.Owner, ig.Repo, pullNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build merge request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ig.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ig.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("merge PR: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("merge PR: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (ig *Integrator) defaultBranch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ig.BaseURL+"/repos/"+ig.Owner+"/"+ig.Repo, nil)
	if err != nil {
		return "", fmt.Errorf("build repo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ig.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ig.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("query repo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("query repo: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode repo response: %w", err)
	}
	if out.DefaultBranch == "" {
		return "", errors.New("repo has no default branch")
	}
	return out.DefaultBranch, nil
}

// ClassifyDeliveryFailure categorizes a delivery (push/PR) error the same way the engine's
// ladder categorizes a run failure — a delivery failure is never an "attempt" or "policy"
// failure (FR-008/FR-011: retried with backoff, never burns a ladder rung).
func ClassifyDeliveryFailure(errStr string) (task.FailureClass, string) {
	lower := strings.ToLower(errStr)
	switch {
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "404"):
		return task.FailureProvider, "github-auth"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "timed out"):
		return task.FailureInfra, "github-unreachable"
	default:
		return task.FailureInfra, "delivery-infra"
	}
}
