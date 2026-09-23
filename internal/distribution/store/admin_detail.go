package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

// ReadAdminDistributorDetail and the companion queries remain in the
// Distribution store: staff detail pages must not join Customer, Order or
// Payment tables to make a richer-looking screen.
func (r *Repository) ReadAdminDistributorDetail(ctx context.Context, id int64) (distributionport.AdminDistributorDetail, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributionport.AdminDistributorDetail{}, err
	}
	if id < 1 {
		return distributionport.AdminDistributorDetail{}, distributionport.ErrNotFound
	}
	var value distributionport.AdminDistributorDetail
	err = tx.QueryRow(ctx, `SELECT id,customer_id,public_no,agreement_version,enabled,receiver_ready,receiver_reason,registered_at,version FROM distribution_distributors WHERE id=$1`, id).
		Scan(&value.Distributor.ID, &value.Distributor.CustomerID, &value.Distributor.PublicNo, &value.Distributor.AgreementVersion, &value.Distributor.Enabled, &value.Distributor.ReceiverReady, &value.Distributor.ReceiverReason, &value.Distributor.RegisteredAt, &value.Distributor.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, distributionport.ErrNotFound
	}
	if err != nil {
		return value, mapError(err)
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(c.original_item_paid_minor),0),COALESCE(SUM(c.successful_refund_minor),0),COALESCE(SUM(c.initial_minor),0),COALESCE(SUM(c.current_payable_minor-c.initial_minor),0),COALESCE(SUM(CASE WHEN c.status NOT IN ('paid','cancelled','zero_commission') THEN GREATEST(c.current_payable_minor-c.paid_minor,0) ELSE 0 END),0),COALESCE(SUM(c.paid_minor),0),COALESCE(SUM((SELECT SUM(a.delta_minor) FROM distribution_commission_adjustments a WHERE a.commission_id=c.id AND a.kind='manual_recovery')),0) FROM distribution_commissions c WHERE c.distributor_id=$1`, id).
		Scan(&value.Earnings.GrossPaidSalesMinor, &value.Earnings.SuccessfulRefundsMinor, &value.Earnings.InitialCommissionMinor, &value.Earnings.CommissionAdjustmentsMinor, &value.Earnings.UnsettledPayableMinor, &value.Earnings.PaidCommissionMinor, &value.Earnings.RecoveredMinor)
	value.Earnings.Currency = "CNY"
	return value, mapError(err)
}

func (r *Repository) ListAdminOrdersByDistributor(ctx context.Context, distributorID int64, cursor string, limit int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, err
	}
	after, err := readModelCursor(cursor)
	if err != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, err
	}
	if distributorID < 1 {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, distributionport.ErrNotFound
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT true FROM distribution_distributors WHERE id=$1`, distributorID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, distributionport.ErrNotFound
	} else if err != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, mapError(err)
	}
	rows, err := tx.Query(ctx, `SELECT a.id,'order-'||a.order_id,a.order_item_line,p.product_id,p.product_type,a.product_name,d.public_no,d.customer_id,a.qualification_state,a.qualification_evidence_reference,a.policy_version,a.commission_rate_basis_points,a.wait_days,COALESCE(c.original_item_paid_minor,0),'CNY',a.attributed_at FROM distribution_order_attributions a JOIN distribution_product_policies p ON p.id=a.policy_id JOIN distribution_distributors d ON d.id=a.distributor_id LEFT JOIN distribution_commissions c ON c.attribution_id=a.id WHERE a.distributor_id=$1 AND a.id>$2 ORDER BY a.id LIMIT $3`, distributorID, after, adminLimit(limit))
	if err != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, mapError(err)
	}
	defer rows.Close()
	page := distributionport.AdminPage[distributionport.AdminOrder]{}
	for rows.Next() {
		var item distributionport.AdminOrder
		if err = rows.Scan(&item.AttributionID, &item.OrderReference, &item.ItemLine, &item.ProductID, &item.ProductType, &item.ProductName, &item.DistributorPublicNo, &item.DistributorCustomerID, &item.QualificationState, &item.QualificationEvidenceReference, &item.PolicyVersion, &item.RateBasisPoints, &item.WaitDays, &item.PaidMinor, &item.Currency, &item.AttributedAt); err != nil {
			return page, mapError(err)
		}
		page.Items = append(page.Items, item)
	}
	if len(page.Items) == int(adminLimit(limit)) {
		page.NextCursor = strconv.FormatInt(page.Items[len(page.Items)-1].AttributionID, 10)
	}
	return page, mapError(rows.Err())
}

