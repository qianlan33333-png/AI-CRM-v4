// Package migration contains Payment-owned, read-only checks for the approved
// commerce-history importer. It deliberately never reads Order, Customer or
// Identity tables; the composition root supplies only opaque local IDs and
// already-approved source facts.
package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

var ErrHistoricalReconciliationMismatch = errors.New("payment history reconciliation mismatch")

type HistoricalPaymentFact struct {
	SourceStatus, HistoryReason                             string
	OrderID                                                 int64
	Provider                                                paymentdomain.Provider
	MerchantOrderNo                                         string
	PayerIdentityID, PayerCustomerID, BeneficiaryCustomerID int64
	AmountMinor                                             int64
	Currency                                                string
	Status                                                  paymentdomain.Status
	ProviderTransactionReference                            string
	SourceDigest                                            [32]byte
	PaidConfirmedAt                                         *time.Time
	CreatedAt, UpdatedAt                                    time.Time
}

type HistoricalRefundFact struct {
	Status                            paymentdomain.RefundStatus
	OrderID                           int64
	Provider                          paymentdomain.Provider
	MerchantOrderNo, RefundNo, Reason string
	AmountMinor                       int64
	ProviderRefundReference           string
	SourceDigest                      [32]byte
	OccurredAt                        time.Time
}

type HistoricalReconciliation struct {
	Payments, Refunds int64
	AmountMinor       int64
	RefundMinor       int64
}

// PostgreSQLVerifier is owned by Payment and reads only Payment tables. The
// caller supplies target Order IDs through the Composition Root so no domain
// Store crosses into another Owner's tables.
type PostgreSQLVerifier struct{ Pool *pgxpool.Pool }

