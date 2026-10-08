// Package telegram implements the daemon's side of the Telegram Bot API: long polling for
// updates and the handful of calls needed to send messages, edit inline keyboards, and
// answer callback queries (specs/003-daemon-telegram-control/research.md R1). No SDK: about
// a dozen hand-written JSON calls don't justify a dependency (constitution Principle IX).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

// Client calls the Telegram Bot API for one bot token.
type Client struct {
	Token       string
	HTTP        *http.Client
	BaseURL     string        // overridable for tests; defaults to the real Bot API
	BackoffBase time.Duration // overridable for tests; defaults to 1s
}

// NewClient returns a Client ready to use against the real Telegram API.
func NewClient(token string) *Client {
	return &Client{
		Token:       token,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		BaseURL:     defaultBaseURL,
		BackoffBase: time.Second,
	}
}

type apiResponse[T any] struct {
	OK     bool   `json:"ok"`
	Result T      `json:"result"`
	Descr  string `json:"description"`
}

func (c *Client) call(ctx context.Context, method string, query map[string]string, body any, out any) error {
	url := c.BaseURL + "/bot" + c.Token + "/" + method
	var req *http.Request
	var err error
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return fmt.Errorf("marshal %s request: %w", method, merr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	}
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	if query != nil {
		q := req.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %d", method, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	return nil
}

// GetUpdates fetches updates at or after offset, long-polling up to timeoutSec.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var out apiResponse[[]Update]
	err := c.call(ctx, "getUpdates", map[string]string{
		"offset":  strconv.FormatInt(offset, 10),
		"timeout": strconv.Itoa(timeoutSec),
	}, nil, &out)
	if err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("getUpdates: %s", out.Descr)
	}
	return out.Result, nil
}

// SendMessageRequest is sendMessage's request body (Telegram Bot API).
type SendMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// SendMessage posts a message, returning the sent Message (its MessageID is needed for a
// later editMessageReplyMarkup, e.g. disabling buttons once an approval resolves).
func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (Message, error) {
	var out apiResponse[Message]
	if err := c.call(ctx, "sendMessage", nil, req, &out); err != nil {
		return Message{}, err
	}
	if !out.OK {
		return Message{}, fmt.Errorf("sendMessage: %s", out.Descr)
	}
	return out.Result, nil
}

// EditMessageReplyMarkup replaces a sent message's inline keyboard (e.g. disabling the
// Approve/Reject buttons once an approval resolves, so a stale tap can't be sent twice).
func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, markup *InlineKeyboardMarkup) error {
	var out apiResponse[json.RawMessage]
	body := struct {
		ChatID      int64                 `json:"chat_id"`
		MessageID   int64                 `json:"message_id"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{chatID, messageID, markup}
	if err := c.call(ctx, "editMessageReplyMarkup", nil, body, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("editMessageReplyMarkup: %s", out.Descr)
	}
	return nil
}

// AnswerCallbackQuery acknowledges an inline-button tap so Telegram stops showing its
// loading spinner on the client.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackQueryID, text string) error {
	var out apiResponse[json.RawMessage]
	body := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
	}{callbackQueryID, text}
	if err := c.call(ctx, "answerCallbackQuery", nil, body, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("answerCallbackQuery: %s", out.Descr)
	}
	return nil
}

// Poll long-polls for updates starting at startOffset, calling handle once per update in
// arrival order, until ctx is cancelled. The offset only advances past an update once
// handle has returned for it, and a request error is retried at the same offset after a
// backoff — Telegram being unreachable never skips an update (spec's "Telegram unreachable"
// edge case, research.md R1).
func (c *Client) Poll(ctx context.Context, startOffset int64, handle func(Update)) error {
	offset := startOffset
	backoff := c.BackoffBase
	if backoff <= 0 {
		backoff = time.Second
	}
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		updates, err := c.GetUpdates(ctx, offset, 25)
		if err != nil {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = c.BackoffBase
		if backoff <= 0 {
			backoff = time.Second
		}
		for _, u := range updates {
			handle(u)
			offset = u.UpdateID + 1
		}
	}
}
