package store

import (
	"context"
	"encoding/json"
	"strconv"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// ReadPaidOverview uses Payment's persisted, original paid-confirmation fact
// for both native and history payments. A history row with no original source
// time cannot be placed in a date range, so its money is returned separately
// as missing evidence instead of being concealed by a native-only denominator.
func (r *Repository) ReadPaidOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.PaidOverview{}, err
	}
	if !window.Valid() {
		return paymentport.PaidOverview{}, paymentport.ErrInvalid
	}
	// One statement is intentional so amount, count, trend and missing evidence
	// describe one Payment snapshot. The composed overview reader runs this
	// statement together with its payer keysets and Identity root reads in one
	// read-only repeatable-read UoW, keeping the completed canonical count on
	// that same snapshot.
	var result paymentport.PaidOverview
	var grossJSON, trendJSON, missingJSON []byte
	err = t.QueryRow(ctx, `WITH paid_in_range AS MATERIALIZED (
		SELECT order_id,payer_customer_id,amount_minor,currency,paid_confirmed_at
		FROM payments
		WHERE status='paid' AND paid_confirmed_at IS NOT NULL
			AND paid_confirmed_at >= $1 AND paid_confirmed_at < $2
	), summary AS (
		SELECT COALESCE(COUNT(DISTINCT order_id),0) AS order_count,
			COALESCE(COUNT(DISTINCT order_id) FILTER (WHERE payer_customer_id IS NULL),0) AS missing_payer_count
		FROM paid_in_range
	), gross_rows AS (
		SELECT currency,COALESCE(SUM(amount_minor),0) AS amount_minor
		FROM paid_in_range GROUP BY currency
	), gross AS (
		SELECT COALESCE(jsonb_agg(jsonb_build_object('amount_minor',amount_minor,'currency',currency) ORDER BY currency),'[]'::jsonb) AS value
		FROM gross_rows
	), trend_currency AS (
		SELECT TO_CHAR(paid_confirmed_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD') AS date,
			currency,COUNT(DISTINCT order_id) AS order_count,COALESCE(SUM(amount_minor),0) AS amount_minor
		FROM paid_in_range GROUP BY 1,currency
	), trend_days AS (
		SELECT date,SUM(order_count) AS order_count,
			jsonb_agg(jsonb_build_object('amount_minor',amount_minor,'currency',currency) ORDER BY currency) AS gross
		FROM trend_currency GROUP BY date
	), trend AS (
		SELECT COALESCE(jsonb_agg(jsonb_build_object('date',date,'order_count',order_count,'gross',gross) ORDER BY date),'[]'::jsonb) AS value
		FROM trend_days
	), missing_rows AS (
		SELECT currency,COUNT(DISTINCT order_id) AS order_count,COALESCE(SUM(amount_minor),0) AS amount_minor
		FROM payments WHERE status='paid' AND paid_confirmed_at IS NULL GROUP BY currency
	), missing AS (
		SELECT COALESCE(SUM(order_count),0) AS order_count,
			COALESCE(jsonb_agg(jsonb_build_object('amount_minor',amount_minor,'currency',currency) ORDER BY currency),'[]'::jsonb) AS value
		FROM missing_rows
	)
	SELECT summary.order_count,summary.missing_payer_count,
		gross.value,trend.value,missing.order_count,missing.value
	FROM summary CROSS JOIN gross CROSS JOIN trend CROSS JOIN missing`, window.Start.UTC(), window.End.UTC()).Scan(
		&result.OrderCount, &result.MissingPayerCount,
		&grossJSON, &trendJSON, &result.MissingConfirmationEvidenceCount, &missingJSON,
	)
	if err != nil {
		return paymentport.PaidOverview{}, mapError(err)
	}
	if result.Gross, err = decodeOverviewMoney(grossJSON); err != nil {
		return paymentport.PaidOverview{}, err
	}
	if result.Trend, err = decodePaidTrend(trendJSON); err != nil {
		return paymentport.PaidOverview{}, err
	}
	if result.MissingConfirmationEvidenceAmount, err = decodeOverviewMoney(missingJSON); err != nil {
		return paymentport.PaidOverview{}, err
	}
	return result, nil
}

