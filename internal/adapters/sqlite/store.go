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

// --- runs (US2) ---

const runColumns = `id, record_id, arm, arm_version, rep, base_commit, status, claimed,
	tokens_input, tokens_cached, tokens_cache_write, tokens_output, tokens_reasoning,
	usd, cost_source, wall_s, pid, pid_started_at, error, started_at, ended_at`

// StartRun implements eval.RunStore.
func (s *Store) StartRun(ctx context.Context, key eval.RunKey, pid eval.PIDFingerprint) (eval.ReplayRun, bool, error) {
	var out eval.ReplayRun
	var reused bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM replay_runs
			WHERE record_id = ? AND arm = ? AND arm_version = ? AND rep = ? AND base_commit = ?`,
			key.RecordID, key.Arm, key.ArmVersion, key.Rep, key.Base)
		existing, err := scanRun(row)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return s.insertRun(ctx, tx, key, pid, &out)
		case err != nil:
			return fmt.Errorf("read run: %w", err)
		case existing.Status == eval.StatusRunning:
			return fmt.Errorf("run %s: %w", existing.ID, eval.ErrRunInProgress)
		case existing.Status == eval.StatusInfraError:
			return s.restartRun(ctx, tx, existing, pid, &out)
		default:
			out, reused = existing, true
			return nil
		}
	})
	if err != nil {
		return eval.ReplayRun{}, false, err
	}
	return out, reused, nil
}

func (s *Store) insertRun(ctx context.Context, tx *sql.Tx, key eval.RunKey, pid eval.PIDFingerprint, out *eval.ReplayRun) error {
	at := s.clock()
	id, err := s.newID("run", at)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO replay_runs
		(id, record_id, arm, arm_version, rep, base_commit, status, pid, pid_started_at, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, key.RecordID, key.Arm, key.ArmVersion, key.Rep, key.Base, string(eval.StatusRunning),
		pid.PID, pid.StartedAt, at.Format(tsLayout)); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	if err := s.insertEvent(ctx, tx, "run", id, nil, string(eval.StatusRunning), map[string]any{"arm": key.Arm, "rep": key.Rep}); err != nil {
		return err
	}
	*out = eval.ReplayRun{ID: id, Key: key, Status: eval.StatusRunning, PID: pid, StartedAt: at}
	return nil
}

// restartRun resets an infra_error row for a retry.
func (s *Store) restartRun(ctx context.Context, tx *sql.Tx, r eval.ReplayRun, pid eval.PIDFingerprint, out *eval.ReplayRun) error {
	at := s.clock()
	if _, err := tx.ExecContext(ctx, `UPDATE replay_runs SET status = ?, claimed = NULL,
		tokens_input = NULL, tokens_cached = NULL, tokens_cache_write = NULL, tokens_output = NULL,
		tokens_reasoning = NULL, usd = NULL, cost_source = NULL, wall_s = NULL,
		pid = ?, pid_started_at = ?, error = NULL, started_at = ?, ended_at = NULL WHERE id = ?`,
		string(eval.StatusRunning), pid.PID, pid.StartedAt, at.Format(tsLayout), r.ID); err != nil {
		return fmt.Errorf("restart run %s: %w", r.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM grades WHERE run_id = ?`, r.ID); err != nil {
		return fmt.Errorf("clear grade of %s: %w", r.ID, err)
	}
	from := string(eval.StatusInfraError)
	if err := s.insertEvent(ctx, tx, "run", r.ID, &from, string(eval.StatusRunning), map[string]string{"retry_of": r.Error}); err != nil {
		return err
	}
	*out = eval.ReplayRun{ID: r.ID, Key: r.Key, Status: eval.StatusRunning, PID: pid, StartedAt: at}
	return nil
}

// FinishRun implements eval.RunStore.
func (s *Store) FinishRun(ctx context.Context, r eval.ReplayRun, g *eval.Grade) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		ended := r.EndedAt
		if ended.IsZero() {
			ended = s.clock()
		}
		u := r.Usage
		res, err := tx.ExecContext(ctx, `UPDATE replay_runs SET status = ?, claimed = ?,
			tokens_input = ?, tokens_cached = ?, tokens_cache_write = ?, tokens_output = ?, tokens_reasoning = ?,
			tokens_billable = ?, usd = ?, cost_source = ?, wall_s = ?, error = ?, ended_at = ?
			WHERE id = ? AND status = ?`,
			string(r.Status), nullString(string(r.Claimed)),
			u.Input, u.Cached, u.CacheWrite, u.Output, u.Reasoning, u.Billable(), u.USD, nullString(u.CostSource),
			r.Wall.Seconds(), nullString(r.Error), ended.UTC().Format(tsLayout),
			r.ID, string(eval.StatusRunning))
		if err != nil {
			return fmt.Errorf("finish run %s: %w", r.ID, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("finish run %s: not running", r.ID)
		}
		if g != nil {
			if err := insertGrade(ctx, tx, r.ID, *g, s.timestamp()); err != nil {
				return err
			}
		}
		from := string(eval.StatusRunning)
		return s.insertEvent(ctx, tx, "run", r.ID, &from, string(r.Status), map[string]string{"error": r.Error})
	})
}

