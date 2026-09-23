package externaleffects

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// RetryTagCatalogMutationWithin retains the original effect and all attempt
// evidence. Only EER's durable evidence can authorize another generation.
func (r *Repository) RetryTagCatalogMutationWithin(ctx context.Context, c port.TagCatalogMutationRetryCommand) (port.Projection, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Projection{}, err
	}
	control := ControlCommand{EffectID: c.EffectID, ReceiptKey: c.ReceiptKey, ActorAdminUserID: c.ActorAdminUserID}
	if !control.Valid() || !ValidDigest(c.SourceRefDigest) {
		return Projection{}, port.ErrReconciliationConflict
	}
	id, err := parseEffectID(c.EffectID)
	if err != nil {
		return Projection{}, port.ErrReconciliationConflict
	}
	// The owner may already hold its rows. Never wait on a worker holding
	// the effect lock while it projects completion back into those rows.
	var owner, kind, source, state, fingerprint string
	var count int32
	err = tx.QueryRow(ctx, `SELECT owner,kind,source_ref_digest,state,attempt_count,envelope_fingerprint FROM external_effects WHERE id=$1 FOR UPDATE NOWAIT`, id).Scan(&owner, &kind, &source, &state, &count, &fingerprint)
	var lockError *pgconn.PgError
	if errors.As(err, &lockError) && lockError.Code == "55P03" {
		return Projection{}, port.ErrReconciliationConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, port.ErrReconciliationNotFound
	}
	if err != nil {
		return Projection{}, err
	}
	if Owner(owner) != OwnerOutbound || Kind(kind) != KindWeComTagCatalogMutation || Digest(source) != c.SourceRefDigest {
		return Projection{}, port.ErrReconciliationConflict
	}
	var replay bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM external_effect_operation_receipts WHERE effect_id=$1 AND operation='retry' AND receipt_key_digest=$2)`, id, c.ReceiptKey).Scan(&replay); err != nil {
		return Projection{}, err
	}
	if !replay {
		if (State(state) != StateFinalFailed && State(state) != StateRetryable) || count < 1 {
			return Projection{}, port.ErrReconciliationConflict
		}
		rows, e := tx.Query(ctx, `SELECT number,state,COALESCE(receipt_digest,''),call_attempted,real_external_call_executed,completed_at IS NOT NULL FROM external_effect_attempts WHERE effect_id=$1 ORDER BY number`, id)
		if e != nil {
			return Projection{}, e
		}
		seen := int32(0)
		safe := true
		for rows.Next() {
			var number int32
			var attemptState, receipt string
			var called, executed, completed bool
			if e = rows.Scan(&number, &attemptState, &receipt, &called, &executed, &completed); e != nil {
				rows.Close()
				return Projection{}, e
			}
			seen++
			if number != seen || !safeTagCatalogRetryAttempt(id, c.EffectID, Digest(fingerprint), number, State(attemptState), Digest(receipt), called, executed, completed) {
				safe = false
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return Projection{}, e
		}
		if !safe || seen != count {
			return Projection{}, port.ErrReconciliationConflict
		}
		// The state adjustment and normal retry (including owner projection and
		// queue insertion) are part of the caller's single Unit of Work.
		if _, err = tx.Exec(ctx, `UPDATE external_effects SET state='retryable_failed' WHERE id=$1`, id); err != nil {
			return Projection{}, err
		}
	}
	p, _, err := r.controlWithin(ctx, tx, control, "retry")
	if errors.Is(err, ErrTransition) || errors.Is(err, ErrPayloadMismatch) {
		return Projection{}, port.ErrReconciliationConflict
	}
	return p, err
}

func safeTagCatalogRetryAttempt(id int64, ref string, fingerprint Digest, number int32, state State, receipt Digest, called, executed, completed bool) bool {
	if !completed || executed || !ValidDigest(receipt) {
		return false
	}
	if state == StateRetryable {
		return !called
	}
	if state != StateFinalFailed {
		return false
	}
	n := strconv.Itoa(int(number))
	if receipt == Hash("wecom.tag.catalog.mutation.rejected", ref, n) {
		return true
	}
	return !called && (receipt == Hash("provider-disabled", strconv.FormatInt(id, 10), n) || receipt == Hash("outbound.provider.not-configured", string(KindWeComTagCatalogMutation)) || (ValidDigest(fingerprint) && receipt == Hash("wecom.tag.catalog.mutation.dispatch_changed", string(fingerprint))))
}
