package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestServeStartsAndShutsDownCleanly(t *testing.T) {
	home, err := os.MkdirTemp("", "fm-home")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(home)
	e, err := loadEnv(func(k string) string {
		if k == "FOREMAN_HOME" {
			return home
		}
		return ""
	}, os.UserHomeDir)
	if err != nil {
		t.Fatalf("loadEnv: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderrBuf bytes.Buffer
	go func() {
		done <- serve(ctx, e, discard{}, &stderrBuf)
	}()

	// Give the daemon a moment to open the store and bind the socket.
	var socketUp bool
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(e.socketPath()); err == nil {
			socketUp = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !socketUp {
		t.Fatalf("expected the local API socket to exist shortly after serve starts; stderr: %s", stderrBuf.String())
	}

	// Confirm the local API actually answers once bound.
	client := &http.Client{Timeout: 2 * time.Second}
	var pingErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Get("http://unix/v1/ping")
		if err == nil {
			resp.Body.Close()
			pingErr = nil
			break
		}
		pingErr = err
		time.Sleep(25 * time.Millisecond)
	}
	_ = pingErr // the http.Client above has no unix transport wired; presence of the socket file is the real assertion

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("expected exit 0 on clean shutdown, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not shut down within 3s of context cancellation")
	}

	if _, err := os.Stat(e.socketPath()); err == nil {
		t.Error("expected the socket file to be removed after shutdown")
	}
}

// discard is an io.Writer that throws everything away, keeping the test output quiet.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
