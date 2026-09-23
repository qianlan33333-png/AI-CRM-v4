package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

// ExternalClickQuery reads one logical successful opening per Radar visit
// session. CustomerIDs are canonical IDs supplied by the authenticated Host;
// when FilterApplied is true, an empty slice deliberately returns no clicks.
// This keeps a selected scope from widening when identity resolution found no
// eligible customer. Pending and conflict sessions have no CustomerID and are
// available only to an explicitly unbound, full-scope caller.
type ExternalClickQuery struct {
	CustomerIDs   []customerdomain.CustomerID
	FilterApplied bool
	RadarID       radar.RadarID
	RadarCode     string
	SessionID     int64
	Start         *time.Time
	End           *time.Time
	BeforeOpened  time.Time
	BeforeEventID int64
	Limit         int32
}

// ExternalClick contains no raw external identity, session token, receipt,
// network metadata, OAuth code, or Provider payload. SessionID is the stable
// Radar-owned source record for one logical opening; EventID identifies the
// first successful open event used to order that logical record.
type ExternalClick struct {
	SessionID         int64
	EventID           int64
	RadarID           radar.RadarID
	RadarCode         string
	OpenedAt          time.Time
	OpenStage         EventStage
	AttributionStatus AttributionStatus
	CustomerID        *customerdomain.CustomerID
}

type ExternalClickPage struct {
	Items   []ExternalClick
	HasMore bool
}

// ExternalClickReader is Radar's narrow, Owner-owned Open Platform read
// boundary. Consumers cannot obtain a Radar repository or query Radar tables.
type ExternalClickReader interface {
	ExternalClicks(context.Context, ExternalClickQuery) (ExternalClickPage, error)
}

// ExternalClickStore is internal to Radar's application boundary and requires
// the transaction context opened by QueryService.
type ExternalClickStore interface {
	ExternalClicks(context.Context, ExternalClickQuery) (ExternalClickPage, error)
}
