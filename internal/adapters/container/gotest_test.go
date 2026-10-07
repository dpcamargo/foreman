package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// The testdata/gotest/*.jsonl streams are real `go test -json` output from Go 1.27.1.
func TestParseGoTestJSON(t *testing.T) {
	tests := []struct {
		file       string
		exitCode   int
		wantPassed int
		wantFailed int
		wantTests  map[string]string // test name -> result
	}{
		{file: "pass.jsonl", exitCode: 0, wantPassed: 2, wantFailed: 0,
			wantTests: map[string]string{"TestOne": "pass", "TestTwo": "pass"}},
		{file: "fail.jsonl", exitCode: 1, wantPassed: 1, wantFailed: 1,
			wantTests: map[string]string{"TestOne": "fail", "TestOK": "pass", "TestSkip": "skip"}},
		{file: "buildfail.jsonl", exitCode: 1, wantPassed: 0, wantFailed: 1,
			wantTests: map[string]string{"(package)": "fail"}},
		{file: "panic.jsonl", exitCode: 1, wantPassed: 1, wantFailed: 1,
			wantTests: map[string]string{"TestFirst": "pass", "TestOne": "fail"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", "gotest", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			passed, failed, results := ParseGoTestJSON(f, tt.exitCode)
			if passed != tt.wantPassed || failed != tt.wantFailed {
				t.Errorf("passed/failed = %d/%d, want %d/%d", passed, failed, tt.wantPassed, tt.wantFailed)
			}
			got := map[string]string{}
			for _, r := range results {
				got[r.Test] = r.Result
				if r.Package == "" {
					t.Errorf("result %+v has no package", r)
				}
			}
			for name, want := range tt.wantTests {
				if got[name] != want {
					t.Errorf("%s = %q, want %q (all: %v)", name, got[name], want, got)
				}
			}
		})
	}
}

func TestParseGoTestJSONEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		stream     string
		exitCode   int
		wantPassed int
		wantFailed int
	}{
		{name: "nothing ran, exit 0", stream: "", exitCode: 0},
		{name: "nothing ran, exit non-zero is a failure", stream: "", exitCode: 2, wantFailed: 1},
		{name: "non-JSON noise is ignored", stream: "go: downloading x\n{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"TestA\"}\n", wantPassed: 1},
		{name: "subtests are not double counted",
			stream: `{"Action":"pass","Package":"p","Test":"TestA/one"}
{"Action":"pass","Package":"p","Test":"TestA"}
`, wantPassed: 1},
		{name: "failing subtest fails its parent once",
			stream: `{"Action":"fail","Package":"p","Test":"TestA/one"}
{"Action":"fail","Package":"p","Test":"TestA"}
{"Action":"fail","Package":"p"}
`, exitCode: 1, wantFailed: 1},
		{name: "exit non-zero with only passes still counts a failure",
			stream: `{"Action":"pass","Package":"p","Test":"TestA"}
`, exitCode: 1, wantPassed: 1, wantFailed: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, failed, _ := ParseGoTestJSON(strings.NewReader(tt.stream), tt.exitCode)
			if passed != tt.wantPassed || failed != tt.wantFailed {
				t.Errorf("passed/failed = %d/%d, want %d/%d", passed, failed, tt.wantPassed, tt.wantFailed)
			}
		})
	}
}

var _ = eval.TestResult{} // results are core types
