package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrViewConflict = errors.New("dashboard view conflict")

type View struct {
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Config  json.RawMessage `json:"config"`
	Version int64           `json:"version"`
}

func (s *PostgreSQL) ListViews(ctx context.Context, kind string, actor int64) ([]View, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,config,version FROM hxc_dashboard_views WHERE owner_id=$1 AND owner_kind=$2 ORDER BY id`, actor, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		var v View
		if err = rows.Scan(&v.ID, &v.Name, &v.Config, &v.Version); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Receipt, CAS mutation and the caller's audit use the same PostgreSQL UoW.
func (s *PostgreSQL) SaveView(ctx context.Context, kind string, actor int64, key, digest []byte, v View, remove bool) (View, bool, error) {
	namespaced := sha256.Sum256(append([]byte(kind+"\x00"), key...))
	key = namespaced[:]
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return v, false, err
	}
	// Serialize identical idempotency keys, including concurrent first requests.
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("hxc-view:%d:%x", actor, key))
	if err != nil {
		return v, false, err
	}
	var priorDigest, raw []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest,result FROM hxc_dashboard_view_receipts WHERE actor_id=$1 AND key_digest=$2`, actor, key).Scan(&priorDigest, &raw)
	if err == nil {
		if string(priorDigest) != string(digest) {
			return v, false, ErrViewConflict
		}
		err = json.Unmarshal(raw, &v)
		return v, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return v, false, err
	}
	if v.ID == 0 && !remove {
		err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_views(owner_id,name,config,owner_kind) VALUES($1,$2,$3,$4) RETURNING id,name,config,version`, actor, v.Name, v.Config, kind).Scan(&v.ID, &v.Name, &v.Config, &v.Version)
	} else if remove {
		err = tx.QueryRow(ctx, `DELETE FROM hxc_dashboard_views WHERE id=$1 AND owner_id=$2 AND version=$3 AND owner_kind=$4 RETURNING id,name,config,version`, v.ID, actor, v.Version, kind).Scan(&v.ID, &v.Name, &v.Config, &v.Version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE hxc_dashboard_views SET name=$4,config=$5,version=version+1,updated_at=now() WHERE id=$1 AND owner_id=$2 AND version=$3 AND owner_kind=$6 RETURNING id,name,config,version`, v.ID, actor, v.Version, v.Name, v.Config, kind).Scan(&v.ID, &v.Name, &v.Config, &v.Version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, ErrViewConflict
	}
	if err != nil {
		return v, false, err
	}
	raw, err = json.Marshal(v)
	if err != nil {
		return v, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hxc_dashboard_view_receipts(actor_id,key_digest,payload_digest,result) VALUES($1,$2,$3,$4)`, actor, key, digest, raw)
	return v, false, err
}
