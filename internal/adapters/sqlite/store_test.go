package sqlite

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dpcamargo/foreman/internal/core/eval"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr[T any](v T) *T { return &v }

func fullRecord() eval.Record {
	return eval.Record{
		FormatVersion:        1,
		Source:               eval.SourceManual,
		TaskType:             eval.TaskTypeCode,
		Category:             eval.CategoryBugFix,
		Request:              "Fix the off-by-one in Sum",
		RepoURL:              "/repos/addrepo.git",
		BaseCommit:           "0123456789abcdef0123456789abcdef01234567",
		TestCommand:          []string{"go", "test", "-json", "./..."},
		TestFormat:           eval.FormatGoTestJSON,
		FullTestCommand:      []string{"go", "test", "./..."},
		WallBudget:           25 * time.Minute,
		ExpectsClarification: true,
		Live: &eval.LiveOutcome{
			Outcome: eval.OutcomeRescued, Attempts: 3, TokensBillable: 123456,
			USD: 1.25, Wall: 7 * time.Minute, Interventions: 1,
		},
	}
}

func files() eval.RecordFiles {
	return eval.RecordFiles{
		Visible:        fstest.MapFS{"visible_test.go": {Data: []byte("package v\n")}},
		Hidden:         fstest.MapFS{"pkg/hidden_test.go": {Data: []byte("package h\n")}},
		ReferencePatch: []byte("diff --git a/x b/x\n"),
	}
}

var recordID = regexp.MustCompile(`^rec_\d{14}_[0-9a-f]{8}$`)

func TestAddRecordRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	in := fullRecord()

	got, err := s.AddRecord(ctx, in, files())
	if err != nil {
		t.Fatal(err)
	}
	if !recordID.MatchString(got.ID) {
		t.Errorf("id %q does not match %s", got.ID, recordID)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}

	read, err := s.Record(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := in
	want.ID, want.CreatedAt = got.ID, got.CreatedAt
	if !reflect.DeepEqual(read, want) {
		t.Errorf("round trip mismatch\n got %+v\nwant %+v", read, want)
	}

	// Blobs are copied under state/records/<id>/.
	dir := filepath.Join(s.stateDir, "records", got.ID)
	for _, p := range []string{"visible/visible_test.go", "hidden/pkg/hidden_test.go", "reference.patch"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("blob %s: %v", p, err)
		}
	}

	rf, err := s.RecordFiles(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := fs.ReadFile(rf.Hidden, "pkg/hidden_test.go"); err != nil || string(b) != "package h\n" {
		t.Errorf("hidden blob = %q, %v", b, err)
	}
	if string(rf.ReferencePatch) != "diff --git a/x b/x\n" {
		t.Errorf("reference patch = %q", rf.ReferencePatch)
	}
}

