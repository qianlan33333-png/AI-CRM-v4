package port

import "context"

// GroupMessageRequest is the narrow outbound-only representation of one
// immutable Group Ops execution. Chat IDs remain in memory and must not be
// persisted by External Effects.
type GroupMessageRequest struct {
	SenderUserID string
	ChatIDs      []string
	Text         string
	Attachments  []GroupMessageAttachment
}

type GroupMessageAttachment struct {
	MsgType     string
	MediaID     string
	AppID       string
	PagePath    string
	Title       string
	URL         string
	Description string
	PicURL      string
}

// GroupMessageReceipt proves only that WeCom accepted a task for the exact
// requested chat list. It never proves a member received the message.
type GroupMessageReceipt struct{ MessageID string }

// GroupMessageSender is a write-only connector boundary. The bool reports
// whether the provider boundary was crossed, so EER can preserve unknown
// outcomes under the original key.
type GroupMessageSender interface {
	SendGroupMessage(context.Context, GroupMessageRequest) (GroupMessageReceipt, bool, error)
}

// GroupMessageTaskReader is deliberately a separate, read-only evidence
// boundary. A task msgid is accepted input; callers must still match sender,
// chat and the returned status before treating it as delivery evidence.
type GroupMessageTaskReader interface {
	// senderUserID is frozen by the Group Ops owner receipt.  WeCom requires it
	// alongside msgid; callers must never supply a replacement sender.
	GetGroupMessageSendResult(context.Context, string, string, string, int) (GroupMessageSendResultPage, error)
}

type GroupMessageSendResultPage struct {
	Items      []GroupMessageSendResult
	NextCursor string
}

type GroupMessageSendResult struct {
	SenderUserID string
	ChatID       string
	Status       int
}

type GroupMessageSendError interface {
	error
	OutcomeUnknown() bool
}