func (r *Repository) ReadAdminOrderDetail(ctx context.Context, attributionID int64) (distributionport.AdminOrderDetail, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributionport.AdminOrderDetail{}, err
	}
	if attributionID < 1 {
		return distributionport.AdminOrderDetail{}, distributionport.ErrNotFound
	}
	var value distributionport.AdminOrderDetail
	err = tx.QueryRow(ctx, `SELECT a.id,'order-'||a.order_id,a.order_item_line,p.product_id,p.product_type,a.product_name,d.public_no,d.customer_id,a.qualification_state,a.qualification_evidence_reference,a.policy_version,a.commission_rate_basis_points,a.wait_days,COALESCE(c.original_item_paid_minor,0),'CNY',a.attributed_at FROM distribution_order_attributions a JOIN distribution_product_policies p ON p.id=a.policy_id JOIN distribution_distributors d ON d.id=a.distributor_id LEFT JOIN distribution_commissions c ON c.attribution_id=a.id WHERE a.id=$1`, attributionID).
		Scan(&value.Order.AttributionID, &value.Order.OrderReference, &value.Order.ItemLine, &value.Order.ProductID, &value.Order.ProductType, &value.Order.ProductName, &value.Order.DistributorPublicNo, &value.Order.DistributorCustomerID, &value.Order.QualificationState, &value.Order.QualificationEvidenceReference, &value.Order.PolicyVersion, &value.Order.RateBasisPoints, &value.Order.WaitDays, &value.Order.PaidMinor, &value.Order.Currency, &value.Order.AttributedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, distributionport.ErrNotFound
	}
	if err != nil {
		return value, mapError(err)
	}
	var commissionID int64
	commission := distributionport.AdminCommissionDetail{}
	err = tx.QueryRow(ctx, `SELECT c.id,'order-'||c.order_id,a.product_name,c.original_item_paid_minor,c.successful_refund_minor,c.initial_minor,c.current_payable_minor,c.paid_minor,c.status,c.hold_reason,c.cancel_reason,c.exception_reason,c.paid_confirmed_at,c.due_at,c.created_at,'CNY' FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id WHERE c.attribution_id=$1`, attributionID).
		Scan(&commissionID, &commission.OrderReference, &commission.ProductName, &commission.OriginalItemPaidMinor, &commission.SuccessfulRefundMinor, &commission.InitialMinor, &commission.CurrentPayableMinor, &commission.PaidMinor, &commission.Status, &commission.HoldReason, &commission.CancelReason, &commission.ExceptionReason, &commission.PaidConfirmedAt, &commission.DueAt, &commission.CreatedAt, &commission.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, mapError(err)
	}
	commission.CommissionID = strconv.FormatInt(commissionID, 10)
	value.Commission = &commission
	rows, err := tx.Query(ctx, `SELECT id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at FROM distribution_commission_adjustments WHERE commission_id=$1 ORDER BY id`, commissionID)
	if err != nil {
		return value, mapError(err)
	}
	for rows.Next() {
		var item distributionport.AdminCommissionAdjustment
		if err = rows.Scan(&item.ID, &item.Kind, &item.DeltaMinor, &item.ResultingPayableMinor, &item.Reason, &item.SourceReference, &item.OccurredAt); err != nil {
			rows.Close()
			return value, mapError(err)
		}
		value.Adjustments = append(value.Adjustments, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return value, mapError(err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT s.id,s.settlement_reference,s.amount_minor,s.currency,s.state,s.provider_deadline_at,
		(SELECT MAX(ae.occurred_at) FROM distribution_audit_events ae
		 WHERE ae.aggregate_type='commission' AND ae.aggregate_id=s.commission_id
		   AND ae.event_type='distribution.settlement_paid.v1'
		   AND ae.payload->>'settlement_reference'=s.settlement_reference),
		s.created_at,s.updated_at
		FROM distribution_settlements s WHERE s.commission_id=$1 ORDER BY s.id`, commissionID)
	if err != nil {
		return value, mapError(err)
	}
	for rows.Next() {
		var item distributionport.AdminSettlement
		if err = rows.Scan(&item.ID, &item.Reference, &item.AmountMinor, &item.Currency, &item.State, &item.ProviderDeadlineAt, &item.SettlementConfirmedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return value, mapError(err)
		}
		value.Settlements = append(value.Settlements, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return value, mapError(err)
	}
	rows.Close()
	value.Exceptions, err = r.adminExceptionsForCommission(ctx, tx, commissionID)
	return value, err
}

func (r *Repository) ReadAdminExceptionDetail(ctx context.Context, exceptionID int64) (distributionport.AdminException, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributionport.AdminException{}, err
	}
	if exceptionID < 1 {
		return distributionport.AdminException{}, distributionport.ErrNotFound
	}
	items, err := r.adminExceptionsWhere(ctx, tx, `e.id=$1`, exceptionID)
	if err != nil {
		return distributionport.AdminException{}, err
	}
	if len(items) != 1 {
		return distributionport.AdminException{}, distributionport.ErrNotFound
	}
	value := items[0]
	value.Audit, err = r.adminExceptionAudit(ctx, tx, exceptionID)
	return value, err
}

func (r *Repository) adminExceptionAudit(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, exceptionID int64) ([]distributionport.AdminExceptionAuditFact, error) {
	rows, err := tx.Query(ctx, `SELECT event_type,actor_scope,payload,occurred_at FROM distribution_audit_events WHERE aggregate_type='exception' AND aggregate_id=$1 ORDER BY id`, exceptionID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	facts := []distributionport.AdminExceptionAuditFact{}
	for rows.Next() {
		var fact distributionport.AdminExceptionAuditFact
		var payload []byte
		if err = rows.Scan(&fact.EventType, &fact.ActorScope, &payload, &fact.OccurredAt); err != nil {
			return nil, mapError(err)
		}
		var values map[string]any
		if json.Unmarshal(payload, &values) == nil {
			if reason, ok := values["reason"].(string); ok {
				fact.Reason = reason
			}
			if evidence, ok := values["evidence_reference"].(string); ok {
				fact.EvidenceReference = evidence
			}
			if amount, ok := values["amount_minor"].(float64); ok && amount >= 0 && amount == float64(int64(amount)) {
				fact.AmountMinor = int64(amount)
			}
		}
		facts = append(facts, fact)
	}
	return facts, mapError(rows.Err())
}

func (r *Repository) adminExceptionsForCommission(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, commissionID int64) ([]distributionport.AdminException, error) {
	return r.adminExceptionsWhere(ctx, tx, `e.commission_id=$1`, commissionID)
}
func (r *Repository) adminExceptionsWhere(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, where string, value int64) ([]distributionport.AdminException, error) {
	rows, err := tx.Query(ctx, `SELECT e.id,e.commission_id,d.public_no,d.customer_id,'order-'||c.order_id,e.kind,e.status,e.unpaid_due_minor,e.already_paid_minor,e.amount_minor,'CNY',e.reason,COALESCE(s.payment_instruction_reference,''),e.evidence_reference,e.actor_scope,e.created_at,e.updated_at,e.version,CASE WHEN e.kind='unfreeze_final_failed' AND e.evidence_reference ~ '^psunfreeze_[1-9][0-9]*$' THEN 'unfreeze' WHEN COALESCE(s.payment_instruction_reference,'') ~ '^psinst_[1-9][0-9]*$' THEN 'split' ELSE '' END,(e.kind <> 'settlement_deadline_imminent' AND e.status IN ('open','querying') AND ((e.kind='unfreeze_final_failed' AND e.evidence_reference ~ '^psunfreeze_[1-9][0-9]*$') OR (COALESCE(s.payment_instruction_reference,'') ~ '^psinst_[1-9][0-9]*$' AND c.status NOT IN ('cancelled','zero_commission') AND GREATEST(c.current_payable_minor-c.paid_minor,0)>0))),(e.status IN ('open','resolved') AND c.status NOT IN ('cancelled','zero_commission') AND (CASE WHEN e.kind='buyer_refund_after_paid' THEN GREATEST(c.paid_minor-c.current_payable_minor,0) WHEN e.kind='qualification_revoked_after_paid' AND e.reason='qualification_revoked_after_paid' THEN c.paid_minor ELSE 0 END) > COALESCE((SELECT SUM(recorded.delta_minor) FROM distribution_commission_adjustments recorded WHERE recorded.commission_id=c.id AND recorded.kind IN ('manual_recovery','merchant_liability')),0)),(e.status IN ('open','resolved') AND c.status NOT IN ('cancelled','zero_commission') AND (CASE WHEN e.kind='buyer_refund_after_paid' THEN GREATEST(c.paid_minor-c.current_payable_minor,0) WHEN e.kind='qualification_revoked_after_paid' AND e.reason='qualification_revoked_after_paid' THEN c.paid_minor ELSE 0 END) > COALESCE((SELECT SUM(recorded.delta_minor) FROM distribution_commission_adjustments recorded WHERE recorded.commission_id=c.id AND recorded.kind IN ('manual_recovery','merchant_liability')),0)) FROM distribution_exceptions e JOIN distribution_commissions c ON c.id=e.commission_id JOIN distribution_distributors d ON d.id=c.distributor_id LEFT JOIN distribution_settlements s ON s.id=e.settlement_id WHERE `+where+` ORDER BY e.id`, value)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	items := []distributionport.AdminException{}
	for rows.Next() {
		var item distributionport.AdminException
		if err = rows.Scan(&item.ExceptionID, &item.CommissionID, &item.DistributorPublicNo, &item.DistributorCustomerID, &item.OrderReference, &item.Kind, &item.Status, &item.UnpaidDueMinor, &item.AlreadyPaidMinor, &item.AmountMinor, &item.Currency, &item.Reason, &item.PaymentInstructionReference, &item.EvidenceReference, &item.ActorScope, &item.CreatedAt, &item.UpdatedAt, &item.Version, &item.ReconcileTarget, &item.CanReconcile, &item.CanRecordRecovery, &item.CanRecordMerchantLiability); err != nil {
			return nil, mapError(err)
		}
		items = append(items, item)
	}
	return items, mapError(rows.Err())
}
