package store

import (
	"context"
	"encoding/hex"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"time"
)

func (r *Repository) CheckoutRestartAllowed(ctx context.Context, id int64) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var allowed bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_checkout_restart_permissions r WHERE r.payment_id=$1) AND NOT EXISTS(SELECT 1 FROM payment_handoffs WHERE payment_id=$1) AND NOT EXISTS(SELECT 1 FROM payment_callback_receipts WHERE payment_id=$1)`, id).Scan(&allowed)
	return allowed, mapError(err)
}
func (r *Repository) RecordCheckoutRestart(ctx context.Context, c paymentport.AbandonCheckoutCommand, completed, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	digest, err := hex.DecodeString(c.EvidenceDigest)
	if err != nil || len(digest) != 32 {
		return paymentport.ErrInvalid
	}
	var reviewed bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_checkout_abandonments WHERE payment_id=$1 AND evidence_digest=$2 AND evidence_kind='human_no_debit_invalid_legacy_order') AND NOT EXISTS(SELECT 1 FROM payment_handoffs WHERE payment_id=$1) AND NOT EXISTS(SELECT 1 FROM payment_callback_receipts WHERE payment_id=$1)`, c.PaymentID, digest).Scan(&reviewed)
	if err != nil {
		return mapError(err)
	}
	if !reviewed {
		return paymentport.ErrConflict
	}
	result, err := t.Exec(ctx, `INSERT INTO payment_checkout_restart_permissions(payment_id,actor_scope,evidence_digest,attempt_completed_at,approved_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(payment_id) DO NOTHING`, c.PaymentID, c.ActorScope, digest, completed, now)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	return appendFacts(ctx, t, "payment.checkout_restart_allowed", c.PaymentID, c.ActorScope, now)
}
