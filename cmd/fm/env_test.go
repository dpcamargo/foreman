package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var nowForTest = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func TestLoadEnv(t *testing.T) {
	home := func() (string, error) { return "/Users/op", nil }
	tests := []struct {
		name    string
		vars    map[string]string
		want    env
		wantErr bool
	}{
		{
			name: "defaults",
			vars: map[string]string{},
			want: env{
				Home:        "/Users/op/.foreman",
				State:       "/Users/op/.foreman/state",
				Work:        "/Users/op/.foreman/work",
				HermesHome:  "/Users/op/.hermes/profiles/foreman-arm",
				GraderImage: defaultGraderImage,
			},
		},
		{
			name: "overrides",
			vars: map[string]string{
				"FOREMAN_HOME":           "/srv/fm/",
				"FOREMAN_DOCKER_CONTEXT": "foreman",
				"FOREMAN_HERMES_HOME":    "/srv/hermes",
				"FOREMAN_GRADER_IMAGE":   "golang:x",
			},
			want: env{
				Home:          "/srv/fm",
				State:         "/srv/fm/state",
				Work:          "/srv/fm/work",
				DockerContext: "foreman",
				HermesHome:    "/srv/hermes",
				GraderImage:   "golang:x",
			},
		},
		{name: "relative FOREMAN_HOME is rejected", vars: map[string]string{"FOREMAN_HOME": "fm"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadEnv(func(k string) string { return tt.vars[k] }, home)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestEnsureDirs(t *testing.T) {
	root := t.TempDir()
	e := env{State: filepath.Join(root, "state"), Work: filepath.Join(root, "work")}
	if err := os.MkdirAll(e.State, 0o755); err != nil { // pre-existing, too open
		t.Fatal(err)
	}
	if err := e.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]os.FileMode{e.State: 0o700, e.Work: 0o755} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", dir, got, want)
		}
	}
}
