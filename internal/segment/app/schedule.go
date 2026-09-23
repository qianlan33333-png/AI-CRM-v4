package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	"github.com/robfig/cron/v3"
)

type ScheduledRefreshStore interface {
	ScheduledConfigurations(context.Context, int) ([]segmentdomain.ScheduledConfiguration, error)
	ClaimScheduledOccurrence(context.Context, segmentdomain.ScheduledConfiguration, time.Time, time.Time, time.Time) (bool, error)
}

type ScheduledRefreshAccepter interface {
	AcceptRefreshWithin(context.Context, RefreshCommand) (segmentdomain.RefreshRun, error)
}

type ScheduledRefreshService struct {
	uow      platformport.UnitOfWork
	store    ScheduledRefreshStore
	refresh  ScheduledRefreshAccepter
	now      func() time.Time
	maxItems int
}

func NewScheduledRefreshService(uow platformport.UnitOfWork, store ScheduledRefreshStore, refresh ScheduledRefreshAccepter) (*ScheduledRefreshService, error) {
	if uow == nil || store == nil || refresh == nil {
		return nil, ErrNotReady
	}
	return &ScheduledRefreshService{uow: uow, store: store, refresh: refresh, now: time.Now, maxItems: 10000}, nil
}

func ValidateRefreshCronUTC(expression string) error {
	if expression == "" {
		return nil
	}
	if _, err := cron.ParseStandard(expression); err != nil {
		return ErrInvalid
	}
	return nil
}

// ValidateRefresh preserves arbitrary historical UTC cron expressions under
// legacy_custom. New donor modes derive their schedules below and therefore
// must not smuggle a second cron expression into the configuration version.
func ValidateRefresh(mode, expression string) error {
	if mode == "" {
		mode = "legacy_custom"
	}
	switch mode {
	case "legacy_custom":
		return ValidateRefreshCronUTC(expression)
	case "manual", "every_3m", "daily_0200", "every_3m_plus_daily_0200":
		if expression != "" {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}

func (s *ScheduledRefreshService) ScanScheduled(ctx context.Context) error {
	if s == nil {
		return ErrNotReady
	}
	now := s.now().UTC().Truncate(time.Second)
	var items []segmentdomain.ScheduledConfiguration
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, err = s.store.ScheduledConfigurations(tx, s.maxItems)
		return err
	}); err != nil {
		return classify(err)
	}
	if len(items) == s.maxItems {
		return ErrUnavailable
	}
	type dueItem struct {
		item       segmentdomain.ScheduledConfiguration
		occurrence time.Time
		next       time.Time
	}
	groups := map[string][]dueItem{}
	order := []string{}
	for _, item := range items {
		schedule, err := cron.ParseStandard(item.CronUTC)
		if err != nil {
			return fmt.Errorf("scheduled audience configuration %d: %w", item.ConfigurationVersionID, ErrInvalid)
		}
		occurrence, next, due, err := dueOccurrence(schedule, item, now)
		if err != nil {
			return err
		}
		if !due {
			continue
		}
		key := fmt.Sprintf("%d:%d:%s", item.PackageID, item.ConfigurationVersionID, occurrence.Format(time.RFC3339Nano))
		if _, found := groups[key]; !found {
			order = append(order, key)
		}
		groups[key] = append(groups[key], dueItem{item: item, occurrence: occurrence, next: next})
	}
	for _, key := range order {
		group := groups[key]
		err := s.uow.Within(ctx, func(tx context.Context) error {
			claimedAny := false
			kind := segmentdomain.RefreshIncremental
			for _, due := range group {
				// Both cursor advances and the one shared refresh acceptance are
				// transaction-bound. Daily wins before a River job can observe it.
				if due.item.Kind == "daily" {
					kind = segmentdomain.RefreshDaily
				}
				if due.item.Kind == "legacy" {
					kind = segmentdomain.RefreshLegacy
				}
				claimed, claimErr := s.store.ClaimScheduledOccurrence(tx, due.item, due.occurrence, due.next, now)
				if claimErr != nil {
					return claimErr
				}
				claimedAny = claimedAny || claimed
			}
			if !claimedAny {
				return nil
			}
			first := group[0]
			actor, actorErr := storedMutationActor(first.item.Actor, first.item.ActorKind, first.item.ActorReference)
			if actorErr != nil {
				return ErrInvalid
			}
			digest := sha256.Sum256([]byte(fmt.Sprintf("audience.schedule.v1:%d:%d:%s", first.item.PackageID, first.item.ConfigurationVersionID, first.occurrence.Format(time.RFC3339))))
			_, claimErr := s.refresh.AcceptRefreshWithin(tx, RefreshCommand{PackageID: first.item.PackageID, Actor: actor.StaffID, MutationActor: actor, IdempotencyKey: "schedule-" + hex.EncodeToString(digest[:]), ReferenceTime: first.occurrence, RefreshKind: kind})
			return claimErr
		})
		if err != nil {
			return classify(err)
		}
	}
	return nil
}

func dueOccurrence(schedule cron.Schedule, item segmentdomain.ScheduledConfiguration, now time.Time) (time.Time, time.Time, bool, error) {
	if schedule == nil || item.ConfigurationCreatedAt.IsZero() || now.IsZero() {
		return time.Time{}, time.Time{}, false, ErrInvalid
	}
	start := item.ConfigurationCreatedAt.UTC()
	if item.NextDueAt != nil {
		start = item.NextDueAt.UTC().Add(-time.Second)
	}
	occurrence := schedule.Next(start)
	if occurrence.After(now) {
		return occurrence, occurrence, false, nil
	}
	// Collapse downtime to the latest due occurrence. The resulting refresh is
	// idempotent for that scheduled timestamp, while intermediate stale runs are
	// intentionally skipped.
	for steps := 0; steps < 600000; steps++ {
		next := schedule.Next(occurrence)
		if next.After(now) {
			return occurrence.UTC(), next.UTC(), true, nil
		}
		if !next.After(occurrence) {
			return time.Time{}, time.Time{}, false, ErrInvalid
		}
		occurrence = next
	}
	return time.Time{}, time.Time{}, false, errors.New("schedule catch-up limit exceeded")
}
