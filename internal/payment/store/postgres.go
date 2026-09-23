package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type Repository struct{}

func NewPostgreSQL() *Repository             { return &Repository{} }
func tx(ctx context.Context) (pgx.Tx, error) { return platformpostgres.RequireTransaction(ctx) }

// ExternalOrderRefundSummaries is the only Payment projection consumed by the
// public Order API. It deliberately exposes amounts and terminal state only,
// never provider references, callback payloads, or effect identifiers.
func (r *Repository) ExternalOrderRefundSummaries(ctx context.Context, orderIDs []int64) (map[int64]paymentport.ExternalOrderRefundSummary, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]paymentport.ExternalOrderRefundSummary, len(orderIDs))
	if len(orderIDs) == 0 {
		return result, nil
	}
	rows, err := t.Query(ctx, `SELECT p.order_id,COUNT(r.id)>0,
COALESCE(SUM(r.amount_minor) FILTER (WHERE r.status='completed'),0),
COALESCE(SUM(r.amount_minor) FILTER (WHERE r.status IN ('requested','history_requested')),0),
COALESCE(SUM(r.amount_minor) FILTER (WHERE r.status IN ('effect_accepted','history_processing')),0),
COALESCE(SUM(r.amount_minor) FILTER (WHERE r.status='outcome_unknown'),0),
COALESCE(SUM(r.amount_minor) FILTER (WHERE r.status IN ('final_failed','history_failed','history_closed')),0)
FROM payments p LEFT JOIN payment_refunds r ON r.payment_id=p.id WHERE p.order_id=ANY($1) GROUP BY p.order_id`, orderIDs)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var s paymentport.ExternalOrderRefundSummary
		s.Available = true
		if err = rows.Scan(&id, &s.HasRefund, &s.CompletedMinor, &s.RequestedMinor, &s.ProcessingMinor, &s.OutcomeUnknownMinor, &s.FinalFailedMinor); err != nil {
			return nil, mapError(err)
		}
		result[id] = s
	}
	return result, mapError(rows.Err())
}

func (r *Repository) ExternalOrderRefundDetails(ctx context.Context, orderIDs []int64) (map[int64][]paymentport.ExternalOrderRefundDetail, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	result := map[int64][]paymentport.ExternalOrderRefundDetail{}
	if len(orderIDs) == 0 {
		return result, nil
	}
	rows, err := t.Query(ctx, `SELECT p.order_id,r.id,r.status,r.amount_minor,r.created_at,r.updated_at FROM payments p JOIN payment_refunds r ON r.payment_id=p.id WHERE p.order_id=ANY($1) ORDER BY p.order_id,r.created_at,r.id`, orderIDs)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var orderID int64
		var detail paymentport.ExternalOrderRefundDetail
		if err = rows.Scan(&orderID, &detail.RefundID, &detail.Status, &detail.AmountMinor, &detail.CreatedAt, &detail.UpdatedAt); err != nil {
			return nil, mapError(err)
		}
		result[orderID] = append(result[orderID], detail)
	}
	return result, mapError(rows.Err())
}

// ExternalRefundedOrderIDs returns only orders with terminal completed money.
// Requested, accepted, unknown and failed refunds never make is_refunded true.
func (r *Repository) ExternalRefundedOrderIDs(ctx context.Context, customerIDs []int64) ([]int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	args := []any{}
	where := "r.status='completed'"
	if len(customerIDs) > 0 {
		args = append(args, customerIDs)
		where += " AND (p.payer_customer_id=ANY($1) OR p.beneficiary_customer_id=ANY($1))"
	}
	rows, err := t.Query(ctx, `SELECT DISTINCT p.order_id FROM payments p JOIN payment_refunds r ON r.payment_id=p.id WHERE `+where+` ORDER BY p.order_id`, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapError(rows.Err())
}

// ExternalRefundKnownOrderIDs identifies orders whose refund state is backed
// by Payment's local projection. It lets Order distinguish `false` from an
// order for which no Payment/refund facts are available at all.
func (r *Repository) ExternalRefundKnownOrderIDs(ctx context.Context, customerIDs []int64) ([]int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	args := []any{}
	where := "TRUE"
	if len(customerIDs) > 0 {
		args = append(args, customerIDs)
		where = "(p.payer_customer_id=ANY($1) OR p.beneficiary_customer_id=ANY($1))"
	}
	rows, err := t.Query(ctx, `SELECT DISTINCT p.order_id FROM payments p WHERE `+where+` ORDER BY p.order_id`, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapError(rows.Err())
}

var _ paymentport.ExternalOrderRefundReader = (*Repository)(nil)

func (r *Repository) CreatePayment(ctx context.Context, p domain.Payment, key, payload [32]byte, actor string) (domain.Payment, bool, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, false, e
	}
	if _, e = t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "payment:create:"+hex.EncodeToString(key[:])); e != nil {
		return domain.Payment{}, false, e
	}
	var resultID int64
	var oldPayload []byte
	e = t.QueryRow(ctx, `SELECT result_id,payload_digest FROM payment_operation_receipts WHERE operation='create' AND actor_scope=$1 AND key_digest=$2`, actor, key[:]).Scan(&resultID, &oldPayload)
	if e == nil {
		if string(oldPayload) != string(payload[:]) {
			return domain.Payment{}, false, paymentport.ErrConflict
		}
		found, e := r.GetPayment(ctx, resultID, false)
		return found, false, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return domain.Payment{}, false, e
	}
	e = t.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,profit_sharing_marked,currency,status,version,created_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)RETURNING id`, p.OrderID, p.Provider, p.Channel, p.MerchantOrderNo, p.PayerIdentityID, p.PayerCustomerID, p.BeneficiaryCustomerID, p.AmountMinor, p.ProfitSharingMarked, p.Currency, p.Status, p.Version, p.CreatedAt, p.UpdatedAt).Scan(&p.ID)
	if e != nil {
		return domain.Payment{}, false, mapError(e)
	}
	if _, e = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at)VALUES('create',$1,$2,$3,'payment',$4,$5)`, actor, key[:], payload[:], p.ID, p.CreatedAt); e != nil {
		return domain.Payment{}, false, mapError(e)
	}
	if e = appendFacts(ctx, t, "payment.created", p.ID, actor, p.CreatedAt); e != nil {
		return domain.Payment{}, false, e
	}
	return p, true, nil
}

