package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type GroupMember struct {
	ID      int64 `json:"id"`
	Version int64 `json:"expected_version"`
}
type GroupCommand struct {
	ID      int64         `json:"id"`
	Version int64         `json:"expected_version"`
	Name    string        `json:"name"`
	GroupID *int64        `json:"group_id"`
	Items   []GroupMember `json:"items"`
}

func (r *Repository) ManageMaterialGroup(ctx context.Context, kind, operation string, actor int64, key string, command GroupCommand) (map[string]any, error) {
	table := groupTable(kind)
	if table == "" {
		return nil, ErrInvalid
	}
	command.Name = strings.TrimSpace(command.Name)
	if operation == "create" || operation == "rename" {
		if command.Name == "" || command.Name == "全部分组" || command.Name == "未分组" || !utf8.ValidString(command.Name) || len([]rune(command.Name)) > 100 || strings.ContainsAny(command.Name, "\x00\r\n") {
			return nil, ErrInvalid
		}
	}
	if operation != "create" && operation != "rename" && operation != "delete" && operation != "move" {
		return nil, ErrInvalid
	}
	if (operation == "rename" || operation == "delete") && (command.ID < 1 || command.Version < 1) {
		return nil, ErrInvalid
	}
	if operation == "move" {
		if len(command.Items) < 1 || len(command.Items) > 200 || (command.GroupID != nil && *command.GroupID < 1) {
			return nil, ErrInvalid
		}
		sort.Slice(command.Items, func(i, j int) bool { return command.Items[i].ID < command.Items[j].ID })
		for i, item := range command.Items {
			if item.ID < 1 || item.Version < 1 || (i > 0 && command.Items[i-1].ID == item.ID) {
				return nil, ErrInvalid
			}
		}
	}
	payload, _ := json.Marshal(command)
	var out map[string]any
	err := r.Within(ctx, func(c context.Context) error {
		replay, owned, e := r.reserve(c, "material.group."+operation, kind, actor, key, string(payload))
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(c)
		// Serialize management commands per type before taking material row locks.
		// Legacy category writers remain protected by FK/row locks and rollback on conflict.
		if _, e = tx.Exec(c, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "media.groups."+kind); e != nil {
			return e
		}
		out = map[string]any{}
		switch operation {
		case "create":
			var id int64
			e = tx.QueryRow(c, `INSERT INTO media_material_groups(kind,name,created_by,updated_by) VALUES($1,$2,$3,$3) RETURNING id`, kind, command.Name, actor).Scan(&id)
			if e != nil {
				return e
			}
			out = map[string]any{"id": id, "name": command.Name, "version": int64(1), "count": 0}
		case "rename", "delete":
			var name string
			var version int64
			e = tx.QueryRow(c, `SELECT name,version FROM media_material_groups WHERE id=$1 AND kind=$2 FOR UPDATE`, command.ID, kind).Scan(&name, &version)
			if e == pgx.ErrNoRows {
				return ErrNotFound
			}
			if e != nil {
				return e
			}
			if version != command.Version {
				return ErrConflict
			}
			if operation == "rename" {
				_, e = tx.Exec(c, `UPDATE media_material_groups SET name=$1,version=version+1,updated_by=$2,updated_at=clock_timestamp() WHERE id=$3`, command.Name, actor, command.ID)
				if e != nil {
					return e
				}
				// Set the projected name to the canonical name in the same transaction.
				_, e = tx.Exec(c, `UPDATE `+table+` SET category=$1,version=version+1,updated_by=$2,updated_at=clock_timestamp() WHERE group_id=$3`, command.Name, actor, command.ID)
				out = map[string]any{"id": command.ID, "name": command.Name, "version": version + 1}
			} else {
				var result pgconn.CommandTag
				result, e = tx.Exec(c, `UPDATE `+table+` SET group_id=NULL,category='',version=version+1,updated_by=$1,updated_at=clock_timestamp() WHERE group_id=$2`, actor, command.ID)
				if e != nil {
					return e
				}
				_, e = tx.Exec(c, `DELETE FROM media_material_groups WHERE id=$1`, command.ID)
				out = map[string]any{"id": command.ID, "moved_count": result.RowsAffected(), "deleted": true}
			}
			if e != nil {
				return e
			}
		case "move":
			name := ""
			if command.GroupID != nil {
				e = tx.QueryRow(c, `SELECT name FROM media_material_groups WHERE id=$1 AND kind=$2 FOR SHARE`, *command.GroupID, kind).Scan(&name)
				if e == pgx.ErrNoRows {
					return ErrNotFound
				}
				if e != nil {
					return e
				}
			}
			items := make([]map[string]any, 0, len(command.Items))
			for _, item := range command.Items {
				var version int64
				e = tx.QueryRow(c, `UPDATE `+table+` SET group_id=$1,category=$2,version=version+1,updated_by=$3,updated_at=clock_timestamp() WHERE id=$4 AND version=$5 RETURNING version`, command.GroupID, name, actor, item.ID, item.Version).Scan(&version)
				if e == pgx.ErrNoRows {
					return ErrConflict
				}
				if e != nil {
					return e
				}
				items = append(items, map[string]any{"id": item.ID, "version": version, "group_id": command.GroupID, "category": name})
			}
			out = map[string]any{"items": items, "moved_count": len(items)}
		}
		resourceID := command.ID
		if operation == "create" {
			resourceID = out["id"].(int64)
		}
		return r.complete(c, "material.group."+operation, kind, actor, key, resourceID, out, "media.material_group_"+operation)
	})
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && (pgerr.Code == "23505" || pgerr.Code == "40001" || pgerr.Code == "40P01") {
		return nil, ErrConflict
	}
	return out, err
}

// applyGroupID is called within an existing material create/update transaction.
func (r *Repository) applyGroupID(c context.Context, kind string, id int64, groupID *int64) (string, error) {
	tx, e := platformpostgres.RequireTransaction(c)
	if e != nil {
		return "", e
	}
	name := ""
	if groupID != nil {
		if *groupID < 1 {
			return "", ErrInvalid
		}
		e = tx.QueryRow(c, `SELECT name FROM media_material_groups WHERE id=$1 AND kind=$2 FOR SHARE`, *groupID, kind).Scan(&name)
		if e == pgx.ErrNoRows {
			return "", ErrNotFound
		}
		if e != nil {
			return "", e
		}
	}
	_, e = tx.Exec(c, `UPDATE `+groupTable(kind)+` SET group_id=$1,category=$2 WHERE id=$3`, groupID, name, id)
	return name, e
}
func groupIDValue(value any) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	var n int64
	switch v := value.(type) {
	case float64:
		n = int64(v)
		if float64(n) != v {
			return nil, ErrInvalid
		}
	case int64:
		n = v
	default:
		return nil, ErrInvalid
	}
	if n < 1 {
		return nil, ErrInvalid
	}
	return &n, nil
}

// MaterialGroupMembers provides authoritative versions for selected visible rows.
func (r *Repository) MaterialGroupMembers(ctx context.Context, kind string, ids []int64) ([]map[string]any, error) {
	table := groupTable(kind)
	if table == "" || len(ids) > 200 {
		return nil, ErrInvalid
	}
	out := []map[string]any{}
	e := r.Within(ctx, func(c context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(c)
		rows, e := tx.Query(c, `SELECT id,version,group_id,category FROM `+table+` WHERE id=ANY($1)`, ids)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, v int64
			var gid *int64
			var name string
			if e = rows.Scan(&id, &v, &gid, &name); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "version": v, "group_id": gid, "category": name})
		}
		return rows.Err()
	})
	return out, e
}
