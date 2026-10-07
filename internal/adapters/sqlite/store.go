// Package sqlite implements the eval RecordStore on SQLite in WAL mode
// (specs/001-evaluation-harness/data-model.md). Every state change is committed in
// the same transaction as its events row (constitution VII).
package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/dpcamargo/foreman/internal/core/eval"
)

// tsLayout is RFC 3339, UTC, fixed width, so timestamps sort lexically.
const tsLayout = "2006-01-02T15:04:05.000000Z07:00"

// Store is the SQLite-backed eval store.
type Store struct {
	db       *sql.DB
	stateDir string
	now      func() time.Time
	rand     io.Reader
}

// Open opens (creating if needed) stateDir/foreman.db and applies pending migrations.
func Open(ctx context.Context, stateDir string) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	dsn := "file:" + filepath.Join(stateDir, "foreman.db") +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1) // one writer process; keeps transactions simple
	s := &Store{db: db, stateDir: stateDir, now: func() time.Time { return time.Now().UTC() }, rand: rand.Reader}
	if err := migrate(ctx, db, s.timestamp()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

func (s *Store) timestamp() string { return s.clock().Format(tsLayout) }

func (s *Store) newID(prefix string, at time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + at.Format("20060102150405") + "_" + hex.EncodeToString(b), nil
}

// withTx runs fn in a transaction, committing only if fn succeeds.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// insertEvent writes one events row inside tx.
func (s *Store) insertEvent(ctx context.Context, tx *sql.Tx, entity, id string, from *string, to string, detail any) error {
	var d *string
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("encode event detail: %w", err)
		}
		str := string(b)
		d = &str
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (entity, entity_id, from_status, to_status, detail, at) VALUES (?, ?, ?, ?, ?, ?)`,
		entity, id, from, to, d, s.timestamp()); err != nil {
		return fmt.Errorf("insert %s event: %w", entity, err)
	}
	return nil
}

// AddRecord implements eval.RecordStore.
func (s *Store) AddRecord(ctx context.Context, r eval.Record, f eval.RecordFiles) (eval.Record, error) {
	at := s.clock()
	id, err := s.newID("rec", at)
	if err != nil {
		return eval.Record{}, err
	}
	r.ID, r.CreatedAt = id, at
	dir := s.recordDir(id)
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := insertRecord(ctx, tx, r); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, "record", id, nil, "created", map[string]string{"source": string(r.Source)}); err != nil {
			return err
		}
		return writeBlobs(dir, f)
	})
	if err != nil {
		os.RemoveAll(dir)
		return eval.Record{}, fmt.Errorf("add record: %w", err)
	}
	return r, nil
}

func (s *Store) recordDir(id string) string { return filepath.Join(s.stateDir, "records", id) }

func insertRecord(ctx context.Context, tx *sql.Tx, r eval.Record) error {
	testCmd, err := argvJSON(r.TestCommand)
	if err != nil {
		return err
	}
	fullCmd, err := argvJSON(r.FullTestCommand)
	if err != nil {
		return err
	}
	var (
		outcome                 *string
		attempts, interventions *int
		tokens, liveWall        *int64
		usd                     *float64
		testFormat              *string
	)
	if r.TestFormat != "" {
		tf := string(r.TestFormat)
		testFormat = &tf
	}
	if l := r.Live; l != nil {
		o, w := string(l.Outcome), int64(l.Wall/time.Second)
		outcome, attempts, interventions, tokens, liveWall, usd = &o, &l.Attempts, &l.Interventions, &l.TokensBillable, &w, &l.USD
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records (
		id, format_version, source, task_type, category, request, repo_url, base_commit,
		test_command, test_format, full_test_command, base_full_suite_passed, wall_budget_s,
		expects_clarification, live_outcome, live_attempts, live_tokens_billable, live_usd,
		live_wall_s, live_interventions, replayable, not_replayable_reason, validated_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.FormatVersion, string(r.Source), r.TaskType, string(r.Category), r.Request, r.RepoURL, r.BaseCommit,
		testCmd, testFormat, fullCmd, boolPtrInt(r.BaseFullSuitePassed), int64(r.WallBudget/time.Second),
		boolInt(r.ExpectsClarification), outcome, attempts, tokens, usd,
		liveWall, interventions, boolPtrInt(r.Replayable), nullString(r.NotReplayableReason),
		nullTime(r.ValidatedAt), r.CreatedAt.Format(tsLayout))
	if err != nil {
		return fmt.Errorf("insert record: %w", err)
	}
	return nil
}

// writeBlobs copies the visible and hidden trees and the reference patch into dir.
func writeBlobs(dir string, f eval.RecordFiles) error {
	for _, tree := range []struct {
		name string
		fsys fs.FS
	}{{"visible", f.Visible}, {"hidden", f.Hidden}} {
		dst := filepath.Join(dir, tree.name)
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return fmt.Errorf("create %s dir: %w", tree.name, err)
		}
		if err := copyTree(tree.fsys, dst); err != nil {
			return fmt.Errorf("copy %s files: %w", tree.name, err)
		}
	}
	if f.ReferencePatch != nil {
		if err := os.WriteFile(filepath.Join(dir, "reference.patch"), f.ReferencePatch, 0o600); err != nil {
			return fmt.Errorf("write reference patch: %w", err)
		}
	}
	return nil
}

// copyTree copies every regular file of fsys under dst. ListFiles refuses symlinks and
// escaping names, so nothing can be written outside dst.
func copyTree(fsys fs.FS, dst string) error {
	names, err := eval.ListFiles(fsys)
	if err != nil {
		return err
	}
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// RecordFiles implements eval.RecordStore.
func (s *Store) RecordFiles(ctx context.Context, id string) (eval.RecordFiles, error) {
	if _, err := s.Record(ctx, id); err != nil {
		return eval.RecordFiles{}, err
	}
	dir := s.recordDir(id)
	patch, err := os.ReadFile(filepath.Join(dir, "reference.patch"))
	if errors.Is(err, fs.ErrNotExist) {
		patch, err = nil, nil
	}
	if err != nil {
		return eval.RecordFiles{}, fmt.Errorf("read reference patch: %w", err)
	}
	return eval.RecordFiles{
		Visible:        os.DirFS(filepath.Join(dir, "visible")),
		Hidden:         os.DirFS(filepath.Join(dir, "hidden")),
		ReferencePatch: patch,
	}, nil
}

const recordColumns = `id, format_version, source, task_type, category, request, repo_url, base_commit,
	test_command, test_format, full_test_command, base_full_suite_passed, wall_budget_s,
	expects_clarification, live_outcome, live_attempts, live_tokens_billable, live_usd,
	live_wall_s, live_interventions, replayable, not_replayable_reason, validated_at, created_at`

// Record implements eval.RecordStore.
func (s *Store) Record(ctx context.Context, id string) (eval.Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM records WHERE id = ?`, id)
	r, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return eval.Record{}, fmt.Errorf("record %s: %w", id, eval.ErrNotFound)
	}
	return r, err
}

