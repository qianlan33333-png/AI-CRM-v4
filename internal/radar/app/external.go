package app

import (
	"context"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.ExternalLinkMappingReader = (*Service)(nil)

// ExternalLinkMappings retains the donor external mapping semantics under a
// Radar-owned Port. It is intentionally separate from the admin List query:
// this query uses descending Radar IDs, keyset pagination, a 500-row ceiling,
// and includes disabled historical mappings.
func (service *Service) ExternalLinkMappings(ctx context.Context, query radarport.ExternalLinkMappingQuery) (radarport.ExternalLinkMappingPage, error) {
	if service == nil || service.uow == nil || service.repository == nil {
		return radarport.ExternalLinkMappingPage{}, radarport.ErrUnavailable
	}
	query.RadarCode = strings.TrimSpace(query.RadarCode)
	if (query.RadarID != 0 && !query.RadarID.Valid()) || (query.BeforeRadarID != 0 && !query.BeforeRadarID.Valid()) {
		return radarport.ExternalLinkMappingPage{}, radar.ErrInvalidArgument
	}
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 500 {
		return radarport.ExternalLinkMappingPage{}, radar.ErrInvalidArgument
	}
	reader, ok := service.repository.(radarport.ExternalLinkMappingStore)
	if !ok || reader == nil {
		return radarport.ExternalLinkMappingPage{}, radarport.ErrUnavailable
	}
	var page radarport.ExternalLinkMappingPage
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = reader.ExternalLinkMappings(tx, query)
		return readErr
	})
	if err != nil {
		return radarport.ExternalLinkMappingPage{}, classify(err)
	}
	return page, nil
}
