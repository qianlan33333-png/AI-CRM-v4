package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// ListQualificationPurchaseEvidenceWithin is intentionally narrower than the
// historic Owned reader. It returns only exact-product native checkouts with a
// durable first-paid event and both persisted checkout relationships. Payment
// remains the authority for refund exposure/finality.
func (r *Repository) ListQualificationPurchaseEvidenceWithin(ctx context.Context, query orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if len(query.CustomerIDs) < 1 || len(query.CustomerIDs) > 100 || query.ProductID < 1 || (query.ProductType != "standard_product" && query.ProductType != "service_period") {
		return nil, ErrInvalid
	}
	for _, id := range query.CustomerIDs {
		if id < 1 {
			return nil, ErrInvalid
		}
	}
	rows, err := tx.Query(ctx, `SELECT
e.order_id,e.order_item_line,e.payer_customer_id,e.beneficiary_customer_id,
e.item_paid_minor,e.payment_confirmed_at,e.record_origin
FROM (
  SELECT o.id AS order_id,i.line_no AS order_item_line,o.payer_customer_id,o.beneficiary_customer_id,
         i.line_amount_minor AS item_paid_minor,pe.occurred_at AS payment_confirmed_at,'native'::text AS record_origin
  FROM order_checkout_snapshots c
  JOIN orders o ON o.id=c.order_id AND o.record_origin='native'
  JOIN order_items i ON i.order_id=o.id AND i.product_id=c.product_id
  JOIN order_paid_events pe ON pe.order_id=o.id
  WHERE c.product_id=$1 AND c.product_type=$2
  UNION ALL
  SELECT h.order_id,h.order_item_line,h.payer_customer_id,h.beneficiary_customer_id,
         h.item_paid_minor,h.payment_confirmed_at,'history'::text AS record_origin
  FROM order_distribution_qualification_evidence h
  WHERE h.product_id=$1 AND h.product_type=$2
) e
WHERE e.payer_customer_id = ANY($3::bigint[]) OR e.beneficiary_customer_id = ANY($3::bigint[])
ORDER BY e.payment_confirmed_at DESC,e.order_id DESC,e.order_item_line ASC`, query.ProductID, query.ProductType, query.CustomerIDs)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]orderport.QualificationPurchaseEvidence, 0)
	for rows.Next() {
		var evidence orderport.QualificationPurchaseEvidence
		if err = rows.Scan(&evidence.OrderID, &evidence.OrderItemLine, &evidence.PayerCustomerID, &evidence.BeneficiaryCustomerID, &evidence.ItemPaidMinor, &evidence.PaymentConfirmedAt, &evidence.RecordOrigin); err != nil {
			return nil, mapError(err)
		}
		evidence.ProductID = query.ProductID
		if evidence.OrderID < 1 || evidence.OrderItemLine < 1 || evidence.PayerCustomerID < 1 || evidence.BeneficiaryCustomerID < 1 || evidence.ItemPaidMinor < 1 || evidence.PaymentConfirmedAt.IsZero() || (evidence.RecordOrigin != "native" && evidence.RecordOrigin != "history") {
			// A malformed/incomplete candidate is not proof but also must not
			// hide a later valid purchase for this same customer/product.
			continue
		}
		result = append(result, evidence)
	}
	if err = rows.Err(); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, mapError(err)
	}
	return result, nil
}

func (r *Repository) ImportHistoricalQualificationEvidenceWithin(ctx context.Context, evidence orderport.HistoricalQualificationEvidence) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if !evidence.Valid() {
		return ErrInvalid
	}
	var matches bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
	SELECT 1 FROM orders o
	JOIN order_items i ON i.order_id=o.id
	LEFT JOIN order_checkout_snapshots c ON c.order_id=o.id
	WHERE o.id=$1 AND o.record_origin='history' AND o.status IN ('paid','partially_refunded','refunded')
	  AND o.source_row_digest=$5 AND i.line_no=$2 AND i.product_id=$3 AND i.line_amount_minor=$4
	  AND i.product_code=$6
	  AND (c.order_id IS NULL OR (c.product_id=$3 AND c.product_type=$7))
	)`, evidence.OrderID, evidence.OrderItemLine, evidence.ProductID, evidence.ItemPaidMinor,
		evidence.SourceOrderDigest[:], evidence.SourceProductCode, evidence.ProductType).Scan(&matches)
	if err != nil {
		return mapError(err)
	}
	if !matches {
		return distributionEvidenceConflict()
	}

	// The evidence table is append-only.  A bare ON CONFLICT DO NOTHING would
	// silently turn a changed product, identity, amount, timestamp, or source
	// digest into a successful replay.  Insert first so concurrent imports
	// serialize on the primary key; a no-row return then reads and compares the
	// immutable row under a key-share lock.
	var insertedOrderID int64
	err = tx.QueryRow(ctx, `INSERT INTO order_distribution_qualification_evidence(
	order_id,order_item_line,product_id,product_type,source_product_code,payer_customer_id,beneficiary_customer_id,
	item_paid_minor,payment_confirmed_at,source_order_digest,source_digest,created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,clock_timestamp())
ON CONFLICT(order_id,order_item_line) DO NOTHING
RETURNING order_id`, evidence.OrderID, evidence.OrderItemLine, evidence.ProductID, evidence.ProductType,
		evidence.SourceProductCode, evidence.PayerCustomerID, evidence.BeneficiaryCustomerID, evidence.ItemPaidMinor,
		evidence.PaymentConfirmedAt.UTC(), evidence.SourceOrderDigest[:], evidence.SourceDigest[:]).Scan(&insertedOrderID)
	if err == nil {
		if insertedOrderID != evidence.OrderID {
			return distributionEvidenceConflict()
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mapError(err)
	}

	var stored struct {
		productID, payerCustomerID, beneficiaryCustomerID, itemPaidMinor int64
		productType, sourceProductCode                                   string
		paymentConfirmedAt                                               time.Time
		sourceOrderDigest, sourceDigest                                  []byte
	}
	err = tx.QueryRow(ctx, `SELECT product_id,product_type,source_product_code,payer_customer_id,beneficiary_customer_id,
	item_paid_minor,payment_confirmed_at,source_order_digest,source_digest
