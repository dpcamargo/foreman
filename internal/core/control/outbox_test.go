package control_test

import (
	"testing"
	"time"

	"github.com/dpcamargo/foreman/internal/core/control"
)

func TestAdvanceOutboxCoalescesIntoOneDigest(t *testing.T) {
	events := []control.Event{
		{ID: 5, Entity: "task", EntityID: "tsk_1", ToStatus: "succeeded", At: time.Now()},
		{ID: 6, Entity: "task", EntityID: "tsk_2", ToStatus: "failed", At: time.Now()},
		{ID: 7, Entity: "run", EntityID: "run_1", ToStatus: "succeeded", At: time.Now()},
	}
	digest, newCursor := control.AdvanceOutbox(events, 4)

	if len(digest.Events) != 3 {
		t.Fatalf("expected 3 events coalesced into one digest, got %d", len(digest.Events))
	}
	if newCursor != 7 {
		t.Errorf("expected cursor to advance to the last event id 7, got %d", newCursor)
	}
}

func TestAdvanceOutboxNoOpWhenNoNewEvents(t *testing.T) {
	events := []control.Event{
		{ID: 1, Entity: "task", EntityID: "tsk_1", ToStatus: "succeeded", At: time.Now()},
	}
	digest, newCursor := control.AdvanceOutbox(events, 5) // cursor already past id 1

	if len(digest.Events) != 0 {
		t.Errorf("expected no events in the digest, got %d", len(digest.Events))
	}
	if newCursor != 5 {
		t.Errorf("expected cursor to stay at 5, got %d", newCursor)
	}
}

func TestAdvanceOutboxIgnoresEventsAtOrBeforeCursor(t *testing.T) {
	events := []control.Event{
		{ID: 3, Entity: "task", EntityID: "tsk_1", ToStatus: "succeeded", At: time.Now()},
		{ID: 4, Entity: "task", EntityID: "tsk_2", ToStatus: "succeeded", At: time.Now()},
	}
	digest, newCursor := control.AdvanceOutbox(events, 3)

	if len(digest.Events) != 1 || digest.Events[0].ID != 4 {
		t.Fatalf("expected only event id 4 past the cursor, got %+v", digest.Events)
	}
	if newCursor != 4 {
		t.Errorf("expected cursor 4, got %d", newCursor)
	}
}
