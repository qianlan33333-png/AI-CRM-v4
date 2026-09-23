package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.ExternalClickStore = (*Postgres)(nil)

// ExternalClicks groups technical stages into a single logical opening per
// (session_id, radar_version), preserving the first successful stage and its
// time. The CTE deliberately filters CustomerIDs before it applies the
// keyset/limit, so a scoped machine read cannot paginate across another
// customer's clicks.
func (store *Postgres) ExternalClicks(ctx context.Context, query radarport.ExternalClickQuery) (radarport.ExternalClickPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ExternalClickPage{}, err
	}
	ids := make([]int64, 0, len(query.CustomerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(query.CustomerIDs))
	for _, id := range query.CustomerIDs {
		if id < 1 {
			return radarport.ExternalClickPage{}, radarport.ErrUnavailable
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	args := []any{query.FilterApplied || len(ids) > 0, ids}
	conditions := []string{
		"event.stage=ANY(ARRAY['content_opened','redirected','image_loaded','pdf_opened']::text[])",
		"session.attribution_status=ANY(ARRAY['resolved','pending','conflict']::text[])",
		"(NOT $1::boolean OR session.customer_id=ANY($2::bigint[]))",
	}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if query.RadarID != 0 {
		add("session.radar_id=$%d", query.RadarID)
	}
	if query.RadarCode != "" {
		add("link.public_code=$%d", query.RadarCode)
	}
	if query.SessionID != 0 {
		add("session.id=$%d", query.SessionID)
	}
	base := `WITH clicks AS (
		SELECT session.id AS session_id,session.radar_id,link.public_code,session.attribution_status,session.customer_id,
			min(event.occurred_at) AS opened_at,
			(array_agg(event.id ORDER BY event.occurred_at ASC,event.id ASC))[1] AS first_event_id,
			(array_agg(event.stage ORDER BY event.occurred_at ASC,event.id ASC))[1] AS first_stage
		FROM radar_view_sessions session
		JOIN radar_events event ON event.session_id=session.id AND event.radar_version=session.radar_version
		JOIN radar_links link ON link.id=session.radar_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		GROUP BY session.id,session.radar_id,link.public_code,session.attribution_status,session.customer_id
	)`
	outer := []string{"TRUE"}
	if query.Start != nil {
		args = append(args, query.Start.UTC())
		outer = append(outer, "opened_at >= $"+strconv.Itoa(len(args)))
	}
	if query.End != nil {
		args = append(args, query.End.UTC())
		outer = append(outer, "opened_at < $"+strconv.Itoa(len(args)))
	}
	if !query.BeforeOpened.IsZero() {
		args = append(args, query.BeforeOpened.UTC(), query.BeforeEventID)
		outer = append(outer, "(opened_at,first_event_id) < ($"+strconv.Itoa(len(args)-1)+",$"+strconv.Itoa(len(args))+")")
	}
	args = append(args, query.Limit+1)
	rows, err := tx.Query(ctx, base+` SELECT session_id,first_event_id,radar_id,public_code,opened_at,first_stage,attribution_status,customer_id
		FROM clicks WHERE `+strings.Join(outer, " AND ")+` ORDER BY opened_at DESC,first_event_id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return radarport.ExternalClickPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.ExternalClick, 0, query.Limit+1)
	for rows.Next() {
		var item radarport.ExternalClick
		var customerID *int64
		if err = rows.Scan(&item.SessionID, &item.EventID, &item.RadarID, &item.RadarCode, &item.OpenedAt, &item.OpenStage, &item.AttributionStatus, &customerID); err != nil {
			return radarport.ExternalClickPage{}, mapError(err)
		}
		if customerID != nil {
			id := customerdomain.CustomerID(*customerID)
			item.CustomerID = &id
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.ExternalClickPage{}, mapError(err)
	}
	hasMore := len(items) > int(query.Limit)
	if hasMore {
		items = items[:query.Limit]
	}
	return radarport.ExternalClickPage{Items: items, HasMore: hasMore}, nil
}