FROM order_distribution_qualification_evidence
WHERE order_id=$1 AND order_item_line=$2
FOR KEY SHARE`, evidence.OrderID, evidence.OrderItemLine).Scan(
		&stored.productID, &stored.productType, &stored.sourceProductCode, &stored.payerCustomerID, &stored.beneficiaryCustomerID,
		&stored.itemPaidMinor, &stored.paymentConfirmedAt, &stored.sourceOrderDigest, &stored.sourceDigest,
	)
	if err != nil {
		return mapError(err)
	}
	if stored.productID != evidence.ProductID || stored.productType != evidence.ProductType || stored.sourceProductCode != evidence.SourceProductCode ||
		stored.payerCustomerID != evidence.PayerCustomerID || stored.beneficiaryCustomerID != evidence.BeneficiaryCustomerID ||
		stored.itemPaidMinor != evidence.ItemPaidMinor || !stored.paymentConfirmedAt.Equal(evidence.PaymentConfirmedAt.UTC()) ||
		len(stored.sourceOrderDigest) != len(evidence.SourceOrderDigest) || string(stored.sourceOrderDigest) != string(evidence.SourceOrderDigest[:]) ||
		len(stored.sourceDigest) != len(evidence.SourceDigest) || string(stored.sourceDigest) != string(evidence.SourceDigest[:]) {
		return distributionEvidenceConflict()
	}
	return nil
}

// ListQualificationRefundEvidenceByOrderWithin is the Order-owned union used
// by refund-finality and refund-exposure workers. A native Order must have its
// immutable checkout snapshot and first-paid event. A history Order must have
// the append-only mapping written after the controlled verifier established
// identity, payment confirmation, positive payment, and no-refund facts.
// Raw historical rows, Owned flags, and manual notes are never candidates.
func (r *Repository) ListQualificationRefundEvidenceByOrderWithin(ctx context.Context, orderID int64) ([]orderport.QualificationRefundEvidence, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if orderID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT order_id,order_item_line,product_id,product_type,
payer_customer_id,beneficiary_customer_id,item_paid_minor,payment_confirmed_at,record_origin
FROM (
	SELECT o.id AS order_id,i.line_no AS order_item_line,c.product_id AS product_id,c.product_type AS product_type,
	       o.payer_customer_id AS payer_customer_id,o.beneficiary_customer_id AS beneficiary_customer_id,i.line_amount_minor AS item_paid_minor,
	       pe.occurred_at AS payment_confirmed_at,'native'::text AS record_origin
	FROM orders o
	JOIN order_checkout_snapshots c ON c.order_id=o.id
	JOIN order_items i ON i.order_id=o.id AND i.product_id=c.product_id
	JOIN order_paid_events pe ON pe.order_id=o.id
	WHERE o.id=$1 AND o.record_origin='native'
	  AND o.payer_customer_id IS NOT NULL AND o.beneficiary_customer_id IS NOT NULL
	UNION ALL
	SELECT h.order_id,h.order_item_line,h.product_id,h.product_type,
	       h.payer_customer_id,h.beneficiary_customer_id,h.item_paid_minor,
	       h.payment_confirmed_at,'history'::text AS record_origin
	FROM order_distribution_qualification_evidence h
	WHERE h.order_id=$1
) evidence
ORDER BY order_item_line,record_origin`, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]orderport.QualificationRefundEvidence, 0)
	for rows.Next() {
		var evidence orderport.QualificationRefundEvidence
		if err = rows.Scan(&evidence.OrderID, &evidence.OrderItemLine, &evidence.ProductID, &evidence.ProductType,
			&evidence.PayerCustomerID, &evidence.BeneficiaryCustomerID, &evidence.ItemPaidMinor, &evidence.PaymentConfirmedAt, &evidence.RecordOrigin); err != nil {
			return nil, mapError(err)
		}
		if !evidence.Valid() {
			return nil, orderport.ErrUnavailable
		}
		result = append(result, evidence)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

// ListHistoricalQualificationEvidenceByOrderWithin preserves the former
// history-only method while RefundService migrates to the union Port. The
// historical portion intentionally remains filtered to accepted mappings.
func (r *Repository) ListHistoricalQualificationEvidenceByOrderWithin(ctx context.Context, orderID int64) ([]orderport.HistoricalQualificationRefundEvidence, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if orderID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT order_id,order_item_line,product_id,product_type,
payer_customer_id,beneficiary_customer_id,item_paid_minor,payment_confirmed_at,'history'::text AS record_origin
FROM order_distribution_qualification_evidence
WHERE order_id=$1
ORDER BY order_item_line`, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]orderport.HistoricalQualificationRefundEvidence, 0)
	for rows.Next() {
		var evidence orderport.HistoricalQualificationRefundEvidence
		if err = rows.Scan(&evidence.OrderID, &evidence.OrderItemLine, &evidence.ProductID, &evidence.ProductType,
			&evidence.PayerCustomerID, &evidence.BeneficiaryCustomerID, &evidence.ItemPaidMinor, &evidence.PaymentConfirmedAt, &evidence.RecordOrigin); err != nil {
			return nil, mapError(err)
		}
		if !evidence.Valid() {
			return nil, orderport.ErrUnavailable
		}
		result = append(result, evidence)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func distributionEvidenceConflict() error { return orderport.ErrConflict }
