package app

import (
	"context"
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"golang.org/x/sync/singleflight"
	"time"
)

// detailReadFailure means the failed observation was persisted successfully.
type detailReadFailure struct{ error }

func (e detailReadFailure) Unwrap() error { return e.error }

type CatalogStore interface {
	p.CatalogReader
	CatalogStatus(context.Context) (p.CatalogStatus, error)
	SaveCatalogGroup(context.Context, p.CatalogGroup, int64) error
	WithinCatalogRequest(context.Context, bool, time.Time, func(context.Context, int64) error) error
	StartCatalogRun(context.Context, int64) (p.CatalogRun, error)
	CheckpointCatalogRun(context.Context, int64, string) error
	FinishCatalogRun(context.Context, int64, bool, time.Time, func(context.Context, int64) error) error
}
type CatalogService struct {
	RetryDetail func(context.Context, string) error
	reads       singleflight.Group
	Owner       func(context.Context, string) (int64, error)
	Store       CatalogStore
	Provider    w.AllGroupChatReader
	Enqueue     func(context.Context, int64) error
	Enabled     bool
}

func (s *CatalogService) ListCatalog(ctx context.Context, q string, l, o int) (p.CatalogPage, error) {
	return s.Store.ListCatalog(ctx, q, l, o)
}
func (s *CatalogService) ReadCatalogGroup(ctx context.Context, id string) (p.CatalogGroup, error) {
	return s.Store.ReadCatalogGroup(ctx, id)
}
func (s *CatalogService) CatalogStatus(ctx context.Context) (p.CatalogStatus, error) {
	v, e := s.Store.CatalogStatus(ctx)
	v.Enabled = s.Enabled
	return v, e
}
func (s *CatalogService) RequestCatalogSync(ctx context.Context, manual bool) (p.CatalogStatus, error) {
	if !s.Enabled || s.Provider == nil || s.Enqueue == nil {
		return p.CatalogStatus{}, ErrProviderDisabled
	}
	if err := s.Store.WithinCatalogRequest(ctx, manual, time.Now().UTC(), s.Enqueue); err != nil {
		return p.CatalogStatus{}, err
	}
	return s.CatalogStatus(ctx)
}
func (s *CatalogService) RefreshCatalogGroup(ctx context.Context, id string) (p.CatalogGroup, error) {
	return s.refresh(ctx, id, 0)
}

type catalogReadResult struct {
	group p.CatalogGroup
	run   int64
}

func (s *CatalogService) refresh(ctx context.Context, id string, run int64) (p.CatalogGroup, error) {
	value, err, _ := s.reads.Do(id, func() (any, error) {
		group, e := s.fetch(ctx, id, run)
		return catalogReadResult{group, run}, e
	})
	result := value.(catalogReadResult)
	// Coalesced callers still record their own full-scan observation, without
	// issuing another Provider request or advancing the observation timestamp.
	if run > 0 && run != result.run {
		var detailErr detailReadFailure
		if err == nil || errors.As(err, &detailErr) {
			if saveErr := s.Store.SaveCatalogGroup(ctx, result.group, run); saveErr != nil {
				return result.group, saveErr
			}
		}
	}
	return result.group, err
}
func (s *CatalogService) fetch(ctx context.Context, id string, run int64) (p.CatalogGroup, error) {
	if !s.Enabled || s.Provider == nil {
		return p.CatalogGroup{}, ErrProviderDisabled
	}
	// Capture before the request: a slower earlier request must never win over
	// a newer observation. Failed reads preserve the last successful facts.
	now := time.Now().UTC()
	g := p.CatalogGroup{ChatID: id, State: "failed", CheckedAt: now}
	detail, readErr := s.Provider.GetGroupChat(ctx, id)
	if readErr == nil && detail.ChatID == id {
		g.Name = detail.Name
		g.OwnerUserID = detail.OwnerUserID
		if s.Owner != nil {
			owner, err := s.Owner(ctx, detail.OwnerUserID)
			if err != nil {
				return g, err
			}
			g.OwnerStaffID = owner
		}
		g.MemberCount = detail.MemberCount
		g.ExternalMemberCount = detail.ExternalMemberCount
		g.State = "ready"
		g.ObservedAt = &now
	} else if readErr == nil {
		readErr = errors.New("group detail mismatch")
	}
	if err := s.Store.SaveCatalogGroup(ctx, g, run); err != nil {
		return g, err
	}
	if readErr != nil {
		return g, detailReadFailure{readErr}
	}
	return g, nil
}
func (s *CatalogService) ProcessCatalog(ctx context.Context, id int64) error {
	if !s.Enabled || s.Provider == nil {
		return ErrProviderDisabled
	}
	run, err := s.Store.StartCatalogRun(ctx, id)
	if err != nil {
		return err
	}
	if run.State != "running" {
		return nil
	}
	seen := map[string]bool{}
	for {
		if seen[run.Cursor] {
			return errors.New("group catalog cursor repeated")
		}
		seen[run.Cursor] = true
		page, err := s.Provider.ListAllGroupChats(ctx, run.Cursor, 100)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Detail failures are recorded per group. Continue discovering other groups.
			cached, cacheErr := s.Store.ReadCatalogGroup(ctx, item.ChatID)
			if cacheErr == nil && cached.State == "ready" && cached.ObservedAt != nil && !cached.ObservedAt.Before(run.StartedAt) {
				err = s.Store.SaveCatalogGroup(ctx, cached, id)
			} else {
				_, err = s.refresh(ctx, item.ChatID, id)
			}
			if err != nil {
				var detailErr detailReadFailure
				if !errors.As(err, &detailErr) {
					return err
				}
				if s.RetryDetail != nil {
					if retryErr := s.RetryDetail(ctx, item.ChatID); retryErr != nil {
						return retryErr
					}
				}
			}
		}
		if page.NextCursor == "" {
			return s.Store.FinishCatalogRun(ctx, id, false, time.Now().UTC(), s.Enqueue)
		}
		if err = s.Store.CheckpointCatalogRun(ctx, id, page.NextCursor); err != nil {
			return err
		}
		run.Cursor = page.NextCursor
	}
}
func (s *CatalogService) FailCatalog(ctx context.Context, id int64) error {
	return s.Store.FinishCatalogRun(ctx, id, true, time.Now().UTC(), s.Enqueue)
}