// ReadPaidOverviewPayerPage exposes a bounded, stable keyset of Payment's
// immutable historical payer facts. Canonicalization belongs to Identity and
// is composed by Payment's application reader in the same read transaction.
func (r *Repository) ReadPaidOverviewPayerPage(ctx context.Context, window paymentport.OverviewWindow, afterCustomerID customerdomain.CustomerID, limit int) (paymentport.PaidOverviewPayerPage, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.PaidOverviewPayerPage{}, err
	}
	if !window.Valid() || afterCustomerID < 0 || limit < 1 || limit > 500 {
		return paymentport.PaidOverviewPayerPage{}, paymentport.ErrInvalid
	}
	rows, err := t.Query(ctx, `SELECT DISTINCT payer_customer_id
		FROM payments
		WHERE status='paid' AND paid_confirmed_at IS NOT NULL
			AND paid_confirmed_at >= $1 AND paid_confirmed_at < $2
			AND payer_customer_id IS NOT NULL AND payer_customer_id > $3
		ORDER BY payer_customer_id
		LIMIT $4`, window.Start.UTC(), window.End.UTC(), int64(afterCustomerID), limit)
	if err != nil {
		return paymentport.PaidOverviewPayerPage{}, mapError(err)
	}
	defer rows.Close()
	result := paymentport.PaidOverviewPayerPage{CustomerIDs: []customerdomain.CustomerID{}}
	for rows.Next() {
		var customerID customerdomain.CustomerID
		if err = rows.Scan(&customerID); err != nil {
			return paymentport.PaidOverviewPayerPage{}, mapError(err)
		}
		if customerID < 1 {
			return paymentport.PaidOverviewPayerPage{}, paymentport.ErrInvalid
		}
		result.CustomerIDs = append(result.CustomerIDs, customerID)
	}
	if err = rows.Err(); err != nil {
		return paymentport.PaidOverviewPayerPage{}, mapError(err)
	}
	return result, nil
}

