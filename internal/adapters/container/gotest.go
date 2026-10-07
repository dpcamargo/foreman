// Package container implements the eval Grader: patches are graded in clean,
// offline Docker containers (specs/001-evaluation-harness/research.md R5).
package container

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

type goTestEvent struct {
	Action  string
	Package string
	Test    string
}

// ParseGoTestJSON reads a `go test -json` stream and counts top-level tests (subtests
// roll up into their parent). A package that failed without any failing test (a build
// failure) counts as one failure. A non-zero exit with no failures recorded also counts
// as one failure, so a broken run can never look like "all passed". Lines that aren't
// JSON events are ignored.
func ParseGoTestJSON(r io.Reader, exitCode int) (passed, failed int, results []eval.TestResult) {
	type key struct{ pkg, test string }
	final := map[key]string{}
	var order []key
	pkgFailed := map[string]bool{}
	var pkgOrder []string

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev goTestEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
		default:
			continue
		}
		if ev.Test == "" {
			if ev.Action == "fail" && !pkgFailed[ev.Package] {
				pkgFailed[ev.Package] = true
				pkgOrder = append(pkgOrder, ev.Package)
			}
			continue
		}
		k := key{ev.Package, ev.Test}
		if _, seen := final[k]; !seen {
			order = append(order, k)
		}
		final[k] = ev.Action
	}

	pkgHasFailingTest := map[string]bool{}
	for _, k := range order {
		res := final[k]
		results = append(results, eval.TestResult{Package: k.pkg, Test: k.test, Result: res})
		if strings.Contains(k.test, "/") {
			continue // subtest: its parent carries the result
		}
		switch res {
		case "pass":
			passed++
		case "fail":
			failed++
			pkgHasFailingTest[k.pkg] = true
		}
	}
	for _, pkg := range pkgOrder {
		if !pkgHasFailingTest[pkg] {
			failed++
			results = append(results, eval.TestResult{Package: pkg, Test: "(package)", Result: "fail"})
		}
	}
	if exitCode != 0 && failed == 0 {
		failed++
		results = append(results, eval.TestResult{Package: "(command)", Test: "(exit status)", Result: "fail"})
	}
	return passed, failed, results
}