func insertGrade(ctx context.Context, tx *sql.Tx, runID string, g eval.Grade, at string) error {
	perTest, err := json.Marshal(g.PerTest)
	if err != nil {
		return fmt.Errorf("encode per-test results: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO grades
		(run_id, hidden_passed, hidden_failed, per_test, full_suite_regressed, contaminated, success, false_success, graded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, g.HiddenPassed, g.HiddenFailed, string(perTest), boolPtrInt(g.FullSuiteRegressed),
		boolInt(g.Contaminated), boolInt(g.Success), boolInt(g.FalseSuccess), at); err != nil {
		return fmt.Errorf("insert grade: %w", err)
	}
	return nil
}

// RunGrade implements eval.RunStore. A run without a grade returns nil, nil.
func (s *Store) RunGrade(ctx context.Context, runID string) (*eval.Grade, error) {
	var (
		g                                eval.Grade
		perTest                          string
		regressed                        *int64
		contaminated, success, falseSucc int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT run_id, hidden_passed, hidden_failed, per_test, full_suite_regressed,
		contaminated, success, false_success FROM grades WHERE run_id = ?`, runID).
		Scan(&g.RunID, &g.HiddenPassed, &g.HiddenFailed, &perTest, &regressed, &contaminated, &success, &falseSucc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read grade of %s: %w", runID, err)
	}
	if err := json.Unmarshal([]byte(perTest), &g.PerTest); err != nil {
		return nil, fmt.Errorf("decode per-test results: %w", err)
	}
	g.FullSuiteRegressed = intPtrBool(regressed)
	g.Contaminated, g.Success, g.FalseSuccess = contaminated != 0, success != 0, falseSucc != 0
	return &g, nil
}

// SweepLostRuns implements eval.RunStore.
func (s *Store) SweepLostRuns(ctx context.Context, alive func(eval.PIDFingerprint) bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, coalesce(pid, 0), coalesce(pid_started_at, '') FROM replay_runs WHERE status = ?`,
		string(eval.StatusRunning))
	if err != nil {
		return 0, fmt.Errorf("list running runs: %w", err)
	}
	type candidate struct {
		id  string
		pid eval.PIDFingerprint
	}
	var lost []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.pid.PID, &c.pid.StartedAt); err != nil {
			rows.Close()
			return 0, fmt.Errorf("read running run: %w", err)
		}
		if !alive(c.pid) {
			lost = append(lost, c)
		}
	}
	rows.Close()
	for _, c := range lost {
		err := s.withTx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE replay_runs SET status = ?, error = 'lost', ended_at = ? WHERE id = ? AND status = ?`,
				string(eval.StatusInfraError), s.timestamp(), c.id, string(eval.StatusRunning))
			if err != nil {
				return fmt.Errorf("mark %s lost: %w", c.id, err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return nil // finished meanwhile
			}
			from := string(eval.StatusRunning)
			return s.insertEvent(ctx, tx, "run", c.id, &from, string(eval.StatusInfraError), map[string]any{"error": "lost", "pid": c.pid.PID})
		})
		if err != nil {
			return 0, err
		}
	}
	return len(lost), nil
}

func scanRun(sc scanner) (eval.ReplayRun, error) {
	var (
		r                                            eval.ReplayRun
		status                                       string
		claimed, costSource, pidStarted, errMsg, end *string
		in, cached, cw, outT, reasoning, pid         *int64
		usd, wall                                    *float64
		started                                      string
	)
	err := sc.Scan(&r.ID, &r.Key.RecordID, &r.Key.Arm, &r.Key.ArmVersion, &r.Key.Rep, &r.Key.Base, &status, &claimed,
		&in, &cached, &cw, &outT, &reasoning, &usd, &costSource, &wall, &pid, &pidStarted, &errMsg, &started, &end)
	if err != nil {
		return eval.ReplayRun{}, err
	}
	r.Status = eval.RunStatus(status)
	if claimed != nil {
		r.Claimed = eval.Claim(*claimed)
	}
	r.Usage = eval.Usage{Input: deref(in), Cached: deref(cached), CacheWrite: deref(cw), Output: deref(outT), Reasoning: deref(reasoning), USD: usd}
	if costSource != nil {
		r.Usage.CostSource = *costSource
	}
	if wall != nil {
		r.Wall = time.Duration(*wall * float64(time.Second))
	}
	r.PID.PID = int(deref(pid))
	if pidStarted != nil {
		r.PID.StartedAt = *pidStarted
	}
	if errMsg != nil {
		r.Error = *errMsg
	}
	if r.StartedAt, err = time.Parse(tsLayout, started); err != nil {
		return eval.ReplayRun{}, fmt.Errorf("parse started_at: %w", err)
	}
	if end != nil {
		if r.EndedAt, err = time.Parse(tsLayout, *end); err != nil {
			return eval.ReplayRun{}, fmt.Errorf("parse ended_at: %w", err)
		}
	}
	return r, nil
}
