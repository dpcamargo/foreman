package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
)

func TestClientGetUpdatesParsesResponse(t *testing.T) {
	var gotOffset string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("offset")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":[{"update_id":5,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/tasks"}}]}`)
	}))
	defer srv.Close()

	c := telegram.NewClient("test-token")
	c.BaseURL = srv.URL

	updates, err := c.GetUpdates(context.Background(), 3, 1)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 || updates[0].UpdateID != 5 {
		t.Fatalf("unexpected updates: %+v", updates)
	}
	if updates[0].Message.Text != "/tasks" {
		t.Errorf("expected message text /tasks, got %q", updates[0].Message.Text)
	}
	if gotOffset != "3" {
		t.Errorf("expected offset=3 in the request, got %q", gotOffset)
	}
}

func TestClientSendMessagePostsExpectedBody(t *testing.T) {
	type sendReq struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	var got sendReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":42,"type":"private"}}}`)
	}))
	defer srv.Close()

	c := telegram.NewClient("test-token")
	c.BaseURL = srv.URL

	msg, err := c.SendMessage(context.Background(), telegram.SendMessageRequest{ChatID: 42, Text: "hello"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg.MessageID != 7 {
		t.Errorf("expected message_id 7, got %d", msg.MessageID)
	}
	if got.ChatID != 42 || got.Text != "hello" {
		t.Errorf("unexpected request body: %+v", got)
	}
}

// TestClientPollResumesFromLastUpdateIDAfterError confirms the polling loop advances its
// offset only on success and retries the same offset (not skipping updates) after a
// transient server error (spec's "Telegram unreachable" edge case).
func TestClientPollResumesFromLastUpdateIDAfterError(t *testing.T) {
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&call, 1)
		offset := r.URL.Query().Get("offset")
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if offset != "1" {
			t.Errorf("expected the retry to reuse offset=1, got %q", offset)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/tasks"}}]}`)
	}))
	defer srv.Close()

	c := telegram.NewClient("test-token")
	c.BaseURL = srv.URL
	c.BackoffBase = time.Millisecond

	var received []telegram.Update
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = c.Poll(ctx, 1, func(u telegram.Update) {
			received = append(received, u)
			if len(received) == 1 {
				cancel()
			}
		})
		close(done)
	}()
	<-done

	if len(received) != 1 || received[0].UpdateID != 1 {
		t.Fatalf("expected exactly 1 update with id 1, got %+v", received)
	}
	if atomic.LoadInt32(&call) < 2 {
		t.Errorf("expected at least 2 requests (1 failed + 1 retry), got %d", call)
	}
}