func (r *Repository) ReplayPayment(ctx context.Context, key, payload [32]byte, actor string) (domain.Payment, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Payment{}, false, err
	}
	var resultID int64
	var oldPayload []byte
	var resultKind string
	err = t.QueryRow(ctx, `SELECT result_id,payload_digest,result_kind FROM payment_operation_receipts WHERE operation='create' AND actor_scope=$1 AND key_digest=$2`, actor, key[:]).Scan(&resultID, &oldPayload, &resultKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, false, nil
	}
	if err != nil {
		return domain.Payment{}, false, mapError(err)
	}
	if string(oldPayload) != string(payload[:]) || resultKind != "payment" {
		return domain.Payment{}, false, paymentport.ErrConflict
	}
	payment, err := r.GetPayment(ctx, resultID, false)
	return payment, err == nil, err
}

func (r *Repository) BindPaymentEffect(ctx context.Context, p domain.Payment, intent effectport.PaymentV1Intent, snapshot map[string]any) (domain.Payment, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	id, e := effectNumeric(p.EffectID)
	if e != nil {
		return domain.Payment{}, e
	}
	raw, _ := json.Marshal(snapshot)
	result, e := t.Exec(ctx, `UPDATE payments SET external_effect_id=$2,version=$3,updated_at=$4 WHERE id=$1 AND version=$5 AND external_effect_id IS NULL`, p.ID, id, p.Version, p.UpdatedAt, p.Version-1)
	if e != nil || result.RowsAffected() != 1 {
		if e != nil {
			return domain.Payment{}, mapError(e)
		}
		return domain.Payment{}, paymentport.ErrConflict
	}
	if _, e = t.Exec(ctx, `INSERT INTO payment_provider_intents(payment_id,effect_kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,request_snapshot,created_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, intent.Kind, intent.SourceRefDigest, intent.TargetRefDigest, intent.PayloadDigest, intent.PolicyVersionHash, raw, p.UpdatedAt); e != nil {
		return domain.Payment{}, mapError(e)
	}
	return p, nil
}

func (r *Repository) GetPayment(ctx context.Context, id int64, lock bool) (domain.Payment, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	q := `SELECT id,order_id,provider,payment_channel,merchant_order_no,COALESCE(payer_identity_id,0),COALESCE(payer_customer_id,0),COALESCE(beneficiary_customer_id,0),amount_minor,profit_sharing_marked,currency,status,COALESCE('eer_'||external_effect_id::text,''),COALESCE(provider_transaction_reference,''),COALESCE(provider_transaction_digest,''),version,paid_confirmed_at,created_at,updated_at,historical,source_status,history_reason FROM payments WHERE id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	var p domain.Payment
	e = t.QueryRow(ctx, q, id).Scan(&p.ID, &p.OrderID, &p.Provider, &p.Channel, &p.MerchantOrderNo, &p.PayerIdentityID, &p.PayerCustomerID, &p.BeneficiaryCustomerID, &p.AmountMinor, &p.ProfitSharingMarked, &p.Currency, &p.Status, &p.EffectID, &p.ProviderTransactionReference, &p.ProviderTransactionDigest, &p.Version, &p.PaidConfirmedAt, &p.CreatedAt, &p.UpdatedAt, &p.Historical, &p.SourceStatus, &p.HistoryReason)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	return p, mapError(e)
}

func (r *Repository) RefundRelatedOrderIDsWithin(ctx context.Context, orderIDs []int64) (map[int64]struct{}, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]struct{})
	if len(orderIDs) == 0 {
		return result, nil
	}
	for _, id := range orderIDs {
		if id < 1 {
			return nil, paymentport.ErrInvalid
		}
	}
	rows, err := tx.Query(ctx, `
SELECT DISTINCT p.order_id
FROM payment_refunds r
JOIN payments p ON p.id=r.payment_id
WHERE p.order_id=ANY($1::bigint[])
  AND r.status IN ('requested','effect_accepted','outcome_unknown','completed','history_requested','history_processing')`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

func (r *Repository) ReservedRefundMinor(ctx context.Context, paymentID int64) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	err = t.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0)::bigint FROM payment_refunds WHERE payment_id=$1 AND status NOT IN ('final_failed','history_failed','history_closed')`, paymentID).Scan(&total)
	return total, mapError(err)
}

// HasNonTerminalRefund is called only after the owning Payment row is locked.
// It gates a new WeChat Pay key while an earlier refund still has an uncertain
// external outcome; completed and final-failed refunds remain eligible for a
// later explicit partial refund when the remaining amount permits it.
func (r *Repository) HasNonTerminalRefund(ctx context.Context, paymentID int64) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_refunds WHERE payment_id=$1 AND status IN ('requested','effect_accepted','outcome_unknown'))`, paymentID).Scan(&exists)
	return exists, mapError(err)
}

func (r *Repository) GetHandoff(ctx context.Context, paymentID int64) (paymentport.Handoff, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.Handoff{}, err
	}
	var out paymentport.Handoff
	out.PaymentID = paymentID
	err = t.QueryRow(ctx, `SELECT payload,expires_at FROM payment_handoffs WHERE payment_id=$1`, paymentID).Scan(&out.Payload, &out.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentport.Handoff{}, paymentport.ErrNotFound
	}
	if err != nil {
		return paymentport.Handoff{}, mapError(err)
	}
	if !json.Valid(out.Payload) {
		return paymentport.Handoff{}, paymentport.ErrConflict
	}
	return out, nil
}

func (r *Repository) GetPaymentByMerchant(ctx context.Context, merchantOrderNo string, lock bool) (domain.Payment, error) {
	return r.GetPaymentByMerchantProvider(ctx, domain.ProviderWeChatPay, merchantOrderNo, lock)
}

