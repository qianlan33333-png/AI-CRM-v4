package externaleffects

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) GetWithin(ctx context.Context, ref string) (port.Projection, error) {
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return port.Projection{}, e
	}
	id, e := parseEffectID(ref)
	if e != nil {
		return port.Projection{}, e
	}
	var p port.Projection
	p.ID = ref
	e = tx.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1`, id).Scan(&p.Owner, &p.Kind, &p.State, &p.AttemptCount, &p.Generation, &p.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, e
}
