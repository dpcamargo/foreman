package eval

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedact(t *testing.T) {
	r := NewRedactor(map[string]string{
		"GITHUB_TOKEN":  "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"MY_API_KEY":    "value-of-api-key-123",
		"DB_PASSWORD":   "hunter2hunter2",
		"CLIENT_SECRET": "s3cr3t-s3cr3t",
		"SHORT_TOKEN":   "abc",       // under 8 characters: never redacted
		"HOME":          "/Users/op", // not a secret-looking name
		"EMPTY_SECRET":  "",
	})
	tests := []struct {
		name, in, want string
	}{
		{"env value", "auth with value-of-api-key-123 now", "auth with [REDACTED] now"},
		{"password value", "pw=hunter2hunter2;", "pw=[REDACTED];"},
		{"secret value", "s3cr3t-s3cr3t", "[REDACTED]"},
		{"short values untouched", "abc def", "abc def"},
		{"non-secret env untouched", "cd /Users/op", "cd /Users/op"},
		{"github classic", "token gho_" + strings.Repeat("A", 36) + " end", "token [REDACTED] end"},
		{"github fine-grained", "github_pat_" + strings.Repeat("x", 60), "[REDACTED]"},
		{"github server", "ghs_" + strings.Repeat("b", 40), "[REDACTED]"},
		{"openai-style", "key=sk-proj-" + strings.Repeat("Z", 30), "key=[REDACTED]"},
		{"slack", "xoxb-" + strings.Repeat("1", 20), "[REDACTED]"},
		{"aws", "AKIA" + strings.Repeat("Q", 16), "[REDACTED]"},
		{"pem", "a\n-----BEGIN OPENSSH PRIVATE KEY-----\nMIIabc\ndef\n-----END OPENSSH PRIVATE KEY-----\nb", "a\n[REDACTED]\nb"},
		{"ordinary text", "go test ./... ok sk- is not a key", "go test ./... ok sk- is not a key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Redact(tt.in); got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	r := NewRedactor(nil)
	if got := r.Truncate("short", 64); got != "short" {
		t.Errorf("short input changed: %q", got)
	}
	long := strings.Repeat("é", 100) // 2 bytes each
	got := r.Truncate(long, 101)
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a UTF-8 sequence: %q", got)
	}
	if !strings.HasSuffix(got, truncatedMarker) {
		t.Errorf("truncated output lacks marker: %q", got)
	}
	if body := strings.TrimSuffix(got, truncatedMarker); len(body) > 101 {
		t.Errorf("body is %d bytes, limit 101", len(body))
	}
}
