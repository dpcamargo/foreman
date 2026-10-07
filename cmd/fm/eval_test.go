package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtures = "../../testdata/fixtures"

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func requestFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "request.md")
	if err := os.WriteFile(p, []byte("Add Sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sha = "4c979416b80fd702664709a8af97bf3b17a9ec89"

func TestRecordInputErrors(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())
	req := requestFile(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing required", []string{"--repo", "/r"}, "required"},
		{"hidden without test-cmd", []string{"--repo", "/r", "--base", sha, "--request-file", req, "--hidden", fixtures + "/addrepo/hidden"}, "go together"},
		{"bad test-cmd JSON", []string{"--repo", "/r", "--base", sha, "--request-file", req, "--hidden", fixtures + "/addrepo/hidden", "--test-cmd", "go test"}, "JSON array"},
		{"short sha", []string{"--repo", "/r", "--base", "abc", "--request-file", req}, "base_commit"},
		{"symlink escape", []string{"--repo", "/r", "--base", sha, "--request-file", req,
			"--hidden", fixtures + "/addrepo/hidden-escape", "--test-cmd", `["go","test","./..."]`}, "symlink"},
		{"bad category", []string{"--repo", "/r", "--base", sha, "--request-file", req, "--category", "chore"}, "category"},
		{"hidden not a dir", []string{"--repo", "/r", "--base", sha, "--request-file", req, "--hidden", "/nope", "--test-cmd", `["go"]`}, "not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, append([]string{"eval", "record"}, tt.args...)...)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr %q lacks %q", stderr, tt.want)
			}
		})
	}
	// Nothing was stored by any failed command.
	code, stdout, _ := runCLI(t, "eval", "list")
	if code != exitOK || !strings.Contains(stdout, "0 records") {
		t.Errorf("list after failures: exit %d, %q", code, stdout)
	}
}

func TestRecordNoValidateAndList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FOREMAN_HOME", home)
	req := requestFile(t)

	code, stdout, stderr := runCLI(t, "eval", "record", "--repo", "/repos/addrepo.git", "--base", sha,
		"--request-file", req, "--hidden", fixtures+"/addrepo/hidden", "--test-cmd", `["go","test","-json","./..."]`,
		"--reference", fixtures+"/addrepo/reference.patch", "--category", "bug_fix", "--no-validate")
	if code != exitOK {
		t.Fatalf("record: exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "pending validation") {
		t.Errorf("record output %q", stdout)
	}
	id := strings.Fields(stdout)[0]
	if !strings.HasPrefix(id, "rec_") {
		t.Fatalf("no record id in %q", stdout)
	}
	if fi, err := os.Stat(filepath.Join(home, "state")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir: %v, mode %v", err, fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(home, "state", "records", id, "hidden", "sub_test.go")); err != nil {
		t.Errorf("hidden test not stored: %v", err)
	}

	// A metrics-only record (no hidden tests) is accepted.
	code, _, stderr = runCLI(t, "eval", "record", "--repo", "/repos/addrepo.git", "--base", sha,
		"--request-file", req, "--live-outcome", "rescued", "--live-interventions", "1", "--no-validate")
	if code != exitOK {
		t.Fatalf("metrics-only record: exit %d: %s", code, stderr)
	}

	code, stdout, _ = runCLI(t, "eval", "list")
	if code != exitOK || !strings.Contains(stdout, "2 records, 0 replayable") || !strings.Contains(stdout, id) ||
		!strings.Contains(stdout, "rescued") || !strings.Contains(stdout, "pending") {
		t.Errorf("list: exit %d\n%s", code, stdout)
	}

	code, stdout, _ = runCLI(t, "eval", "list", "--json")
	var rows []map[string]any
	if code != exitOK || json.Unmarshal([]byte(stdout), &rows) != nil || len(rows) != 2 {
		t.Fatalf("list --json: exit %d, %q", code, stdout)
	}
	if rows[0]["category"] != "bug_fix" || rows[0]["base"] != sha {
		t.Errorf("first row = %v", rows[0])
	}

	code, stdout, _ = runCLI(t, "eval", "list", "--replayable")
	if code != exitOK || !strings.Contains(stdout, "0 records") {
		t.Errorf("list --replayable: %q", stdout)
	}
}

func TestValidateFlagConflicts(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())
	if code, _, _ := runCLI(t, "eval", "validate", "--all", "rec_1"); code != exitUsage {
		t.Errorf("--all with ids: exit %d, want %d", code, exitUsage)
	}
}

func TestParseSince(t *testing.T) {
	if _, err := parseSince("nonsense", nowForTest); err == nil {
		t.Error("want error for nonsense")
	}
	for _, s := range []string{"24h", "2026-10-01", "2026-10-01T10:00:00Z"} {
		if _, err := parseSince(s, nowForTest); err != nil {
			t.Errorf("parseSince(%q): %v", s, err)
		}
	}
}

func TestDecideFlagValidation(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())
	for _, args := range [][]string{
		{"eval", "decide", "--arms", "a1"},                              // no budget
		{"eval", "decide", "--budget-tokens", "100"},                    // no arms
		{"eval", "decide", "--arms", "a1,a1", "--budget-tokens", "100"}, // duplicate arm
		{"eval", "decide", "--arms", "zz", "--budget-tokens", "100"},    // unknown arm
	} {
		if code, _, stderr := runCLI(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d (%s)", args, code, exitUsage, stderr)
		}
	}
	if code, _, _ := runCLI(t, "eval", "report"); code != exitUsage {
		t.Error("report without an id must be a usage error")
	}
	if code, _, _ := runCLI(t, "eval", "report", "rpt_nope"); code != exitUsage {
		t.Error("unknown report id must be exit 2")
	}
}

func TestListFooterCountsSinceLastSpot(t *testing.T) {
	t.Setenv("FOREMAN_HOME", t.TempDir())
	_, stdout, _ := runCLI(t, "eval", "list")
	if !strings.Contains(stdout, "0 replayable records since the last spot check") {
		t.Errorf("list footer missing: %q", stdout)
	}
}
