package telegram

import "strings"

// CallbackData is an inline button's parsed payload: {approval_id, nonce} plus which
// button was tapped (contracts/approval-nonce.md, contracts/telegram-commands.md).
type CallbackData struct {
	ApprovalID string
	Nonce      string
	Decision   string // "approve" or "reject"
}

// ParseCallbackData parses a callback_data string of the form
// "<approval_id>|<nonce>|<decision>".
func ParseCallbackData(data string) (CallbackData, bool) {
	parts := strings.Split(data, "|")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return CallbackData{}, false
	}
	return CallbackData{ApprovalID: parts[0], Nonce: parts[1], Decision: parts[2]}, true
}

// NewApprovalKeyboard builds the [Approve] [Reject] inline keyboard for a spec (or deliver)
// approval, with the approval id and nonce bound into each button's callback data so a
// stale or edited approval can't be approved by tapping an old message
// (contracts/approval-nonce.md).
func NewApprovalKeyboard(approvalID, nonce string) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Approve", CallbackData: approvalID + "|" + nonce + "|approve"},
				{Text: "Reject", CallbackData: approvalID + "|" + nonce + "|reject"},
			},
		},
	}
}
