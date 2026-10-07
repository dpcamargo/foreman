package container

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// maxLog bounds the grading log kept with a result (research R5).
const maxLog = 64 * 1024

// Cloner is the part of eval.Workspaces the grader needs: a fresh clone of the base,
// with the visible tests applied.
type Cloner interface {
	Prepare(ctx context.Context, id, repoURL, base string, overlay fs.FS) (eval.Workspace, error)
	Destroy(ctx context.Context, ws eval.Workspace) error
}

// Config configures a Grader.
type Config struct {
	Image         string // pinned by digest
	DockerContext string // "" = current context
	Work          string // $FOREMAN_HOME/work; must be visible to the container VM
	Clones        Cloner
	Redact        func(string) string
}

// Grader implements eval.Grader with the docker CLI (no Docker SDK; research R5).
type Grader struct{ cfg Config }

// New returns a Grader.
func New(cfg Config) *Grader {
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	return &Grader{cfg: cfg}
}

// EnsureImage pulls the grader image if it isn't present. Grade itself never pulls.
func (g *Grader) EnsureImage(ctx context.Context) error {
	if _, _, code, err := g.docker(ctx, "image", "inspect", g.cfg.Image); err == nil && code == 0 {
		return nil
	}
	if _, stderr, code, err := g.docker(ctx, "pull", "-q", g.cfg.Image); err != nil || code != 0 {
		return fmt.Errorf("pull grader image %s: exit %d: %v %s", g.cfg.Image, code, err, stderr)
	}
	return nil
}

// Grade implements eval.Grader.
func (g *Grader) Grade(ctx context.Context, in eval.GradeInput) (eval.GradeOutput, error) {
	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
	}
	id, err := newID()
	if err != nil {
		return eval.GradeOutput{}, err
	}
	ws, err := g.cfg.Clones.Prepare(ctx, id, in.RepoURL, in.Base, in.Visible)
	if err != nil {
		return eval.GradeOutput{}, fmt.Errorf("prepare grading clone: %w", err)
	}
	defer g.cfg.Clones.Destroy(context.WithoutCancel(ctx), ws)
	runDir := filepath.Dir(ws.Dir)

	if in.Patch != nil {
		if msg, err := applyPatch(ctx, ws.Dir, filepath.Join(runDir, "grade.patch"), in.Patch); err != nil {
			return eval.GradeOutput{}, err
		} else if msg != "" {
			return eval.GradeOutput{ApplyError: msg, Log: g.log(msg)}, nil
		}
	}
	hiddenDir := filepath.Join(runDir, "hidden")
	if err := writeTree(in.Hidden, hiddenDir); err != nil {
		return eval.GradeOutput{}, fmt.Errorf("stage hidden tests: %w", err)
	}

	vol, err := g.ensureModules(ctx, ws.Dir)
	if err != nil {
		if in.Patch != nil { // the patch changed go.mod/go.sum into something unfetchable
			return eval.GradeOutput{ApplyError: err.Error(), Log: g.log(err.Error())}, nil
		}
		return eval.GradeOutput{}, err
	}

	var out eval.GradeOutput
	var logs strings.Builder
	// The full suite runs first: the hidden-test stage copies hidden tests into the tree.
	if len(in.FullTest) > 0 {
		_, stderr, code, err := g.runStage(ctx, "fm-"+id+"-full", g.testArgs("fm-"+id+"-full", ws.Dir, "", vol, false, in.FullTest))
		if err != nil {
			return eval.GradeOutput{}, err
		}
		passed := code == 0
		out.FullSuitePassed = &passed
		fmt.Fprintf(&logs, "== full suite: exit %d\n%s\n", code, stderr)
	}
	stdout, stderr, code, err := g.runStage(ctx, "fm-"+id, g.testArgs("fm-"+id, ws.Dir, hiddenDir, vol, true, in.TestCommand))
	if err != nil {
		return eval.GradeOutput{}, err
	}
	switch in.TestFormat {
	case eval.FormatExitCode:
		if code == 0 {
			out.HiddenPassed = 1
		} else {
			out.HiddenFailed = 1
		}
	default:
		out.HiddenPassed, out.HiddenFailed, out.PerTest = ParseGoTestJSON(bytes.NewReader(stdout), code)
	}
	fmt.Fprintf(&logs, "== hidden tests: exit %d\n%s\n%s", code, stderr, tail(stdout, 16*1024))
	out.Log = g.log(logs.String())
	return out, nil
}

func (g *Grader) log(s string) string { return g.cfg.Redact(eval.TruncateUTF8(s, maxLog)) }

// runStage runs one container. Exit codes 125-127 mean the container or its command
// could not run at all, which is an infrastructure failure, not a test result.
func (g *Grader) runStage(ctx context.Context, name string, args []string) (stdout, stderr []byte, code int, err error) {
	stdout, stderr, code, err = g.dockerArgs(ctx, args)
	if ctx.Err() != nil {
		g.docker(context.Background(), "rm", "-f", name)
		return nil, nil, 0, fmt.Errorf("grading %s: %w", name, ctx.Err())
	}
	if err != nil {
		return nil, nil, 0, fmt.Errorf("run %s: %w", name, err)
	}
	if code >= 125 && code <= 127 {
		return nil, nil, 0, fmt.Errorf("run %s: exit %d: %s", name, code, strings.TrimSpace(string(stderr)))
	}
	return stdout, stderr, code, nil
}

