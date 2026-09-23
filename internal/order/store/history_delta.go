package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// ApplyHistoricalDeltaWithin requires the composition root's shared transaction
// so payment/refund import failure rolls back this delta and its audit together.
func (r *Repository) ApplyHistoricalDeltaWithin(ctx context.Context, c orderport.HistoricalDeltaCommand) (domain.Snapshot, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	fail := func() (domain.Snapshot, error) { return domain.Snapshot{}, orderport.ErrConflict }
	after := c.Order
	if c.Before.Version < 1 || c.Before.SourceDigest == ([32]byte{}) || c.SourceDigest == ([32]byte{}) || after.SourceSystem != "commerce-history" || after.RecordOrigin != domain.RecordOriginHistory || after.EffectEligible {
		return fail()
	}
	var runID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM order_import_runs WHERE run_key=$1 AND status='applying' FOR UPDATE`, c.RunID).Scan(&runID); err != nil {
		return fail()
	}
	var id int64
	var sourceDigest []byte
	if err = tx.QueryRow(ctx, `SELECT id,source_row_digest FROM orders WHERE source_system=$1 AND source_key=$2 FOR UPDATE`, after.SourceSystem, after.SourceKey).Scan(&id, &sourceDigest); err != nil {
		return fail()
	}
	existing, err := r.Get(ctx, id, false)
	if err != nil {
		return fail()
	}
	before := existing.Snapshot()
	var storedAfter []byte
	var storedVersion int64
	err = tx.QueryRow(ctx, `SELECT after_source_digest,after_version FROM order_history_source_deltas WHERE run_id=$1 AND order_id=$2`, runID, id).Scan(&storedAfter, &storedVersion)
	if err == nil {
		if before.RecordOrigin != domain.RecordOriginHistory || before.EffectEligible || before.Status != after.Status || before.RefundedMinor != after.RefundedMinor || before.Amount != after.Amount || !bytes.Equal(storedAfter, c.SourceDigest[:]) || !bytes.Equal(sourceDigest, c.SourceDigest[:]) || before.Version != storedVersion {
			return fail()
		}
		return before, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fail()
	}
	if before.RecordOrigin != domain.RecordOriginHistory || before.EffectEligible || before.Version != c.Before.Version || !bytes.Equal(sourceDigest, c.Before.SourceDigest[:]) {
		return fail()
	}
	// A persisted receipt is mandatory; a caller-supplied digest alone is insufficient.
	var proof bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM order_import_receipts WHERE order_id=$1 AND source_row_digest=$2 AND outcome IN ('imported','replayed'))`, id, sourceDigest).Scan(&proof); err != nil || !proof {
		return fail()
	}
	if before.Provider != after.Provider || before.MerchantOrderNo != after.MerchantOrderNo || before.Amount != after.Amount || !before.CreatedAt.Equal(after.CreatedAt) || !reflect.DeepEqual(before.Items, after.Items) || !reflect.DeepEqual(before.PayerCustomerID, after.PayerCustomerID) || !reflect.DeepEqual(before.BeneficiaryCustomerID, after.BeneficiaryCustomerID) {
		return fail()
	}
	if before.ProviderTransactionNo != "" && before.ProviderTransactionNo != after.ProviderTransactionNo {
		return fail()
	}
	if after.RefundedMinor < before.RefundedMinor || after.UpdatedAt.Before(before.CreatedAt) {
		return fail()
	}
	if !historicalDeltaTransition(before.Status, after.Status) {
		return fail()
	}
	after.ID = id
	after.Version = before.Version + 1
	if _, err = domain.Restore(after); err != nil {
		return fail()
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status=$2,refunded_minor=$3,provider_transaction_no=$4,updated_at=$5,version=$6,source_row_digest=$7 WHERE id=$1`, id, after.Status, after.RefundedMinor, after.ProviderTransactionNo, after.UpdatedAt, after.Version, c.SourceDigest[:]); err != nil {
		return domain.Snapshot{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_history_source_deltas(order_id,run_id,before_source_digest,after_source_digest,before_version,after_version,before_status,after_status,before_refunded_minor,after_refunded_minor,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, runID, c.Before.SourceDigest[:], c.SourceDigest[:], before.Version, after.Version, before.Status, after.Status, before.RefundedMinor, after.RefundedMinor, after.UpdatedAt); err != nil {
		return domain.Snapshot{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,$2,$3,$4,'replayed',$5)`, runID, after.SourceSystem, after.SourceKey, c.SourceDigest[:], id); err != nil {
		return domain.Snapshot{}, err
	}
	from := before.Status
	if err = r.appendFacts(ctx, tx, after, &from, "migration:"+c.RunID, "order.history_delta_applied", after.UpdatedAt); err != nil {
		return domain.Snapshot{}, err
	}
	return after, nil
}
func historicalDeltaTransition(from, to domain.Status) bool {
	if from == to {
		return true
	}
	switch from {
	case domain.StatusPendingPayment:
		return to == domain.StatusPaid || to == domain.StatusPartiallyRefunded || to == domain.StatusRefunded || to == domain.StatusClosed || to == domain.StatusCancelled || to == domain.StatusPaymentFailed
	case domain.StatusPaid:
		return to == domain.StatusPartiallyRefunded || to == domain.StatusRefunded
	case domain.StatusPartiallyRefunded:
		return to == domain.StatusRefunded
	}
	return false
}
