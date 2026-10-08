package ntfy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/ntfy"
)

// T046: one net/http POST to the configured ntfy topic URL. Never auto-resolves the
// approval itself (spec.md Assumptions: the secondary channel only pings, it doesn't
// decide).
func TestPublishSendsOnePost(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	var gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotTitle = r.Header.Get("Title")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := ntfy.New(srv.URL + "/foreman-approvals")
	if err := c.Publish(context.Background(), "approval apv_1 unanswered for 4h", "foreman"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/foreman-approvals" {
		t.Errorf("path = %q, want /foreman-approvals", gotPath)
	}
	if gotTitle != "foreman" {
		t.Errorf("Title header = %q, want foreman", gotTitle)
	}
	if !strings.Contains(gotBody, "apv_1") {
		t.Errorf("body = %q, want it to mention the approval id", gotBody)
	}
}

// T046: a failing ntfy endpoint is reported as an error, not silently swallowed — the
// caller (the housekeeping tick) decides whether to retry or log, this adapter just
// surfaces the failure.
func TestPublishSurfacesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := ntfy.New(srv.URL + "/foreman-approvals")
	if err := c.Publish(context.Background(), "msg", "foreman"); err == nil {
		t.Error("expected an error from a 500 response, got nil")
	}
}
