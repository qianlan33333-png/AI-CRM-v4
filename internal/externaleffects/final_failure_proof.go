package externaleffects

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// FinalFailureWithoutExternalCallWithin proves the complete durable attempt
// history for one effect while holding the effect lock. It intentionally does
// not alter the old effect or imply why it failed; callers can only use this
// evidence to decide whether a separately reviewed, new intent is safe to
// accept in the same Unit of Work.
func (r *Repository) FinalFailureWithoutExternalCallWithin(ctx context.Context, effectRef string, expectedOwner port.Owner, expectedKind port.Kind) (port.FinalFailureWithoutExternalCallEvidence, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return port.FinalFailureWithoutExternalCallEvidence{}, err
	}
	id, err := parseEffectID(effectRef)
	if err != nil || expectedOwner == "" || expectedKind == "" {
		return port.FinalFailureWithoutExternalCallEvidence{}, port.ErrReconciliationConflict
	}
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1 FOR UPDATE NOWAIT`, id).Scan(&owner, &kind, &state, &attempts, &generation, &updated)
	var lockError *pgconn.PgError
	if errors.As(err, &lockError) && lockError.Code == "55P03" {
		return port.FinalFailureWithoutExternalCallEvidence{}, port.ErrReconciliationConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return port.FinalFailureWithoutExternalCallEvidence{}, ErrNotFound
	}
	if err != nil {
		return port.FinalFailureWithoutExternalCallEvidence{}, err
	}
	if port.Owner(owner) != expectedOwner || port.Kind(kind) != expectedKind || port.State(state) != port.StateFinalFailed || attempts < 1 {
		return port.FinalFailureWithoutExternalCallEvidence{}, port.ErrReconciliationConflict
	}

	rows, err := tx.Query(ctx, `SELECT number,state,call_attempted,real_external_call_executed,completed_at FROM external_effect_attempts WHERE effect_id=$1 ORDER BY number`, id)
	if err != nil {
		return port.FinalFailureWithoutExternalCallEvidence{}, err
	}
	defer rows.Close()
	var (
		seen      int32
		completed time.Time
	)
	for rows.Next() {
		var (
			number               int32
			attemptState         string
			callAttempted        bool
			externalCallExecuted bool
			endedAt              *time.Time
		)
		if err = rows.Scan(&number, &attemptState, &callAttempted, &externalCallExecuted, &endedAt); err != nil {
			return port.FinalFailureWithoutExternalCallEvidence{}, err
		}
		seen++
		if number != seen || port.State(attemptState) != port.StateFinalFailed || callAttempted || externalCallExecuted || endedAt == nil {
			return port.FinalFailureWithoutExternalCallEvidence{}, port.ErrReconciliationConflict
		}
		if completed.IsZero() || endedAt.After(completed) {
			completed = endedAt.UTC()
		}
	}
	if err = rows.Err(); err != nil {
		return port.FinalFailureWithoutExternalCallEvidence{}, err
	}
	if seen != attempts || completed.IsZero() {
		return port.FinalFailureWithoutExternalCallEvidence{}, port.ErrReconciliationConflict
	}
	return port.FinalFailureWithoutExternalCallEvidence{Projection: projection(id, port.Owner(owner), port.Kind(kind), port.State(state), attempts, generation, updated), CompletedAt: completed}, nil
}

var _ port.FinalFailureWithoutExternalCallReader = (*Repository)(nil)
