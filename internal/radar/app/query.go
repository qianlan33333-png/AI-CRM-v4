package app

import (
	"context"
	"strings"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type QueryService struct {
	uow      platformport.UnitOfWork
	query    radarport.QueryService
	visitors radarport.AdminVisitorPresentationReader
}

// BindAdminVisitorPresentation wires the composition-owned, sensitive
// Customer/Identity presentation path. It remains separate from public Radar
// event projections and is only used by the admin visitor query.
func (s *QueryService) BindAdminVisitorPresentation(reader radarport.AdminVisitorPresentationReader) error {
	if s == nil || reader == nil {
		return radarport.ErrUnavailable
	}
	s.visitors = reader
	return nil
}

func NewQueryService(uow platformport.UnitOfWork, query radarport.QueryService) (*QueryService, error) {
	if uow == nil || query == nil {
		return nil, radarport.ErrUnavailable
	}
	return &QueryService{uow: uow, query: query}, nil
}
func (s *QueryService) Stats(ctx context.Context, id radar.RadarID) (radarport.Stats, error) {
	if s == nil || !id.Valid() {
		return radarport.Stats{}, radar.ErrInvalidArgument
	}
	var out radarport.Stats
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.query.Stats(tx, id); return e })
	return out, classify(err)
}
func (s *QueryService) CustomerActivities(ctx context.Context, query radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
	if s == nil || s.uow == nil || s.query == nil || query.CustomerID < 1 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() {
		return radarport.CustomerActivityPage{}, radar.ErrInvalidArgument
	}
	store, ok := s.query.(radarport.CustomerActivityStore)
	if !ok || store == nil {
		return radarport.CustomerActivityPage{}, radarport.ErrUnavailable
	}
	var page radarport.CustomerActivityPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = store.CustomerActivities(tx, query)
		return readErr
	})
	if err != nil {
		return radarport.CustomerActivityPage{}, classify(err)
	}
	return page, nil
}

var _ radarport.CustomerActivityReader = (*QueryService)(nil)

func (s *QueryService) Events(ctx context.Context, q radarport.EventQuery) (radarport.EventPage, error) {
	if s == nil || !q.RadarID.Valid() {
		return radarport.EventPage{}, radar.ErrInvalidArgument
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > 500 || q.Offset < 0 {
		return radarport.EventPage{}, radar.ErrInvalidArgument
	}
	var out radarport.EventPage
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.query.Events(tx, q); return e })
	return out, classify(err)
}

func (s *QueryService) Visitors(ctx context.Context, q radarport.VisitorQuery, search string) (radarport.VisitorPage, error) {
	if s == nil || s.uow == nil || s.query == nil || s.visitors == nil || !q.RadarID.Valid() {
		return radarport.VisitorPage{}, radarport.ErrUnavailable
	}
	search = strings.TrimSpace(search)
	if len([]rune(search)) > radarport.MaximumVisitorSearchChars || q.Limit < 0 || q.Offset < 0 || q.Offset > radarport.MaximumVisitorOffset || q.Start != nil && q.End != nil && !q.Start.Before(*q.End) {
		return radarport.VisitorPage{}, radar.ErrInvalidArgument
	}
	if q.Limit == 0 {
		q.Limit = radarport.MaximumVisitorLimit
	}
	if q.Limit < 1 || q.Limit > radarport.MaximumVisitorLimit {
		return radarport.VisitorPage{}, radar.ErrInvalidArgument
	}
	if search != "" {
		ids, err := s.visitors.SearchRadarVisitors(ctx, search, radarport.MaximumVisitorCandidates+1)
		if err != nil {
			return radarport.VisitorPage{}, classify(err)
		}
		if len(ids) > radarport.MaximumVisitorCandidates {
			return radarport.VisitorPage{}, radarport.ErrVisitorSearchTooWide
		}
		q.CustomerIDs = ids
		q.FilterApplied = true
	}
	store, ok := s.query.(radarport.VisitorStore)
	if !ok || store == nil {
		return radarport.VisitorPage{}, radarport.ErrUnavailable
	}
	var sessions radarport.VisitorSessionPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		sessions, readErr = store.Visitors(tx, q)
		return readErr
	})
	if err != nil {
		return radarport.VisitorPage{}, classify(err)
	}
	items, err := s.visitors.PresentRadarVisitors(ctx, sessions.Items)
	if err != nil {
		return radarport.VisitorPage{}, classify(err)
	}
	if len(items) != len(sessions.Items) {
		return radarport.VisitorPage{}, radarport.ErrUnavailable
	}
	return radarport.VisitorPage{Items: items, Total: sessions.Total, Limit: sessions.Limit, Offset: sessions.Offset, HasMore: sessions.HasMore}, nil
}

var _ radarport.VisitorQueryService = (*QueryService)(nil)
