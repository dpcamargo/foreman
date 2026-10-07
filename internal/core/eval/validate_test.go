package eval

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/fstest"
)

// memStore is an in-memory RecordStore for core tests.
type memStore struct {
	records map[string]Record
	files   map[string]RecordFiles
	order   []string
}

func newMemStore() *memStore {
	return &memStore{records: map[string]Record{}, files: map[string]RecordFiles{}}
}

func (m *memStore) AddRecord(_ context.Context, r Record, f RecordFiles) (Record, error) {
	r.ID = fmt.Sprintf("rec_%d", len(m.order)+1)
	m.records[r.ID], m.files[r.ID] = r, f
	m.order = append(m.order, r.ID)
	return r, nil
}

func (m *memStore) Record(_ context.Context, id string) (Record, error) {
	r, ok := m.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

func (m *memStore) Records(_ context.Context, f RecordFilter) ([]Record, error) {
	var out []Record
	for _, id := range m.order {
		r := m.records[id]
		if len(f.IDs) > 0 && !slices.Contains(f.IDs, id) {
			continue
		}
		if f.PendingOnly && r.Replayable != nil {
			continue
		}
		if f.ReplayableOnly && (r.Replayable == nil || !*r.Replayable) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *memStore) RecordFiles(_ context.Context, id string) (RecordFiles, error) {
	return m.files[id], nil
}

func (m *memStore) SetReplayable(_ context.Context, id string, v Validation) error {
	r := m.records[id]
	ok := v.Replayable
	r.Replayable, r.NotReplayableReason, r.BaseFullSuitePassed = &ok, v.Reason, v.BaseFullSuitePassed
	m.records[id] = r
	return nil
}

// fakeGrader answers base grades (no patch) and reference grades (with a patch).
type fakeGrader struct {
	base, ref       GradeOutput
	baseErr, refErr error
	calls           []GradeInput
}

func (g *fakeGrader) Grade(_ context.Context, in GradeInput) (GradeOutput, error) {
	g.calls = append(g.calls, in)
	if in.Patch == nil {
		return g.base, g.baseErr
	}
	return g.ref, g.refErr
}

func boolp(b bool) *bool { return &b }

var (
	failsOnBase = GradeOutput{HiddenPassed: 0, HiddenFailed: 1}
	passesAll   = GradeOutput{HiddenPassed: 1, HiddenFailed: 0}
	withTests   = RecordFiles{Hidden: fstest.MapFS{"sub_test.go": {Data: []byte("package p")}}}
)

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name       string
		record     func(*Record)
		files      RecordFiles
		grader     fakeGrader
		wantOK     bool
		wantReason string
		wantErr    bool // infrastructure error: record stays pending
		wantGrades int
	}{
		{name: "0: no acceptance tests", files: RecordFiles{}, wantReason: "no acceptance tests"},
		{name: "0: empty hidden dir", files: RecordFiles{Hidden: fstest.MapFS{}}, wantReason: "no acceptance tests"},
		{name: "1: unsupported task type", record: func(r *Record) { r.TaskType = "research" }, files: withTests,
			wantReason: "no verifier for task type research"},
		{name: "2: base not fetchable", files: withTests, grader: fakeGrader{baseErr: fmt.Errorf("clone: %w", ErrBaseNotFound)},
			wantReason: "base commit not fetchable", wantGrades: 1},
		{name: "3: hidden tests pass on base", files: withTests, grader: fakeGrader{base: passesAll},
			wantReason: "hidden tests pass on base", wantGrades: 1},
		{name: "3: nothing ran on base", files: withTests, grader: fakeGrader{base: GradeOutput{}},
			wantReason: "hidden tests pass on base", wantGrades: 1},
		{name: "4: reference does not apply",
			files:      RecordFiles{Hidden: withTests.Hidden, ReferencePatch: []byte("p")},
			grader:     fakeGrader{base: failsOnBase, ref: GradeOutput{ApplyError: "patch does not apply"}},
			wantReason: "reference patch does not apply", wantGrades: 2},
		{name: "4: reference fails hidden tests",
			files:      RecordFiles{Hidden: withTests.Hidden, ReferencePatch: []byte("p")},
			grader:     fakeGrader{base: failsOnBase, ref: GradeOutput{HiddenPassed: 1, HiddenFailed: 1}},
			wantReason: "reference patch fails hidden tests", wantGrades: 2},
		{name: "replayable with reference",
			files:  RecordFiles{Hidden: withTests.Hidden, ReferencePatch: []byte("p")},
			grader: fakeGrader{base: failsOnBase, ref: passesAll}, wantOK: true, wantGrades: 2},
		{name: "replayable without reference", files: withTests, grader: fakeGrader{base: failsOnBase},
			wantOK: true, wantGrades: 1},
		{name: "infra error leaves the record pending", files: withTests,
			grader: fakeGrader{baseErr: errors.New("docker: daemon not running")}, wantErr: true, wantGrades: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMemStore()
			r := validRecord()
			r.TaskType = TaskTypeCode
			if tt.record != nil {
				tt.record(&r)
			}
			added, _ := store.AddRecord(ctx, r, tt.files)
			g := tt.grader
			res, err := Validator{Store: store, Grader: &g}.Validate(ctx, []string{added.ID}, ValidateIDs)
			if err != nil {
				t.Fatal(err)
			}
			if len(res) != 1 {
				t.Fatalf("got %d results, want 1", len(res))
			}
			got := store.records[added.ID]
			if tt.wantErr {
				if res[0].Err == nil {
					t.Error("want an infrastructure error")
				}
				if got.Replayable != nil {
					t.Error("record must stay pending after an infrastructure error")
				}
			} else {
				if res[0].Err != nil {
					t.Fatalf("unexpected error: %v", res[0].Err)
				}
				if got.Replayable == nil || *got.Replayable != tt.wantOK || got.NotReplayableReason != tt.wantReason {
					t.Errorf("replayable = %v, reason = %q; want %v, %q", got.Replayable, got.NotReplayableReason, tt.wantOK, tt.wantReason)
				}
			}
			if len(g.calls) != tt.wantGrades {
				t.Errorf("grader called %d times, want %d", len(g.calls), tt.wantGrades)
			}
		})
	}
}

