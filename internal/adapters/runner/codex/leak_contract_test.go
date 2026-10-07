//go:build live

package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/artifacts"
	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

const graderImage = "golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452"

// T-leak-scan: a real replay whose arm prints foreman's state path is graded
// contaminated (research R1). Needs codex and Docker; paths live under $HOME so the
// Colima VM can mount them.
func TestLiveLeakScan(t *testing.T) {
	ctx := context.Background()
	home, _ := os.UserHomeDir()
	os.MkdirAll(filepath.Join(home, ".foreman-test"), 0o755)
	root, err := os.MkdirTemp(filepath.Join(home, ".foreman-test"), "leak-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state, work := filepath.Join(root, "state"), filepath.Join(root, "work")

	// The addrepo fixture as a one-commit repo.
	src := filepath.Join(root, "addrepo")
	if err := os.CopyFS(src, os.DirFS("../../../../testdata/fixtures/addrepo/src")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = src
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, _ := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()

	store, err := sqlite.Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rec, err := eval.ValidateNew(eval.Record{
		Source: eval.SourceManual, Category: eval.CategoryFeature, Request: "Add Sub(a, b int) int.",
		RepoURL: src, BaseCommit: strings.TrimSpace(string(head)), TestCommand: []string{"go", "test", "-json", "./..."},
		WallBudget: 4 * time.Minute,
	}, eval.RecordFiles{Hidden: os.DirFS("../../../../testdata/fixtures/addrepo/hidden")})
	if err != nil {
		t.Fatal(err)
	}
	rec, err = store.AddRecord(ctx, rec, eval.RecordFiles{Hidden: os.DirFS("../../../../testdata/fixtures/addrepo/hidden")})
	if err != nil {
		t.Fatal(err)
	}
	store.SetReplayable(ctx, rec.ID, eval.Validation{Replayable: true})
	rec, _ = store.Record(ctx, rec.ID)

	ws := sandbox.New(work)
	grader := container.New(container.Config{Image: graderImage, Work: work, Clones: ws})
	if err := grader.EnsureImage(ctx); err != nil {
		t.Fatal(err)
	}
	self, _ := proc.Self()
	replayer := eval.Replayer{
		Records: store, Runs: store, Workspaces: ws, Artifacts: artifacts.New(state, work, func(s string) string { return s }),
		Grader: grader, StateDir: state, PID: self, TokenCap: 500_000,
		// The override makes the arm say the state path out loud.
		PromptTemplate: "Run exactly this shell command once: echo " + filepath.Join(state, "records") +
			"\nThen reply with only this JSON: {\"status\":\"done\",\"summary\":\"<the output>\",\"question\":null}",
	}
	res, err := replayer.ReplayOnce(ctx, eval.ReplayRequest{Record: rec, Arm: liveArm(t, filepath.Join(work, "gomodcache")), Rep: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("run: %+v grade: %+v", res.Run, res.Grade)
	if res.Run.Status != eval.StatusContaminated || res.Grade == nil || !res.Grade.Contaminated {
		t.Fatalf("T-leak-scan FAILED: status %s, grade %+v", res.Run.Status, res.Grade)
	}
	if b, err := os.ReadFile(filepath.Join(state, "runs", res.Run.ID, "transcript.jsonl")); err != nil || len(b) == 0 {
		t.Errorf("transcript not collected into state: %v", err)
	}
	t.Log("T-leak-scan PASSED")
}
