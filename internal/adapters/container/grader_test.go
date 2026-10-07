//go:build container

package container

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// These tests need Docker (Colima) and the pinned grader image. Colima shares only
// $HOME with its VM, so every bind-mounted path lives under $HOME/.foreman-test.

const testImage = "golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452"

func homeTemp(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".foreman-test")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "grader-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newGrader(t *testing.T) (*Grader, string) {
	t.Helper()
	root := homeTemp(t)
	work := filepath.Join(root, "work")
	g := New(Config{
		Image:  testImage,
		Work:   work,
		Clones: sandbox.New(work),
		Redact: func(s string) string { return s },
	})
	if err := g.EnsureImage(context.Background()); err != nil {
		t.Fatal(err)
	}
	return g, root
}

// fixtureRepo turns testdata/fixtures/<name>/src into a one-commit repo under root.
func fixtureRepo(t *testing.T, root, name string) (string, string) {
	t.Helper()
	src := filepath.Join("..", "..", "..", "testdata", "fixtures", name, "src")
	dir := filepath.Join(root, "src-"+name)
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return commitAll(t, dir)
}

func commitAll(t *testing.T, dir string) (string, string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"},
	} {
		run(t, dir, "git", args...)
	}
	return dir, strings.TrimSpace(run(t, dir, "git", "rev-parse", "HEAD"))
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func fixtureFS(name, sub string) fs.FS {
	return os.DirFS(filepath.Join("..", "..", "..", "testdata", "fixtures", name, sub))
}

func goTest() []string { return []string{"go", "test", "-json", "./..."} }

// gradeHidden grades the unpatched addrepo base with a single hidden test file.
func gradeHidden(t *testing.T, g *Grader, root, testSrc string) eval.GradeOutput {
	t.Helper()
	repo, base := fixtureRepo(t, root, "addrepo")
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base,
		Hidden:      fstest.MapFS{"probe_test.go": {Data: []byte(testSrc)}},
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON,
		Timeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantAllPass(t *testing.T, out eval.GradeOutput) {
	t.Helper()
	if out.HiddenFailed != 0 || out.HiddenPassed == 0 {
		t.Fatalf("passed/failed = %d/%d, want all pass\nlog:\n%s", out.HiddenPassed, out.HiddenFailed, out.Log)
	}
}

func TestTestStageHasNoNetwork(t *testing.T) {
	g, root := newGrader(t)
	out := gradeHidden(t, g, root, `package addrepo
import ("net"; "testing"; "time")
func TestNoNetwork(t *testing.T) {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err == nil { c.Close(); t.Fatal("outbound connection succeeded from the test stage") }
}`)
	wantAllPass(t, out)
}

func TestNoHostMounts(t *testing.T) {
	g, root := newGrader(t)
	out := gradeHidden(t, g, root, `package addrepo
import ("os"; "testing")
func TestNoUsers(t *testing.T) {
	if _, err := os.Stat("/Users"); err == nil { t.Fatal("/Users is visible in the grader") }
}`)
	wantAllPass(t, out)
}

func TestNoHostEnv(t *testing.T) {
	t.Setenv("FM_GRADER_CANARY", "leaked-canary-value")
	home, _ := os.UserHomeDir()
	g, root := newGrader(t)
	out := gradeHidden(t, g, root, `package addrepo
import ("os"; "testing")
func TestNoHostEnv(t *testing.T) {
	if os.Getenv("FM_GRADER_CANARY") != "" { t.Error("host env var leaked") }
	if os.Getenv("HOME") == "`+home+`" { t.Error("host HOME leaked") }
	if os.Getenv("PATH") == "`+os.Getenv("PATH")+`" { t.Error("host PATH leaked") }
}`)
	wantAllPass(t, out)
}

func TestHiddenReadOnly(t *testing.T) {
	g, root := newGrader(t)
	out := gradeHidden(t, g, root, `package addrepo
import ("os"; "testing")
func TestHiddenRO(t *testing.T) {
	if err := os.WriteFile("/hidden/x", []byte("x"), 0o644); err == nil { t.Fatal("/hidden is writable") }
}`)
	wantAllPass(t, out)
}

func TestFailOnBase(t *testing.T) {
	g, root := newGrader(t)
	repo, base := fixtureRepo(t, root, "addrepo")
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base, Hidden: fixtureFS("addrepo", "hidden"),
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON, Timeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.HiddenFailed == 0 {
		t.Fatalf("hidden tests passed on base: %+v\n%s", out, out.Log)
	}
}

func TestPassWithReference(t *testing.T) {
	g, root := newGrader(t)
	repo, base := fixtureRepo(t, root, "addrepo")
	patch, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "addrepo", "reference.patch"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base, Patch: patch, Hidden: fixtureFS("addrepo", "hidden"),
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON, Timeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAllPass(t, out)
	if out.ApplyError != "" {
		t.Errorf("ApplyError = %q", out.ApplyError)
	}
}

func TestPatchThatDoesNotApply(t *testing.T) {
	g, root := newGrader(t)
	repo, base := fixtureRepo(t, root, "addrepo")
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base, Patch: []byte("diff --git a/nope.go b/nope.go\n--- a/nope.go\n+++ b/nope.go\n@@ -1 +1 @@\n-x\n+y\n"),
		Hidden: fixtureFS("addrepo", "hidden"), TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON,
		Timeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatalf("a patch that doesn't apply is an attempt failure, not an infra error: %v", err)
	}
	if out.ApplyError == "" || out.HiddenPassed != 0 {
		t.Errorf("want ApplyError and nothing passed, got %+v", out)
	}
}

