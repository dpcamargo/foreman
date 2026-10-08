package verify_test

import (
	"testing"

	"github.com/dpcamargo/foreman/internal/core/verify"
)

func TestParseProjectConfig(t *testing.T) {
	yamlInput := `
version: 1
verifier_image: foreman-grader:latest
setup:
  - ["go", "version"]
verify:
  - ["go", "test", "-json", "./..."]
protected_paths:
  - ".foreman/**"
  - "go.mod"
`
	cfg, err := verify.ParseProjectConfig([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseProjectConfig failed: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if cfg.VerifierImage != "foreman-grader:latest" {
		t.Errorf("expected verifier_image foreman-grader:latest, got %s", cfg.VerifierImage)
	}
	if len(cfg.Setup) != 1 || cfg.Setup[0][0] != "go" || cfg.Setup[0][1] != "version" {
		t.Errorf("unexpected setup command: %v", cfg.Setup)
	}
	if len(cfg.Verify) != 1 || cfg.Verify[0][0] != "go" || cfg.Verify[0][1] != "test" {
		t.Errorf("unexpected verify command: %v", cfg.Verify)
	}
	if len(cfg.ProtectedPaths) != 2 || cfg.ProtectedPaths[0] != ".foreman/**" || cfg.ProtectedPaths[1] != "go.mod" {
		t.Errorf("unexpected protected_paths: %v", cfg.ProtectedPaths)
	}
}

func TestParseProjectConfigInvalid(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "non integer version", data: `version: "not an int"`},
		{name: "unsupported version", data: "version: 2\nverifier_image: image\nverify:\n  - [\"go\", \"test\", \"./...\"]"},
		{name: "missing image", data: "version: 1\nverify:\n  - [\"go\", \"test\", \"./...\"]"},
		{name: "missing verify commands", data: "version: 1\nverifier_image: image"},
		{name: "empty command", data: "version: 1\nverifier_image: image\nverify:\n  - []"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verify.ParseProjectConfig([]byte(tt.data))
			if err == nil {
				t.Error("expected invalid config error, got nil")
			}
		})
	}
}