// Records implements eval.RecordStore.
func (s *Store) Records(ctx context.Context, f eval.RecordFilter) ([]eval.Record, error) {
	var where []string
	var args []any
	if len(f.IDs) > 0 {
		where = append(where, "id IN (?"+strings.Repeat(", ?", len(f.IDs)-1)+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if f.ReplayableOnly {
		where = append(where, "replayable = 1")
	}
	if f.PendingOnly {
		where = append(where, "replayable IS NULL")
	}
	if f.RepoURL != "" {
		where = append(where, "repo_url = ?")
		args = append(args, f.RepoURL)
	}
	if !f.CreatedSince.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.CreatedSince.UTC().Format(tsLayout))
	}
	q := `SELECT ` + recordColumns + ` FROM records`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at, id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query records: %w", err)
	}
	defer rows.Close()
	var out []eval.Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	return out, nil
}

// SetReplayable implements eval.RecordStore.
func (s *Store) SetReplayable(ctx context.Context, id string, v eval.Validation) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current *int64
		err := tx.QueryRowContext(ctx, `SELECT replayable FROM records WHERE id = ?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("record %s: %w", id, eval.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("read record %s: %w", id, err)
		}
		reason := v.Reason
		if v.Replayable {
			reason = ""
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE records SET replayable = ?, not_replayable_reason = ?, base_full_suite_passed = ?, validated_at = ? WHERE id = ?`,
			boolInt(v.Replayable), nullString(reason), boolPtrInt(v.BaseFullSuitePassed), s.timestamp(), id); err != nil {
			return fmt.Errorf("update record %s: %w", id, err)
		}
		from := replayState(current)
		return s.insertEvent(ctx, tx, "record", id, &from, replayStateBool(v.Replayable), map[string]string{"reason": reason})
	})
}