func (r *Repository) GetPaymentByMerchantProvider(ctx context.Context, provider domain.Provider, merchantOrderNo string, lock bool) (domain.Payment, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	query := `SELECT id FROM payments WHERE provider=$1 AND merchant_order_no=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	err = t.QueryRow(ctx, query, provider, merchantOrderNo).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	if err != nil {
		return domain.Payment{}, mapError(err)
	}
	return r.GetPayment(ctx, id, false)
}

func (r *Repository) CreateRefund(ctx context.Context, v domain.Refund, key, payload [32]byte, actor string) (domain.Refund, bool, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Refund{}, false, e
	}
	if _, e = t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "payment:refund:"+hex.EncodeToString(key[:])); e != nil {
		return domain.Refund{}, false, e
	}
	var id int64
	var old []byte
	e = t.QueryRow(ctx, `SELECT result_id,payload_digest FROM payment_operation_receipts WHERE operation='refund' AND actor_scope=$1 AND key_digest=$2`, actor, key[:]).Scan(&id, &old)
	if e == nil {
		if string(old) != string(payload[:]) {
			return domain.Refund{}, false, paymentport.ErrConflict
		}
		found, e := r.GetRefund(ctx, id, false)
		return found, false, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return domain.Refund{}, false, e
	}
	e = t.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)RETURNING id`, v.PaymentID, v.Provider, v.RefundNo, v.AmountMinor, v.Reason, v.Status, v.Version, v.CreatedAt, v.UpdatedAt).Scan(&v.ID)
	if e != nil {
		return domain.Refund{}, false, mapError(e)
	}
	if _, e = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at)VALUES('refund',$1,$2,$3,'refund',$4,$5)`, actor, key[:], payload[:], v.ID, v.CreatedAt); e != nil {
		return domain.Refund{}, false, mapError(e)
	}
	if e = appendFacts(ctx, t, "payment.refund_requested", v.ID, actor, v.CreatedAt); e != nil {
		return domain.Refund{}, false, e
	}
	return v, true, nil
}

func (r *Repository) ReplayRefund(ctx context.Context, key, payload [32]byte, actor string) (domain.Refund, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Refund{}, false, err
	}
	var resultID int64
	var oldPayload []byte
	var resultKind string
	err = t.QueryRow(ctx, `SELECT result_id,payload_digest,result_kind FROM payment_operation_receipts WHERE operation='refund' AND actor_scope=$1 AND key_digest=$2`, actor, key[:]).Scan(&resultID, &oldPayload, &resultKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Refund{}, false, nil
	}
	if err != nil {
		return domain.Refund{}, false, mapError(err)
	}
	if string(oldPayload) != string(payload[:]) || resultKind != "refund" {
		return domain.Refund{}, false, paymentport.ErrConflict
	}
	refund, err := r.GetRefund(ctx, resultID, false)
	return refund, err == nil, err
}

// FindRefundByIdempotencyKey is the recovery-only counterpart to ReplayRefund.
// It deliberately compares no payload because the caller no longer holds it;
// actor scope and the outer Payment identity remain mandatory boundaries.
func (r *Repository) FindRefundByIdempotencyKey(ctx context.Context, key [32]byte, actor string) (domain.Refund, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Refund{}, false, err
	}
	var resultID int64
	var resultKind string
	err = t.QueryRow(ctx, `SELECT result_id,result_kind FROM payment_operation_receipts WHERE operation='refund' AND actor_scope=$1 AND key_digest=$2`, actor, key[:]).Scan(&resultID, &resultKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Refund{}, false, nil
	}
	if err != nil {
		return domain.Refund{}, false, mapError(err)
	}
	if resultKind != "refund" || resultID < 1 {
		return domain.Refund{}, false, nil
	}
	refund, err := r.GetRefund(ctx, resultID, false)
	if errors.Is(err, paymentport.ErrNotFound) {
		return domain.Refund{}, false, nil
	}
	if err != nil {
		return domain.Refund{}, false, err
	}
	return refund, true, nil
}
func (r *Repository) BindRefundEffect(ctx context.Context, v domain.Refund, intent effectport.PaymentV1Intent, snapshot map[string]any) (domain.Refund, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Refund{}, e
	}
	id, e := effectNumeric(v.EffectID)
	if e != nil {
		return domain.Refund{}, e
	}
	raw, _ := json.Marshal(snapshot)
	result, e := t.Exec(ctx, `UPDATE payment_refunds SET status=$2,external_effect_id=$3,version=$4,updated_at=$5 WHERE id=$1 AND version=$6 AND external_effect_id IS NULL`, v.ID, v.Status, id, v.Version, v.UpdatedAt, v.Version-1)
	if e != nil || result.RowsAffected() != 1 {
		if e != nil {
			return domain.Refund{}, mapError(e)
		}
		return domain.Refund{}, paymentport.ErrConflict
	}
	if _, e = t.Exec(ctx, `INSERT INTO payment_provider_intents(refund_id,effect_kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,request_snapshot,created_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, intent.Kind, intent.SourceRefDigest, intent.TargetRefDigest, intent.PayloadDigest, intent.PolicyVersionHash, raw, v.UpdatedAt); e != nil {
		return domain.Refund{}, mapError(e)
	}
	return v, nil
}
func (r *Repository) GetRefund(ctx context.Context, id int64, lock bool) (domain.Refund, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Refund{}, e
	}
	q := `SELECT id,payment_id,provider,refund_no,reason,amount_minor,status,COALESCE('eer_'||external_effect_id::text,''),COALESCE(provider_refund_reference,''),COALESCE(provider_refund_digest,''),version,created_at,updated_at FROM payment_refunds WHERE id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	var v domain.Refund
	e = t.QueryRow(ctx, q, id).Scan(&v.ID, &v.PaymentID, &v.Provider, &v.RefundNo, &v.Reason, &v.AmountMinor, &v.Status, &v.EffectID, &v.ProviderRefundReference, &v.ProviderRefundDigest, &v.Version, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.Refund{}, paymentport.ErrNotFound
	}
	return v, mapError(e)
}

func (r *Repository) GetRefundByNumber(ctx context.Context, refundNo string, lock bool) (domain.Refund, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Refund{}, err
	}
	query := `SELECT id FROM payment_refunds WHERE refund_no=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	err = t.QueryRow(ctx, query, refundNo).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Refund{}, paymentport.ErrNotFound
	}
	if err != nil {
		return domain.Refund{}, mapError(err)
	}
	return r.GetRefund(ctx, id, false)
}

