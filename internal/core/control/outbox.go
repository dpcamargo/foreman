package control

import "time"

// Event mirrors Feature 002's events table row shape (internal/adapters/sqlite's insertEvent).
type Event struct {
	ID         int64
	Entity     string
	EntityID   string
	FromStatus string
	ToStatus   string
	Detail     string
	At         time.Time
}

// Digest is a coalesced batch of queued notifications delivered as one message
// (spec FR-007, SC-004).
type Digest struct {
	Events []Event
}

// AdvanceOutbox returns every event past cursor as one digest, and the new cursor position
// (the highest event id seen, or cursor unchanged if there were none). It is pure: the
// caller (the notifier adapter) is responsible for actually delivering the digest and
// persisting the new cursor, in that order, so a crash between the two is safe to retry
// (at-least-once delivery, never silently lost).
func AdvanceOutbox(events []Event, cursor int64) (Digest, int64) {
	var digest Digest
	newCursor := cursor
	for _, e := range events {
		if e.ID <= cursor {
			continue
		}
		digest.Events = append(digest.Events, e)
		if e.ID > newCursor {
			newCursor = e.ID
		}
	}
	return digest, newCursor
}
