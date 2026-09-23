package app

import (
	"context"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.ExternalClickReader = (*QueryService)(nil)

// ExternalClicks serves a Radar-owned, session-level projection for the V1
// machine API. It does not resolve identities or expand Customer scopes: the
// authenticated composition Host supplies canonical, pre-authorized IDs.
func (s *QueryService) ExternalClicks(ctx context.Context, query radarport.ExternalClickQuery) (radarport.ExternalClickPage, error) {
	if s == nil || s.uow == nil || s.query == nil {
		return radarport.ExternalClickPage{}, radarport.ErrUnavailable
	}
	query.RadarCode = strings.TrimSpace(query.RadarCode)
	if (query.RadarID != 0 && !query.RadarID.Valid()) || query.SessionID < 0 || query.BeforeEventID < 0 ||
		(query.BeforeOpened.IsZero() != (query.BeforeEventID == 0)) ||
		(query.Start != nil && query.End != nil && !query.Start.Before(*query.End)) {
		return radarport.ExternalClickPage{}, radar.ErrInvalidArgument
	}
	if query.Limit == 0 {
		query.Limit = radarport.MaximumLimit
	}
	if query.Limit < 1 || query.Limit > radarport.MaximumLimit {
		return radarport.ExternalClickPage{}, radar.ErrInvalidArgument
	}
	for _, id := range query.CustomerIDs {
		if id < 1 {
			return radarport.ExternalClickPage{}, radar.ErrInvalidArgument
		}
	}
	store, ok := s.query.(radarport.ExternalClickStore)
	if !ok || store == nil {
		return radarport.ExternalClickPage{}, radarport.ErrUnavailable
	}
	var page radarport.ExternalClickPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = store.ExternalClicks(tx, query)
		return readErr
	})
	if err != nil {
		return radarport.ExternalClickPage{}, classify(err)
	}
	return page, nil
}