func (r *Repository) GetRefundByProviderReference(ctx context.Context, provider domain.Provider, reference string, lock bool) (domain.Refund, error) {
	t, err := tx(ctx)
	if err != nil {
		return domain.Refund{}, err
	}
	query := `SELECT id FROM payment_refunds WHERE provider=$1 AND provider_refund_reference=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var id int64
	err = t.QueryRow(ctx, query, provider, reference).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Refund{}, paymentport.ErrNotFound
	}
	if err != nil {
		return domain.Refund{}, mapError(err)
	}
	return r.GetRefund(ctx, id, false)
}

func (r *Repository) GetShopRefundMaterial(ctx context.Context, refundID int64) (paymentport.ShopRefundMaterial, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.ShopRefundMaterial{}, err
	}
	var out paymentport.ShopRefundMaterial
	var snapshot []byte
	err = t.QueryRow(ctx, `
		SELECT r.id,r.payment_id,r.refund_no,r.amount_minor,p.currency,i.request_snapshot
		FROM payment_refunds r JOIN payments p ON p.id=r.payment_id
		JOIN payment_provider_intents i ON i.refund_id=r.id AND i.effect_kind='wechat_shop_refund_v1'
		WHERE r.id=$1 AND r.provider='wechat_shop'`, refundID).Scan(&out.RefundID, &out.PaymentID, &out.RefundNo, &out.AmountMinor, &out.Currency, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentport.ShopRefundMaterial{}, paymentport.ErrNotFound
	}
	if err != nil {
		return paymentport.ShopRefundMaterial{}, mapError(err)
	}
	var material struct {
		ProviderOrderID string `json:"provider_order_id"`
		ProductID       string `json:"product_id"`
		SKUID           string `json:"sku_id"`
		RefundCount     int64  `json:"refund_count"`
		ReasonCode      string `json:"reason_code"`
	}
	if json.Unmarshal(snapshot, &material) != nil {
		return paymentport.ShopRefundMaterial{}, paymentport.ErrConflict
	}
	out.ProviderOrderID, out.ProductID, out.SKUID, out.RefundCount, out.ReasonCode = material.ProviderOrderID, material.ProductID, material.SKUID, material.RefundCount, material.ReasonCode
	return out, nil
}

func (r *Repository) RecordReconciliation(ctx context.Context, refundID int64, evidence effectport.Digest, outcome string, now time.Time) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	if !effectport.ValidDigest(evidence) || (outcome != "refunded" && outcome != "pending" && outcome != "not_found" && outcome != "final_failed") {
		return false, paymentport.ErrConflict
	}
	digest, err := strconvDigest(evidence)
	if err != nil {
		return false, paymentport.ErrConflict
	}
	result, err := t.Exec(ctx, `INSERT INTO payment_reconciliations(refund_id,evidence_digest,outcome,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(evidence_digest) DO NOTHING`, refundID, digest, outcome, now)
	if err != nil {
		return false, mapError(err)
	}
	return result.RowsAffected() == 1, nil
}

func (r *Repository) RecordPaymentReconciliation(ctx context.Context, paymentID int64, evidence effectport.Digest, outcome string, now time.Time) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	if !effectport.ValidDigest(evidence) || (outcome != "paid" && outcome != "pending" && outcome != "not_found" && outcome != "final_failed") {
		return false, paymentport.ErrConflict
	}
	digest, err := strconvDigest(evidence)
	if err != nil {
		return false, paymentport.ErrConflict
	}
	result, err := t.Exec(ctx, `INSERT INTO payment_reconciliations(payment_id,evidence_digest,outcome,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(evidence_digest) DO NOTHING`, paymentID, digest, outcome, now)
	if err != nil {
		return false, mapError(err)
	}
	return result.RowsAffected() == 1, nil
}

func (r *Repository) ListRefunds(ctx context.Context, limit, offset int32) ([]paymentport.RefundProjection, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM payment_refunds`).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `
		SELECT r.id,r.payment_id,r.provider,r.refund_no,r.reason,r.amount_minor,r.status,
			COALESCE('eer_'||r.external_effect_id::text,''),COALESCE(r.provider_refund_reference,''),COALESCE(r.provider_refund_digest,''),r.version,r.created_at,r.updated_at,
			p.order_id,p.merchant_order_no,p.amount_minor,p.currency
		FROM payment_refunds r JOIN payments p ON p.id=r.payment_id
		ORDER BY r.created_at DESC,r.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	result := make([]paymentport.RefundProjection, 0, limit)
	for rows.Next() {
		var item paymentport.RefundProjection
		if err = rows.Scan(&item.Refund.ID, &item.Refund.PaymentID, &item.Refund.Provider, &item.Refund.RefundNo, &item.Refund.Reason, &item.Refund.AmountMinor, &item.Refund.Status, &item.Refund.EffectID, &item.Refund.ProviderRefundReference, &item.Refund.ProviderRefundDigest, &item.Refund.Version, &item.Refund.CreatedAt, &item.Refund.UpdatedAt, &item.OrderID, &item.MerchantOrder, &item.OrderAmount, &item.Currency); err != nil {
			return nil, 0, mapError(err)
		}
		result = append(result, item)
	}
	return result, total, mapError(rows.Err())
}

// ListRefundsForPayment keeps a detail timeline anchored to the Payment that
// owns the requested provider/merchant-order pair. The uniqueness constraints
// on payments and orders make this an exact association, rather than a
// presentation-side best effort over a global refund page.
func (r *Repository) ListRefundsForPayment(ctx context.Context, provider domain.Provider, merchantOrderNo string, limit, offset int32) ([]paymentport.RefundProjection, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM payment_refunds r JOIN payments p ON p.id=r.payment_id WHERE p.provider=$1 AND p.merchant_order_no=$2`, provider, merchantOrderNo).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `
		SELECT r.id,r.payment_id,r.provider,r.refund_no,r.reason,r.amount_minor,r.status,
			COALESCE('eer_'||r.external_effect_id::text,''),COALESCE(r.provider_refund_reference,''),COALESCE(r.provider_refund_digest,''),r.version,r.created_at,r.updated_at,
			p.order_id,p.merchant_order_no,p.amount_minor,p.currency
		FROM payment_refunds r JOIN payments p ON p.id=r.payment_id
		WHERE p.provider=$1 AND p.merchant_order_no=$2
		ORDER BY r.created_at DESC,r.id DESC LIMIT $3 OFFSET $4`, provider, merchantOrderNo, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	result := make([]paymentport.RefundProjection, 0, limit)
	for rows.Next() {
		var item paymentport.RefundProjection
		if err = rows.Scan(&item.Refund.ID, &item.Refund.PaymentID, &item.Refund.Provider, &item.Refund.RefundNo, &item.Refund.Reason, &item.Refund.AmountMinor, &item.Refund.Status, &item.Refund.EffectID, &item.Refund.ProviderRefundReference, &item.Refund.ProviderRefundDigest, &item.Refund.Version, &item.Refund.CreatedAt, &item.Refund.UpdatedAt, &item.OrderID, &item.MerchantOrder, &item.OrderAmount, &item.Currency); err != nil {
			return nil, 0, mapError(err)
		}
		result = append(result, item)
	}
	return result, total, mapError(rows.Err())
}

