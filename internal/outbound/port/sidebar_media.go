package port

import (
	"context"
	"time"
)

// SidebarImagePreparationSource is captured from Media by composition after
// sidebar authorization. Scope identifies the configured WeCom corp and agent.
type SidebarImagePreparationSource struct {
	ImageID                    int64
	SourceDigest               [32]byte
	Content                    []byte
	FileName, MediaType, Scope string
}

type SidebarImagePreparation struct {
	State, EffectID, MediaID string
	ReadyUntil               time.Time
}

type SidebarImagePreparer interface {
	PrepareSidebarImage(context.Context, SidebarImagePreparationSource, time.Time) (SidebarImagePreparation, error)
}

type SidebarImageUploadReceipt struct {
	MediaID    string
	ReadyUntil time.Time
}

// SidebarImageUploader uploads a temporary image only. It must never send a
// message; bool records whether the Provider upload request was attempted.
// Execute supplies Scope as its persisted sha256 configuration digest, never
// raw configuration. The uploader must compare it with its own configured scope.
type SidebarImageUploader interface {
	UploadSidebarImage(context.Context, SidebarImagePreparationSource) (SidebarImageUploadReceipt, bool, error)
}

// SidebarImageUploadError separates an attempted upload from an uncertain
// result. Only an explicit Provider rejection may return OutcomeUnknown false
// after an attempt. Codes and HTTP status are safe diagnostics, never raw text.
type SidebarImageUploadError interface {
	error
	OutcomeUnknown() bool
	FailureCode() string
	ProviderErrorCode() int64
	HTTPStatusCode() int
}
