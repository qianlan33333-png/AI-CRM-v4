package port

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

// ChannelMiniProgramReferenceReader returns whether a Mini Program card is
// present and locally enabled while holding its row lock in the caller's
// UnitOfWork. It deliberately exposes no card metadata.
type ChannelMiniProgramReferenceReader interface {
	ChannelMiniProgramEligible(context.Context, int64) (bool, error)
}

// MiniProgramMetadataReader is the generic, transaction-bound existence fact
// for a local enabled Mini Program material. It does not expose provider-ready
// fields, thumbnail bytes, or a concrete store.
type MiniProgramMetadataReader interface {
	MiniProgramExists(context.Context, int64) (bool, error)
}

// MiniProgram is the local material-card fact. The fields mirror the immutable
// legacy material record; neither the card nor its thumbnail cache is proof
// that a WeCom provider accepted a send.
type MiniProgram struct {
	ID                      int64      `json:"id"`
	Name                    string     `json:"name"`
	AppID                   string     `json:"appid"`
	PagePath                string     `json:"pagepath"`
	Title                   string     `json:"title"`
	ThumbnailImageURL       string     `json:"thumb_image_url"`
	ThumbnailImageBase64    string     `json:"thumb_image_base64"`
	ThumbnailMediaID        string     `json:"thumb_media_id"`
	ThumbnailMediaExpiresAt *time.Time `json:"thumb_media_id_expires_at,omitempty"`
	ThumbnailImageID        *int64     `json:"thumb_image_id,omitempty"`
	Enabled                 bool       `json:"enabled"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	CreatedBy               int64      `json:"created_by"`
	UpdatedBy               int64      `json:"updated_by"`
	Version                 int64      `json:"version"`
}

// MarshalJSON preserves the two page-path spellings consumed by the immutable
// legacy page. The local fact has one canonical value, so aliases cannot
// diverge in storage or in a receipt snapshot.
func (item MiniProgram) MarshalJSON() ([]byte, error) {
	type output struct {
		ID                      int64      `json:"id"`
		Name                    string     `json:"name"`
		AppID                   string     `json:"appid"`
		PagePath                string     `json:"pagepath"`
		PagePathAlias           string     `json:"page_path"`
		Title                   string     `json:"title"`
		ThumbnailImageURL       string     `json:"thumb_image_url"`
		ThumbnailImageBase64    string     `json:"thumb_image_base64"`
		ThumbnailMediaID        string     `json:"thumb_media_id"`
		ThumbnailMediaExpiresAt *time.Time `json:"thumb_media_id_expires_at,omitempty"`
		ThumbnailImageID        *int64     `json:"thumb_image_id,omitempty"`
		Enabled                 bool       `json:"enabled"`
		CreatedAt               time.Time  `json:"created_at"`
		UpdatedAt               time.Time  `json:"updated_at"`
		CreatedBy               int64      `json:"created_by"`
		UpdatedBy               int64      `json:"updated_by"`
		Version                 int64      `json:"version"`
	}
	return json.Marshal(output{
		ID: item.ID, Name: item.Name, AppID: item.AppID,
		PagePath: item.PagePath, PagePathAlias: item.PagePath, Title: item.Title,
		ThumbnailImageURL: item.ThumbnailImageURL, ThumbnailImageBase64: item.ThumbnailImageBase64,
		ThumbnailMediaID: item.ThumbnailMediaID, ThumbnailMediaExpiresAt: item.ThumbnailMediaExpiresAt,
		ThumbnailImageID: item.ThumbnailImageID, Enabled: item.Enabled, CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt, CreatedBy: item.CreatedBy, UpdatedBy: item.UpdatedBy, Version: item.Version,
	})
}

// OptionalInt64 distinguishes an omitted JSON field from an explicit JSON
// null. Future HTTP adapters must set Present whenever thumb_image_id occurs
// in the request body; Present=true with Value=nil clears the local cover.
type OptionalInt64 struct {
	Present bool
	Value   *int64
}

func (value *OptionalInt64) UnmarshalJSON(input []byte) error {
	value.Present = true
	value.Value = nil
	if bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
		return nil
	}
	var parsed int64
	if err := json.Unmarshal(input, &parsed); err != nil {
		return err
	}
	value.Value = &parsed
	return nil
}

// OptionalString distinguishes an omitted JSON field from an explicit null.
// Every client-supplied thumb_media_id occurrence, including a request to
// clear it, must be rejected unless it came from the Media-owned local cache
// resolver.
type OptionalString struct {
	Present bool
	Value   *string
}

func (value *OptionalString) UnmarshalJSON(input []byte) error {
	value.Present = true
	value.Value = nil
	if bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
		return nil
	}
	var parsed string
	if err := json.Unmarshal(input, &parsed); err != nil {
		return err
	}
	value.Value = &parsed
	return nil
}

type MiniProgramPatch struct {
	Name             *string       `json:"name,omitempty"`
	AppID            *string       `json:"appid,omitempty"`
	PagePath         *string       `json:"pagepath,omitempty"`
	Title            *string       `json:"title,omitempty"`
	ThumbnailImageID OptionalInt64 `json:"thumb_image_id"`
	// ThumbMediaID retains the legacy wire field with presence information. It
	// is deliberately rejected by the application: only a Media-owned local
	// cache resolver may write MiniProgram.ThumbnailMediaID.
	ThumbMediaID      OptionalString `json:"thumb_media_id"`
	ResolveThumbMedia *bool          `json:"resolve_thumb_media,omitempty"`
	Enabled           *bool          `json:"enabled,omitempty"`
}

// UnmarshalJSON accepts the two legacy write aliases while normalizing them to
// the canonical application fields. If both spellings are supplied, the
// canonical legacy spelling takes precedence, matching the immutable legacy
// request validator.
func (patch *MiniProgramPatch) UnmarshalJSON(input []byte) error {
	type rawPatch struct {
		Name             *string        `json:"name"`
		AppID            *string        `json:"appid"`
		AppIDAlias       *string        `json:"app_id"`
		PagePath         *string        `json:"pagepath"`
		PagePathAlias    *string        `json:"page_path"`
		Title            *string        `json:"title"`
		ThumbnailImageID OptionalInt64  `json:"thumb_image_id"`
		ThumbnailMediaID OptionalString `json:"thumb_media_id"`
		ResolveThumb     *bool          `json:"resolve_thumb_media"`
		Enabled          *bool          `json:"enabled"`
	}

	var raw rawPatch
	if err := json.Unmarshal(input, &raw); err != nil {
		return err
	}
	if raw.AppID == nil {
		raw.AppID = raw.AppIDAlias
	}
	if raw.PagePath == nil {
		raw.PagePath = raw.PagePathAlias
	}
	*patch = MiniProgramPatch{
		Name:              raw.Name,
		AppID:             raw.AppID,
		PagePath:          raw.PagePath,
		Title:             raw.Title,
		ThumbnailImageID:  raw.ThumbnailImageID,
		ThumbMediaID:      raw.ThumbnailMediaID,
		ResolveThumbMedia: raw.ResolveThumb,
		Enabled:           raw.Enabled,
	}
	return nil
}

type MiniProgramCreateCommand struct {
	Name, AppID, PagePath, Title string
	ThumbnailImageID             *int64
	ThumbMediaID                 OptionalString
	ResolveThumbMedia            *bool
	Enabled                      *bool
	Actor                        int64
	IdempotencyKey               string
}

type MiniProgramUpdateCommand struct {
	ID int64
	MiniProgramPatch
	Actor          int64
	IdempotencyKey string
}

type MiniProgramDeleteCommand struct {
	ID             int64
	Actor          int64
	IdempotencyKey string
}

type MiniProgramResolveThumbnailCommand struct {
	ID             int64
	Actor          int64
	IdempotencyKey string
}

type MiniProgramListQuery struct {
	Limit       int32
	Offset      int32
	EnabledOnly bool
	Search      string
}

type MiniProgramPage struct {
	Items  []MiniProgram `json:"items"`
	Total  int64         `json:"total"`
	Limit  int32         `json:"limit"`
	Offset int32         `json:"offset"`
}

type MiniProgramMutationResult struct {
	Item             MiniProgram               `json:"item"`
	Changed          bool                      `json:"changed"`
	ThumbnailResolve *ThumbnailCacheResolution `json:"thumb_resolve,omitempty"`
}

type MiniProgramDeleteResult struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

type ThumbnailResolutionStatus string

const (
	ThumbnailResolved       ThumbnailResolutionStatus = "resolved"
	ThumbnailNotAvailable   ThumbnailResolutionStatus = "not_available"
	ThumbnailOutcomeUnknown ThumbnailResolutionStatus = "outcome_unknown"
)

const ThumbnailCacheOwner = "media.thumbnail_cache"

// ThumbnailCacheResolution is intentionally local-only. A successful result
// needs a Media-owned cache receipt; callers must not treat it as an external
// provider receipt or as a claim that the mini-program is reachable.
type ThumbnailCacheResolution struct {
	Status                   ThumbnailResolutionStatus `json:"status"`
	CacheOwner               string                    `json:"cache_owner"`
	CacheReceipt             string                    `json:"cache_receipt"`
	MediaID                  string                    `json:"thumb_media_id,omitempty"`
	ExpiresAt                *time.Time                `json:"thumb_media_id_expires_at,omitempty"`
	SideEffectExecuted       bool                      `json:"side_effect_executed"`
	RealExternalCallExecuted bool                      `json:"real_external_call_executed"`
}

type MiniProgramThumbnailResolutionResult struct {
	Item       MiniProgram              `json:"item"`
	Resolution ThumbnailCacheResolution `json:"resolution"`
	Changed    bool                     `json:"changed"`
}

// ThumbnailCacheResolver may only query a Media-owned local cache in the
// supplied transaction context. It must never invoke a provider or follow a
// URL/redirect.
type ThumbnailCacheResolver interface {
	ResolveThumbnailFromCache(context.Context, MiniProgram) (ThumbnailCacheResolution, error)
}