func (r *Repository) ListEffectBindings(ctx context.Context, provider domain.Provider, merchantOrderNo string) ([]paymentport.EffectProjection, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `
		SELECT 'eer_'||p.external_effect_id::text, i.effect_kind
		FROM payments p JOIN payment_provider_intents i ON i.payment_id=p.id
		WHERE p.provider=$1 AND p.merchant_order_no=$2 AND p.external_effect_id IS NOT NULL
		UNION ALL
		SELECT 'eer_'||r.external_effect_id::text, i.effect_kind
		FROM payments p JOIN payment_refunds r ON r.payment_id=p.id
		JOIN payment_provider_intents i ON i.refund_id=r.id
		WHERE p.provider=$1 AND p.merchant_order_no=$2 AND r.external_effect_id IS NOT NULL
		ORDER BY 1`, provider, merchantOrderNo)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]paymentport.EffectProjection, 0, 4)
	for rows.Next() {
		var item paymentport.EffectProjection
		if err = rows.Scan(&item.EffectID, &item.Kind); err != nil {
			return nil, mapError(err)
		}
		result = append(result, item)
	}
	return result, mapError(rows.Err())
}

func (r *Repository) ClaimCallback(ctx context.Context, provider string, eventDigest, bodyDigest [32]byte, kind, outcome string, id int64) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	if outcome != "settled" && outcome != "replayed" && outcome != "query_required" {
		return false, paymentport.ErrConflict
	}
	var existingBody []byte
	var paymentID, refundID *int64
	err = t.QueryRow(ctx, `SELECT body_digest,payment_id,refund_id FROM payment_callback_receipts WHERE provider=$1 AND event_digest=$2`, provider, eventDigest[:]).Scan(&existingBody, &paymentID, &refundID)
	if err == nil {
		matchesID := kind == "payment" && paymentID != nil && *paymentID == id || kind == "refund" && refundID != nil && *refundID == id
		if string(existingBody) != string(bodyDigest[:]) || !matchesID {
			return false, paymentport.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, mapError(err)
	}
	var paymentValue, refundValue any
	if kind == "payment" {
		paymentValue = id
	} else if kind == "refund" {
		refundValue = id
	} else {
		return false, paymentport.ErrConflict
	}
	_, err = t.Exec(ctx, `INSERT INTO payment_callback_receipts(provider,event_digest,body_digest,signature_verified,outcome,payment_id,refund_id) VALUES($1,$2,$3,true,$4,$5,$6)`, provider, eventDigest[:], bodyDigest[:], outcome, paymentValue, refundValue)
	return false, mapError(err)
}

func (r *Repository) ImportTerminalPayment(ctx context.Context, payment domain.Payment, digest [32]byte, runID string) (domain.Payment, error) {
	// Older history snapshots may prove a terminal paid ledger row without
	// preserving the provider's immutable confirmation timestamp. Keep that
	// ledger import nullable. Distribution's evidence check rejects nil instead
	// of turning a later import/update timestamp into a payment fact.
	if payment.EffectID != "" || (payment.Status != domain.StatusPaid && payment.Status != domain.StatusFailed && payment.Status != domain.StatusCancelled) || payment.PayerIdentityID < 0 || payment.PayerCustomerID < 0 || payment.BeneficiaryCustomerID < 0 || ((payment.PayerIdentityID == 0) != (payment.PayerCustomerID == 0)) || (payment.PayerCustomerID == 0 && payment.BeneficiaryCustomerID != 0) || (payment.PaidConfirmedAt != nil && (payment.Status != domain.StatusPaid || payment.PaidConfirmedAt.IsZero() || payment.PaidConfirmedAt.Before(payment.CreatedAt) || payment.PaidConfirmedAt.After(payment.UpdatedAt))) {
		return domain.Payment{}, paymentport.ErrConflict
	}
	payment.Historical = true
	t, err := tx(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	key := sha256.Sum256([]byte("payment-history:" + runID + ":" + payment.MerchantOrderNo))
	var resultID int64
	var oldDigest []byte
	err = t.QueryRow(ctx, `SELECT result_id,payload_digest FROM payment_operation_receipts WHERE operation='history_import' AND actor_scope=$1 AND key_digest=$2`, runID, key[:]).Scan(&resultID, &oldDigest)
	if err == nil {
		if string(oldDigest) != string(digest[:]) {
			return domain.Payment{}, paymentport.ErrConflict
		}
		return r.GetPayment(ctx, resultID, false)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, mapError(err)
	}
	if payment.Channel == "" {
		payment.Channel = domain.ChannelMiniProgram
	}
	existing, lookupErr := r.GetPaymentByMerchantProvider(ctx, payment.Provider, payment.MerchantOrderNo, true)
	if lookupErr == nil {
		return r.importPaymentDelta(ctx, existing, payment, digest, runID, key)
	}
	if !errors.Is(lookupErr, paymentport.ErrNotFound) {
		return domain.Payment{}, lookupErr
	}
	err = t.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,provider_transaction_digest,version,paid_confirmed_at,created_at,updated_at,historical,source_status,history_reason) VALUES($1,$2,$3,$4,NULLIF($5,0),NULLIF($6,0),NULLIF($7,0),$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,true,$16,$17) RETURNING id`, payment.OrderID, payment.Provider, payment.Channel, payment.MerchantOrderNo, payment.PayerIdentityID, payment.PayerCustomerID, payment.BeneficiaryCustomerID, payment.AmountMinor, payment.Currency, payment.Status, payment.ProviderTransactionDigest, payment.Version, payment.PaidConfirmedAt, payment.CreatedAt, payment.UpdatedAt, payment.SourceStatus, payment.HistoryReason).Scan(&payment.ID)
	if err != nil {
		return domain.Payment{}, mapError(err)
	}
	_, err = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('history_import',$1,$2,$3,'payment',$4,$5)`, runID, key[:], digest[:], payment.ID, payment.CreatedAt)
	if err != nil {
		return domain.Payment{}, mapError(err)
	}
	if err = appendFacts(ctx, t, "payment.history_imported", payment.ID, "migration:"+runID, payment.CreatedAt); err != nil {
		return domain.Payment{}, err
	}
	return payment, nil
}

