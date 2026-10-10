package store

import (
	"context"
	"strconv"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

// The caller holds the original instruction lock. Observations use Payment's
// existing audit; they never schedule or retry a Provider write.
func (r *Repository) RecordProfitSharingAbsenceWithin(ctx context.Context, instruction domain.ProfitSharingInstruction, effect effectport.Projection, evidence effectport.Digest, at time.Time) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var count int
	var first, last *time.Time
	err = t.QueryRow(ctx, `SELECT count(*),min(occurred_at),max(occurred_at) FROM payment_profit_sharing_audit_events
 WHERE aggregate_kind='instruction' AND aggregate_id=$1 AND event_type='payment.profit_sharing.order_absent'
 AND id>COALESCE((SELECT max(id) FROM payment_profit_sharing_audit_events WHERE aggregate_kind='instruction' AND aggregate_id=$1 AND event_type<>'payment.profit_sharing.order_absent'),0)
 AND payload->>'effect_generation'=$2 AND payload->>'attempt_count'=$3 AND payload->>'effect_updated_at'=$4`, instruction.ID, strconv.FormatInt(effect.Generation, 10), strconv.FormatInt(int64(effect.AttemptCount), 10), effect.UpdatedAt.UTC().Format(time.RFC3339Nano)).Scan(&count, &first, &last)
	if err != nil {
		return false, mapError(err)
	}
	if at.Before(effect.UpdatedAt) || (last != nil && at.Before(last.Add(10*time.Minute))) {
		return false, nil
	}
	if err = profitSharingAudit(ctx, t, "instruction", instruction.ID, "payment.profit_sharing.order_absent", "provider-query", at, map[string]any{"evidence_digest": evidence, "effect_generation": effect.Generation, "attempt_count": effect.AttemptCount, "effect_updated_at": effect.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		return false, err
	}
	count++
	if first == nil {
		first = &at
	}
	return count >= 3 && !at.Before(first.Add(20*time.Minute)) && !at.Before(effect.UpdatedAt.Add(30*time.Minute)), nil
}

func (r *Repository) ResetProfitSharingAbsenceWithin(ctx context.Context, instructionID int64, at time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	var lastType string
	err = t.QueryRow(ctx, `SELECT COALESCE((SELECT event_type FROM payment_profit_sharing_audit_events WHERE aggregate_kind='instruction' AND aggregate_id=$1 ORDER BY id DESC LIMIT 1),'')`, instructionID).Scan(&lastType)
	if err != nil {
		return mapError(err)
	}
	if lastType != "payment.profit_sharing.order_absent" {
		return nil
	}
	return profitSharingAudit(ctx, t, "instruction", instructionID, "payment.profit_sharing.absence_interrupted", "provider-query", at, map[string]any{"reason": "original_query_not_verified_absent"})
}

func (r *Repository) ReservedProfitSharingReferencesWithin(ctx context.Context, paymentID int64) ([]string, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT 'psinst_'||i.id::text FROM payment_profit_sharing_instructions i JOIN payment_profit_sharing_reserves r ON r.instruction_id=i.id WHERE i.payment_id=$1 AND r.state='reserved' ORDER BY i.id`, paymentID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			return nil, mapError(err)
		}
		refs = append(refs, ref)
	}
	return refs, mapError(rows.Err())
}
