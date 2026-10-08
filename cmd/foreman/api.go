package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dpcamargo/foreman/internal/core/task"
)

// taskAPIResponse is the JSON shape returned for one task (contracts/local-api.md).
type taskAPIResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	RepoURL     string `json:"repo_url"`
	RequestText string `json:"request_text"`
}

func toTaskAPIResponse(t task.Task) taskAPIResponse {
	return taskAPIResponse{ID: t.ID, Status: string(t.Status), RepoURL: t.RepoURL, RequestText: t.RequestText}
}

// newAPIMux builds the local API's HTTP routes (contracts/local-api.md): the same
// create/status/list semantics /task, /status, and /tasks give Telegram, for the local `fm`
// CLI talking over the unix socket.
func newAPIMux(store fullStore, sup dispatcherApprovals) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	mux.HandleFunc("/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Repo    string `json:"repo"`
				Request string `json:"request"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			tsk, err := createTask(r.Context(), store, sup, body.Repo, body.Request)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, toTaskAPIResponse(tsk))
		case http.MethodGet:
			tasks, err := store.RecentTasks(r.Context(), 50)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			tasks = nonTerminalTasks(tasks)
			out := make([]taskAPIResponse, len(tasks))
			for i, t := range tasks {
				out[i] = toTaskAPIResponse(t)
			}
			writeJSON(w, http.StatusOK, out)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/v1/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
		if id, ok := strings.CutSuffix(path, "/pause"); ok && r.Method == http.MethodPost {
			var body struct {
				Now bool `json:"now"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body) // an empty body means now=false
			var err error
			if body.Now {
				err = pauseTaskNow(r.Context(), store, sup, id)
			} else {
				pauseTaskGraceful(sup, id)
			}
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "pausing"})
			return
		}
		if id, ok := strings.CutSuffix(path, "/resume"); ok && r.Method == http.MethodPost {
			if err := resumeTask(r.Context(), store, sup, id); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
			return
		}
		if id, ok := strings.CutSuffix(path, "/cancel"); ok && r.Method == http.MethodPost {
			if err := cancelTask(r.Context(), store, sup, id); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
			return
		}

		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		id := path
		if id == "" {
			writeJSONError(w, http.StatusNotFound, "task id required")
			return
		}
		tsk, err := store.Task(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "task not found: "+id)
			return
		}
		writeJSON(w, http.StatusOK, toTaskAPIResponse(tsk))
	})

	mux.HandleFunc("/v1/approvals/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/respond") {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/approvals/"), "/respond")
		if id == "" {
			writeJSONError(w, http.StatusBadRequest, "approval id required")
			return
		}
		var body struct {
			Nonce    string `json:"nonce"`
			Response string `json:"response"` // "approve" or "reject"
			Reason   string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if err := resolveApproval(r.Context(), store, sup, id, body.Nonce, body.Response, body.Reason); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": body.Response})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
