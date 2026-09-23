package main

import (
	"context"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"time"
)

// Runs inside the existing River schedule scan, before ordinary occurrence
// dispatch. Every Provider read is outside UoW. Missing groups stop only their
// own evaluation; unrelated audiences retain the existing scheduler behavior.
type audienceGroupPreparedSchedule struct {
	uow     platformport.UnitOfWork
	targets segmentport.GroupRefreshTargetReader
	facts   wecomport.AudienceGroupMembershipReader
	refresh interface {
		Refresh(context.Context, string) (wecomport.AudienceGroupMembership, error)
	}
	next interface{ ScanScheduled(context.Context) error }
	corp string
	now  func() time.Time
}

func (s *audienceGroupPreparedSchedule) ScanScheduled(ctx context.Context) error {
	if s.refresh == nil {
		return s.next.ScanScheduled(ctx)
	}
	var targets []string
	if e := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		targets, e = s.targets.ActiveGroupRefreshTargets(tx)
		return e
	}); e != nil {
		return e
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	for _, chat := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fresh := false
		if e := s.uow.Within(ctx, func(tx context.Context) error {
			f, e := s.facts.AudienceGroupMembership(tx, s.corp, chat, now().UTC(), 5*time.Minute)
			fresh = e == nil && f.Complete
			return nil
		}); e != nil {
			return e
		}
		if !fresh {
			_, _ = s.refresh.Refresh(ctx, chat)
		} // Owner persists safe failure summary; evaluator fails closed.
	}
	return s.next.ScanScheduled(ctx)
}
