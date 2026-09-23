package main

import (
	"context"
	"fmt"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"sort"
	"strings"
	"time"
)

// Owner reads remain bounded and customer scoped. No domain table is read here.
// IDs are display/cursor keys only, never customer IDs or business references.
type sidebarBusinessTimeline struct {
	surveys  surveyport.CustomerHistoryReader
	orders   orderport.CustomerActivityReader
	radar    radarport.CustomerActivityReader
	channels channelport.CustomerActivityReader
	uow      platformport.UnitOfWork
}

func (sidebarBusinessTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (a sidebarBusinessTimeline) CustomerTimeline(ctx context.Context, id customerdomain.CustomerID, q customerport.PageQuery) (customerport.TimelinePage, error) {
	unavailable := func() (customerport.TimelinePage, error) {
		return customerport.TimelinePage{}, customerport.ErrSectionUnavailable
	}
	if id < 1 || q.Limit < 1 || q.Limit > 101 || q.Watermark.IsZero() || q.AfterID < 0 || q.Filter != "" || (!q.AfterAt.IsZero() && (q.AfterID < 4 || q.AfterAt.After(q.Watermark))) {
		return unavailable()
	}
	if a.surveys == nil || a.orders == nil || a.radar == nil || a.channels == nil || a.uow == nil {
		return unavailable()
	}
	items := make([]customerport.TimelineItem, 0, q.Limit*4)
	add := func(slot, sourceID int64, event, title, domain string, at time.Time) bool {
		// Keep exact integer representation in browser JSON and prevent overflow.
		if sourceID < 1 || sourceID > (9007199254740991-3)/4 || at.IsZero() || at.After(q.Watermark) {
			return false
		}
		key := sourceID*4 + slot
		if !q.AfterAt.IsZero() && (at.After(q.AfterAt) || (at.Equal(q.AfterAt) && key >= q.AfterID)) {
			return false
		}
		items = append(items, customerport.TimelineItem{ID: key, EventType: event, Title: title, SourceDomain: domain, OccurredAt: at})
		return true
	}
	boundary := func(slot int64) int64 {
		if q.AfterAt.IsZero() {
			return 0
		}
		n := q.AfterID / 4
		if slot < q.AfterID%4 {
			n++
		}
		return n
	}
	surveys, err := a.surveys.CustomerHistoryWindow(ctx, surveyport.CustomerHistoryQuery{CustomerID: int64(id), Limit: int32(q.Limit), Watermark: q.Watermark, AfterAt: q.AfterAt, AfterID: surveyport.ID(boundary(0))})
	if err != nil {
		return unavailable()
	}
	for _, v := range surveys.Items {
		if !add(0, int64(v.ID), "survey.submitted", "提交问卷："+timelineObjectName(v.QuestionnaireTitle, "问卷", int64(v.QuestionnaireID)), "survey", v.SubmittedAt) {
			return unavailable()
		}
	}
	var channels customerport.TimelinePage
	err = a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		cq := q
		cq.AfterID = boundary(1)
		channels, e = a.channels.CustomerChannelActivities(tx, id, cq)
		return e
	})
	if err != nil {
		return unavailable()
	}
	for _, v := range channels.Items {
		if !add(1, v.ID, v.EventType, v.Title, "channel", v.OccurredAt) {
			return unavailable()
		}
	}
	orders, err := a.orders.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: int64(id), Limit: int32(q.Limit), Watermark: q.Watermark, AfterAt: q.AfterAt, AfterID: boundary(2)})
	if err != nil {
		return unavailable()
	}
	for _, v := range orders.Items {
		state := map[string]string{"pending_payment": "待支付", "paid": "已支付", "payment_failed": "支付失败", "cancelled": "已取消", "closed": "已关闭", "refunded": "已退款", "partially_refunded": "部分退款"}[string(v.Status)]
		if state == "" {
			state = "状态待核实"
		}
		action := "创建订单（" + state + "）："
		if v.Relationship == "beneficiary" {
			action = "受益订单（" + state + "）："
		}
		title := action + timelineObjectName(strings.Join(v.ProductNames, "、"), "订单", v.OrderID)
		if !add(2, v.OrderID, "order.created", title, "order", v.OccurredAt) {
			return unavailable()
		}
	}
	radar, err := a.radar.CustomerActivities(ctx, radarport.CustomerActivityQuery{BusinessOnly: true, CustomerID: id, Limit: int32(q.Limit), Watermark: q.Watermark, AfterAt: q.AfterAt, AfterID: boundary(3)})
	if err != nil {
		return unavailable()
	}
	for _, v := range radar.Items {
		action := map[radarport.EventStage]string{radarport.EventLanding: "访问雷达", radarport.EventOAuthStarted: "开始雷达授权", radarport.EventOAuthVerified: "完成雷达授权", radarport.EventIdentityResolved: "雷达身份已确认", radarport.EventContentOpened: "打开雷达内容", radarport.EventRedirected: "点击雷达链接", radarport.EventImageLoaded: "查看雷达图片", radarport.EventPDFOpened: "查看雷达文档", radarport.EventFailed: "雷达访问失败"}[v.Stage]
		if action == "" {
			action = "雷达活动"
		}
		if !add(3, v.EventID, "radar."+string(v.Stage), action+"："+timelineObjectName(v.Title, "雷达", int64(v.RadarID)), "radar", v.OccurredAt) {
			return unavailable()
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].OccurredAt.After(items[j].OccurredAt)
		}
		return items[i].ID > items[j].ID
	})
	if len(items) > q.Limit {
		items = items[:q.Limit]
	}
	at := q.Watermark.UTC()
	return customerport.TimelinePage{Items: items, Status: customerport.SectionStatus{State: customerport.SectionReady, AsOf: &at}}, nil
}

func timelineObjectName(name, kind string, id int64) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return fmt.Sprintf("%s #%d", kind, id)
	}
	return name
}
