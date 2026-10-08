package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/ntfy"
	"github.com/dpcamargo/foreman/internal/core/control"
)

// staleApprovalNotifier is the subset of *ntfy.Client the housekeeping tick needs.
type staleApprovalNotifier interface {
	Publish(ctx context.Context, message, title string) error
}

// housekeepingStore is the persistence subset the housekeeping tick needs.
type housekeepingStore interface {
	PendingApprovalsSince(ctx context.Context, cutoff time.Time) ([]string, error)
}

// runHousekeepingTick runs one housekeeping pass (T045/T046, FR-018): sweeps on-disk
// artifacts past their retention window under $FOREMAN_HOME/work/runs and
// $FOREMAN_HOME/state/reports, and pings the secondary channel for any approval left
// pending past the unanswered threshold. It never touches structured SQLite rows and never
// resolves or expires an approval itself (FR-011).
func runHousekeepingTick(ctx context.Context, e env, store housekeepingStore, notifier staleApprovalNotifier, now time.Time, out io.Writer) {
	sweepDir(filepath.Join(e.Work, "runs"), control.KindContainerOutput, now, out)
	sweepDir(filepath.Join(e.State, "reports"), control.KindTranscript, now, out)

	if notifier == nil {
		return
	}
	cutoff := now.Add(-staleApprovalThreshold)
	stale, err := store.PendingApprovalsSince(ctx, cutoff)
	if err != nil {
		fmt.Fprintf(out, "housekeeping: query stale approvals: %v\n", err)
		return
	}
	for _, id := range stale {
		msg := fmt.Sprintf("approval %s has been pending for over %s", id, staleApprovalThreshold)
		if err := notifier.Publish(ctx, msg, "foreman"); err != nil {
			fmt.Fprintf(out, "housekeeping: ntfy ping failed for %s: %v\n", id, err)
		}
	}
}

// staleApprovalThreshold is spec.md Assumptions' default: 4 hours, configurable later via
// policy.yaml (Feature 004).
const staleApprovalThreshold = 4 * time.Hour

// sweepDir removes entries under dir whose modtime puts them past the given kind's
// retention window (control.ShouldSweep decides — this only does the I/O). Reports and
// container output are files/dirs by mtime; there's no per-entry status to consult here,
// so KindContainerOutput's 7d window (configurable to 14) applies.
func sweepDir(dir string, kind control.RetentionKind, now time.Time, out io.Writer) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(out, "housekeeping: read %s: %v\n", dir, err)
		}
		return
	}
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if !control.ShouldSweep(kind, age, control.DefaultRetention) {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		if err := removeAll(path); err != nil {
			fmt.Fprintf(out, "housekeeping: sweep %s: %v\n", path, err)
		}
	}
}

func removeAll(path string) error {
	if strings.TrimSpace(path) == "" || path == "/" {
		return fmt.Errorf("refusing to sweep unsafe path %q", path)
	}
	return os.RemoveAll(path)
}

// startHousekeepingTicker runs runHousekeepingTick once immediately and then on every tick
// until ctx is cancelled. Wired in serve.go's startup sequence.
func startHousekeepingTicker(ctx context.Context, e env, store housekeepingStore, notifier staleApprovalNotifier, out io.Writer, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	runHousekeepingTick(ctx, e, store, notifier, time.Now().UTC(), out)
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			runHousekeepingTick(ctx, e, store, notifier, t.UTC(), out)
		}
	}
}

// ntfyForEnv returns a notifier if NtfyTopicURL is configured, else nil (the tick then
// only sweeps files — no secondary channel is required, spec.md Assumptions).
func ntfyForEnv(e env) staleApprovalNotifier {
	if e.NtfyTopicURL == "" {
		return nil
	}
	return ntfy.New(e.NtfyTopicURL)
}
