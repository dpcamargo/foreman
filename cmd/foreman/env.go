package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultGraderImage is the grader's Go image, pinned by digest — same image cmd/fm pins
// (cmd/fm/env.go), so both binaries verify against identical infrastructure.
const defaultGraderImage = "golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452"

// env is foreman's resolved environment, mirroring cmd/fm's env.go (contracts/cli.md
// "Environment"). Duplicated rather than shared: it is a few lines of flag-free I/O
// resolution, and cmd/fm and cmd/foreman are separate main packages that can't import each
// other (AGENTS.md: "cmd/ only wires").
type env struct {
	Home          string // $FOREMAN_HOME
	State         string // $FOREMAN_HOME/state: database, socket, reports
	Work          string // $FOREMAN_HOME/work: workspaces, mirrors
	DockerContext string // "" means the current docker context
	HermesHome    string // Hermes profile used by the hermes arm
	GraderImage   string
	TelegramToken string // "" disables the Telegram client; the local API still runs
	GitHubToken   string // "" disables US4's delivery/merge gate; same .env as Telegram
	GitHubOwner   string
	GitHubRepo    string
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
	hermesHome := getenv("FOREMAN_HERMES_HOME")
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes", "profiles", "foreman-arm")
	}
	image := getenv("FOREMAN_GRADER_IMAGE")
	if image == "" {
		image = defaultGraderImage
	}
	return env{
		Home:          fmHome,
		State:         filepath.Join(fmHome, "state"),
		Work:          filepath.Join(fmHome, "work"),
		DockerContext: getenv("FOREMAN_DOCKER_CONTEXT"),
		HermesHome:    hermesHome,
		GraderImage:   image,
		TelegramToken: getenv("TELEGRAM_BOT_TOKEN"),
		GitHubToken:   getenv("FOREMAN_GITHUB_TOKEN"),
		GitHubOwner:   getenv("FOREMAN_GITHUB_OWNER"),
		GitHubRepo:    getenv("FOREMAN_GITHUB_REPO"),
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
