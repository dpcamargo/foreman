// Package artifacts implements eval.Artifacts: run files live under work/runs/<id>/io
// while an arm can touch them, and move into state/runs/<id>/ (redacted) afterwards, so
// no state/ path ever reaches an arm (research R2).
package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Store places and collects run files.
type Store struct {
	state, work string
	redact      func(string) string
}

// New returns a Store. redact is applied to everything except patches.
func New(state, work string, redact func(string) string) *Store {
	return &Store{state: state, work: work, redact: redact}
}

func checkID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid run id %q", id)
	}
	return nil
}

func (s *Store) ioDir(id string) string  { return filepath.Join(s.work, "runs", id, "io") }
func (s *Store) runDir(id string) string { return filepath.Join(s.state, "runs", id) }

// IODir implements eval.Artifacts.
func (s *Store) IODir(_ context.Context, id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	dir := s.ioDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create io dir: %w", err)
	}
	return dir, nil
}

// Transcript implements eval.Artifacts. Lines are redacted as they complete.
func (s *Store) Transcript(_ context.Context, id string) (io.WriteCloser, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.runDir(id), 0o700); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.runDir(id), "transcript.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	return &lineRedactor{f: f, redact: s.redact}, nil
}

// SavePatch implements eval.Artifacts. Patches are stored verbatim: a redacted patch
// would no longer apply.
func (s *Store) SavePatch(_ context.Context, id string, patch []byte) error {
	if err := checkID(id); err != nil {
		return err
	}
	if err := os.MkdirAll(s.runDir(id), 0o700); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.runDir(id), "patch.diff"), patch, 0o600); err != nil {
		return fmt.Errorf("write patch: %w", err)
	}
	return nil
}

// Collect implements eval.Artifacts.
func (s *Store) Collect(_ context.Context, id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	src := s.ioDir(id)
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read io dir: %w", err)
	}
	dst := s.runDir(id)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue // arms don't write here; anything else is ignored, never followed
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), []byte(s.redact(string(b))), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("remove io dir: %w", err)
	}
	return nil
}

// lineRedactor buffers partial lines so a secret split across writes is still caught.
type lineRedactor struct {
	f      *os.File
	buf    bytes.Buffer
	redact func(string) string
}

func (w *lineRedactor) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		line := w.buf.Next(i + 1)
		if _, err := io.WriteString(w.f, w.redact(string(line))); err != nil {
			return 0, err
		}
	}
}

func (w *lineRedactor) Close() error {
	if w.buf.Len() > 0 {
		if _, err := io.WriteString(w.f, w.redact(w.buf.String())); err != nil {
			w.f.Close()
			return err
		}
		w.buf.Reset()
	}
	return w.f.Close()
}