func (v PostgreSQLVerifier) VerifyHistorical(ctx context.Context, runKey string, importedOrderIDs []int64, payments []HistoricalPaymentFact, refunds []HistoricalRefundFact) (HistoricalReconciliation, error) {
	if v.Pool == nil || runKey == "" {
		return HistoricalReconciliation{}, errors.New("payment history verifier is not configured")
	}
	orderIDs, err := canonicalOrderIDs(importedOrderIDs)
	if err != nil {
		return HistoricalReconciliation{}, err
	}
	paymentByOrder := make(map[int64]HistoricalPaymentFact, len(payments))
	for _, fact := range payments {
		if !containsOrderID(orderIDs, fact.OrderID) || fact.OrderID < 1 || fact.Provider != paymentdomain.ProviderWeChatPay && fact.Provider != paymentdomain.ProviderWeChatShop || fact.MerchantOrderNo == "" || fact.PayerIdentityID < 0 || fact.PayerCustomerID < 0 || fact.BeneficiaryCustomerID < 0 || ((fact.PayerIdentityID == 0) != (fact.PayerCustomerID == 0)) || (fact.PayerCustomerID == 0 && fact.BeneficiaryCustomerID != 0) || fact.AmountMinor < 1 || fact.Currency != "CNY" || (fact.Status != paymentdomain.StatusPaid && fact.Status != paymentdomain.StatusFailed && fact.Status != paymentdomain.StatusCancelled) || fact.CreatedAt.IsZero() || fact.UpdatedAt.Before(fact.CreatedAt) || !validOptionalPaidConfirmation(fact.Status, fact.PaidConfirmedAt, fact.CreatedAt, fact.UpdatedAt) || fact.SourceDigest == ([32]byte{}) {
			return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
		}
		if _, duplicate := paymentByOrder[fact.OrderID]; duplicate {
			return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
		}
		paymentByOrder[fact.OrderID] = fact
	}
	var actualPayments int64
	if len(orderIDs) > 0 {
		if err = v.Pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id=ANY($1::bigint[])`, orderIDs).Scan(&actualPayments); err != nil {
			return HistoricalReconciliation{}, paymentReconciliationError(err)
		}
	}
	if actualPayments != int64(len(payments)) {
		return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
	}

	result := HistoricalReconciliation{}
	paymentIDs := make(map[int64]int64, len(payments))
	for _, fact := range payments {
		paymentID, verifyErr := v.verifyPayment(ctx, runKey, fact)
		if verifyErr != nil {
			return HistoricalReconciliation{}, verifyErr
		}
		paymentIDs[fact.OrderID] = paymentID
		result.Payments++
		result.AmountMinor += fact.AmountMinor
	}
	refundsByOrder := make(map[int64]int, len(refunds))
	for _, fact := range refunds {
		if !containsOrderID(orderIDs, fact.OrderID) || paymentIDs[fact.OrderID] < 1 || fact.Provider != paymentdomain.ProviderWeChatPay && fact.Provider != paymentdomain.ProviderWeChatShop || fact.MerchantOrderNo == "" || fact.RefundNo == "" || fact.Reason == "" || fact.AmountMinor < 1 || fact.OccurredAt.IsZero() || fact.SourceDigest == ([32]byte{}) || !fact.historicalStatus().HistoricalImportable() {
			return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
		}
		if err = v.verifyRefund(ctx, runKey, paymentIDs[fact.OrderID], fact); err != nil {
			return HistoricalReconciliation{}, err
		}
		refundsByOrder[fact.OrderID]++
		result.Refunds++
		if fact.Status == "" || fact.Status == paymentdomain.RefundCompleted {
			result.RefundMinor += fact.AmountMinor
		}
	}
	for orderID, paymentID := range paymentIDs {
		var actual int
		if err = v.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_refunds WHERE payment_id=$1`, paymentID).Scan(&actual); err != nil {
			return HistoricalReconciliation{}, paymentReconciliationError(err)
		}
		if actual != refundsByOrder[orderID] {
			return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
		}
	}
	var paymentReceipts, refundReceipts int64
	if err = v.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE result_kind='payment'),count(*) FILTER (WHERE result_kind='refund') FROM payment_operation_receipts WHERE operation='history_import' AND actor_scope=$1`, runKey).Scan(&paymentReceipts, &refundReceipts); err != nil {
		return HistoricalReconciliation{}, paymentReconciliationError(err)
	}
	if paymentReceipts != result.Payments || refundReceipts != result.Refunds {
		return HistoricalReconciliation{}, ErrHistoricalReconciliationMismatch
	}
	return result, nil
}

func (v PostgreSQLVerifier) verifyPayment(ctx context.Context, runKey string, expected HistoricalPaymentFact) (int64, error) {
	var (
		id, orderID, payerIdentity, payerCustomer, beneficiary int64
		provider, channel, merchant, currency, status          string
		amount, version                                        int64
		externalEffect                                         *int64
		transactionDigest                                      string
		paidConfirmedAt                                        *time.Time
		createdAt, updatedAt                                   time.Time
		historical                                             bool
		sourceStatus, historyReason                            string
	)
	if err := v.Pool.QueryRow(ctx, `SELECT id,order_id,provider,payment_channel,merchant_order_no,COALESCE(payer_identity_id,0),COALESCE(payer_customer_id,0),COALESCE(beneficiary_customer_id,0),amount_minor,currency,status,external_effect_id,COALESCE(provider_transaction_digest,''),version,paid_confirmed_at,created_at,updated_at,historical,source_status,history_reason FROM payments WHERE order_id=$1`, expected.OrderID).
		Scan(&id, &orderID, &provider, &channel, &merchant, &payerIdentity, &payerCustomer, &beneficiary, &amount, &currency, &status, &externalEffect, &transactionDigest, &version, &paidConfirmedAt, &createdAt, &updatedAt, &historical, &sourceStatus, &historyReason); err != nil {
		return 0, paymentReconciliationError(err)
	}
	expectedTransactionDigest := ""
	if expected.ProviderTransactionReference != "" {
		expectedTransactionDigest = string(effectport.Hash("history.transaction", expected.ProviderTransactionReference))
	}
	if !historical || sourceStatus != expected.SourceStatus || historyReason != expected.HistoryReason || orderID != expected.OrderID || provider != string(expected.Provider) || channel != string(paymentdomain.ChannelMiniProgram) || merchant != expected.MerchantOrderNo || payerIdentity != expected.PayerIdentityID || payerCustomer != expected.PayerCustomerID || beneficiary != expected.BeneficiaryCustomerID || amount != expected.AmountMinor || currency != expected.Currency || status != string(expected.Status) || externalEffect != nil || transactionDigest != expectedTransactionDigest || version < 1 || !sameOptionalTime(paidConfirmedAt, expected.PaidConfirmedAt) || !sameTime(createdAt, expected.CreatedAt) || !sameTime(updatedAt, expected.UpdatedAt) {
		return 0, ErrHistoricalReconciliationMismatch
	}
	key := sha256.Sum256([]byte("payment-history:" + runKey + ":" + expected.MerchantOrderNo))
	var payloadDigest []byte
	var resultKind string
	var resultID int64
	if err := v.Pool.QueryRow(ctx, `SELECT payload_digest,result_kind,result_id FROM payment_operation_receipts WHERE operation='history_import' AND actor_scope=$1 AND key_digest=$2`, runKey, key[:]).Scan(&payloadDigest, &resultKind, &resultID); err != nil {
		return 0, paymentReconciliationError(err)
	}
	if !bytes.Equal(payloadDigest, expected.SourceDigest[:]) || resultKind != "payment" || resultID != id {
		return 0, ErrHistoricalReconciliationMismatch
	}
	if err := v.verifyHistoryVersion(ctx, "payment", id, version, runKey, expected.SourceDigest, "payment.history_imported", expected.CreatedAt); err != nil {
		return 0, err
	}
	return id, nil
}

func (v PostgreSQLVerifier) verifyRefund(ctx context.Context, runKey string, paymentID int64, expected HistoricalRefundFact) error {
	var (
		id, actualPaymentID                        int64
		provider, refundNo, reason, status, digest string
		amount, version                            int64
		externalEffect                             *int64
		createdAt, updatedAt                       time.Time
	)
	if err := v.Pool.QueryRow(ctx, `SELECT id,payment_id,provider,refund_no,amount_minor,reason,status,external_effect_id,COALESCE(provider_refund_digest,''),version,created_at,updated_at FROM payment_refunds WHERE payment_id=$1 AND refund_no=$2`, paymentID, expected.RefundNo).
		Scan(&id, &actualPaymentID, &provider, &refundNo, &amount, &reason, &status, &externalEffect, &digest, &version, &createdAt, &updatedAt); err != nil {
		return paymentReconciliationError(err)
	}
	expectedRefundDigest := ""
	if expected.ProviderRefundReference != "" {
		expectedRefundDigest = string(effectport.Hash("history.refund", expected.ProviderRefundReference))
	}
	if actualPaymentID != paymentID || provider != string(expected.Provider) || refundNo != expected.RefundNo || amount != expected.AmountMinor || reason != expected.Reason || status != string(expected.historicalStatus()) || externalEffect != nil || digest != expectedRefundDigest || version < 1 || !sameTime(createdAt, expected.OccurredAt) || !sameTime(updatedAt, expected.OccurredAt) {
		return ErrHistoricalReconciliationMismatch
	}
	key := sha256.Sum256([]byte("refund-history:" + runKey + ":" + expected.RefundNo))
	var payloadDigest []byte
	var resultKind string
	var resultID int64
	if err := v.Pool.QueryRow(ctx, `SELECT payload_digest,result_kind,result_id FROM payment_operation_receipts WHERE operation='history_import' AND actor_scope=$1 AND key_digest=$2`, runKey, key[:]).Scan(&payloadDigest, &resultKind, &resultID); err != nil {
		return paymentReconciliationError(err)
	}
	if !bytes.Equal(payloadDigest, expected.SourceDigest[:]) || resultKind != "refund" || resultID != id {
		return ErrHistoricalReconciliationMismatch
	}
	return v.verifyHistoryVersion(ctx, "refund", id, version, runKey, expected.SourceDigest, "payment.refund_history_imported", expected.OccurredAt)
}

// verifyHistoricalFacts proves the Payment audit/outbox pair committed with a
// historical import. published_at is intentionally not checked because a
// later consumer is allowed to publish the already-committed immutable event.
func (v PostgreSQLVerifier) verifyHistoricalFacts(ctx context.Context, aggregateID int64, runKey, event string, occurredAt time.Time) error {
	payload, err := json.Marshal(map[string]any{"aggregate_id": aggregateID, "occurred_at": occurredAt.UTC()})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	idempotency := event + ":" + strconv.FormatInt(aggregateID, 10) + ":" + fmt.Sprintf("%x", digest[:8])
	actor := "migration:" + runKey
	var audits, outbox int64
	if err = v.Pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM payment_audit_events
			 WHERE event_type=$1 AND aggregate_id=$2 AND actor_scope=$3 AND payload=$4::jsonb AND occurred_at=$5),
			(SELECT count(*) FROM payment_outbox
			 WHERE event_type=$1 AND aggregate_id=$2 AND idempotency_key=$6 AND payload=$4::jsonb AND occurred_at=$5)`,
		event, aggregateID, actor, string(payload), occurredAt.UTC(), idempotency).Scan(&audits, &outbox); err != nil {
		return paymentReconciliationError(err)
	}
	if audits != 1 || outbox != 1 {
		return ErrHistoricalReconciliationMismatch
	}
	return nil
}