func TestValidatePassesRecordToGrader(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	r := validRecord()
	r.FullTestCommand = []string{"go", "test", "./..."}
	files := RecordFiles{
		Visible:        fstest.MapFS{"v_test.go": {Data: []byte("v")}},
		Hidden:         withTests.Hidden,
		ReferencePatch: []byte("diff"),
	}
	added, _ := store.AddRecord(ctx, r, files)
	g := &fakeGrader{base: GradeOutput{HiddenFailed: 1, FullSuitePassed: boolp(true)}, ref: passesAll}
	if _, err := (Validator{Store: store, Grader: g}).Validate(ctx, []string{added.ID}, ValidateIDs); err != nil {
		t.Fatal(err)
	}
	base := g.calls[0]
	if base.RepoURL != r.RepoURL || base.Base != r.BaseCommit || base.Patch != nil ||
		!slices.Equal(base.TestCommand, r.TestCommand) || base.Visible == nil || base.Hidden == nil ||
		!slices.Equal(base.FullTest, r.FullTestCommand) || base.Timeout <= 0 {
		t.Errorf("base grade input wrong: %+v", base)
	}
	if string(g.calls[1].Patch) != "diff" {
		t.Errorf("reference grade got patch %q", g.calls[1].Patch)
	}
	got := store.records[added.ID]
	if got.BaseFullSuitePassed == nil || !*got.BaseFullSuitePassed {
		t.Errorf("BaseFullSuitePassed = %v, want true", got.BaseFullSuitePassed)
	}
}

func TestValidateModes(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	a, _ := store.AddRecord(ctx, validRecord(), withTests)
	b, _ := store.AddRecord(ctx, validRecord(), withTests)
	store.SetReplayable(ctx, a.ID, Validation{Replayable: true})

	g := &fakeGrader{base: failsOnBase}
	res, err := Validator{Store: store, Grader: g}.Validate(ctx, nil, ValidatePending)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != b.ID {
		t.Errorf("pending validated %v, want only %s", res, b.ID)
	}

	g.calls = nil
	res, err = Validator{Store: store, Grader: g}.Validate(ctx, nil, ValidateAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Errorf("all validated %d records, want 2", len(res))
	}

	if _, err := (Validator{Store: store, Grader: g}).Validate(ctx, []string{"rec_nope"}, ValidateIDs); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}
