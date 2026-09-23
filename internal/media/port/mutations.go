package port

import (
	"context"
	"encoding/json"
	"time"
)

// Event is the Media-owned seam for a transactional domain fact.  It mirrors
// the donor's small append contract without importing the v2 events package;
// the v3 adapter remains responsible for writing the event and outbox rows in
// the same UnitOfWork as the Media mutation.
type Event struct {
	Type           string
	Payload        json.RawMessage
	OccurredAt     time.Time
	IdempotencyKey string
}

type EventID int64

type EventAppender interface {
	Append(context.Context, Event) (EventID, error)
}

const (
	EventImageCreated               = "media.image_created"
	EventImageMetadataUpdated       = "media.image_metadata_updated"
	EventImageDeleted               = "media.image_deleted"
	EventAttachmentCreated          = "media.attachment_created"
	EventAttachmentUpdated          = "media.attachment_updated"
	EventAttachmentDeleted          = "media.attachment_deleted"
	EventMiniProgramCreated         = "media.miniprogram.created"
	EventMiniProgramUpdated         = "media.miniprogram.updated"
	EventMiniProgramDeleted         = "media.miniprogram.deleted"
	EventMiniProgramThumbnailCached = "media.miniprogram.thumbnail_cache_resolved"
	EventGroupInviteCreated         = "media.group_invite_created"
	EventGroupInviteUpdated         = "media.group_invite_updated"
	EventGroupInviteArchived        = "media.group_invite_archived"

	// Legacy names are retained as source-compatible aliases for the frozen
	// donor characterization tests. New v3 code should use Event* names above.
	EvMediaImageCreated        = EventImageCreated
	EvMediaGroupInviteCreated  = EventGroupInviteCreated
	EvMediaGroupInviteUpdated  = EventGroupInviteUpdated
	EvMediaGroupInviteArchived = EventGroupInviteArchived
)

// AutomationImageReferenceReader and AutomationAttachmentReferenceReader
// are owner-facing read contracts.  Implementations live in Automation and
// must return stable ascending local IDs; Media never imports that domain's
// store or application layer.
type AutomationImageReferenceReader interface {
	ListImageReferenceAgentIDs(context.Context, int64) ([]int64, error)
}

type AutomationAttachmentReferenceReader interface {
	ListAttachmentReferenceAgentIDs(context.Context, int64) ([]int64, error)
}

// These deletion readers are intentionally distinct from the eligibility
// readers used by channel writes.  Their result is a stable, auditable list
// for the delete response rather than a boolean reference check.
type ChannelImageReferenceReader interface {
	ListImageReferenceChannelIDs(context.Context, int64) ([]int64, error)
}

type ChannelAttachmentDeletionReferenceReader interface {
	ListAttachmentReferenceChannelIDs(context.Context, int64) ([]int64, error)
}

type ChannelMiniProgramDeletionReferenceReader interface {
	ListMiniProgramReferenceChannelIDs(context.Context, int64) ([]int64, error)
}

type ChannelGroupInviteDeletionReferenceReader interface {
	ListGroupInviteReferenceChannelIDs(context.Context, int64) ([]int64, error)
}

// Radar reference readers keep Radar table ownership behind a narrow port.
type RadarImageReferenceReader interface {
	ListImageReferenceLinkIDs(context.Context, int64) ([]int64, error)
}

type RadarAttachmentReferenceReader interface {
	ListAttachmentReferenceLinkIDs(context.Context, int64) ([]int64, error)
}
