package store

import (
	"context"
	"regexp"
	"strings"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	timelineSourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	timelineTypePattern   = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,95}$`)
)

var _ customerport.TimelineWriter = PostgreSQL{}

func (PostgreSQL) AppendTimeline(ctx context.Context, event customerport.TimelineEvent) error {
	if event.CustomerID < 1 || !timelineSourcePattern.MatchString(event.SourceDomain) ||
		!timelineTypePattern.MatchString(event.EventType) || !safeTimelineText(event.SourceEventID, 160) ||
		!safeTimelineText(event.Title, 200) || event.OccurredAt.IsZero() {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_timeline_projection(customer_id,source_domain,source_event_id,event_type,title,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(source_domain,source_event_id) DO NOTHING`, event.CustomerID,
		event.SourceDomain, event.SourceEventID, event.EventType, event.Title, event.OccurredAt.UTC())
	return err
}

func (PostgreSQL) CustomerTimeline(ctx context.Context, customerID customerdomain.CustomerID, query customerport.PageQuery) (customerport.TimelinePage, error) {
	if customerID < 1 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() || len(query.Filter) > 96 {
		return customerport.TimelinePage{}, customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TimelinePage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,event_type,title,source_domain,occurred_at
		FROM customer_timeline_projection
		WHERE customer_id=$1 AND occurred_at <= $2 AND ($3::text='' OR event_type=$3)
		AND ($4::timestamptz IS NULL OR (occurred_at,id) < ($4,$5))
		ORDER BY occurred_at DESC,id DESC LIMIT $6`, customerID, query.Watermark.UTC(), query.Filter,
		nullableTime(query.AfterAt), query.AfterID, query.Limit)
	if err != nil {
		return customerport.TimelinePage{}, err
	}
	defer rows.Close()
	page := customerport.TimelinePage{Items: []customerport.TimelineItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}
	for rows.Next() {
		var item customerport.TimelineItem
		if err = rows.Scan(&item.ID, &item.EventType, &item.Title, &item.SourceDomain, &item.OccurredAt); err != nil {
			return customerport.TimelinePage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return customerport.TimelinePage{}, err
	}
	asOf := query.Watermark.UTC()
	page.Status.AsOf = &asOf
	return page, nil
}

func safeTimelineText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len([]rune(value)) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}
