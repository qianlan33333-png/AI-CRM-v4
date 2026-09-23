package port

import (
	"context"
	"time"
)

type Image struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FileName    string `json:"file_name"`
	FileSize    int32  `json:"file_size"`
	MimeType    string `json:"mime_type"`
	Width       int32  `json:"width"`
	Height      int32  `json:"height"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
	Category    string `json:"category"`
	// Enabled is persisted with the upload and stored in the completion
	// receipt. The legacy multipart /upload adapter deliberately projects its
	// pre-0357 DTO instead of serializing Image directly.
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ImageFacets struct {
	Categories []string
	Tags       []string
}

type ImageListQuery struct {
	Limit         int64
	Offset        int64
	EnabledOnly   bool
	Search        string
	Category      string
	Tags          string
	TagGroups     []string
	OnlyUnlabeled bool
}

type ImageListItem struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	FileName      string   `json:"file_name"`
	MimeType      string   `json:"mime_type"`
	FileSize      int32    `json:"file_size"`
	Enabled       bool     `json:"enabled"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	Category      string   `json:"category"`
	Width         int32    `json:"width"`
	Height        int32    `json:"height"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	Thumb160URL   string   `json:"thumb_160_url"`
	Thumb320URL   string   `json:"thumb_320_url"`
	ThumbURL      string   `json:"thumb_url"`
	PreviewURL    string   `json:"preview_url"`
	Mobile1080URL string   `json:"mobile_1080_url"`
	Large1440URL  string   `json:"large_1440_url"`
	OriginalURL   string   `json:"original_url"`
}

type ImageListPage struct {
	Items  []ImageListItem
	Total  int64
	Limit  int64
	Offset int64
}

type ImageVariant struct {
	Content   []byte
	MediaType string
	ETag      string
}

type ImageVariantReader interface {
	GetImageVariant(context.Context, int64, string) (ImageVariant, error)
}

// EnabledImageVariantReader is the bounded Media projection for a viewer that
// can only discover enabled library items. It deliberately differs from the
// administrator variant reader: an administrator may inspect a disabled image
// while a sidebar viewer must not recover it by guessing an image ID.
type EnabledImageVariantReader interface {
	GetEnabledImageVariant(context.Context, int64, string) (ImageVariant, error)
}

// ImageLibraryReader is the narrow Media-owned local projection consumed by
// sidebar workbench reads. Implementations must not invoke a provider or create
// image variants while serving these methods.
type ImageLibraryReader interface {
	ListImages(context.Context, ImageListQuery) (ImageListPage, error)
	Facets(context.Context) (ImageFacets, error)
	LocalImageExists(context.Context, int64) (bool, error)
}

type UploadCommand struct {
	Actor          int64
	IdempotencyKey string
	FileName       string
	DeclaredType   string
	Content        []byte
	Name           string
	Description    string
	Tags           string
	Category       string
	// Enabled is optional for compatibility: an omitted value preserves the
	// historic upload default of true without changing its receipt digest.
	Enabled *bool
}

type GroupInvite struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	JoinURL      string     `json:"join_url"`
	CoverImageID int64      `json:"cover_image_id,omitempty"`
	Enabled      bool       `json:"enabled"`
	CreatedBy    int64      `json:"created_by"`
	UpdatedBy    int64      `json:"updated_by"`
	Version      int64      `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
}

// ChannelGroupInviteReferenceReader returns whether a group invite is present,
// enabled, and not archived while holding its row lock in the caller's
// UnitOfWork. It deliberately exposes no invite metadata.
type ChannelGroupInviteReferenceReader interface {
	ChannelGroupInviteEligible(context.Context, int64) (bool, error)
}

// GroupInviteMetadataReader is the generic, transaction-bound existence fact
// for a locally enabled, non-archived group-invite material.
type GroupInviteMetadataReader interface {
	GroupInviteExists(context.Context, int64) (bool, error)
}

type GroupInviteCreateCommand struct {
	Name, Title, Description, JoinURL string
	CoverImageID                      int64
	Enabled                           *bool
	Actor                             int64
	IdempotencyKey                    string
}

type GroupInvitePatch struct {
	Name, Title, Description, JoinURL *string
	CoverImageID                      *int64
	Enabled                           *bool
}

type GroupInviteUpdateCommand struct {
	ID int64
	GroupInvitePatch
	Actor          int64
	IdempotencyKey string
}

type GroupInviteArchiveCommand struct {
	ID             int64
	Actor          int64
	IdempotencyKey string
}

type GroupInviteListQuery struct {
	Limit, Offset int32
	EnabledOnly   bool
	Search        string
}

type GroupInvitePage struct {
	Items  []GroupInvite `json:"items"`
	Total  int64         `json:"total"`
	Limit  int32         `json:"limit"`
	Offset int32         `json:"offset"`
}

type ImageMetadataReader interface {
	ImageExists(context.Context, int64) (bool, error)
}
