package externaleffects

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func (r *Repository) StoppedAttemptWithin(ctx context.Context, id string) (port.StoppedAttemptEvidence, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return port.StoppedAttemptEvidence{}, err
	}
	numeric, err := parseEffectID(id)
	if err != nil {
		return port.StoppedAttemptEvidence{}, err
	}
	var owner, kind, state, attemptState string
	var count int32
	var gen int64
	var updated time.Time
	var completed *time.Time
	err = tx.QueryRow(ctx, `SELECT e.owner,e.kind,e.state,e.attempt_count,e.generation,e.updated_at,a.state,a.completed_at FROM external_effects e JOIN external_effect_attempts a ON a.effect_id=e.id AND a.number=e.attempt_count AND a.generation=e.generation WHERE e.id=$1 FOR SHARE OF e,a`, numeric).Scan(&owner, &kind, &state, &count, &gen, &updated, &attemptState, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.StoppedAttemptEvidence{}, ErrNotFound
	}
	if err != nil {
		return port.StoppedAttemptEvidence{}, err
	}
	if state != "outcome_unknown" || attemptState != "outcome_unknown" || count != 1 || completed == nil {
		return port.StoppedAttemptEvidence{}, port.ErrReconciliationConflict
	}
	return port.StoppedAttemptEvidence{Projection: projection(numeric, Owner(owner), Kind(kind), State(state), count, gen, updated), CompletedAt: completed.UTC()}, nil
}
