// Package proc fingerprints processes by PID plus start time, so a reused PID is never
// mistaken for a live run (constitution VII).
package proc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// Fingerprint returns pid's fingerprint, or an error if no such process exists.
func Fingerprint(pid int) (eval.PIDFingerprint, error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return eval.PIDFingerprint{}, fmt.Errorf("process %d: %w", pid, err)
	}
	started := strings.Join(strings.Fields(out.String()), " ")
	if started == "" {
		return eval.PIDFingerprint{}, fmt.Errorf("process %d: no start time", pid)
	}
	return eval.PIDFingerprint{PID: pid, StartedAt: started}, nil
}

// Self fingerprints the current process.
func Self() (eval.PIDFingerprint, error) { return Fingerprint(os.Getpid()) }

// Alive reports whether fp still names a running process.
func Alive(fp eval.PIDFingerprint) bool {
	cur, err := Fingerprint(fp.PID)
	return err == nil && cur.StartedAt == fp.StartedAt
}
