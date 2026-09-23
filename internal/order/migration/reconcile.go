package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

// ErrReconciliationMismatch means the persisted historical facts no longer
// prove the exact manifest that was approved for the named import run.
var ErrReconciliationMismatch = errors.New("commerce reconciliation mismatch")

// FullReconciliationInput carries trusted, read-only subject results from the
// composition root. Product/Order facts remain separate from Identity tables.
type FullReconciliationInput struct {
	SubjectCustomerIDs map[string]int64
}

// FullReconciliation reports Order-owned facts and opaque target IDs for the
// composition root to compare against Payment-owned verification results.
type FullReconciliation struct {
	Orders      int64
	AmountMinor int64
	OrderIDs    map[string]int64
}

// VerifyFull compares each approved commerce-history manifest row with its
// imported Order, immutable item snapshot and initial status fact. It only
// reads Order-owned tables and never advances the run state.
func (store PostgreSQLRuns) VerifyFull(ctx context.Context, manifest Manifest, input FullReconciliationInput) (FullReconciliation, error) {
	if store.Pool == nil {
		return FullReconciliation{}, errors.New("order migration store is not configured")
	}
	if err := manifest.Validate(true); err != nil {
		return FullReconciliation{}, err
	}
	if err := validFullReconciliationInput(manifest, input); err != nil {
		return FullReconciliation{}, err
	}

	var (
		runID                         int64
		persistedDigest, schemaDigest []byte
		inputCount, importedCount     int64
	)
	if err := store.Pool.QueryRow(ctx, `
		SELECT id,source_manifest_digest,source_schema_digest,input_count,imported_count
		FROM order_import_runs
		WHERE run_key=$1 AND status IN ('applied','reconciled')`, manifest.RunKey).
		Scan(&runID, &persistedDigest, &schemaDigest, &inputCount, &importedCount); err != nil {
		return FullReconciliation{}, reconciliationError(err)
	}
	expectedInput := int64(len(manifest.Subjects) + len(manifest.IdentityQuarantines) + len(manifest.Orders) + len(manifest.Refunds))
	expectedSchema := sha256.Sum256([]byte(SchemaVersion))
	if len(persistedDigest) != sha256.Size || len(schemaDigest) != sha256.Size || subtle.ConstantTimeCompare(persistedDigest, manifest.Digest[:]) != 1 || subtle.ConstantTimeCompare(schemaDigest, expectedSchema[:]) != 1 || inputCount != expectedInput || importedCount != expectedInput {
		return FullReconciliation{}, ErrReconciliationMismatch
	}

	refundsByMerchant := make(map[string]int64, len(manifest.Refunds))
	for _, row := range manifest.Refunds {
		if row.Completed() {
			refundsByMerchant[HistoricalMerchantKey(row.Provider, row.MerchantOrderNo)] += row.AmountMinor
		}
	}
	result := FullReconciliation{OrderIDs: make(map[string]int64, len(manifest.Orders))}
	for _, row := range manifest.Orders {
		orderID, err := store.verifyHistoricalOrder(ctx, runID, manifest.RunKey, row, refundsByMerchant[HistoricalMerchantKey(row.Provider, row.MerchantOrderNo)], input)
		if err != nil {
			return FullReconciliation{}, err
		}
		result.OrderIDs[HistoricalMerchantKey(row.Provider, row.MerchantOrderNo)] = orderID
		result.Orders++
		result.AmountMinor += row.AmountMinor
	}
	var receivedOrders, allOrderReceipts int64
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome IN ('imported','replayed')),count(*) FROM order_import_receipts WHERE run_id=$1`, runID).Scan(&receivedOrders, &allOrderReceipts); err != nil {
		return FullReconciliation{}, reconciliationError(err)
	}
	if receivedOrders != int64(len(manifest.Orders)) || allOrderReceipts != int64(len(manifest.Orders)) {
		return FullReconciliation{}, ErrReconciliationMismatch
	}
	return result, nil
}

func validFullReconciliationInput(manifest Manifest, input FullReconciliationInput) error {
	for _, subject := range manifest.Subjects {
		if input.SubjectCustomerIDs[subject.SourceKey] < 1 {
			return ErrReconciliationMismatch
		}
	}
	return nil
}

func (store PostgreSQLRuns) verifyHistoricalOrder(ctx context.Context, runID int64, runKey string, expected OrderRow, refundedMinor int64, input FullReconciliationInput) (int64, error) {
	digest := HistoricalOrderDigest(expected)
	var (
		receiptDigest, sourceDigest []byte
		id                          int64
		provider, sourceSystem      string
		sourceKey, merchant, txn    string
		payer, beneficiary          *int64
		amount, refunded            int64
		currency, status, origin    string
		effectEligible              bool
		version                     int64
		createdAt, updatedAt        time.Time
	)
	err := store.Pool.QueryRow(ctx, `
		SELECT receipt.source_row_digest,
		       o.id,o.provider,o.source_system,o.source_key,o.merchant_order_no,o.provider_transaction_no,
		       o.payer_customer_id,o.beneficiary_customer_id,o.amount_minor,o.refunded_minor,o.currency,o.status,
		       o.record_origin,o.effect_eligible,o.source_row_digest,o.version,o.created_at,o.updated_at
		FROM order_import_receipts receipt
		JOIN orders o ON o.id=receipt.order_id
		WHERE receipt.run_id=$1 AND receipt.source_system='commerce-history' AND receipt.source_key=$2
		  AND receipt.outcome IN ('imported','replayed')`, runID, expected.SourceKey).
		Scan(&receiptDigest, &id, &provider, &sourceSystem, &sourceKey, &merchant, &txn, &payer, &beneficiary, &amount, &refunded, &currency, &status, &origin, &effectEligible, &sourceDigest, &version, &createdAt, &updatedAt)
	if err != nil {
		return 0, reconciliationError(err)
	}
	if !bytes.Equal(receiptDigest, digest[:]) || !bytes.Equal(sourceDigest, digest[:]) ||
		provider != string(expected.Provider) || sourceSystem != "commerce-history" || sourceKey != expected.SourceKey || merchant != expected.MerchantOrderNo || txn != expected.ProviderTransactionNo ||
		amount != expected.AmountMinor || refunded != refundedMinor || currency != expected.Currency || status != expected.Status || origin != string(orderdomain.RecordOriginHistory) || effectEligible || version < 1 ||
		!sameHistoricalTime(createdAt, expected.CreatedAt) || !sameHistoricalTime(updatedAt, expected.UpdatedAt) {
		return 0, ErrReconciliationMismatch
	}
	if expected.PayerIdentityKey == "" {
		if payer != nil || beneficiary != nil {
			return 0, ErrReconciliationMismatch
		}
	} else {
		expectedPayer := input.SubjectCustomerIDs[expected.PayerSubjectKey]
		expectedBeneficiary := input.SubjectCustomerIDs[expected.BeneficiarySubjectKey]
		if expectedPayer < 1 || payer == nil || *payer != expectedPayer || (expected.BeneficiarySubjectKey == "" && beneficiary != nil) || (expected.BeneficiarySubjectKey != "" && (expectedBeneficiary < 1 || beneficiary == nil || *beneficiary != expectedBeneficiary)) {
			return 0, ErrReconciliationMismatch
		}
	}
	if err := store.verifyHistoricalItems(ctx, id, expected.Items); err != nil {
		return 0, err
	}
	if version > 1 {
		if err := store.verifyHistoricalDelta(ctx, id, runID, version, runKey, expected, refundedMinor); err != nil {
			return 0, err
		}
		return id, nil
	}
	if err := store.verifyHistoricalInitialStatus(ctx, id, runKey, expected, refundedMinor); err != nil {
		return 0, err
	}
	if err := store.verifyHistoricalOrderFacts(ctx, id, runKey, expected); err != nil {
		return 0, err
	}
	return id, nil
}

func (store PostgreSQLRuns) verifyHistoricalItems(ctx context.Context, orderID int64, expected []ItemRow) error {
	rows, err := store.Pool.Query(ctx, `SELECT line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor FROM order_items WHERE order_id=$1 ORDER BY line_no`, orderID)
	if err != nil {
		return reconciliationError(err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(expected) {
			return ErrReconciliationMismatch
		}
		var productID, productVersion *int64
		var line ItemRow
		if err = rows.Scan(&line.LineNo, &productID, &productVersion, &line.ProductCode, &line.ProductName, &line.UnitAmountMinor, &line.Quantity, &line.LineAmountMinor); err != nil {
			return reconciliationError(err)
		}
		want := expected[index]
		if productID != nil || productVersion != nil || line != want {
			return ErrReconciliationMismatch
		}
		index++
	}
	if err = rows.Err(); err != nil {
		return reconciliationError(err)
	}
	if index != len(expected) {
		return ErrReconciliationMismatch
	}
	return nil
}

func (store PostgreSQLRuns) verifyHistoricalInitialStatus(ctx context.Context, orderID int64, runKey string, expected OrderRow, refundedMinor int64) error {
	var count int64
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM order_status_history WHERE order_id=$1`, orderID).Scan(&count); err != nil {
		return reconciliationError(err)
	}
	if count != 1 {
		return ErrReconciliationMismatch
	}
	var from *string
	var status, actor string
	var refunded, version int64
	var occurred time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at FROM order_status_history WHERE order_id=$1`, orderID).Scan(&from, &status, &refunded, &version, &actor, &occurred); err != nil {
		return reconciliationError(err)
	}
	if from != nil || status != expected.Status || refunded != refundedMinor || version != 1 || actor != "migration:"+runKey || !sameHistoricalTime(occurred, expected.CreatedAt) {
		return ErrReconciliationMismatch
	}
	return nil
}

// verifyHistoricalOrderFacts proves that the import's status/audit/outbox
// write was committed as one Order-owned fact set. Publication is deliberately
// not constrained: an outbox consumer may have published the immutable event
// after the import committed.
func (store PostgreSQLRuns) verifyHistoricalOrderFacts(ctx context.Context, orderID int64, runKey string, expected OrderRow) error {
	actor := "migration:" + runKey
	payload, err := json.Marshal(map[string]any{
		"order_id":      orderID,
		"status":        expected.Status,
		"version":       int64(1),
		"record_origin": orderdomain.RecordOriginHistory,
	})
	if err != nil {
		return err
	}
	var audits, outbox int64
	if err = store.Pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM order_audit_events
			 WHERE event_type='order.history_imported' AND order_id=$1 AND actor_scope=$2
			   AND payload=$3::jsonb AND occurred_at=$4),
			(SELECT count(*) FROM order_outbox
			 WHERE event_type='order.history_imported' AND aggregate_id=$1
			   AND idempotency_key=$5 AND payload=$3::jsonb AND occurred_at=$4)`,
		orderID, actor, string(payload), expected.CreatedAt.UTC(), "order.history_imported:"+strconv.FormatInt(orderID, 10)+":1").Scan(&audits, &outbox); err != nil {
		return reconciliationError(err)
	}
	if audits != 1 || outbox != 1 {
		return ErrReconciliationMismatch
	}
	return nil
}