// ensureModules makes sure a module volume with the clone's dependencies exists and
// returns its name, or "" when the clone isn't a Go module. Only this step has network.
func (g *Grader) ensureModules(ctx context.Context, wsDir string) (string, error) {
	gomod, err := os.ReadFile(filepath.Join(wsDir, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	gosum, _ := os.ReadFile(filepath.Join(wsDir, "go.sum"))
	sum := sha256.Sum256(append(append(gomod, 0), gosum...))
	vol := "fm-gomod-" + hex.EncodeToString(sum[:])[:16]
	if _, _, code, err := g.docker(ctx, "volume", "inspect", vol); err == nil && code == 0 {
		return vol, nil
	}
	if _, stderr, code, err := g.docker(ctx, "volume", "create", vol); err != nil || code != 0 {
		return "", fmt.Errorf("create module volume: exit %d: %v %s", code, err, stderr)
	}
	_, stderr, code, err := g.dockerArgs(ctx, g.fetchArgs(wsDir, vol))
	if err != nil || code != 0 {
		g.docker(context.Background(), "volume", "rm", "-f", vol)
		return "", fmt.Errorf("dependency fetch failed (exit %d): %v %s", code, err, strings.TrimSpace(string(stderr)))
	}
	return vol, nil
}

func (g *Grader) base() []string {
	if g.cfg.DockerContext != "" {
		return []string{"--context", g.cfg.DockerContext}
	}
	return nil
}

// fetchArgs builds the dependency-fetch container: network on (the one exception,
// plan.md Complexity Tracking), checksum-verified proxy, read-only module mode, and
// only the clone (read-only) and the module volume mounted.
func (g *Grader) fetchArgs(wsDir, vol string) []string {
	return append(g.base(), "run", "--rm", "--pull=never",
		"--cap-drop=ALL", "--security-opt", "no-new-privileges",
		"-v", wsDir+":/src:ro", "-v", vol+":/go/pkg/mod", "-w", "/src",
		"-e", "GOPROXY=https://proxy.golang.org", "-e", "GOSUMDB=sum.golang.org",
		"-e", "GOFLAGS=-mod=readonly", "-e", "GOMODCACHE=/go/pkg/mod",
		"-e", "GOCACHE=/tmp/gocache", "-e", "GOTOOLCHAIN=local",
		g.cfg.Image, "go", "mod", "download")
}

// testArgs builds an offline test container. With withHidden, hidden tests are mounted
// read-only and copied over the tree inside the container before cmd runs.
func (g *Grader) testArgs(name, wsDir, hiddenDir, vol string, withHidden bool, cmd []string) []string {
	args := append(g.base(), "run", "--rm", "--name", name, "--pull=never",
		"--network=none", "--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,size=1g",
		"--cap-drop=ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "512", "--memory", "2g", "--cpus", "2",
		"-v", wsDir+":/src")
	if withHidden {
		args = append(args, "-v", hiddenDir+":/hidden:ro")
	}
	modcache := "/tmp/gomod"
	if vol != "" {
		args = append(args, "-v", vol+":/go/pkg/mod:ro")
		modcache = "/go/pkg/mod"
	}
	args = append(args,
		"-e", "GOPROXY=off", "-e", "GOFLAGS=-mod=readonly", "-e", "GOMODCACHE="+modcache,
		"-e", "GOCACHE=/tmp/gocache", "-e", "GOTMPDIR=/tmp", "-e", "GOTOOLCHAIN=local",
		"-e", "HOME=/tmp", "-w", "/src", g.cfg.Image)
	if withHidden {
		args = append(args, "sh", "-c", `cp -R /hidden/. /src/ && exec "$@"`, "sh")
	}
	return append(args, cmd...)
}

func (g *Grader) docker(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	return g.dockerArgs(ctx, append(g.base(), args...))
}

// dockerArgs runs docker with exactly args. err is set only when docker couldn't run.
func (g *Grader) dockerArgs(ctx context.Context, args []string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), stderr.Bytes(), exit.ExitCode(), nil
	}
	if err != nil {
		return nil, stderr.Bytes(), -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

// applyPatch applies patch in the fm-created grading clone (never an arm-written one).
// A patch that doesn't apply returns a message, not an error.
func applyPatch(ctx context.Context, wsDir, patchPath string, patch []byte) (string, error) {
	if err := os.WriteFile(patchPath, patch, 0o600); err != nil {
		return "", fmt.Errorf("write patch: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "apply", "--whitespace=nowarn", patchPath)
	cmd.Dir = wsDir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "patch does not apply: " + strings.TrimSpace(stderr.String()), nil
		}
		return "", fmt.Errorf("git apply: %w", err)
	}
	return "", nil
}

// writeTree copies the regular files of fsys into dir (created even when fsys is empty).
func writeTree(fsys fs.FS, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names, err := eval.ListFiles(fsys)
	if err != nil {
		return err
	}
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate grading id: %w", err)
	}
	return "grade_" + time.Now().UTC().Format("20060102150405") + "_" + hex.EncodeToString(b), nil
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "…" + string(b[len(b)-n:])
}
