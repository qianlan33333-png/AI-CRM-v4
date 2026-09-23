package port

import (
	"context"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

// ExternalLinkMappingQuery is the frozen, narrow Radar query used by the
// external machine API. It deliberately includes every retained link status:
// historical click mappings need disabled links as well as active ones.
type ExternalLinkMappingQuery struct {
	RadarID       radar.RadarID
	RadarCode     string
	BeforeRadarID radar.RadarID
	Limit         int32
}

type ExternalLinkMapping struct {
	RadarID   radar.RadarID
	RadarCode string
	Title     string
	Status    radar.Status
}

type ExternalLinkMappingPage struct {
	Items   []ExternalLinkMapping
	Total   int64
	HasMore bool
}

// ExternalLinkMappingReader is the Radar-owned read boundary. Consumers do
// not obtain a Radar repository or query the radar_links table directly.
type ExternalLinkMappingReader interface {
	ExternalLinkMappings(context.Context, ExternalLinkMappingQuery) (ExternalLinkMappingPage, error)
}

// ExternalLinkMappingStore is internal to Radar's application boundary and
// requires a transaction-bound context supplied by the Radar service.
type ExternalLinkMappingStore interface {
	ExternalLinkMappings(context.Context, ExternalLinkMappingQuery) (ExternalLinkMappingPage, error)
}
