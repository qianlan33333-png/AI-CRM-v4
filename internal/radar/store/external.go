package store

import (
	"context"
	"strconv"
	"strings"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.ExternalLinkMappingStore = (*Postgres)(nil)

// ExternalLinkMappings is the Radar-owned storage implementation of the
// donor-compatible mapping read. radar_links has no soft-delete state in V3;
// every persisted row is therefore a retained mapping, including disabled and
// draft records.
func (store *Postgres) ExternalLinkMappings(ctx context.Context, query radarport.ExternalLinkMappingQuery) (radarport.ExternalLinkMappingPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ExternalLinkMappingPage{}, err
	}
	conditions := []string{"TRUE"}
	args := make([]any, 0, 5)
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, column+"=$"+strconv.Itoa(len(args)))
	}
	if query.RadarID != 0 {
		add("id", query.RadarID)
	}
	if query.RadarCode != "" {
		add("public_code", query.RadarCode)
	}
	where := strings.Join(conditions, " AND ")
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM radar_links WHERE `+where, args...).Scan(&total); err != nil {
		return radarport.ExternalLinkMappingPage{}, mapError(err)
	}
	if query.BeforeRadarID != 0 {
		args = append(args, query.BeforeRadarID)
		conditions = append(conditions, "id < $"+strconv.Itoa(len(args)))
	}
	args = append(args, query.Limit+1)
	rows, err := tx.Query(ctx, `SELECT id,public_code,title,status FROM radar_links WHERE `+strings.Join(conditions, " AND ")+` ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return radarport.ExternalLinkMappingPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.ExternalLinkMapping, 0, query.Limit)
	for rows.Next() {
		var item radarport.ExternalLinkMapping
		if err = rows.Scan(&item.RadarID, &item.RadarCode, &item.Title, &item.Status); err != nil {
			return radarport.ExternalLinkMappingPage{}, mapError(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.ExternalLinkMappingPage{}, mapError(err)
	}
	hasMore := len(items) > int(query.Limit)
	if hasMore {
		items = items[:query.Limit]
	}
	return radarport.ExternalLinkMappingPage{Items: items, Total: total, HasMore: hasMore}, nil
}
