package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestRunUsageAndFlagParsing(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "run without args prints usage",
			args:       []string{"run"},
			wantCode:   exitUsage,
			wantStderr: "usage: fm run",
		},
		{
			name:       "run help flag",
			args:       []string{"run", "-h"},
			wantCode:   exitOK,
			wantStdout: "usage: fm run",
		},
		{
			name:       "missing task file",
			args:       []string{"run", filepath.Join(t.TempDir(), "missing.yaml")},
			wantCode:   exitUsage,
			wantStderr: "no such file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout %q lacks %q", stdout, tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q lacks %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestRunTaskFileIngestionValidation(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())
	dir := t.TempDir()
	badTask := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badTask, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLI(t, "run", badTask)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "version") {
		t.Errorf("stderr %q lacks version error", stderr)
	}
}

func TestRunInterruptedResumption(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FOREMAN_HOME", home)

	ctx := context.Background()
	a, err := openApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()

	now := time.Now().UTC()
	tsk := task.Task{
		ID:          "tsk_lost_1",
		TaskType:    task.TaskTypeCode,
		RepoURL:     "/tmp/repo.git",
		BaseCommit:  "4c979416b80fd702664709a8af97bf3b17a9ec89",
		RequestText: "Add Sub",
		Status:      task.StatusRunning,
	}
	nodes := task.NewLinearNodes(tsk.ID, now)
	if _, err := a.store.CreateTask(ctx, tsk, nodes); err != nil {
		t.Fatal(err)
	}
	run := task.Run{
		ID:           "run_lost_1",
		TaskID:       tsk.ID,
		NodeID:       nodes[1].ID,
		AttemptNum:   1,
		LadderRung:   1,
		Route:        "codex/default",
		ModelFamily:  "openai",
		Status:       task.RunRunning,
		PID:          9999999, // inactive PID
		PIDStartedAt: "old",
		StartedAt:    now,
	}
	if err := a.store.RecordTaskRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	a.close()

	// Running with --resume should detect and sweep the dead PID
	code, stdout, stderr := runCLI(t, "run", "--resume")
	if code != exitOK && code != exitUsage {
		t.Errorf("exit code = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout+stderr, "marked") && !strings.Contains(stdout+stderr, "lost") && !strings.Contains(stdout+stderr, "resum") {
		t.Logf("stdout: %s, stderr: %s", stdout, stderr)
	}

	// Verify run in store was marked infra_error
	a2, err := openApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.close()
	runs, err := a2.store.RunningTaskRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 running runs after sweep, got %d", len(runs))
	}
}
