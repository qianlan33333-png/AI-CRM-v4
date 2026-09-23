package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// V1ChatRecordQuery is the Open Platform's narrow read request after the Host
// has resolved and authorized a canonical Customer. CustomerIDs is populated
// only by MessageArchive's lineage reader; callers cannot widen the query with
// ingest-time IDs, raw participant values, or SQL expressions.
type V1ChatRecordQuery struct {
	CustomerID       customerdomain.CustomerID
	CustomerIDs      []customerdomain.CustomerID
	ChatType         string
	StaffUserID      int64
	StartAt          time.Time
	EndAt            time.Time
	SourceSystem     string
	SourceRecordID   string
	MessageID        string
	BeforeOccurredAt time.Time
	BeforeMessageID  int64
	Limit            int
}

// V1ChatRecord is a local archive projection. It intentionally excludes
// external_userid, UnionID, OpenID, sender/receiver provider values, SDK file
// references, provider payloads, and protected media. SourceRecordID is the
// archive-owned immutable message row ID; MessageID is the provider's stable
// chat record identifier already held by this authorized Chat projection.
type V1ChatRecord struct {
	MessageID          string
	SourceSystem       string
	SourceRecordID     string
	ChatType           string
	MessageType        string
	Content            string
	RenderType         string
	Direction          string
	OccurredAt         time.Time
	ConversationID     string
	GroupName          string
	MediaArchiveStatus string
	MediaAvailability  string
	StaffIDs           []int64 `json:"-"`
	Staff              []StaffOption
}

type V1ChatRecordPage struct {
	Items   []V1ChatRecord
	HasMore bool
}

// V1ChatRecordReader is the stable Owner boundary for Open Platform chat
// reads. It has no Provider read or media retrieval method.
type V1ChatRecordReader interface {
	V1ChatRecords(context.Context, V1ChatRecordQuery) (V1ChatRecordPage, error)
}

// V1ChatStaffResolver resolves an exact, already-projected WeCom employee
// user ID to Archive's durable Access staff ID. It deliberately offers no
// browsing or Provider lookup capability.
type V1ChatStaffResolver interface {
	V1ChatStaffID(context.Context, string) (int64, error)
}
