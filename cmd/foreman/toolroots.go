package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// toolRoots are software install roots an arm may read: the Homebrew prefix and the real
// install directory of the arm's CLI. They hold programs, not secrets.
//
// research.md R1 (codex-cli): codex's own sandbox helper re-executes the `codex` binary
// under the profile, so its install root must be readable or the session fails to start
// ("failed to load AGENTS.md instructions … execvp() … Operation not permitted"). The
// permissions profile in internal/adapters/runner/codex passes these as
// `permissions.fmarm.filesystem` read entries. Without them the spec node failed in ~1.5s
// on a live daemon dispatch (found during 003's live quickstart, T049) — the daemon's
// buildEngineConfig was constructing the codex arm without ToolRoots, unlike cmd/fm.
//
// This mirrors cmd/fm/eval.go's toolRoots exactly (cmd/ only wires; AGENTS.md forbids a
// shared util package, so each command keeps its own copy).
func toolRoots(binary string) []string {
	var roots []string
	for _, p := range []string{"/opt/homebrew", "/usr/local/Cellar", "/usr/local/Caskroom"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			roots = append(roots, p)
		}
	}
	if path, err := exec.LookPath(binary); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			dir := filepath.Dir(filepath.Dir(real))
			covered := false
			for _, r := range roots {
				if dir == r || strings.HasPrefix(dir, r+string(filepath.Separator)) {
					covered = true
				}
			}
			if !covered {
				roots = append(roots, dir)
			}
		}
	}
	return roots
}