func TestBadImageIsInfraError(t *testing.T) {
	root := homeTemp(t)
	work := filepath.Join(root, "work")
	g := New(Config{Image: "foreman.invalid/no-such-image:latest", Work: work, Clones: sandbox.New(work),
		Redact: func(s string) string { return s }})
	repo, base := fixtureRepo(t, root, "addrepo")
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base, Hidden: fixtureFS("addrepo", "hidden"),
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON, Timeout: time.Minute,
	})
	if err == nil {
		t.Fatalf("missing image returned no error: %+v", out)
	}
	if out.HiddenPassed != 0 || out.HiddenFailed != 0 {
		t.Errorf("infra error must not carry a grade: %+v", out)
	}
}

func TestFullSuite(t *testing.T) {
	g, root := newGrader(t)
	repo, base := fixtureRepo(t, root, "addrepo")
	in := eval.GradeInput{
		RepoURL: repo, Base: base, Hidden: fixtureFS("addrepo", "hidden"),
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON,
		FullTest: []string{"go", "test", "./..."}, Timeout: 3 * time.Minute,
	}
	// The full suite runs without the hidden tests, so base passes it.
	out, err := g.Grade(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.FullSuitePassed == nil || !*out.FullSuitePassed {
		t.Fatalf("base full suite = %v, want pass\n%s", out.FullSuitePassed, out.Log)
	}
	// A patch that breaks the existing TestAdd fails the full suite.
	in.Patch = []byte(`diff --git a/add.go b/add.go
--- a/add.go
+++ b/add.go
@@ -2,4 +2,4 @@
 package addrepo
 
 // Add returns a + b.
-func Add(a, b int) int { return a + b }
+func Add(a, b int) int { return a - b }
`)
	out, err = g.Grade(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.ApplyError != "" {
		t.Fatalf("patch did not apply: %s", out.ApplyError)
	}
	if out.FullSuitePassed == nil || *out.FullSuitePassed {
		t.Fatalf("broken full suite = %v, want fail\n%s", out.FullSuitePassed, out.Log)
	}
}

// A repo with a real third-party dependency: the fetch stage downloads it with network,
// and the offline test stage builds against the read-only module volume.
func TestDependencyFetchThenOfflineTest(t *testing.T) {
	g, root := newGrader(t)
	dir := filepath.Join(root, "src-dep")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/dep\n\ngo 1.27\n\nrequire rsc.io/quote v1.5.2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "q.go"), []byte("package dep\n\nimport \"rsc.io/quote\"\n\nfunc Hello() string { return quote.Hello() }\n"), 0o644)
	run(t, dir, "go", "mod", "tidy")
	repo, base := commitAll(t, dir)
	out, err := g.Grade(context.Background(), eval.GradeInput{
		RepoURL: repo, Base: base,
		Hidden: fstest.MapFS{"q_test.go": {Data: []byte(`package dep
import "testing"
func TestHello(t *testing.T) { if Hello() == "" { t.Fatal("empty") } }`)}},
		TestCommand: goTest(), TestFormat: eval.FormatGoTestJSON, Timeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAllPass(t, out)
}

func TestExitCodeFormat(t *testing.T) {
	g, root := newGrader(t)
	repo, base := fixtureRepo(t, root, "addrepo")
	for _, tc := range []struct {
		cmd        []string
		wantPassed int
	}{{[]string{"true"}, 1}, {[]string{"false"}, 0}} {
		out, err := g.Grade(context.Background(), eval.GradeInput{
			RepoURL: repo, Base: base, Hidden: fixtureFS("addrepo", "hidden"),
			TestCommand: tc.cmd, TestFormat: eval.FormatExitCode, Timeout: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.HiddenPassed != tc.wantPassed || out.HiddenPassed+out.HiddenFailed != 1 {
			t.Errorf("%v: passed/failed = %d/%d", tc.cmd, out.HiddenPassed, out.HiddenFailed)
		}
	}
}
