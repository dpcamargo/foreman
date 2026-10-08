package engine

import (
	"testing"
)

func TestValidateTaskSpec(t *testing.T) {
	existingFiles := map[string]bool{
		"src/add.go":      true,
		"src/add_test.go": true,
		"go.mod":          true,
	}

	treeChecker := func(path string) bool {
		return existingFiles[path]
	}

	tests := []struct {
		name    string
		spec    TaskSpec
		wantErr string
	}{
		{
			name: "valid spec",
			spec: TaskSpec{
				Summary:            "Implement Subtraction",
				AcceptanceCriteria: []string{"Sub(2, 1) returns 1"},
				VerifyCommands:     [][]string{{"go", "test", "./..."}},
				CitedFiles:         []string{"src/add.go"},
			},
		},
		{
			name: "empty summary",
			spec: TaskSpec{
				Summary:            "  ",
				AcceptanceCriteria: []string{"Sub(2, 1) returns 1"},
				VerifyCommands:     [][]string{{"go", "test", "./..."}},
				CitedFiles:         []string{"src/add.go"},
			},
			wantErr: "summary cannot be empty",
		},
		{
			name: "empty acceptance criteria",
			spec: TaskSpec{
				Summary:            "Valid summary",
				AcceptanceCriteria: nil,
				VerifyCommands:     [][]string{{"go", "test", "./..."}},
				CitedFiles:         []string{"src/add.go"},
			},
			wantErr: "acceptance criteria cannot be empty",
		},
		{
			name: "empty verify commands",
			spec: TaskSpec{
				Summary:            "Valid summary",
				AcceptanceCriteria: []string{"Works"},
				VerifyCommands:     nil,
				CitedFiles:         []string{"src/add.go"},
			},
			wantErr: "verify commands cannot be empty",
		},
		{
			name: "cited file does not exist on base commit",
			spec: TaskSpec{
				Summary:            "Valid summary",
				AcceptanceCriteria: []string{"Works"},
				VerifyCommands:     [][]string{{"go", "test", "./..."}},
				CitedFiles:         []string{"src/nonexistent.go"},
			},
			wantErr: "cited file not found in tree: src/nonexistent.go",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTaskSpec(tc.spec, treeChecker)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" {
				if err == nil || !contains(err.Error(), tc.wantErr) {
					t.Fatalf("got err %v, want substring %q", err, tc.wantErr)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) > 0 && searchSub(s, substr))
}

func searchSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
