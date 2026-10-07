package sandbox

import (
	"os"
	"syscall"
	"testing"
)

func linkCount(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("link count not available on this platform")
	}
	return uint64(st.Nlink)
}
