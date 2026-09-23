package store

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"time"
)

// Historical refreshes never create intents. Each new source run must prove a
// prior import receipt and preserve all attribution and financial identifiers.
func (r *Repository) historyDeltaReceipt(ctx context.Context, kind string, id, beforeVersion, afterVersion int64, beforeStatus, afterStatus string, digest [32]byte, run string, key [32]byte) error {
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	var prior []byte
	e = t.QueryRow(ctx, `SELECT payload_digest FROM payment_operation_receipts WHERE operation='history_import' AND result_kind=$1 AND result_id=$2 ORDER BY id DESC LIMIT 1`, kind, id).Scan(&prior)
	if e != nil {
		return paymentport.ErrConflict
	}
	_, e = t.Exec(ctx, `INSERT INTO payment_history_source_deltas(result_kind,result_id,run_key,before_digest,after_digest,before_version,after_version,before_status,after_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, kind, id, run, prior, digest[:], beforeVersion, afterVersion, beforeStatus, afterStatus)
	if e != nil {
		return mapError(e)
	}
	_, e = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('history_import',$1,$2,$3,$4,$5,clock_timestamp())`, run, key[:], digest[:], kind, id)
	return mapError(e)
}
func (r *Repository) importPaymentDelta(ctx context.Context, old, in domain.Payment, digest [32]byte, run string, key [32]byte) (domain.Payment, error) {
	if !old.Historical || old.EffectID != "" || old.OrderID != in.OrderID || old.Provider != in.Provider || old.MerchantOrderNo != in.MerchantOrderNo || old.PayerIdentityID != in.PayerIdentityID || old.PayerCustomerID != in.PayerCustomerID || old.BeneficiaryCustomerID != in.BeneficiaryCustomerID || old.AmountMinor != in.AmountMinor || old.Currency != in.Currency || old.Channel != in.Channel || !old.CreatedAt.Equal(in.CreatedAt) || in.UpdatedAt.Before(old.UpdatedAt) || old.Status != in.Status || !samePaidConfirmation(old.PaidConfirmedAt, in.PaidConfirmedAt) || (old.ProviderTransactionDigest != "" && old.ProviderTransactionDigest != in.ProviderTransactionDigest) {
		return domain.Payment{}, paymentport.ErrConflict
	}
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	next := old.Version + 1
	if e = r.historyDeltaReceipt(ctx, "payment", old.ID, old.Version, next, string(old.Status), string(in.Status), digest, run, key); e != nil {
		return domain.Payment{}, e
	}
	_, e = t.Exec(ctx, `UPDATE payments SET provider_transaction_digest=NULLIF($2,''),source_status=$3,history_reason=$4,updated_at=$5,version=$6 WHERE id=$1`, old.ID, in.ProviderTransactionDigest, in.SourceStatus, in.HistoryReason, in.UpdatedAt, next)
	if e != nil {
		return domain.Payment{}, mapError(e)
	}
	return r.GetPayment(ctx, old.ID, false)
}
func (r *Repository) importRefundDelta(ctx context.Context, old, in domain.Refund, digest [32]byte, run string, key [32]byte) (domain.Refund, error) {
	if old.EffectID != "" || old.ProviderRefundReference != "" || !old.Status.HistoricalImportable() || old.PaymentID != in.PaymentID || old.Provider != in.Provider || old.RefundNo != in.RefundNo || old.AmountMinor != in.AmountMinor || old.Reason != in.Reason || !old.CreatedAt.Equal(in.CreatedAt) || in.UpdatedAt.Before(old.UpdatedAt) || (old.ProviderRefundDigest != "" && old.ProviderRefundDigest != in.ProviderRefundDigest) || ((old.Status == domain.RefundCompleted || old.Status == domain.RefundHistoryClosed) && old.Status != in.Status) {
		return domain.Refund{}, paymentport.ErrConflict
	}
	payment, e := r.GetPayment(ctx, in.PaymentID, false)
	if e != nil {
		return domain.Refund{}, e
	}
	if !payment.Historical || payment.EffectID != "" {
		return domain.Refund{}, paymentport.ErrConflict
	}
	t, e := tx(ctx)
	if e != nil {
		return domain.Refund{}, e
	}
	next := old.Version + 1
	if e = r.historyDeltaReceipt(ctx, "refund", old.ID, old.Version, next, string(old.Status), string(in.Status), digest, run, key); e != nil {
		return domain.Refund{}, e
	}
	_, e = t.Exec(ctx, `UPDATE payment_refunds SET status=$2,provider_refund_digest=NULLIF($3,''),updated_at=$4,version=$5 WHERE id=$1`, old.ID, in.Status, in.ProviderRefundDigest, in.UpdatedAt, next)
	if e != nil {
		return domain.Refund{}, mapError(e)
	}
	return r.GetRefund(ctx, old.ID, false)
}

func samePaidConfirmation(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}
