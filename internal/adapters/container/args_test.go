package container

import (
	"slices"
	"strings"
	"testing"
)

// TestFetchArgs pins the dependency-fetch container's isolation (research R5; plan.md
// Complexity Tracking): checksum-verified proxy, read-only module mode, and nothing
// mounted but the clone (read-only) and the module volume.
func TestFetchArgs(t *testing.T) {
	g := New(Config{Image: "img@sha256:x", DockerContext: "foreman"})
	args := g.fetchArgs("/work/ws", "fm-gomod-abc")

	var envs, mounts []string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-e":
			envs = append(envs, args[i+1])
		case "-v":
			mounts = append(mounts, args[i+1])
		}
	}
	slices.Sort(envs)
	wantEnv := []string{
		"GOCACHE=/tmp/gocache", "GOFLAGS=-mod=readonly", "GOMODCACHE=/go/pkg/mod",
		"GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOTOOLCHAIN=local",
	}
	if !slices.Equal(envs, wantEnv) {
		t.Errorf("env = %v\nwant  %v", envs, wantEnv)
	}
	wantMounts := []string{"/work/ws:/src:ro", "fm-gomod-abc:/go/pkg/mod"}
	if !slices.Equal(mounts, wantMounts) {
		t.Errorf("mounts = %v, want %v", mounts, wantMounts)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--context foreman", "run --rm", "--pull=never", "--cap-drop=ALL", "img@sha256:x go mod download"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	for _, bad := range []string{"GONOSUMDB", "GOPRIVATE", "GOINSECURE", "--network=host"} {
		if strings.Contains(joined, bad) {
			t.Errorf("args contain %q: %s", bad, joined)
		}
	}
}

// TestTestStageArgs pins the offline test container's hardening flags.
func TestTestStageArgs(t *testing.T) {
	g := New(Config{Image: "img"})
	args := g.testArgs("fm-grade-1", "/work/ws", "/work/hidden", "fm-gomod-abc", true, []string{"go", "test", "./..."})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--network=none", "--read-only", "--tmpfs /tmp:rw,exec,nosuid,size=1g", "--cap-drop=ALL",
		"--security-opt no-new-privileges", "--pids-limit 512", "--memory 2g", "--cpus 2", "--pull=never",
		"-v /work/ws:/src", "-v /work/hidden:/hidden:ro", "-v fm-gomod-abc:/go/pkg/mod:ro",
		"-e GOPROXY=off", "-e GOFLAGS=-mod=readonly",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q:\n%s", want, joined)
		}
	}
	noHidden := strings.Join(g.testArgs("n", "/work/ws", "", "vol", false, []string{"go", "test"}), " ")
	if strings.Contains(noHidden, "/hidden") {
		t.Errorf("full-suite stage must not mount hidden tests: %s", noHidden)
	}
}
