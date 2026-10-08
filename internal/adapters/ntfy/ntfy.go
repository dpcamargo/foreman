// Package ntfy implements the secondary notification channel for unanswered approvals
// (spec.md Assumptions, ARCHITECTURE.md §9): a single net/http POST to an ntfy topic URL.
// It only pings the operator — it never resolves, expires, or otherwise decides anything
// about the approval itself (FR-011: approvals are never auto-resolved on a timeout).
package ntfy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client publishes to one ntfy topic URL.
type Client struct {
	TopicURL string // e.g. https://ntfy.sh/foreman-approvals
	HTTP     *http.Client
}

// New returns a Client publishing to topicURL.
func New(topicURL string) *Client {
	return &Client{
		TopicURL: strings.TrimRight(topicURL, "/"),
		HTTP:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Publish posts a title/message pair to the topic. This is the adapter's only method —
// one POST, no retry loop here (the caller, cmd/foreman's housekeeping tick, decides).
func (c *Client) Publish(ctx context.Context, message, title string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TopicURL, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("build ntfy request: %w", err)
	}
	req.Header.Set("Title", title)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("post to ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy: unexpected status %d", resp.StatusCode)
	}
	return nil
}
