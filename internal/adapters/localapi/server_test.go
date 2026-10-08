package localapi_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/localapi"
)

func TestServerServesSameUIDConnection(t *testing.T) {
	dir, err := os.MkdirTemp("", "fm-sock")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "s.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	srv, err := localapi.New(sockPath, mux)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()

	go srv.Serve()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
		Timeout: 2 * time.Second,
	}

	var resp *http.Response
	for i := 0; i < 20; i++ {
		resp, err = client.Get("http://unix/v1/ping")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /v1/ping: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "pong" {
		t.Fatalf("expected 200 pong, got %d %q", resp.StatusCode, body)
	}
}

func TestServerSocketModeIsRestricted(t *testing.T) {
	dir, err := os.MkdirTemp("", "fm-sock")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "s.sock")

	srv, err := localapi.New(sockPath, http.NewServeMux())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("expected socket mode 0700, got %o", info.Mode().Perm())
	}
}

// TestPeerUIDRejectsMismatch exercises the peer-credential check directly, since actually
// connecting as a different UID would need a second OS user (not available in CI). It
// confirms the check function used by the server rejects a UID that doesn't match the
// daemon's own, which is what FR-013 requires.
func TestPeerUIDRejectsMismatch(t *testing.T) {
	ownUID := os.Getuid()
	if localapi.AuthorizedUID(uint32(ownUID), uint32(ownUID)) != true {
		t.Error("expected the daemon's own UID to be authorized")
	}
	if localapi.AuthorizedUID(uint32(ownUID), uint32(ownUID)+1) != false {
		t.Error("expected a different UID to be rejected")
	}
}
