// Package sandbox implements eval.Workspaces: fresh, disposable clones pinned to a base
// commit, and a trusted export path (specs/001-evaluation-harness/research.md R6).
//
// Every git call disables hooks and ignores the user's and the system's git config.
// Export never uses the workspace's own .git, because the agent can write to it: a
// planted core.fsmonitor, diff.external or filter driver would run on the host.
package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// Manager makes and removes workspaces under a work root.
type Manager struct {
	work string // $FOREMAN_HOME/work
}

// New returns a Manager rooted at work.
func New(work string) *Manager { return &Manager{work: work} }

func (m *Manager) mirrorPath(repoURL string) string {
	sum := sha256.Sum256([]byte(repoURL))
	return filepath.Join(m.work, "mirrors", hex.EncodeToString(sum[:])[:16]+".git")
}

func (m *Manager) runDir(id string) string { return filepath.Join(m.work, "runs", id) }

// Prepare implements eval.Workspaces.
func (m *Manager) Prepare(ctx context.Context, id, repoURL, base string, overlay fs.FS) (eval.Workspace, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return eval.Workspace{}, fmt.Errorf("invalid workspace id %q", id)
	}
	overlayFiles, err := eval.ListFiles(overlay)
	if err != nil {
		return eval.Workspace{}, fmt.Errorf("overlay: %w", err)
	}
	mirror, err := m.ensureMirror(ctx, repoURL, base)
	if err != nil {
		return eval.Workspace{}, err
	}

	dir := m.runDir(id)
	if err := os.RemoveAll(dir); err != nil { // a retried id starts from scratch
		return eval.Workspace{}, fmt.Errorf("clear run dir: %w", err)
	}
	ws := eval.Workspace{
		ID: id, Dir: filepath.Join(dir, "ws"), TrustedGitDir: filepath.Join(dir, "trusted.git"),
		RepoURL: repoURL, Base: base, Overlay: overlayFiles,
	}
	if err := m.populate(ctx, mirror, ws, overlay); err != nil {
		os.RemoveAll(dir)
		return eval.Workspace{}, err
	}
	return ws, nil
}

func (m *Manager) populate(ctx context.Context, mirror string, ws eval.Workspace, overlay fs.FS) error {
	if err := os.MkdirAll(filepath.Dir(ws.Dir), 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	if _, err := Git(ctx, "", "clone", "-q", "--no-hardlinks", "--no-checkout", mirror, ws.Dir); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	if _, err := Git(ctx, ws.Dir, "checkout", "-q", "--detach", ws.Base); err != nil {
		return fmt.Errorf("checkout %s: %w", ws.Base, err)
	}
	// Capture the trusted git dir before anything else can touch the workspace.
	if err := os.CopyFS(ws.TrustedGitDir, os.DirFS(filepath.Join(ws.Dir, ".git"))); err != nil {
		return fmt.Errorf("capture trusted git dir: %w", err)
	}
	for _, name := range ws.Overlay {
		b, err := fs.ReadFile(overlay, name)
		if err != nil {
			return fmt.Errorf("read overlay %s: %w", name, err)
		}
		dst := filepath.Join(ws.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("overlay %s: %w", name, err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return fmt.Errorf("overlay %s: %w", name, err)
		}
	}
	return nil
}

// ensureMirror creates or refreshes the bare mirror of repoURL and checks that base is
// in it.
func (m *Manager) ensureMirror(ctx context.Context, repoURL, base string) (string, error) {
	mirror := m.mirrorPath(repoURL)
	if _, err := os.Stat(mirror); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
			return "", fmt.Errorf("create mirrors dir: %w", err)
		}
		if _, err := Git(ctx, "", "clone", "-q", "--mirror", repoURL, mirror); err != nil {
			os.RemoveAll(mirror)
			return "", fmt.Errorf("mirror %s: %w", repoURL, err)
		}
	} else if _, err := Git(ctx, "", "--git-dir="+mirror, "fetch", "-q", "--prune"); err != nil {
		return "", fmt.Errorf("fetch %s: %w", repoURL, err)
	}
	if _, err := Git(ctx, "", "--git-dir="+mirror, "cat-file", "-e", base+"^{commit}"); err != nil {
		return "", fmt.Errorf("%s in %s: %w", base, repoURL, eval.ErrBaseNotFound)
	}
	return mirror, nil
}

// Export implements eval.Workspaces. Only the trusted git dir is used.
func (m *Manager) Export(ctx context.Context, ws eval.Workspace) ([]byte, error) {
	pathspec := []string{"--", ".", ":(exclude).fm", ":(exclude).git"}
	for _, f := range ws.Overlay {
		pathspec = append(pathspec, ":(exclude,literal)"+f)
	}
	trusted := []string{
		"--git-dir=" + ws.TrustedGitDir, "--work-tree=" + ws.Dir,
		"-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "core.attributesFile=/dev/null",
	}
	if _, err := Git(ctx, ws.Dir, append(append(trusted, "add", "-A"), pathspec...)...); err != nil {
		return nil, fmt.Errorf("stage workspace changes: %w", err)
	}
	diff, err := Git(ctx, ws.Dir, append(trusted, "diff", "--cached", "--binary", "--no-color",
		"--no-ext-diff", "--no-textconv", ws.Base)...)
	if err != nil {
		return nil, fmt.Errorf("diff workspace: %w", err)
	}
	return diff, nil
}

// Destroy implements eval.Workspaces.
func (m *Manager) Destroy(_ context.Context, ws eval.Workspace) error {
	if ws.ID == "" {
		return nil
	}
	if err := os.RemoveAll(m.runDir(ws.ID)); err != nil {
		return fmt.Errorf("remove workspace %s: %w", ws.ID, err)
	}
	return nil
}

// PrepareWorkspace implements engine.WorkspaceCloner.
func (m *Manager) PrepareWorkspace(ctx context.Context, taskID, runID, repoURL, baseCommit string) (string, error) {
	id := runID
	if id == "" {
		id = taskID
	}
	ws, err := m.Prepare(ctx, id, repoURL, baseCommit, nil)
	if err != nil {
		return "", err
	}
	return ws.Dir, nil
}

// DestroyWorkspace implements engine.WorkspaceCloner.
func (m *Manager) DestroyWorkspace(_ context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(dir))
}

var _ engine.WorkspaceCloner = (*Manager)(nil)

// Git runs git with hooks disabled, no system or global config, no prompts, and no
// inherited GIT_* variables. Exported so other adapters (e.g. internal/adapters/integrator)
// reuse this exact hardened invocation rather than reimplementing a look-alike.
func Git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	full := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = GitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// GitEnv returns the hardened environment every git call here (and in any adapter reusing
// Git) runs under: no system config, no global config, no credential prompts, no inherited
// GIT_* or LC_ALL overrides.
func GitEnv() []string {
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}
