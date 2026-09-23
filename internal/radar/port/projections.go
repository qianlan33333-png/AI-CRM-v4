package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

const (
	DefaultLimit int32 = 20
	MaximumLimit int32 = 100
)

type ListQuery struct {
	Search        string
	ContentType   radar.ContentType
	Status        radar.Status
	AuthPolicy    radar.AuthPolicy
	Limit         int32
	Offset        int32
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type LinkSummary struct {
	Link             radar.Link           `json:"link"`
	StatisticsStatus LinkStatisticsStatus `json:"statistics_status"`
	TotalLandings    int64                `json:"total_landings"`
	AuthorizedUsers  int64                `json:"authorized_users"`
	AuthorizedViews  int64                `json:"authorized_views"`
	ViewCount        int64                `json:"view_count"`
	LastViewedAt     *time.Time           `json:"last_viewed_at,omitempty"`
}

// LinkStatisticsStatus distinguishes a measured zero from a statistics read
// that did not complete. Link metadata remains safe to show in either case.
type LinkStatisticsStatus string

const (
	LinkStatisticsReady       LinkStatisticsStatus = "ready"
	LinkStatisticsUnavailable LinkStatisticsStatus = "unavailable"
)

type LinkPage struct {
	Items   []LinkSummary `json:"items"`
	Total   int64         `json:"total"`
	Limit   int32         `json:"limit"`
	Offset  int32         `json:"offset"`
	HasMore bool          `json:"has_more"`
}

type LinkDetail struct {
	Link  radar.Link `json:"link"`
	Stats Stats      `json:"stats"`
}

type Stats struct {
	TotalLandings   int64      `json:"total_landings"`
	AuthorizedUsers int64      `json:"authorized_users"`
	AuthorizedViews int64      `json:"authorized_views"`
	ViewCount       int64      `json:"view_count"`
	ConversionRate  float64    `json:"conversion_rate"`
	TotalEvents     int64      `json:"total_events"`
	Redirects       int64      `json:"redirects"`
	ImageLoaded     int64      `json:"image_loaded"`
	PDFOpened       int64      `json:"pdf_opened"`
	TodayLandings   int64      `json:"today_landings"`
	TodayViews      int64      `json:"today_views"`
	LastViewedAt    *time.Time `json:"last_viewed_at,omitempty"`
}

type AttributionStatus string

const (
	AttributionAnonymous AttributionStatus = "anonymous"
	AttributionResolved  AttributionStatus = "resolved"
	AttributionPending   AttributionStatus = "pending"
	AttributionConflict  AttributionStatus = "conflict"
	AttributionFailed    AttributionStatus = "failed"
)

type EventStage string

const (
	EventLanding          EventStage = "landing"
	EventOAuthStarted     EventStage = "oauth_started"
	EventOAuthVerified    EventStage = "oauth_verified"
	EventIdentityResolved EventStage = "identity_resolved"
	EventContentOpened    EventStage = "content_opened"
	EventRedirected       EventStage = "redirected"
	EventImageLoaded      EventStage = "image_loaded"
	EventPDFOpened        EventStage = "pdf_opened"
	EventFailed           EventStage = "failed"
)

// EventProjection intentionally contains no raw UnionID, OpenID,
// external_userid, phone, IP, user agent, referrer, OAuth code or token.
type EventProjection struct {
	EventID         int64             `json:"event_id"`
	ReceiptID       string            `json:"receipt_id"`
	RadarID         radar.RadarID     `json:"radar_id"`
	Version         radar.LinkVersion `json:"version"`
	Stage           EventStage        `json:"stage"`
	Attribution     AttributionStatus `json:"attribution"`
	CustomerRef     string            `json:"customer_ref,omitempty"`
	CustomerDisplay string            `json:"customer_display,omitempty"`
	OccurredAt      time.Time         `json:"occurred_at"`
}

type EventQuery struct {
	RadarID     radar.RadarID
	Stage       EventStage
	Attribution AttributionStatus
	Start       *time.Time
	End         *time.Time
	Limit       int32
	Offset      int32
}

type EventPage struct {
	Items   []EventProjection `json:"items"`
	Total   int64             `json:"total"`
	Limit   int32             `json:"limit"`
	Offset  int32             `json:"offset"`
	HasMore bool              `json:"has_more"`
}

const (
	MaximumVisitorLimit       int32 = 500
	MaximumVisitorOffset      int32 = 1_000_000
	MaximumVisitorSearchChars       = 200
	MaximumVisitorCandidates        = 100000
)

// VisitorSession is Radar-owned session metadata used only inside the admin
// visitor query. It intentionally never leaves the Radar application as an
// HTTP response.
type VisitorSession struct {
	SessionID   int64
	Version     radar.LinkVersion
	CustomerID  customerdomain.CustomerID
	Attribution AttributionStatus
	OpenedAt    time.Time
}

type VisitorQuery struct {
	RadarID       radar.RadarID
	CustomerIDs   []customerdomain.CustomerID
	FilterApplied bool
	Start         *time.Time
	End           *time.Time
	Limit         int32
	Offset        int32
}

type VisitorSessionPage struct {
	Items   []VisitorSession
	Total   int64
	Limit   int32
	Offset  int32
	HasMore bool
}

// VisitorStore keeps the session aggregation within Radar's table ownership.
// It has no dependency on Customer or Identity projections.
type VisitorStore interface {
	Visitors(context.Context, VisitorQuery) (VisitorSessionPage, error)
}

type VisitorExternalContactStatus string

const (
	VisitorExternalContactAvailable   VisitorExternalContactStatus = "available"
	VisitorExternalContactMissing     VisitorExternalContactStatus = "missing"
	VisitorExternalContactAmbiguous   VisitorExternalContactStatus = "ambiguous"
	VisitorExternalContactUnavailable VisitorExternalContactStatus = "unavailable"
)

// Visitor is the authenticated admin-only response model. Public event
// projections remain separate and must not gain these fields.
type Visitor struct {
	Nickname              *string                      `json:"nickname"`
	ExternalContactID     *string                      `json:"external_contact_id"`
	ExternalContactStatus VisitorExternalContactStatus `json:"external_contact_status"`
	OneID                 *string                      `json:"oneid"`
	OpenedAt              time.Time                    `json:"opened_at"`
	AttributionStatus     AttributionStatus            `json:"attribution_status"`
}

type VisitorPage struct {
	Items   []Visitor `json:"items"`
	Total   int64     `json:"total"`
	Limit   int32     `json:"limit"`
	Offset  int32     `json:"offset"`
	HasMore bool      `json:"has_more"`
}

// AdminVisitorPresentationReader is composition-owned. It joins stable
// Customer and Identity ports after Radar has read its own sessions; it never
// provides raw Provider identities to public event flows.
type AdminVisitorPresentationReader interface {
	SearchRadarVisitors(context.Context, string, int) ([]customerdomain.CustomerID, error)
	PresentRadarVisitors(context.Context, []VisitorSession) ([]Visitor, error)
}

type VisitorQueryService interface {
	Visitors(context.Context, VisitorQuery, string) (VisitorPage, error)
}

type QueryService interface {
	Stats(context.Context, radar.RadarID) (Stats, error)
	Events(context.Context, EventQuery) (EventPage, error)
}

// CustomerActivityQuery is the Radar-owned, canonical-customer projection
// used by the V1 activity stream. It exposes no raw identity, device, network
// or provider payload data. The Host supplies a signed aggregate cursor while
// Radar owns the per-type descending keyset.
type CustomerActivityQuery struct {
	// BusinessOnly excludes OAuth/identity bookkeeping before pagination.
	BusinessOnly bool
	CustomerID   customerdomain.CustomerID
	Limit        int32
	Watermark    time.Time
	AfterAt      time.Time
	AfterID      int64
}

type CustomerActivity struct {
	Title      string        `json:"title,omitempty"`
	EventID    int64         `json:"event_id"`
	RadarID    radar.RadarID `json:"radar_id"`
	Stage      EventStage    `json:"stage"`
	OccurredAt time.Time     `json:"occurred_at"`
}

type CustomerActivityPage struct {
	Items []CustomerActivity `json:"items"`
}

// CustomerActivityReader is the only cross-domain Radar read seam for a
// canonical customer's activity timeline. Consumers never receive a Radar
// store or query radar_events themselves.
type CustomerActivityReader interface {
	CustomerActivities(context.Context, CustomerActivityQuery) (CustomerActivityPage, error)
}

// CustomerActivityStore remains inside Radar's application boundary and
// requires the caller's transaction-bound context.
type CustomerActivityStore interface {
	CustomerActivities(context.Context, CustomerActivityQuery) (CustomerActivityPage, error)
}
