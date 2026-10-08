package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestAPICreateAndListTasks(t *testing.T) {
	origResolve := resolveBaseCommit
	resolveBaseCommit = func(ctx context.Context, repoURL string) (string, error) {
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	defer func() { resolveBaseCommit = origResolve }()

	store := newFakeTaskStore()
	disp := &fakeDispatcher{}
	mux := newAPIMux(store, disp)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"repo": "/r.git", "request": "Add Sub"})
	resp, err := http.Post(srv.URL+"/v1/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/tasks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created taskAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Status != string(task.StatusPending) {
		t.Fatalf("unexpected created task: %+v", created)
	}

	getResp, err := http.Get(srv.URL + "/v1/tasks/" + created.ID)
	if err != nil {
		t.Fatalf("GET /v1/tasks/{id}: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}

	listResp, err := http.Get(srv.URL + "/v1/tasks")
	if err != nil {
		t.Fatalf("GET /v1/tasks: %v", err)
	}
	defer listResp.Body.Close()
	var list []taskAPIResponse
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected the created task in the list, got %+v", list)
	}
}

func TestAPIGetMissingTaskReturns404(t *testing.T) {
	store := newFakeTaskStore()
	mux := newAPIMux(store, &fakeDispatcher{})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/tasks/tsk_missing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
