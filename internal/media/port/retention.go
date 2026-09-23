package port

import (
	"context"
	"errors"
	"time"
)

var ErrRetentionRequest = errors.New("invalid media retention request")

// UploadPartRetentionCommand deletes only expired temporary upload parts.
// Before must be at least 30 days old; no business object or receipt is deleted.
// Apply=false is a read-only preview. Each invocation is bounded to 1000 parts.
type UploadPartRetentionCommand struct {
	Before time.Time
	Limit  int
	Apply  bool
}

type UploadPartRetentionReport struct {
	Before     time.Time `json:"before"`
	Candidates int64     `json:"candidates"`
	Deleted    int64     `json:"deleted"`
	Bytes      int64     `json:"bytes"`
	Remaining  bool      `json:"remaining"`
}

type UploadPartRetention interface {
	CleanupExpiredUploadParts(context.Context, UploadPartRetentionCommand) (UploadPartRetentionReport, error)
}

// TransactionalUploadPartRetention allows maintenance evidence to share the
// same PostgreSQL Unit of Work as the Owner's deletion.
type TransactionalUploadPartRetention interface {
	UploadPartRetention
	CleanupExpiredUploadPartsWithin(context.Context, UploadPartRetentionCommand) (UploadPartRetentionReport, error)
}