func canonicalOrderIDs(ids []int64) ([]int64, error) {
	result := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, ErrHistoricalReconciliationMismatch
		}
		if _, exists := seen[id]; exists {
			return nil, ErrHistoricalReconciliationMismatch
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func containsOrderID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func sameTime(actual, expected time.Time) bool { return actual.UTC().Equal(expected.UTC()) }

func sameOptionalTime(actual, expected *time.Time) bool {
	if actual == nil || expected == nil {
		return actual == nil && expected == nil
	}
	return sameTime(*actual, *expected)
}

func validOptionalPaidConfirmation(status paymentdomain.Status, confirmed *time.Time, created, updated time.Time) bool {
	if confirmed == nil {
		return true
	}
	return status == paymentdomain.StatusPaid && !confirmed.IsZero() && !confirmed.Before(created) && !confirmed.After(updated)
}

func paymentReconciliationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrHistoricalReconciliationMismatch
	}
	return err
}

func (f HistoricalRefundFact) historicalStatus() paymentdomain.RefundStatus {
	if f.Status == "" {
		return paymentdomain.RefundCompleted
	}
	return f.Status
}

// Cross-run evidence must form an unbroken chain back to the initial audit.
func (v PostgreSQLVerifier) verifyHistoryVersion(ctx context.Context, kind string, id, version int64, run string, digest [32]byte, event string, created time.Time) error {
	if version == 1 {
		return v.verifyHistoricalFacts(ctx, id, run, event, created)
	}
	var firstRun string
	var previous []byte
	if e := v.Pool.QueryRow(ctx, `SELECT actor_scope,payload_digest FROM payment_operation_receipts WHERE operation='history_import' AND result_kind=$1 AND result_id=$2 ORDER BY id LIMIT 1`, kind, id).Scan(&firstRun, &previous); e != nil {
		return paymentReconciliationError(e)
	}
	rows, e := v.Pool.Query(ctx, `SELECT run_key,before_digest,after_digest,before_version,after_version FROM payment_history_source_deltas WHERE result_kind=$1 AND result_id=$2 ORDER BY id`, kind, id)
	if e != nil {
		return paymentReconciliationError(e)
	}
	defer rows.Close()
	want := int64(1)
	lastRun := ""
	for rows.Next() {
		var currentRun string
		var before, after []byte
		var bv, av int64
		if e = rows.Scan(&currentRun, &before, &after, &bv, &av); e != nil {
			return e
		}
		if bv != want || av != want+1 || !bytes.Equal(previous, before) {
			return ErrHistoricalReconciliationMismatch
		}
		want = av
		previous = after
		lastRun = currentRun
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if want != version || lastRun != run || !bytes.Equal(previous, digest[:]) {
		return ErrHistoricalReconciliationMismatch
	}
	return v.verifyHistoricalFacts(ctx, id, firstRun, event, created)
}