func (r *Repository) ImportTerminalRefund(ctx context.Context, refund domain.Refund, digest [32]byte, runID string) (domain.Refund, error) {
	if !refund.Status.HistoricalImportable() || refund.EffectID != "" {
		return domain.Refund{}, paymentport.ErrConflict
	}
	t, err := tx(ctx)
	if err != nil {
		return domain.Refund{}, err
	}
	key := sha256.Sum256([]byte("refund-history:" + runID + ":" + refund.RefundNo))
	var resultID int64
	var oldDigest []byte
	err = t.QueryRow(ctx, `SELECT result_id,payload_digest FROM payment_operation_receipts WHERE operation='history_import' AND actor_scope=$1 AND key_digest=$2`, runID, key[:]).Scan(&resultID, &oldDigest)
	if err == nil {
		if string(oldDigest) != string(digest[:]) {
			return domain.Refund{}, paymentport.ErrConflict
		}
		return r.GetRefund(ctx, resultID, false)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Refund{}, mapError(err)
	}
	existing, lookupErr := r.GetRefundByNumber(ctx, refund.RefundNo, true)
	if lookupErr == nil {
		return r.importRefundDelta(ctx, existing, refund, digest, runID, key)
	}
	if !errors.Is(lookupErr, paymentport.ErrNotFound) {
		return domain.Refund{}, lookupErr
	}
	err = t.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,provider_refund_digest,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10) RETURNING id`, refund.PaymentID, refund.Provider, refund.RefundNo, refund.AmountMinor, refund.Reason, refund.Status, refund.ProviderRefundDigest, refund.Version, refund.CreatedAt, refund.UpdatedAt).Scan(&refund.ID)
	if err != nil {
		return domain.Refund{}, mapError(err)
	}
	_, err = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('history_import',$1,$2,$3,'refund',$4,$5)`, runID, key[:], digest[:], refund.ID, refund.CreatedAt)
	if err != nil {
		return domain.Refund{}, mapError(err)
	}
	if err = appendFacts(ctx, t, "payment.refund_history_imported", refund.ID, "migration:"+runID, refund.CreatedAt); err != nil {
		return domain.Refund{}, err
	}
	return refund, nil
}

func (r *Repository) ProviderIntent(ctx context.Context, kind effectport.Kind, source effectport.Digest) (paymentport.ProviderIntent, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.ProviderIntent{}, err
	}
	var out paymentport.ProviderIntent
	var storedKind, storedSource, payload string
	var requestSnapshot []byte
	err = t.QueryRow(ctx, `
		SELECT i.effect_kind,i.source_ref_digest,i.payload_digest,i.request_snapshot,
			COALESCE(p.id, rp.id),COALESCE(i.refund_id,0),
			COALESCE(p.payer_identity_id,rp.payer_identity_id),COALESCE(p.payment_channel,rp.payment_channel),COALESCE(p.merchant_order_no,rp.merchant_order_no),
			COALESCE(r.refund_no,''),COALESCE(r.reason,''),
			COALESCE(r.amount_minor,p.amount_minor),COALESCE(p.amount_minor,rp.amount_minor),COALESCE(p.profit_sharing_marked,false),COALESCE(p.currency,rp.currency)
		FROM payment_provider_intents i
		LEFT JOIN payments p ON p.id=i.payment_id
		LEFT JOIN payment_refunds r ON r.id=i.refund_id
		LEFT JOIN payments rp ON rp.id=r.payment_id
		WHERE i.effect_kind=$1 AND i.source_ref_digest=$2`, kind, source,
	).Scan(&storedKind, &storedSource, &payload, &requestSnapshot, &out.PaymentID, &out.RefundID,
		&out.PayerIdentityID, &out.Channel, &out.MerchantOrderNo, &out.RefundNo, &out.RefundReason,
		&out.AmountMinor, &out.TotalMinor, &out.ProfitSharingMarked, &out.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentport.ProviderIntent{}, paymentport.ErrNotFound
	}
	if err != nil {
		return paymentport.ProviderIntent{}, mapError(err)
	}
	out.Kind = effectport.Kind(storedKind)
	out.SourceRefDigest = effectport.Digest(storedSource)
	out.PayloadDigest = effectport.Digest(payload)
	if out.Kind == effectport.KindWeChatShopRefund {
		var material struct {
			ProviderOrderID string `json:"provider_order_id"`
			ProductID       string `json:"product_id"`
			SKUID           string `json:"sku_id"`
			RefundCount     int64  `json:"refund_count"`
			ReasonCode      string `json:"reason_code"`
		}
		if json.Unmarshal(requestSnapshot, &material) != nil {
			return paymentport.ProviderIntent{}, paymentport.ErrConflict
		}
		out.ProviderOrderID, out.ProductID, out.SKUID = material.ProviderOrderID, material.ProductID, material.SKUID
		out.RefundCount, out.ReasonCode = material.RefundCount, material.ReasonCode
	}
	if out.Kind != kind || out.SourceRefDigest != source || !effectport.ValidDigest(out.PayloadDigest) {
		return paymentport.ProviderIntent{}, paymentport.ErrConflict
	}
	return out, nil
}