// ReadPaidOverviewRecords returns the exact paid-confirmation records behind
// the overview gross and business-order count. It is deliberately separate
// from Order: Payment owns the original paid-confirmation fact and a merchant
// reference is only unambiguous together with this row's provider.
func (r *Repository) ReadPaidOverviewRecords(ctx context.Context, window paymentport.OverviewWindow, after *paymentport.PaidOverviewRecordCursor, limit int) (paymentport.PaidOverviewRecordPage, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.PaidOverviewRecordPage{}, err
	}
	if !window.Valid() || limit < 1 || limit > 100 || (after != nil && !after.Valid()) {
		return paymentport.PaidOverviewRecordPage{}, paymentport.ErrInvalid
	}
	args := []any{window.Start.UTC(), window.End.UTC()}
	query := `SELECT provider,merchant_order_no,payer_customer_id,amount_minor,currency,paid_confirmed_at,id
		FROM payments
		WHERE status='paid' AND paid_confirmed_at IS NOT NULL
			AND paid_confirmed_at >= $1 AND paid_confirmed_at < $2`
	if after != nil {
		args = append(args, after.PaidConfirmedAt.UTC(), after.PaymentID)
		query += ` AND (paid_confirmed_at < $3 OR (paid_confirmed_at = $3 AND id < $4))`
	}
	args = append(args, limit+1)
	query += ` ORDER BY paid_confirmed_at DESC,id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := t.Query(ctx, query, args...)
	if err != nil {
		return paymentport.PaidOverviewRecordPage{}, mapError(err)
	}
	defer rows.Close()
	result := paymentport.PaidOverviewRecordPage{Items: []paymentport.PaidOverviewRecord{}}
	var lastPaymentID int64
	for rows.Next() {
		var item paymentport.PaidOverviewRecord
		var paymentID int64
		if err = rows.Scan(&item.Provider, &item.OrderReference, &item.PayerCustomerID, &item.AmountMinor, &item.Currency, &item.PaidConfirmedAt, &paymentID); err != nil {
			return paymentport.PaidOverviewRecordPage{}, mapError(err)
		}
		if !validPaidOverviewRecord(item, paymentID) {
			return paymentport.PaidOverviewRecordPage{}, paymentport.ErrInvalid
		}
		if len(result.Items) == limit {
			last := result.Items[len(result.Items)-1]
			result.NextCursor = &paymentport.PaidOverviewRecordCursor{PaidConfirmedAt: last.PaidConfirmedAt.UTC(), PaymentID: lastPaymentID}
			break
		}
		result.Items = append(result.Items, item)
		lastPaymentID = paymentID
	}
	if err = rows.Err(); err != nil {
		return paymentport.PaidOverviewRecordPage{}, mapError(err)
	}
	return result, nil
}

func validPaidOverviewRecord(item paymentport.PaidOverviewRecord, paymentID int64) bool {
	if paymentID < 1 || item.OrderReference == "" || item.AmountMinor < 1 || item.Currency == "" || item.PaidConfirmedAt.IsZero() {
		return false
	}
	if item.Provider != "wechat_pay" && item.Provider != "wechat_shop" {
		return false
	}
	return item.PayerCustomerID == nil || *item.PayerCustomerID > 0
}

type overviewMoneyJSON struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type paidTrendJSON struct {
	Date       string              `json:"date"`
	Gross      []overviewMoneyJSON `json:"gross"`
	OrderCount int64               `json:"order_count"`
}

func decodeOverviewMoney(raw []byte) ([]paymentport.OverviewMoney, error) {
	var decoded []overviewMoneyJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	result := make([]paymentport.OverviewMoney, 0, len(decoded))
	for _, item := range decoded {
		result = append(result, paymentport.OverviewMoney{AmountMinor: item.AmountMinor, Currency: item.Currency})
	}
	return result, nil
}

func decodePaidTrend(raw []byte) ([]paymentport.PaidOverviewTrend, error) {
	var decoded []paidTrendJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	result := make([]paymentport.PaidOverviewTrend, 0, len(decoded))
	for _, item := range decoded {
		gross := make([]paymentport.OverviewMoney, 0, len(item.Gross))
		for _, money := range item.Gross {
			gross = append(gross, paymentport.OverviewMoney{AmountMinor: money.AmountMinor, Currency: money.Currency})
		}
		result = append(result, paymentport.PaidOverviewTrend{Date: item.Date, Gross: gross, OrderCount: item.OrderCount})
	}
	return result, nil
}

// ReadRefundOverview deliberately selects the latest settlement append by its
// immutable audit sequence (id DESC), not by occurred_at. Provider callbacks
// can arrive late with an earlier provider event time; their later committed
// state transition is the fact that determines whether the refund is now
// completed, while occurred_at remains the correct completed-at metric time.
func (r *Repository) ReadRefundOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.RefundOverview{}, err
	}
	if !window.Valid() {
		return paymentport.RefundOverview{}, paymentport.ErrInvalid
	}
	// Keep the missing-evidence diagnosis and in-window completed aggregate in
	// one statement snapshot for the same reason as ReadPaidOverview above.
	result := paymentport.RefundOverview{}
	var completedJSON []byte
	err = t.QueryRow(ctx, `WITH latest_settlement AS (
		SELECT DISTINCT ON (audit.aggregate_id) audit.aggregate_id,audit.occurred_at
		FROM payment_audit_events audit
		WHERE audit.event_type='payment.refund_settled'
		ORDER BY audit.aggregate_id,audit.id DESC
	), historical_import AS (
		-- ImportTerminalRefund persists this event with the manifest's original
		-- refund OccurredAt, never the later import transaction time.
		SELECT DISTINCT ON (audit.aggregate_id) audit.aggregate_id,audit.occurred_at
		FROM payment_audit_events audit
		WHERE audit.event_type='payment.refund_history_imported'
		ORDER BY audit.aggregate_id,audit.id DESC
	), completed AS (
		SELECT payment.currency,refund.amount_minor,
			COALESCE(settlement.occurred_at,history.occurred_at) AS completed_at
		FROM payment_refunds refund
		JOIN payments payment ON payment.id=refund.payment_id
		LEFT JOIN latest_settlement settlement ON settlement.aggregate_id=refund.id AND NOT payment.historical
		LEFT JOIN historical_import history ON history.aggregate_id=refund.id AND payment.historical
		WHERE refund.status='completed'
	), missing AS (
		SELECT COALESCE(COUNT(*),0) AS count
		FROM completed WHERE completed_at IS NULL
	), in_range_rows AS (
		SELECT currency,COUNT(*) AS count,COALESCE(SUM(amount_minor),0) AS amount_minor
		FROM completed WHERE completed_at >= $1 AND completed_at < $2 GROUP BY currency
	), in_range AS (
		SELECT COALESCE(SUM(count),0) AS count,
			COALESCE(jsonb_agg(jsonb_build_object('amount_minor',amount_minor,'currency',currency) ORDER BY currency),'[]'::jsonb) AS value
		FROM in_range_rows
	)
	SELECT missing.count,in_range.count,in_range.value FROM missing CROSS JOIN in_range`, window.Start.UTC(), window.End.UTC()).Scan(
		&result.MissingCompletionEvidence, &result.CompletedCount, &completedJSON,
	)
	if err != nil {
		return paymentport.RefundOverview{}, mapError(err)
	}
	if result.Completed, err = decodeOverviewMoney(completedJSON); err != nil {
		return paymentport.RefundOverview{}, err
	}
	return result, nil
}

var _ paymentport.OverviewReader = (*Repository)(nil)
