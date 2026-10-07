package eval

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// RedactedMarker replaces every secret found by a Redactor.
const RedactedMarker = "[REDACTED]"

const truncatedMarker = "\n…[truncated]"

// minSecretLen is the shortest environment value treated as a secret; shorter values
// would redact ordinary words.
const minSecretLen = 8

// tokenPatterns are common credential formats (research R5, Redaction).
var tokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[opusr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{50,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`xox[abpr]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// Redactor removes secrets from text before it is persisted. It is pure: the caller
// passes in the environment whose secret-looking values must never be written down.
type Redactor struct {
	values []string // longest first, so a value containing another is replaced whole
}

// NewRedactor treats the values of env vars whose names contain TOKEN, KEY, SECRET or
// PASSWORD (and are at least 8 characters long) as secrets.
func NewRedactor(env map[string]string) *Redactor {
	var values []string
	for name, value := range env {
		upper := strings.ToUpper(name)
		if len(value) < minSecretLen {
			continue
		}
		for _, marker := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD"} {
			if strings.Contains(upper, marker) {
				values = append(values, value)
				break
			}
		}
	}
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	return &Redactor{values: slices.Compact(values)}
}

// Redact replaces known secret values and token patterns with RedactedMarker.
func (r *Redactor) Redact(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, RedactedMarker)
	}
	for _, p := range tokenPatterns {
		s = p.ReplaceAllString(s, RedactedMarker)
	}
	return s
}

// Truncate cuts s to at most limit bytes (on a UTF-8 boundary) and marks the cut.
func (r *Redactor) Truncate(s string, limit int) string { return TruncateUTF8(s, limit) }

// TruncateUTF8 cuts s to at most limit bytes on a UTF-8 boundary and marks the cut.
func TruncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedMarker
}
