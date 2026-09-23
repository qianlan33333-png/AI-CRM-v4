package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// ExternalChatRecordQuery is the narrow legacy-machine projection. The caller
// must first resolve CustomerID through OneID and enforce its machine owner
// scope; Archive then applies the canonical lineage predicate in its own
// transaction. It accepts no arbitrary search or table expression.
type ExternalChatRecordQuery struct {
	CustomerID  customerdomain.CustomerID
	CustomerIDs []customerdomain.CustomerID
	// ExternalUserID is the one verified WeCom identity selected by the
	// composition Host after OneID resolution. Archive applies it alongside
	// the lineage predicate so a merged customer's other external identities
	// cannot broaden this frozen legacy read.
	ExternalUserID string
	ChatScene      string
	StartAt        time.Time
	WithUserID     string
	Limit          int
	Offset         int
}

// ExternalChatRecord deliberately contains only the frozen legacy API fields.
// Provider identity values leave Archive only for this authorized machine read
// projection and must not be logged or persisted by its caller.
type ExternalChatRecord struct {
	MessageID      string    `json:"msgid"`
	ChatScene      string    `json:"chat_scene"`
	ChatType       string    `json:"chat_type"`
	UnionID        string    `json:"unionid"`
	ExternalUserID string    `json:"external_userid"`
	WithUserID     string    `json:"with_userid"`
	Sender         string    `json:"sender"`
	Receiver       string    `json:"receiver"`
	ChatID         string    `json:"chat_id"`
	RoomID         string    `json:"roomid"`
	GroupName      string    `json:"group_name"`
	MessageType    string    `json:"msgtype"`
	Content        string    `json:"content"`
	MediaID        string    `json:"media_id"`
	OccurredAt     time.Time `json:"-"`
	SourceID       string    `json:"source_id"`
}

type ExternalChatRecordPage struct {
	Items []ExternalChatRecord
	Total int64
}

// ExternalChatRecordReader is intentionally separate from CustomerMessageReader:
// the internal customer UI never receives clear provider identifiers, while the
// frozen external route does after machine authentication and scope checks.
type ExternalChatRecordReader interface {
	ExternalCustomerMessages(context.Context, ExternalChatRecordQuery) (ExternalChatRecordPage, error)
}
