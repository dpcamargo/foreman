package telegram_test

import (
	"testing"

	"github.com/dpcamargo/foreman/internal/adapters/telegram"
)

func TestParseCallbackData(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantOK     bool
		wantApv    string
		wantNonce  string
		wantDecide string
	}{
		{
			name: "approve", data: "apv_1|noncevalue|approve",
			wantOK: true, wantApv: "apv_1", wantNonce: "noncevalue", wantDecide: "approve",
		},
		{
			name: "reject", data: "apv_1|noncevalue|reject",
			wantOK: true, wantApv: "apv_1", wantNonce: "noncevalue", wantDecide: "reject",
		},
		{name: "malformed, too few parts", data: "apv_1|noncevalue", wantOK: false},
		{name: "empty", data: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cb, ok := telegram.ParseCallbackData(tc.data)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if cb.ApprovalID != tc.wantApv || cb.Nonce != tc.wantNonce || cb.Decision != tc.wantDecide {
				t.Errorf("got %+v", cb)
			}
		})
	}
}

func TestNewApprovalKeyboardRoundTrips(t *testing.T) {
	kb := telegram.NewApprovalKeyboard("apv_1", "noncevalue")
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("expected one row of 2 buttons, got %+v", kb.InlineKeyboard)
	}
	approve, reject := kb.InlineKeyboard[0][0], kb.InlineKeyboard[0][1]
	cb, ok := telegram.ParseCallbackData(approve.CallbackData)
	if !ok || cb.Decision != "approve" || cb.ApprovalID != "apv_1" || cb.Nonce != "noncevalue" {
		t.Errorf("approve button callback data didn't round-trip: %+v", cb)
	}
	cb, ok = telegram.ParseCallbackData(reject.CallbackData)
	if !ok || cb.Decision != "reject" || cb.ApprovalID != "apv_1" || cb.Nonce != "noncevalue" {
		t.Errorf("reject button callback data didn't round-trip: %+v", cb)
	}
}
