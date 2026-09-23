package port

import (
	"context"
	"time"
)

const (
	DefaultAttachmentListLimit int64 = 100
	MaximumAttachmentListLimit int64 = 500
)

// Attachment is private Media metadata. Its blob is never included in list or
// detail projections and is only exposed through the authenticated download
// operation.
type Attachment struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	FileName    string    `json:"file_name"`
	MimeType    string    `json:"mime_type"`
	FileSize    int64     `json:"file_size"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Enabled     bool      `json:"enabled"`
	Version     int64     `json:"version"`
	CreatedBy   int64     `json:"created_by"`
	UpdatedBy   int64     `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AttachmentListQuery struct {
	Limit       int64
	Offset      int64
	EnabledOnly bool
	Search      string
}

type AttachmentListPage struct {
	Items  []Attachment `json:"items"`
	Total  int64        `json:"total"`
	Limit  int64        `json:"limit"`
	Offset int64        `json:"offset"`
}

// AttachmentUploadCommand is the canonical multipart create command. There
// is deliberately no separate JSON-only create lifecycle without a blob.
type AttachmentUploadCommand struct {
	Actor          int64
	IdempotencyKey string
	FileName       string
	DeclaredType   string
	Content        []byte
	Name           string
	Description    string
	Tags           []string
	Enabled        *bool
}

// AttachmentUpdateCommand is a full CAS PUT of the mutable metadata fields.
// File identity and blob bytes are immutable after multipart upload.
type AttachmentUpdateCommand struct {
	AttachmentID    int64
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
	Name            string
	Description     string
	Tags            []string
	Enabled         bool
}

type AttachmentDeleteCommand struct {
	AttachmentID   int64
	Actor          int64
	IdempotencyKey string
}

// AttachmentMetadataReader obtains a transaction-bound FOR KEY SHARE lock
// before another domain persists an attachment reference.
type AttachmentMetadataReader interface {
	AttachmentExists(context.Context, int64) (bool, error)
}

// ChannelAttachmentReferenceReader returns whether an attachment is both
// present and locally enabled while holding a KEY SHARE lock in the caller's
// UnitOfWork. It deliberately exposes no attachment metadata.
type ChannelAttachmentReferenceReader interface {
	ChannelAttachmentEligible(context.Context, int64) (bool, error)
}
