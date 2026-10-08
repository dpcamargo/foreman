package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
)

type fakeNotifier struct {
	published []string
}

func (f *fakeNotifier) Publish(ctx context.Context, message, title string) error {
	f.published = append(f.published, message)
	return nil
}

type fakeHousekeepingStore struct {
	stale []string
}

func (f *fakeHousekeepingStore) PendingApprovalsSince(ctx context.Context, cutoff time.Time) ([]string, error) {
	return f.stale, nil
}

// T045: a file past its retention window is swept, one inside it is kept.
func TestSweepDirRemovesOnlyExpiredEntries(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.log")
	fresh := filepath.Join(dir, "fresh.log")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pastTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(old, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	sweepDir(dir, control.KindContainerOutput, time.Now(), discard{})

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expected old.log to be swept, stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("expected fresh.log to be kept, stat err = %v", err)
	}
}

// T045/T046: one tick sweeps expired files and pings for each stale approval — and never
// resolves or deletes an approval itself (FR-011).
func TestRunHousekeepingTickSweepsAndPings(t *testing.T) {
	work := t.TempDir()
	state := t.TempDir()
	runsDir := filepath.Join(work, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(runsDir, "old-output.log")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(old, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	e := env{Work: work, State: state}
	store := &fakeHousekeepingStore{stale: []string{"apv_1", "apv_2"}}
	notifier := &fakeNotifier{}

	runHousekeepingTick(context.Background(), e, store, notifier, time.Now(), discard{})

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expected old output to be swept, stat err = %v", err)
	}
	if len(notifier.published) != 2 {
		t.Errorf("expected 2 ntfy pings (one per stale approval), got %d: %+v", len(notifier.published), notifier.published)
	}
	// T046's contract: never auto-resolves the approval itself — this fake's store only
	// ever returns ids, it has no resolve method to call, which is the point.
}

// T046: a nil notifier (no FOREMAN_NTFY_TOPIC_URL configured) still sweeps files — the
// secondary channel is optional (spec.md Assumptions).
func TestRunHousekeepingTickWithoutNotifierStillSweeps(t *testing.T) {
	work := t.TempDir()
	runsDir := filepath.Join(work, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(runsDir, "old.log")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(old, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	e := env{Work: work, State: t.TempDir()}
	runHousekeepingTick(context.Background(), e, &fakeHousekeepingStore{}, nil, time.Now(), discard{})

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expected old.log swept even without a notifier, stat err = %v", err)
	}
}

// T046: NtfyTopicURL wiring — nil without it, non-nil with it.
func TestNtfyForEnv(t *testing.T) {
	if got := ntfyForEnv(env{}); got != nil {
		t.Errorf("expected nil notifier with no NtfyTopicURL, got %+v", got)
	}
	if got := ntfyForEnv(env{NtfyTopicURL: "https://ntfy.sh/foreman"}); got == nil {
		t.Error("expected a notifier with NtfyTopicURL set, got nil")
	}
}
