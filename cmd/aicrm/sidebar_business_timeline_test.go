package main

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"strings"
	"testing"
	"time"
)

type businessTimelineFixture struct {
	at   time.Time
	fail bool
}

func (f businessTimelineFixture) include(customer int64, at time.Time, id int64) bool {
	return customer == 42 && (at.IsZero() || f.at.Before(at) || (f.at.Equal(at) && 1 < id))
}

type timelineSurveyFixture struct{ businessTimelineFixture }

func (f timelineSurveyFixture) CustomerHistoryWindow(_ context.Context, q surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
	p := surveyport.CustomerHistoryWindow{}
	if f.include(q.CustomerID, q.AfterAt, int64(q.AfterID)) {
		p.Items = []surveyport.Submission{{ID: 1, QuestionnaireID: 9, QuestionnaireTitle: "成长问卷", SubmittedAt: f.at}}
	}
	return p, nil
}

type timelineOrderFixture struct{ businessTimelineFixture }

func (f timelineOrderFixture) CustomerActivities(_ context.Context, q orderport.CustomerActivityQuery) (orderport.CustomerActivityPage, error) {
	p := orderport.CustomerActivityPage{}
	if f.include(q.CustomerID, q.AfterAt, q.AfterID) {
		p.Items = []orderport.CustomerActivity{{OrderID: 1, Relationship: "payer", Status: "pending_payment", ProductNames: []string{"成长课程"}, OccurredAt: f.at}}
	}
	return p, nil
}

type timelineRadarFixture struct{ businessTimelineFixture }

func (f timelineRadarFixture) CustomerActivities(_ context.Context, q radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
	if f.fail {
		return radarport.CustomerActivityPage{}, errors.New("owner unavailable")
	}
	p := radarport.CustomerActivityPage{}
	if f.include(int64(q.CustomerID), q.AfterAt, q.AfterID) {
		p.Items = []radarport.CustomerActivity{{EventID: 1, RadarID: 3, Title: "课程介绍", Stage: radarport.EventRedirected, OccurredAt: f.at}}
	}
	return p, nil
}

type timelineChannelFixture struct{ businessTimelineFixture }

func (f timelineChannelFixture) CustomerChannelActivities(_ context.Context, id customerdomain.CustomerID, q customerport.PageQuery) (customerport.TimelinePage, error) {
	p := customerport.TimelinePage{}
	if f.include(int64(id), q.AfterAt, q.AfterID) {
		p.Items = []customerport.TimelineItem{{ID: 1, Title: "通过渠道码添加：秋季活动", EventType: "channel.entered", OccurredAt: f.at}}
	}
	return p, nil
}

type timelineUOW struct{}

func (timelineUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
func timelineFixture(f businessTimelineFixture) sidebarBusinessTimeline {
	return sidebarBusinessTimeline{surveys: timelineSurveyFixture{f}, orders: timelineOrderFixture{f}, radar: timelineRadarFixture{f}, channels: timelineChannelFixture{f}, uow: timelineUOW{}}
}

func TestSidebarBusinessTimelineNamedSourcesAndCrossSourceKeyset(t *testing.T) {
	at := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	a := timelineFixture(businessTimelineFixture{at: at})
	q := customerport.PageQuery{Limit: 2, Watermark: at.Add(time.Hour)}
	seen := map[int64]bool{}
	titles := []string{}
	for i := 0; i < 3; i++ {
		p, err := a.CustomerTimeline(context.Background(), 42, q)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status.State != customerport.SectionReady {
			t.Fatal(p.Status)
		}
		for _, v := range p.Items {
			if seen[v.ID] {
				t.Fatalf("duplicate cursor key %d", v.ID)
			}
			seen[v.ID] = true
			titles = append(titles, v.Title)
		}
		if len(p.Items) > 0 {
			last := p.Items[len(p.Items)-1]
			q.AfterAt = last.OccurredAt
			q.AfterID = last.ID
		}
	}
	if len(seen) != 4 {
		t.Fatalf("missing activities: %v", titles)
	}
	joined := strings.Join(titles, "|")
	for _, name := range []string{"提交问卷：成长问卷", "秋季活动", "创建订单（待支付）：成长课程", "点击雷达链接：课程介绍"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("missing %s: %s", name, joined)
		}
	}
	if strings.Contains(joined, "已购买") || strings.Contains(joined, "资料已同步") {
		t.Fatal(joined)
	}
	q.AfterAt = time.Time{}
	q.AfterID = 0
	other, err := a.CustomerTimeline(context.Background(), 43, q)
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("customer isolation: %+v %v", other, err)
	}
}
func TestSidebarBusinessTimelineSourceFailureIsNotEmptySuccess(t *testing.T) {
	at := time.Now().UTC()
	a := timelineFixture(businessTimelineFixture{at: at, fail: true})
	p, err := a.CustomerTimeline(context.Background(), 42, customerport.PageQuery{Limit: 20, Watermark: at.Add(time.Hour)})
	if !errors.Is(err, customerport.ErrSectionUnavailable) || len(p.Items) != 0 {
		t.Fatalf("page=%+v err=%v", p, err)
	}
}
