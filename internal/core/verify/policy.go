package verify

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type PolicyResult struct {
	Passed     bool
	Violations []string
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[opusr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{50,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`xox[abpr]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

// CheckDiffPolicy verifies diff policy gates (FR-006, FR-016):
// 1. No deletion of test files.
// 2. No edits to protected paths.
// 3. No secrets leaked.
// 4. No symlink escapes.
func CheckDiffPolicy(patch []byte, cfg ProjectConfig, secrets []string) PolicyResult {
	var violations []string

	// Secret check
	patchStr := string(patch)
	for _, sec := range secrets {
		if sec != "" && strings.Contains(patchStr, sec) {
			violations = append(violations, fmt.Sprintf("secret detected in patch: %s", sec))
		}
	}
	if strings.Contains(patchStr, "«redacted:") {
		violations = append(violations, "redaction marker or leaked token detected in patch")
	}
	for _, p := range secretPatterns {
		if p.MatchString(patchStr) {
			violations = append(violations, "secret pattern detected in patch")
			break
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(patch))
	var currentFileA, currentFileB string
	var isDeleted bool
	var isSymlink bool
	var symlinkTarget string

	checkFile := func() {
		target := currentFileB
		if target == "" || target == "/dev/null" {
			target = currentFileA
		}
		if target == "" {
			return
		}

		// 1. Deletion check
		if isDeleted {
			if isTestFile(currentFileA) {
				violations = append(violations, fmt.Sprintf("test file deleted: %s", currentFileA))
			}
		}

		// 2. Protected paths check
		for _, pattern := range cfg.ProtectedPaths {
			if matchGlob(pattern, target) {
				violations = append(violations, fmt.Sprintf("protected path modified: %s", target))
				break
			}
		}

		// 4. Symlink check. Relative links are allowed only when their resolved
		// target remains inside the repository tree.
		if isSymlink && symlinkEscapes(target, symlinkTarget) {
			violations = append(violations, fmt.Sprintf("symlink escapes workspace: %s -> %s", target, symlinkTarget))
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			checkFile()
			currentFileA = ""
			currentFileB = ""
			isDeleted = false
			isSymlink = false
			symlinkTarget = ""
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFileA = strings.TrimPrefix(parts[2], "a/")
				currentFileB = strings.TrimPrefix(parts[3], "b/")
			}
			continue
		}
		if strings.HasPrefix(line, "deleted file mode ") {
			isDeleted = true
			continue
		}
		if strings.HasPrefix(line, "new file mode 120000") {
			isSymlink = true
			continue
		}
		if strings.HasPrefix(line, "--- a/") {
			currentFileA = strings.TrimPrefix(line, "--- a/")
			continue
		}
		if strings.HasPrefix(line, "+++ b/") {
			currentFileB = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		if strings.HasPrefix(line, "+++ /dev/null") {
			isDeleted = true
			continue
		}
		if isSymlink && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			symlinkTarget = strings.TrimPrefix(line, "+")
		}
	}
	checkFile()

	return PolicyResult{
		Passed:     len(violations) == 0,
		Violations: violations,
	}
}

func symlinkEscapes(linkPath, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return true
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
	return resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator))
}

func isTestFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.HasPrefix(base, "test_") {
		return true
	}
	return strings.Contains(path, "/test/") || strings.Contains(path, "/tests/")
}

func matchGlob(pattern, target string) bool {
	pattern = filepath.Clean(pattern)
	target = filepath.Clean(target)
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return target == prefix || strings.HasPrefix(target, prefix+"/")
	}
	matched, _ := filepath.Match(pattern, target)
	return matched || pattern == target
}
