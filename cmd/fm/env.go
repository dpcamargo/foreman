package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultGraderImage is the grader's Go image, pinned by digest.
//
// Pinned 2026-10-07: golang:1.27.0-bookworm, last pushed 2026-08-25. That is the newest
// build at least 7 days old (dependency cooldown). golang:1.27.1-bookworm was pushed
// 2026-10-06 and is still inside the cooldown; bump to it after 2026-10-13.
const defaultGraderImage = "golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452"

// env is fm's resolved environment (contracts/cli.md "Environment"; research R2, R12).
type env struct {
	Home          string // $FOREMAN_HOME
	State         string // $FOREMAN_HOME/state: database, records, runs, reports; never readable by arms
	Work          string // $FOREMAN_HOME/work: workspaces, mirrors, module cache
	DockerContext string // "" means the current docker context
	HermesHome    string // Hermes profile used by the A0/A2 arms
	GraderImage   string
}

// loadEnv resolves the environment. getenv and homeDir are injected for tests.
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
	hermesHome := getenv("FOREMAN_HERMES_HOME")
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes", "profiles", "foreman-arm")
	}
	image := getenv("FOREMAN_GRADER_IMAGE")
	if image == "" {
		image = defaultGraderImage
	}
	fmHome = filepath.Clean(fmHome)
	return env{
		Home:          fmHome,
		State:         filepath.Join(fmHome, "state"),
		Work:          filepath.Join(fmHome, "work"),
		DockerContext: getenv("FOREMAN_DOCKER_CONTEXT"),
		HermesHome:    hermesHome,
		GraderImage:   image,
	}, nil
}

// ensureDirs creates state/ (0700) and work/ (0755). An existing state/ with looser
// permissions is tightened, because arms must never be able to read it.
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