func (r *Repository) CompleteEffectWithin(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	effectID, err := effectNumeric(effectRef)
	if err != nil || envelope.Owner != effectport.OwnerPayment || attempt.Number < 1 {
		return paymentport.ErrConflict
	}
	var paymentID, refundID int64
	err = t.QueryRow(ctx, `SELECT COALESCE(payment_id,0),COALESCE(refund_id,0) FROM payment_provider_intents WHERE effect_kind=$1 AND source_ref_digest=$2`, envelope.Kind, envelope.SourceRefDigest).Scan(&paymentID, &refundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentport.ErrNotFound
	}
	if err != nil {
		return mapError(err)
	}
	now := time.Now().UTC()
	switch envelope.Kind {
	case effectport.KindWeChatPayPrepay, effectport.KindAlipayWapPay, effectport.KindAlipayPagePay:
		status := domain.StatusFailed
		if result.Completion == effectport.StateExecuted {
			// An explicitly reviewed legacy checkout must never acquire a late
			// client payment credential. Verified settlement callbacks are separate.
			var recovery bool
			if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_checkout_restart_permissions WHERE payment_id=$1)`, paymentID).Scan(&recovery); err != nil {
				return mapError(err)
			}
			if recovery {
				return paymentport.ErrConflict
			}
			status = domain.StatusAwaitingPayment
		} else if result.Completion == effectport.StateUnknown || result.Completion == effectport.StateRetryable {
			return nil
		}
		updated, err := t.Exec(ctx, `UPDATE payments SET status=$2,version=version+1,updated_at=$3 WHERE id=$1 AND external_effect_id=$4 AND status='awaiting_prepay'`, paymentID, status, now, effectID)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return mapError(err)
			}
			return paymentport.ErrConflict
		}
		if status == domain.StatusAwaitingPayment {
			expectedArtifact := "wechat_pay_jsapi_handoff_v1"
			if envelope.Kind == effectport.KindAlipayWapPay || envelope.Kind == effectport.KindAlipayPagePay {
				expectedArtifact = "alipay_web_pay_url_v1"
			}
			if !result.Artifact.Valid() || result.Artifact.Kind != expectedArtifact || !json.Valid(result.Artifact.Payload) {
				return paymentport.ErrConflict
			}
			var handoff struct {
				ExpiresAt time.Time `json:"expiresAt"`
			}
			if json.Unmarshal(result.Artifact.Payload, &handoff) != nil || !handoff.ExpiresAt.After(now) {
				return paymentport.ErrConflict
			}
			_, err = t.Exec(ctx, `INSERT INTO payment_handoffs(payment_id,effect_id,payload,payload_digest,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)`, paymentID, effectID, result.Artifact.Payload, result.Artifact.Digest, handoff.ExpiresAt, now)
			if err != nil {
				return mapError(err)
			}
		}
	case effectport.KindWeChatPayRefund, effectport.KindWeChatShopRefund, effectport.KindAlipayRefund:
		if envelope.Kind == effectport.KindWeChatShopRefund && result.Completion == effectport.StateExecuted {
			if !result.Artifact.Valid() || result.Artifact.Kind != "wechat_shop_refund_acceptance_v1" || !json.Valid(result.Artifact.Payload) {
				return paymentport.ErrConflict
			}
			var artifact struct {
				AfterSaleID string `json:"afterSaleId"`
			}
			if json.Unmarshal(result.Artifact.Payload, &artifact) != nil || strings.TrimSpace(artifact.AfterSaleID) != artifact.AfterSaleID || artifact.AfterSaleID == "" || len(artifact.AfterSaleID) > 200 {
				return paymentport.ErrConflict
			}
			digest := effectport.Hash("wechat-shop/aftersale-id/v1", artifact.AfterSaleID)
			updated, updateErr := t.Exec(ctx, `UPDATE payment_refunds SET provider_refund_reference=$2,provider_refund_digest=$3,updated_at=$4 WHERE id=$1 AND external_effect_id=$5 AND status='effect_accepted' AND provider_refund_reference IS NULL`, refundID, artifact.AfterSaleID, digest, now, effectID)
			if updateErr != nil || updated.RowsAffected() != 1 {
				if updateErr != nil {
					return mapError(updateErr)
				}
				return paymentport.ErrConflict
			}
			return nil
		}
		if result.Completion == effectport.StateUnknown {
			_, err = t.Exec(ctx, `UPDATE payment_refunds SET status='outcome_unknown',version=version+1,updated_at=$2 WHERE id=$1 AND external_effect_id=$3 AND status='effect_accepted'`, refundID, now, effectID)
			return mapError(err)
		}
		if result.Completion == effectport.StateFinalFailed {
			_, err = t.Exec(ctx, `UPDATE payment_refunds SET status='final_failed',version=version+1,updated_at=$2 WHERE id=$1 AND external_effect_id=$3 AND status='effect_accepted'`, refundID, now, effectID)
			return mapError(err)
		}
	}
	return nil
}

func (r *Repository) ReconciliationTargetWithin(ctx context.Context, envelope effectport.Envelope) (paymentport.ReconciliationTarget, error) {
	t, err := tx(ctx)
	if err != nil {
		return paymentport.ReconciliationTarget{}, err
	}
	if envelope.Owner != effectport.OwnerPayment || !effectport.ValidDigest(envelope.SourceRefDigest) {
		return paymentport.ReconciliationTarget{}, paymentport.ErrConflict
	}
	var target paymentport.ReconciliationTarget
	err = t.QueryRow(ctx, `SELECT COALESCE(i.payment_id,0),COALESCE(i.refund_id,0),COALESCE(p.order_id,rp.order_id,0)
		FROM payment_provider_intents i
		LEFT JOIN payments p ON p.id=i.payment_id
		LEFT JOIN payment_refunds r ON r.id=i.refund_id
		LEFT JOIN payments rp ON rp.id=r.payment_id
		WHERE i.effect_kind=$1 AND i.source_ref_digest=$2`, envelope.Kind, envelope.SourceRefDigest).Scan(&target.PaymentID, &target.RefundID, &target.OrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentport.ReconciliationTarget{}, paymentport.ErrNotFound
	}
	if err != nil {
		return paymentport.ReconciliationTarget{}, mapError(err)
	}
	switch envelope.Kind {
	case effectport.KindWeChatPayPrepay, effectport.KindWeChatPayRefund:
		target.Provider = domain.ProviderWeChatPay
	case effectport.KindAlipayWapPay, effectport.KindAlipayPagePay, effectport.KindAlipayRefund:
		target.Provider = domain.ProviderAlipay
	case effectport.KindWeChatShopRefund:
		target.Provider = domain.ProviderWeChatShop
	default:
		return paymentport.ReconciliationTarget{}, paymentport.ErrConflict
	}
	validPayment := (target.Provider == domain.ProviderWeChatPay || target.Provider == domain.ProviderAlipay) && target.PaymentID > 0 && target.RefundID == 0
	validRefund := target.RefundID > 0 && target.PaymentID == 0
	if (!validPayment && !validRefund) || target.OrderID < 1 {
		return paymentport.ReconciliationTarget{}, paymentport.ErrConflict
	}
	return target, nil
}

func (r *Repository) UpdatePaymentSettlement(ctx context.Context, p domain.Payment, providerDigest, receipt string) (domain.Payment, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	if providerDigest != "" && !effectport.ValidDigest(effectport.Digest(providerDigest)) {
		return domain.Payment{}, paymentport.ErrConflict
	}
	result, e := t.Exec(ctx, `UPDATE payments SET status=$2,provider_transaction_reference=NULLIF($3,''),provider_transaction_digest=NULLIF($4,''),paid_confirmed_at=CASE WHEN $2='paid' THEN COALESCE(paid_confirmed_at,$6) ELSE paid_confirmed_at END,version=$5,updated_at=$6 WHERE id=$1 AND version=$7`, p.ID, p.Status, p.ProviderTransactionReference, providerDigest, p.Version, p.UpdatedAt, p.Version-1)
	if e != nil || result.RowsAffected() != 1 {
		if e != nil {
			return domain.Payment{}, mapError(e)
		}
		return domain.Payment{}, paymentport.ErrConflict
	}
	if e = recordSettlement(ctx, t, "callback", "payment", p.ID, receipt, providerDigest, p.UpdatedAt); e != nil {
		return domain.Payment{}, e
	}
	if e = appendFacts(ctx, t, "payment.settled", p.ID, "provider", p.UpdatedAt); e != nil {
		return domain.Payment{}, e
	}
	return p, nil
}

// RestorePaidConfirmation applies a signed Provider query only to a native
// payment that was already paid but lacked its immutable confirmation time.
// It retains the original payment state and records a reconciliation-specific
// receipt and audit fact; callers cannot use it to overwrite an existing
// confirmation or infer time from bookkeeping updates.
func (r *Repository) RestorePaidConfirmation(ctx context.Context, p domain.Payment, providerDigest, receipt string) (domain.Payment, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Payment{}, e
	}
	if p.Historical || p.Status != domain.StatusPaid || p.PaidConfirmedAt == nil || p.PaidConfirmedAt.IsZero() || p.Provider != domain.ProviderWeChatPay || !effectport.ValidDigest(effectport.Digest(providerDigest)) || receipt == "" {
		return domain.Payment{}, paymentport.ErrConflict
	}
	result, e := t.Exec(ctx, `UPDATE payments SET paid_confirmed_at=$2,provider_transaction_reference=NULLIF($3,''),provider_transaction_digest=NULLIF($4,''),version=$5,updated_at=$6 WHERE id=$1 AND status='paid' AND paid_confirmed_at IS NULL AND version=$7`, p.ID, p.PaidConfirmedAt.UTC(), p.ProviderTransactionReference, providerDigest, p.Version, p.UpdatedAt, p.Version-1)
	if e != nil || result.RowsAffected() != 1 {
		if e != nil {
			return domain.Payment{}, mapError(e)
		}
		return domain.Payment{}, paymentport.ErrConflict
	}
	if e = recordSettlement(ctx, t, "reconcile", "payment", p.ID, receipt, providerDigest, p.UpdatedAt); e != nil {
		return domain.Payment{}, e
	}
	payload, _ := json.Marshal(map[string]any{"aggregate_id": p.ID, "confirmed_at": p.PaidConfirmedAt.UTC(), "source": "verified_provider_query"})
	if _, e = t.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at)VALUES('payment.confirmation_reconciled',$1,'provider',$2,$3)`, p.ID, payload, p.UpdatedAt); e != nil {
		return domain.Payment{}, mapError(e)
	}
	return p, nil
}
func (r *Repository) UpdateRefundSettlement(ctx context.Context, v domain.Refund, providerDigest, receipt string) (domain.Refund, error) {
	t, e := tx(ctx)
	if e != nil {
		return domain.Refund{}, e
	}
	if providerDigest != "" && !effectport.ValidDigest(effectport.Digest(providerDigest)) {
		return domain.Refund{}, paymentport.ErrConflict
	}
	result, e := t.Exec(ctx, `UPDATE payment_refunds SET status=$2,provider_refund_digest=NULLIF($3,''),version=$4,updated_at=$5 WHERE id=$1 AND version=$6`, v.ID, v.Status, providerDigest, v.Version, v.UpdatedAt, v.Version-1)
	if e != nil || result.RowsAffected() != 1 {
		if e != nil {
			return domain.Refund{}, mapError(e)
		}
		return domain.Refund{}, paymentport.ErrConflict
	}
	if e = recordSettlement(ctx, t, "callback", "refund", v.ID, receipt, providerDigest, v.UpdatedAt); e != nil {
		return domain.Refund{}, e
	}
	if e = appendFacts(ctx, t, "payment.refund_settled", v.ID, "provider", v.UpdatedAt); e != nil {
		return domain.Refund{}, e
	}
	return v, nil
}

