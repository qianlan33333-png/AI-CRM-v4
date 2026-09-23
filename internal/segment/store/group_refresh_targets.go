package store

import (
	"context"
	"strings"
)

// ActiveGroupRefreshTargets is Segment-owned configuration discovery only.
func (r *Repository) ActiveGroupRefreshTargets(ctx context.Context) ([]string, error) {
	db, e := tx(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := db.Query(ctx, `SELECT DISTINCT c.definition->'parameters'->>'exclude_group_chat' AS chat FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id WHERE p.lifecycle='active' AND c.definition->>'template_key'='member_excluding_group_paid' ORDER BY chat LIMIT 101`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var chat *string
		if e = rows.Scan(&chat); e != nil {
			return nil, e
		}
		if chat == nil || *chat == "" || strings.TrimSpace(*chat) != *chat || len(*chat) > 256 {
			return nil, ErrInvalid
		}
		out = append(out, *chat)
	}
	if len(out) > 100 {
		return nil, ErrInvalid
	}
	return out, rows.Err()
}
