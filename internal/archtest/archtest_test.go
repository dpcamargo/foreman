// Package archtest enforces constitution Principle X: packages under internal/core may
// import only the standard library and other core packages.
package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/dpcamargo/foreman"

// listedPackage is the subset of `go list -json` output the check needs.
type listedPackage struct {
	ImportPath string
	Standard   bool
}

// violations returns every package in deps that a core package must not depend on.
// deps is the output of `go list -deps ./internal/core/...`, i.e. the core packages
// themselves plus everything they transitively import.
func violations(deps []listedPackage) []string {
	var bad []string
	for _, p := range deps {
		switch {
		case p.Standard:
		case p.ImportPath == module+"/internal/core" || strings.HasPrefix(p.ImportPath, module+"/internal/core/"):
		default:
			bad = append(bad, p.ImportPath)
		}
	}
	return bad
}

func TestViolations(t *testing.T) {
	tests := []struct {
		name string
		deps []listedPackage
		want []string
	}{
		{
			name: "stdlib and core only",
			deps: []listedPackage{{ImportPath: "fmt", Standard: true}, {ImportPath: module + "/internal/core/eval"}},
		},
		{
			name: "adapter import",
			deps: []listedPackage{{ImportPath: module + "/internal/core/eval"}, {ImportPath: module + "/internal/adapters/sqlite"}},
			want: []string{module + "/internal/adapters/sqlite"},
		},
		{
			name: "cmd import",
			deps: []listedPackage{{ImportPath: module + "/cmd/fm"}},
			want: []string{module + "/cmd/fm"},
		},
		{
			name: "third-party module",
			deps: []listedPackage{{ImportPath: "modernc.org/sqlite"}},
			want: []string{"modernc.org/sqlite"},
		},
		{
			name: "look-alike prefix is not core",
			deps: []listedPackage{{ImportPath: module + "/internal/corex"}},
			want: []string{module + "/internal/corex"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := violations(tt.deps)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCoreImportsOnlyStdlibAndCore runs the check against the real tree.
func TestCoreImportsOnlyStdlibAndCore(t *testing.T) {
	names, err := goList(t, "-f", "{{.ImportPath}}", module+"/internal/core/...")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(names)) == "" {
		t.Skip("no packages under internal/core yet; nothing to check")
	}
	out, err := goList(t, "-deps", "-json", module+"/internal/core/...")
	if err != nil {
		t.Fatal(err)
	}
	var deps []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		deps = append(deps, p)
	}
	if bad := violations(deps); len(bad) > 0 {
		t.Errorf("internal/core depends on non-core, non-stdlib packages (constitution X):\n  %s",
			strings.Join(bad, "\n  "))
	}
}

func goList(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "matched no packages") {
			return nil, nil
		}
		return nil, fmt.Errorf("go list %v: %w: %s", args, err, stderr.String())
	}
	return out, nil
}