func recordSettlement(ctx context.Context, t pgx.Tx, op, kind string, id int64, key, payload string, now time.Time) error {
	k := sha256.Sum256([]byte(key))
	p := sha256.Sum256([]byte(payload))
	var oldPayload []byte
	var oldKind string
	var oldID int64
	err := t.QueryRow(ctx, `SELECT payload_digest,result_kind,result_id FROM payment_operation_receipts WHERE operation=$1 AND actor_scope='provider' AND key_digest=$2`, op, k[:]).Scan(&oldPayload, &oldKind, &oldID)
	if err == nil {
		if string(oldPayload) != string(p[:]) || oldKind != kind || oldID != id {
			return paymentport.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mapError(err)
	}
	_, err = t.Exec(ctx, `INSERT INTO payment_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at)VALUES($1,'provider',$2,$3,$4,$5,$6)`, op, k[:], p[:], kind, id, now)
	return mapError(err)
}
func appendFacts(ctx context.Context, t pgx.Tx, event string, id int64, actor string, now time.Time) error {
	payload, _ := json.Marshal(map[string]any{"aggregate_id": id, "occurred_at": now.UTC()})
	if _, e := t.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at)VALUES($1,$2,$3,$4,$5)`, event, id, actor, payload, now); e != nil {
		return e
	}
	idempotency := event + ":" + strconv.FormatInt(id, 10) + ":" + hashBytes(payload)
	_, e := t.Exec(ctx, `INSERT INTO payment_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at)VALUES($1,$2,$3,$4,$5)`, event, idempotency, id, payload, now)
	return e
}
func effectNumeric(v string) (int64, error) {
	if !strings.HasPrefix(v, "eer_") {
		return 0, paymentport.ErrConflict
	}
	id, e := strconv.ParseInt(strings.TrimPrefix(v, "eer_"), 10, 64)
	if e != nil || id < 1 {
		return 0, paymentport.ErrConflict
	}
	return id, nil
}
func strconvDigest(value effectport.Digest) ([]byte, error) {
	if !effectport.ValidDigest(value) {
		return nil, paymentport.ErrConflict
	}
	return hex.DecodeString(strings.TrimPrefix(string(value), "sha256:"))
}
func mapError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return paymentport.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(e, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23514" || pgErr.Code == "23503") {
		return paymentport.ErrConflict
	}
	return e
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("%x", digest[:8])
}
