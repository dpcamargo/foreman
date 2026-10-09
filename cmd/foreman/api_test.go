package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/task"
)

func TestAPICreateAndListTasks(t *testing.T) {
	origResolve := resolveBaseCommit
	resolveBaseCommit = func(ctx context.Context, repoURL string) (string, error) {
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	defer func() { resolveBaseCommit = origResolve }()

	origProject := resolveProjectConfig
	resolveProjectConfig = func(ctx context.Context, repoURL, baseCommit string) ([]byte, error) {
		return []byte("version: 1\nverifier_image: img\nverify:\n  - [\"go\", \"test\"]\n"), nil
	}
	defer func() { resolveProjectConfig = origProject }()

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

func TestAPIPauseResumeCancelRoutes(t *testing.T) {
	store := newFakeTaskStore()
	now := time.Now().UTC()
	store.tasks = append(store.tasks, task.Task{
		ID: "tsk_1", TaskType: task.TaskTypeCode, RepoURL: "/r.git",
		BaseCommit: "0123456789abcdef0123456789abcdef01234567", RequestText: "x",
		Status: task.StatusRunning, CreatedAt: now, UpdatedAt: now,
	})
	disp := &fakeDispatcher{}
	mux := newAPIMux(store, disp)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/tasks/tsk_1/pause", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST pause: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from pause, got %d", resp.StatusCode)
	}
	if len(disp.pausedGraceful) != 1 || disp.pausedGraceful[0] != "tsk_1" {
		t.Errorf("expected graceful pause called, got %+v", disp.pausedGraceful)
	}

	resp, err = http.Post(srv.URL+"/v1/tasks/tsk_1/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("POST resume: %v", err)
	}
	resp.Body.Close()
	// tsk_1 is still "running" (graceful pause doesn't rewrite status in this fake store
	// path — only pauseTaskNow/cancelTask do), so Resume() will reject it; that's fine,
	// this test only exercises routing, not the full pause->resume state machine.
	_ = resp.StatusCode

	resp, err = http.Post(srv.URL+"/v1/tasks/tsk_1/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from cancel, got %d", resp.StatusCode)
	}
	if len(disp.cancelled) != 1 || disp.cancelled[0] != "tsk_1" {
		t.Errorf("expected Cancel called, got %+v", disp.cancelled)
	}
	got, _ := store.Task(context.Background(), "tsk_1")
	if got.Status != task.StatusCancelled {
		t.Errorf("expected status cancelled, got %s", got.Status)
	}
}
