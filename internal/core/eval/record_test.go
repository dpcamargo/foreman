package eval

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const goodSHA = "0123456789abcdef0123456789abcdef01234567"

func validRecord() Record {
	return Record{
		Source:      SourceManual,
		TaskType:    TaskTypeCode,
		Category:    CategoryFeature,
		Request:     "Add Sub(a, b int) int",
		RepoURL:     "/tmp/addrepo.git",
		BaseCommit:  goodSHA,
		TestCommand: []string{"go", "test", "-json", "./..."},
	}
}

func hiddenFS() fstest.MapFS {
	return fstest.MapFS{"sub_test.go": {Data: []byte("package addrepo\n")}}
}

func TestValidateNew(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Record, *RecordFiles)
		wantErr string // substring; "" means valid
	}{
		{name: "well-formed record", mutate: func(*Record, *RecordFiles) {}},
		{name: "uppercase SHA is normalized", mutate: func(r *Record, _ *RecordFiles) { r.BaseCommit = strings.ToUpper(goodSHA) }},
		{name: "short SHA", mutate: func(r *Record, _ *RecordFiles) { r.BaseCommit = "abc123" }, wantErr: "base_commit"},
		{name: "non-hex SHA", mutate: func(r *Record, _ *RecordFiles) { r.BaseCommit = strings.Repeat("g", 40) }, wantErr: "base_commit"},
		{name: "empty request", mutate: func(r *Record, _ *RecordFiles) { r.Request = "  " }, wantErr: "request"},
		{name: "empty repo", mutate: func(r *Record, _ *RecordFiles) { r.RepoURL = "" }, wantErr: "repo"},
		{name: "test command with empty element", mutate: func(r *Record, _ *RecordFiles) { r.TestCommand = []string{"go", ""} }, wantErr: "test_command"},
		{name: "unknown category", mutate: func(r *Record, _ *RecordFiles) { r.Category = "chore" }, wantErr: "category"},
		{name: "unknown test format", mutate: func(r *Record, _ *RecordFiles) { r.TestFormat = "junit" }, wantErr: "test_format"},
		{name: "unknown source", mutate: func(r *Record, _ *RecordFiles) { r.Source = "elsewhere" }, wantErr: "source"},
		{name: "full test command with empty element", mutate: func(r *Record, _ *RecordFiles) { r.FullTestCommand = []string{""} }, wantErr: "full_test_command"},
		{
			name: "metrics-only record: no hidden tests, no test command",
			mutate: func(r *Record, f *RecordFiles) {
				r.TestCommand = nil
				f.Hidden = nil
			},
		},
		{
			name: "empty hidden dir without test command",
			mutate: func(r *Record, f *RecordFiles) {
				r.TestCommand = nil
				f.Hidden = fstest.MapFS{}
			},
		},
		{
			name:    "hidden tests without test command",
			mutate:  func(r *Record, _ *RecordFiles) { r.TestCommand = nil },
			wantErr: "test_command",
		},
		{
			name: "symlink in hidden",
			mutate: func(_ *Record, f *RecordFiles) {
				f.Hidden = fstest.MapFS{"x_test.go": {Data: []byte("../../outside"), Mode: fs.ModeSymlink}}
			},
			wantErr: "symlink",
		},
		{
			name: "symlink in visible",
			mutate: func(_ *Record, f *RecordFiles) {
				f.Visible = fstest.MapFS{"dir/link": {Data: []byte("/etc/passwd"), Mode: fs.ModeSymlink}}
			},
			wantErr: "symlink",
		},
		{
			name:    "dot-dot entry in hidden",
			mutate:  func(_ *Record, f *RecordFiles) { f.Hidden = badNameFS{"../x_test.go"} },
			wantErr: "escapes",
		},
		{
			name:    "absolute entry in visible",
			mutate:  func(_ *Record, f *RecordFiles) { f.Visible = badNameFS{"/abs_test.go"} },
			wantErr: "escapes",
		},
		{
			name: "invalid live outcome",
			mutate: func(r *Record, _ *RecordFiles) {
				r.Live = &LiveOutcome{Outcome: "won"}
			},
			wantErr: "live outcome",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRecord()
			f := RecordFiles{Hidden: hiddenFS()}
			tt.mutate(&r, &f)
			got, err := ValidateNew(r, f)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				if got.BaseCommit != strings.ToLower(r.BaseCommit) {
					t.Errorf("BaseCommit not normalized: %q", got.BaseCommit)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !errors.Is(err, ErrInvalidRecord) {
				t.Errorf("error %v does not wrap ErrInvalidRecord", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNewDefaults(t *testing.T) {
	r := validRecord()
	got, err := ValidateNew(r, RecordFiles{Hidden: hiddenFS()})
	if err != nil {
		t.Fatal(err)
	}
	if got.WallBudget != 1800*time.Second {
		t.Errorf("WallBudget = %v, want 30m", got.WallBudget)
	}
	if got.TaskType != TaskTypeCode {
		t.Errorf("TaskType = %q, want %q", got.TaskType, TaskTypeCode)
	}
	if got.TestFormat != FormatGoTestJSON {
		t.Errorf("TestFormat = %q, want %q", got.TestFormat, FormatGoTestJSON)
	}
	if got.FormatVersion != 1 {
		t.Errorf("FormatVersion = %d, want 1", got.FormatVersion)
	}

	noTests := validRecord()
	noTests.TestCommand = nil
	got, err = ValidateNew(noTests, RecordFiles{})
	if err != nil {
		t.Fatal(err)
	}
	if got.TestFormat != "" {
		t.Errorf("metrics-only record got TestFormat %q, want empty", got.TestFormat)
	}
}

func TestListFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"b_test.go":     {Data: []byte("b")},
		"pkg/a_test.go": {Data: []byte("a")},
		"pkg/empty":     {Mode: fs.ModeDir},
	}
	got, err := ListFiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if want := "b_test.go,pkg/a_test.go"; strings.Join(got, ",") != want {
		t.Errorf("ListFiles = %v, want %s", got, want)
	}
	if got, err := ListFiles(nil); err != nil || len(got) != 0 {
		t.Errorf("ListFiles(nil) = %v, %v; want empty, nil", got, err)
	}
}

// badNameFS is a one-entry directory whose entry name is unsafe. Real fs.FS
// implementations never produce such names; a hostile or buggy one could.
type badNameFS []string

func (b badNameFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &badDir{}, nil
	}
	return nil, fs.ErrNotExist
}

func (b badNameFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}
	var out []fs.DirEntry
	for _, n := range b {
		out = append(out, badEntry(n))
	}
	return out, nil
}

type badDir struct{}

func (*badDir) Stat() (fs.FileInfo, error) { return badInfo("."), nil }
func (*badDir) Read([]byte) (int, error)   { return 0, errors.New("is a directory") }
func (*badDir) Close() error               { return nil }

type badEntry string

func (e badEntry) Name() string               { return string(e) }
func (e badEntry) IsDir() bool                { return false }
func (e badEntry) Type() fs.FileMode          { return 0 }
func (e badEntry) Info() (fs.FileInfo, error) { return badInfo(e), nil }

type badInfo string

func (i badInfo) Name() string       { return string(i) }
func (i badInfo) Size() int64        { return 0 }
func (i badInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (i badInfo) ModTime() time.Time { return time.Time{} }
func (i badInfo) IsDir() bool        { return i == "." }
func (i badInfo) Sys() any           { return nil }
