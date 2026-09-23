package port

import (
	"context"
	"errors"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

var (
	ErrNotFound             = errors.New("radar: not found")
	ErrConflict             = errors.New("radar: conflict")
	ErrIdempotencyConflict  = errors.New("radar: idempotency conflict")
	ErrUnavailable          = errors.New("radar: unavailable")
	ErrGone                 = errors.New("radar: disabled")
	ErrVisitorSearchTooWide = errors.New("radar: visitor search too wide")
)

// Stable aliases keep cross-domain consumers on radar/port while preserving
// Radar's validated value objects in Manager signatures.
type RadarID = radar.RadarID
type Status = radar.Status

const StatusEnabled = radar.StatusEnabled

type CreateCommand struct {
	Name           string
	Title          string
	Description    string
	Content        radar.Content
	AuthPolicy     radar.AuthPolicy
	ActorID        int64
	IdempotencyKey string
}

type UpdateCommand struct {
	RadarID        radar.RadarID
	Expected       radar.LinkVersion
	Revision       radar.Revision
	ActorID        int64
	IdempotencyKey string
}

type SetStatusCommand struct {
	RadarID        radar.RadarID
	Expected       radar.LinkVersion
	Target         radar.Status
	ActorID        int64
	IdempotencyKey string
}

// LinkReader is the Radar-owned directory projection for consumers that need
// to validate a persisted radar reference.
type LinkReader interface {
	List(context.Context, ListQuery) (LinkPage, error)
	Get(context.Context, radar.RadarID) (LinkDetail, error)
}

type Manager interface {
	LinkReader
	Create(context.Context, CreateCommand) (LinkDetail, error)
	Update(context.Context, UpdateCommand) (LinkDetail, error)
	SetStatus(context.Context, SetStatusCommand) (LinkDetail, error)
}

// MediaReferenceValidator is implemented at the composition boundary by the
// Media owner and validates references inside the caller's transaction.
type MediaReferenceValidator interface {
	ValidateRadarMedia(context.Context, radar.ContentType, radar.MediaID) error
}

type PublicCodeGenerator interface {
	GeneratePublicCode() (radar.PublicCode, error)
}

type Clock interface {
	Now() time.Time
}