func replayState(v *int64) string {
	switch {
	case v == nil:
		return "pending"
	case *v == 1:
		return "replayable"
	default:
		return "not_replayable"
	}
}

func replayStateBool(ok bool) string {
	if ok {
		return "replayable"
	}
	return "not_replayable"
}

type scanner interface{ Scan(dest ...any) error }

func scanRecord(sc scanner) (eval.Record, error) {
	var (
		r                                      eval.Record
		source, category                       string
		testCmd, testFormat, fullCmd           *string
		baseFull, replayable                   *int64
		wallBudget, expects                    int64
		outcome                                *string
		attempts, interventions, tokens, liveW *int64
		usd                                    *float64
		reason, validatedAt                    *string
		createdAt                              string
	)
	err := sc.Scan(&r.ID, &r.FormatVersion, &source, &r.TaskType, &category, &r.Request, &r.RepoURL, &r.BaseCommit,
		&testCmd, &testFormat, &fullCmd, &baseFull, &wallBudget,
		&expects, &outcome, &attempts, &tokens, &usd,
		&liveW, &interventions, &replayable, &reason, &validatedAt, &createdAt)
	if err != nil {
		return eval.Record{}, err
	}
	r.Source, r.Category = eval.Source(source), eval.Category(category)
	if r.TestCommand, err = parseArgv(testCmd); err != nil {
		return eval.Record{}, err
	}
	if r.FullTestCommand, err = parseArgv(fullCmd); err != nil {
		return eval.Record{}, err
	}
	if testFormat != nil {
		r.TestFormat = eval.TestFormat(*testFormat)
	}
	r.BaseFullSuitePassed = intPtrBool(baseFull)
	r.WallBudget = time.Duration(wallBudget) * time.Second
	r.ExpectsClarification = expects != 0
	if outcome != nil {
		r.Live = &eval.LiveOutcome{
			Outcome:        eval.Outcome(*outcome),
			Attempts:       int(deref(attempts)),
			TokensBillable: deref(tokens),
			USD:            derefF(usd),
			Wall:           time.Duration(deref(liveW)) * time.Second,
			Interventions:  int(deref(interventions)),
		}
	}
	r.Replayable = intPtrBool(replayable)
	if reason != nil {
		r.NotReplayableReason = *reason
	}
	if validatedAt != nil {
		if r.ValidatedAt, err = time.Parse(tsLayout, *validatedAt); err != nil {
			return eval.Record{}, fmt.Errorf("parse validated_at: %w", err)
		}
	}
	if r.CreatedAt, err = time.Parse(tsLayout, createdAt); err != nil {
		return eval.Record{}, fmt.Errorf("parse created_at: %w", err)
	}
	return r, nil
}

func argvJSON(argv []string) (*string, error) {
	if argv == nil {
		return nil, nil
	}
	b, err := json.Marshal(argv)
	if err != nil {
		return nil, fmt.Errorf("encode argv: %w", err)
	}
	s := string(b)
	return &s, nil
}

func parseArgv(s *string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(*s), &argv); err != nil {
		return nil, fmt.Errorf("decode argv: %w", err)
	}
	return argv, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolPtrInt(b *bool) *int {
	if b == nil {
		return nil
	}
	v := boolInt(*b)
	return &v
}

func intPtrBool(v *int64) *bool {
	if v == nil {
		return nil
	}
	b := *v != 0
	return &b
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(tsLayout)
	return &s
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefF(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
