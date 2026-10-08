package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// env is foreman's resolved environment, mirroring cmd/fm's env.go (contracts/cli.md
// "Environment"). Duplicated rather than shared: it is a few lines of flag-free I/O
// resolution, and cmd/fm and cmd/foreman are separate main packages that can't import each
// other (AGENTS.md: "cmd/ only wires").
type env struct {
	Home  string // $FOREMAN_HOME
	State string // $FOREMAN_HOME/state: database, socket, reports
	Work  string // $FOREMAN_HOME/work: workspaces, mirrors
}

func loadEnv(getenv func(string) string, homeDir func() (string, error)) (env, error) {
	home, err := homeDir()
	if err != nil {
		return env{}, fmt.Errorf("resolve user home: %w", err)
	}
	fmHome := getenv("FOREMAN_HOME")
	if fmHome == "" {
		fmHome = filepath.Join(home, ".foreman")
	}
	if !filepath.IsAbs(fmHome) {
		return env{}, errors.New("FOREMAN_HOME must be an absolute path")
	}
	fmHome = filepath.Clean(fmHome)
	return env{
		Home:  fmHome,
		State: filepath.Join(fmHome, "state"),
		Work:  filepath.Join(fmHome, "work"),
	}, nil
}

func (e env) ensureDirs() error {
	if err := os.MkdirAll(e.State, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := os.Chmod(e.State, 0o700); err != nil {
		return fmt.Errorf("restrict state dir: %w", err)
	}
	if err := os.MkdirAll(e.Work, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	return nil
}

func (e env) socketPath() string { return filepath.Join(e.State, "foreman.sock") }