func TestAddRecordMetricsOnly(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	r := fullRecord()
	r.TestCommand, r.TestFormat, r.FullTestCommand, r.Live = nil, "", nil, nil
	got, err := s.AddRecord(ctx, r, eval.RecordFiles{})
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.Record(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.TestCommand != nil || read.Live != nil || read.TestFormat != "" {
		t.Errorf("metrics-only record read back with %+v", read)
	}
	rf, err := s.RecordFiles(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names, err := eval.ListFiles(rf.Hidden); err != nil || len(names) != 0 {
		t.Errorf("hidden = %v, %v; want empty", names, err)
	}
	if rf.ReferencePatch != nil {
		t.Errorf("reference patch = %q, want nil", rf.ReferencePatch)
	}
}

func TestRecordNotFound(t *testing.T) {
	s := openTest(t)
	if _, err := s.Record(context.Background(), "rec_nope"); !errors.Is(err, eval.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRecordsFilter(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { clock = clock.Add(time.Hour); return clock }

	add := func(repo string) string {
		r := fullRecord()
		r.RepoURL = repo
		got, err := s.AddRecord(ctx, r, files())
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	a, b, c := add("/r/one.git"), add("/r/two.git"), add("/r/one.git")
	if err := s.SetReplayable(ctx, a, eval.Validation{Replayable: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReplayable(ctx, b, eval.Validation{Replayable: false, Reason: "no acceptance tests"}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		filter eval.RecordFilter
		want   []string
	}{
		{"all, oldest first", eval.RecordFilter{}, []string{a, b, c}},
		{"replayable only", eval.RecordFilter{ReplayableOnly: true}, []string{a}},
		{"pending only", eval.RecordFilter{PendingOnly: true}, []string{c}},
		{"by repo", eval.RecordFilter{RepoURL: "/r/one.git"}, []string{a, c}},
		{"created since", eval.RecordFilter{CreatedSince: time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)}, []string{b, c}},
		{"by ids", eval.RecordFilter{IDs: []string{c, a}}, []string{a, c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Records(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestSetReplayableOnlyTouchesValidationColumns(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	added, err := s.AddRecord(ctx, fullRecord(), files())
	if err != nil {
		t.Fatal(err)
	}
	v := eval.Validation{Replayable: false, Reason: "hidden tests pass on base", BaseFullSuitePassed: ptr(true)}
	if err := s.SetReplayable(ctx, added.ID, v); err != nil {
		t.Fatal(err)
	}
	got, err := s.Record(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Replayable == nil || *got.Replayable || got.NotReplayableReason != v.Reason {
		t.Errorf("replayable = %v, reason = %q", got.Replayable, got.NotReplayableReason)
	}
	if got.BaseFullSuitePassed == nil || !*got.BaseFullSuitePassed {
		t.Errorf("BaseFullSuitePassed = %v, want true", got.BaseFullSuitePassed)
	}
	if got.ValidatedAt.IsZero() {
		t.Error("ValidatedAt not set")
	}
	// Everything else is unchanged.
	got.Replayable, got.NotReplayableReason, got.BaseFullSuitePassed, got.ValidatedAt = nil, "", nil, time.Time{}
	if !reflect.DeepEqual(got, added) {
		t.Errorf("other columns changed\n got %+v\nwant %+v", got, added)
	}
	if err := s.SetReplayable(ctx, "rec_nope", v); !errors.Is(err, eval.ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestEventsWrittenWithChanges(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	added, err := s.AddRecord(ctx, fullRecord(), files())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetReplayable(ctx, added.ID, eval.Validation{Replayable: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entity, from_status, to_status FROM events WHERE entity_id = ? ORDER BY id`, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var entity, to string
		var from *string
		if err := rows.Scan(&entity, &from, &to); err != nil {
			t.Fatal(err)
		}
		f := "nil"
		if from != nil {
			f = *from
		}
		got = append(got, entity+":"+f+"->"+to)
	}
	want := []string{"record:nil->created", "record:pending->replayable"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// A failing event insert must roll back the change it describes (constitution VII).
func TestEventFailureRollsBack(t *testing.T) {
	ctx := context.Background()

	t.Run("AddRecord", func(t *testing.T) {
		s := openTest(t)
		if _, err := s.db.ExecContext(ctx, `DROP TABLE events`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRecord(ctx, fullRecord(), files()); err == nil {
			t.Fatal("AddRecord succeeded without an events table")
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM records`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("records = %d after failed AddRecord, want 0", n)
		}
		entries, _ := os.ReadDir(filepath.Join(s.stateDir, "records"))
		if len(entries) != 0 {
			t.Errorf("blob dirs left behind: %v", entries)
		}
	})

	t.Run("SetReplayable", func(t *testing.T) {
		s := openTest(t)
		added, err := s.AddRecord(ctx, fullRecord(), files())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `DROP TABLE events`); err != nil {
			t.Fatal(err)
		}
		if err := s.SetReplayable(ctx, added.ID, eval.Validation{Replayable: true}); err == nil {
			t.Fatal("SetReplayable succeeded without an events table")
		}
		got, err := s.Record(ctx, added.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Replayable != nil {
			t.Errorf("replayable = %v after failed SetReplayable, want nil", *got.Replayable)
		}
	})
}

func TestPragmas(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, dir)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("open #%d: schema_migrations has %d rows, want 1", i+1, n)
		}
		s.Close()
	}
}
