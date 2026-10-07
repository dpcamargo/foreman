package proc

import (
	"os"
	"os/exec"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

func TestSelfIsAlive(t *testing.T) {
	fp, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	if fp.PID != os.Getpid() || fp.StartedAt == "" {
		t.Errorf("Self = %+v", fp)
	}
	if !Alive(fp) {
		t.Error("the current process is not alive")
	}
}

func TestReusedPIDIsNotAlive(t *testing.T) {
	fp, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	fp.StartedAt = "Thu Jan  1 00:00:00 1970"
	if Alive(fp) {
		t.Error("same PID with a different start time must not be alive")
	}
}

func TestDeadPIDIsNotAlive(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// Even if the OS already reused the PID, the start time won't match.
	if Alive(eval.PIDFingerprint{PID: cmd.Process.Pid, StartedAt: "Thu Jan  1 00:00:00 1970"}) {
		t.Error("an exited process is alive")
	}
	if _, err := Fingerprint(cmd.Process.Pid); err == nil {
		t.Logf("pid %d was already reused; covered by the start-time check", cmd.Process.Pid)
	}
}
