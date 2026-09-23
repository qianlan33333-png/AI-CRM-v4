package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func groupTable(kind string) string {
	switch kind {
	case "image":
		return "media_images"
	case "attachment":
		return "media_attachments"
	case "miniprogram":
		return "media_miniprograms"
	}
	return ""
}
func (r *Repository) MaterialGroups(ctx context.Context, kind string) ([]map[string]any, error) {
	table := groupTable(kind)
	if table == "" {
		return nil, ErrInvalid
	}
	out := []map[string]any{}
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		rows, e := tx.Query(txctx, `SELECT g.id,g.name,g.version,count(m.id) FROM media_material_groups g LEFT JOIN `+table+` m ON m.group_id=g.id WHERE g.kind=$1 GROUP BY g.id UNION ALL SELECT 0,'',0,count(*) FROM `+table+` WHERE group_id IS NULL ORDER BY 2`, kind)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var count, id, version int64
			if e = rows.Scan(&id, &name, &version, &count); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "name": name, "version": version, "count": count})
		}
		return rows.Err()
	})
	return out, err
}
func (r *Repository) SetMaterialGroup(ctx context.Context, kind string, id, actor, version int64, key, category string) (map[string]any, error) {
	table := groupTable(kind)
	if table == "" || id < 1 || version < 1 || len([]rune(category)) > 100 || category != strings.TrimSpace(category) || strings.ContainsAny(category, "\x00\r\n") {
		return nil, ErrInvalid
	}
	payload, _ := json.Marshal([]any{id, version, category})
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, e := r.reserve(txctx, kind+".group", kind, actor, key, string(payload))
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var next int64
		e = tx.QueryRow(txctx, `UPDATE `+table+` SET category=$1,version=version+1,updated_at=clock_timestamp(),updated_by=$2 WHERE id=$3 AND version=$4 RETURNING version`, category, actor, id, version).Scan(&next)
		if e == pgx.ErrNoRows {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		out = map[string]any{"id": id, "category": category, "version": next}
		return r.complete(txctx, kind+".group", kind, actor, key, id, out, "media.group_updated")
	})
	return out, err
}
