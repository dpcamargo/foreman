package verify_test

import (
	"testing"

	"github.com/dpcamargo/foreman/internal/core/verify"
)

func TestDiffPolicyCleanPatch(t *testing.T) {
	patch := []byte(`diff --git a/add.go b/add.go
index 1234567..89abcdef 100644
--- a/add.go
+++ b/add.go
@@ -1,3 +1,7 @@
 package add
+
+func Sub(a, b int) int {
+    return a - b
+}
`)
	cfg := verify.ProjectConfig{
		ProtectedPaths: []string{".foreman/**", "go.mod"},
	}
	res := verify.CheckDiffPolicy(patch, cfg, nil)
	if !res.Passed {
		t.Errorf("expected clean patch to pass policy, got violations: %v", res.Violations)
	}
}

func TestDiffPolicyTestDeletion(t *testing.T) {
	patch := []byte(`diff --git a/add_test.go b/add_test.go
deleted file mode 100644
index 1234567..0000000
--- a/add_test.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package add
`)
	cfg := verify.ProjectConfig{}
	res := verify.CheckDiffPolicy(patch, cfg, nil)
	if res.Passed {
		t.Error("expected test deletion to fail policy, got passed")
	}
	if len(res.Violations) == 0 || !containsSub(res.Violations[0], "test", "delete") {
		t.Errorf("unexpected violations: %v", res.Violations)
	}
}

func TestDiffPolicyProtectedPath(t *testing.T) {
	patch := []byte(`diff --git a/go.mod b/go.mod
index 1234567..89abcdef 100644
--- a/go.mod
+++ b/go.mod
@@ -1,3 +1,4 @@
 module example.com/pkg
+// unauthorized mod change
`)
	cfg := verify.ProjectConfig{
		ProtectedPaths: []string{"go.mod", ".foreman/**"},
	}
	res := verify.CheckDiffPolicy(patch, cfg, nil)
	if res.Passed {
		t.Error("expected protected path modification to fail policy, got passed")
	}
	if len(res.Violations) == 0 || !containsSub(res.Violations[0], "protected", "go.mod") {
		t.Errorf("unexpected violations: %v", res.Violations)
	}
}

func TestDiffPolicySecretDetected(t *testing.T) {
	patch := []byte(`diff --git a/api.go b/api.go
index 1234567..89abcdef 100644
--- a/api.go
+++ b/api.go
@@ -1,3 +1,4 @@
 package api
+const secret = "«redacted:ghp_…»"
`)
	cfg := verify.ProjectConfig{}
	res := verify.CheckDiffPolicy(patch, cfg, []string{"my-secret-token"})
	if res.Passed {
		t.Error("expected token leak to fail policy, got passed")
	}
}

func TestDiffPolicySymlinks(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantPassed bool
	}{
		{name: "inside workspace", target: "docs/guide.md", wantPassed: true},
		{name: "parent escape", target: "../../outside", wantPassed: false},
		{name: "absolute escape", target: "/etc/passwd", wantPassed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := []byte("diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1 @@\n+" + tt.target + "\n")
			res := verify.CheckDiffPolicy(patch, verify.ProjectConfig{}, nil)
			if res.Passed != tt.wantPassed {
				t.Fatalf("target %q: Passed = %v, want %v; violations: %v", tt.target, res.Passed, tt.wantPassed, res.Violations)
			}
		})
	}
}

func containsSub(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
