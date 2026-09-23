package main

import (
	"context"
	"errors"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type groupScheduleTestUow struct{ inside bool }

func (u *groupScheduleTestUow) Within(c context.Context, f func(context.Context) error) error {
	u.inside = true
	defer func() { u.inside = false }()
	return f(c)
}

type groupScheduleTargets struct{}

func (groupScheduleTargets) ActiveGroupRefreshTargets(context.Context) ([]string, error) {
	return []string{"g"}, nil
}

type groupScheduleReader struct {
	fresh bool
	at    time.Time
}

func (g groupScheduleReader) AudienceGroupMembership(context.Context, string, string, time.Time, time.Duration) (wecomport.AudienceGroupMembership, error) {
	if !g.fresh {
		return wecomport.AudienceGroupMembership{}, errors.New("missing")
	}
	return wecomport.AudienceGroupMembership{Complete: true, ObservedAt: g.at}, nil
}

type groupScheduleRefresh struct {
	u      *groupScheduleTestUow
	events *[]string
	t      *testing.T
	fail   bool
}

func (g groupScheduleRefresh) Refresh(context.Context, string) (wecomport.AudienceGroupMembership, error) {
	if g.u.inside {
		g.t.Fatal("Provider I/O inside transaction")
	}
	*g.events = append(*g.events, "refresh")
	if g.fail {
		return wecomport.AudienceGroupMembership{}, errors.New("provider unavailable")
	}
	return wecomport.AudienceGroupMembership{Complete: true}, nil
}

type groupScheduleNext struct{ events *[]string }

func (g groupScheduleNext) ScanScheduled(context.Context) error {
	*g.events = append(*g.events, "scan")
	return nil
}
func TestExistingRiverScanPreparesGroupFactsBeforeScheduling(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fresh, fail bool
	}{{"missing", false, false}, {"fresh", true, false}, {"provider-failure", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			u := &groupScheduleTestUow{}
			at := time.Now()
			s := audienceGroupPreparedSchedule{uow: u, targets: groupScheduleTargets{}, facts: groupScheduleReader{tc.fresh, at}, refresh: groupScheduleRefresh{u, &events, t, tc.fail}, next: groupScheduleNext{&events}, corp: "wecom-corp:c", now: func() time.Time { return at }}
			if e := s.ScanScheduled(context.Background()); e != nil {
				t.Fatal(e)
			}
			if events[len(events)-1] != "scan" {
				t.Fatal("scheduler not reached")
			}
			if tc.fresh && len(events) != 1 || !tc.fresh && (len(events) != 2 || events[0] != "refresh") {
				t.Fatalf("order=%v", events)
			}
		})
	}
}