func HistoricalOrderDigest(row OrderRow) [32]byte {
	raw, _ := json.Marshal(row)
	return sha256.Sum256(raw)
}

func HistoricalRefundDigest(row RefundRow) [32]byte {
	raw, _ := json.Marshal(row)
	return sha256.Sum256(raw)
}

func HistoricalMerchantKey(provider orderdomain.Provider, merchant string) string {
	return string(provider) + "\x00" + merchant
}

func sameHistoricalTime(actual, expected time.Time) bool {
	return actual.UTC().Equal(expected.UTC())
}

func reconciliationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReconciliationMismatch
	}
	return err
}

// VerifyOrderOnly is the read-only factual counterpart for the narrowly
// approved floating WeChat Pay import. It verifies the same immutable Order
// facts and item snapshots as full reconciliation. Payment/Refund verification
// remains with Payment and is intentionally outside this Order-owned path.
func (store PostgreSQLRuns) VerifyOrderOnly(ctx context.Context, manifest Manifest) (OrderOnlyReconciliation, error) {
	if store.Pool == nil {
		return OrderOnlyReconciliation{}, errors.New("order migration store is not configured")
	}
	if err := ValidateOrderOnly(manifest); err != nil {
		return OrderOnlyReconciliation{}, err
	}
	var result OrderOnlyReconciliation
	var runID, inputCount, importedCount, replayedCount int64
	var persistedDigest, schemaDigest []byte
	if err := store.Pool.QueryRow(ctx, `
		SELECT id,source_manifest_digest,source_schema_digest,input_count,imported_count,replayed_count
		FROM order_import_runs WHERE run_key=$1 AND status IN ('applied','reconciled')`, manifest.RunKey).
		Scan(&runID, &persistedDigest, &schemaDigest, &inputCount, &importedCount, &replayedCount); err != nil {
		return result, reconciliationError(err)
	}
	expectedSchema := sha256.Sum256([]byte(SchemaVersion))
	if len(persistedDigest) != sha256.Size || len(schemaDigest) != sha256.Size || subtle.ConstantTimeCompare(persistedDigest, manifest.Digest[:]) != 1 || subtle.ConstantTimeCompare(schemaDigest, expectedSchema[:]) != 1 || inputCount != int64(len(manifest.Orders)) || importedCount+replayedCount != int64(len(manifest.Orders)) {
		return result, ErrReconciliationMismatch
	}
	for _, row := range manifest.Orders {
		_, err := store.verifyHistoricalOrder(ctx, runID, manifest.RunKey, row, 0, FullReconciliationInput{})
		if err != nil {
			return result, err
		}
		result.Orders++
		result.AmountMinor += row.AmountMinor
		result.Floating++
	}
	var receipts int64
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM order_import_receipts WHERE run_id=$1`, runID).Scan(&receipts); err != nil {
		return result, reconciliationError(err)
	}
	if receipts != result.Orders {
		return result, ErrReconciliationMismatch
	}
	result.Imported, result.Replayed, result.EffectEligible = importedCount, replayedCount, 0
	result.Matched = true
	return result, nil
}
