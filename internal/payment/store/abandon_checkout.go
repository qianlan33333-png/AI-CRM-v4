package store

import (
	"context"
	"encoding/hex"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"time"
)

func (r *Repository) CheckoutAbandoned(ctx context.Context, id int64) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_checkout_abandonments WHERE payment_id=$1)`, id).Scan(&exists)
	return exists, mapError(err)
}
func (r *Repository) RecordCheckoutAbandonment(ctx context.Context, c paymentport.AbandonCheckoutCommand, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	digest, err := hex.DecodeString(c.EvidenceDigest)
	if err != nil || len(digest) != 32 {
		return paymentport.ErrInvalid
	}
	result, err := t.Exec(ctx, `INSERT INTO payment_checkout_abandonments(payment_id,actor_scope,evidence_digest,evidence_kind,created_at) VALUES($1,$2,$3,'human_no_debit_invalid_legacy_order',$4) ON CONFLICT(payment_id) DO NOTHING`, c.PaymentID, c.ActorScope, digest, now)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() == 0 {
		var same bool
		if err = t.QueryRow(ctx, `SELECT evidence_digest=$2 FROM payment_checkout_abandonments WHERE payment_id=$1`, c.PaymentID, digest).Scan(&same); err != nil {
			return mapError(err)
		}
		if !same {
			return paymentport.ErrConflict
		}
		return nil
	}
	return appendFacts(ctx, t, "payment.checkout_abandoned", c.PaymentID, c.ActorScope, now)
}
