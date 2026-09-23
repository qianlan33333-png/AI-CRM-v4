package store

import (
	"context"
	"strconv"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

func adminLimit(v int32) int32 {
	if v < 1 {
		return 50
	}
	if v > 100 {
		return 100
	}
	return v
}

func readModelCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	after, err := strconv.ParseInt(value, 10, 64)
	if err != nil || after < 1 || value != strconv.FormatInt(after, 10) {
		return 0, ErrInvalid
	}
	return after, nil
}

func (r *Repository) ListAdminDistributors(ctx context.Context, cursor string, limit int32) (distributionport.AdminPage[distributionport.AdminDistributor], error) {
	tx, e := transaction(ctx)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminDistributor]{}, e
	}
	after, e := readModelCursor(cursor)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminDistributor]{}, e
	}
	rows, e := tx.Query(ctx, `SELECT id,customer_id,public_no,agreement_version,enabled,receiver_ready,receiver_reason,registered_at,version FROM distribution_distributors WHERE id>$1 ORDER BY id LIMIT $2`, after, adminLimit(limit))
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminDistributor]{}, mapError(e)
	}
	defer rows.Close()
	p := distributionport.AdminPage[distributionport.AdminDistributor]{}
	for rows.Next() {
		var x distributionport.AdminDistributor
		if e = rows.Scan(&x.ID, &x.CustomerID, &x.PublicNo, &x.AgreementVersion, &x.Enabled, &x.ReceiverReady, &x.ReceiverReason, &x.RegisteredAt, &x.Version); e != nil {
			return p, mapError(e)
		}
		p.Items = append(p.Items, x)
	}
	if len(p.Items) == int(adminLimit(limit)) {
		p.NextCursor = strconv.FormatInt(p.Items[len(p.Items)-1].ID, 10)
	}
	return p, mapError(rows.Err())
}
func (r *Repository) ListAdminOrders(ctx context.Context, cursor string, limit int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	tx, e := transaction(ctx)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, e
	}
	after, e := readModelCursor(cursor)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, e
	}
	rows, e := tx.Query(ctx, `SELECT a.id,'order-'||a.order_id,a.order_item_line,p.product_id,p.product_type,a.product_name,d.public_no,d.customer_id,a.qualification_state,a.qualification_evidence_reference,a.policy_version,a.commission_rate_basis_points,a.wait_days,COALESCE(c.original_item_paid_minor,0),'CNY',a.attributed_at FROM distribution_order_attributions a JOIN distribution_product_policies p ON p.id=a.policy_id JOIN distribution_distributors d ON d.id=a.distributor_id LEFT JOIN distribution_commissions c ON c.attribution_id=a.id WHERE a.id>$1 ORDER BY a.id LIMIT $2`, after, adminLimit(limit))
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminOrder]{}, mapError(e)
	}
	defer rows.Close()
	p := distributionport.AdminPage[distributionport.AdminOrder]{}
	for rows.Next() {
		var x distributionport.AdminOrder
		if e = rows.Scan(&x.AttributionID, &x.OrderReference, &x.ItemLine, &x.ProductID, &x.ProductType, &x.ProductName, &x.DistributorPublicNo, &x.DistributorCustomerID, &x.QualificationState, &x.QualificationEvidenceReference, &x.PolicyVersion, &x.RateBasisPoints, &x.WaitDays, &x.PaidMinor, &x.Currency, &x.AttributedAt); e != nil {
			return p, mapError(e)
		}
		p.Items = append(p.Items, x)
	}
	if len(p.Items) == int(adminLimit(limit)) {
		p.NextCursor = strconv.FormatInt(p.Items[len(p.Items)-1].AttributionID, 10)
	}
	return p, mapError(rows.Err())
}
func (r *Repository) ListAdminExceptions(ctx context.Context, cursor string, limit int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	tx, e := transaction(ctx)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminException]{}, e
	}
	after, e := readModelCursor(cursor)
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminException]{}, e
	}
	rows, e := tx.Query(ctx, `SELECT e.id,e.commission_id,d.public_no,d.customer_id,'order-'||c.order_id,e.kind,e.status,e.unpaid_due_minor,e.already_paid_minor,e.amount_minor,'CNY',e.reason,COALESCE(s.payment_instruction_reference,''),e.created_at,e.updated_at,e.version,
		CASE WHEN e.kind='unfreeze_final_failed' AND e.evidence_reference ~ '^psunfreeze_[1-9][0-9]*$' THEN 'unfreeze' WHEN COALESCE(s.payment_instruction_reference,'') ~ '^psinst_[1-9][0-9]*$' THEN 'split' ELSE '' END,
		(e.kind <> 'settlement_deadline_imminent' AND e.status IN ('open','querying') AND ((e.kind='unfreeze_final_failed' AND e.evidence_reference ~ '^psunfreeze_[1-9][0-9]*$') OR (COALESCE(s.payment_instruction_reference,'') ~ '^psinst_[1-9][0-9]*$' AND c.status NOT IN ('cancelled','zero_commission') AND GREATEST(c.current_payable_minor-c.paid_minor,0)>0))),
		(e.status IN ('open','resolved') AND c.status NOT IN ('cancelled','zero_commission') AND (CASE WHEN e.kind='buyer_refund_after_paid' THEN GREATEST(c.paid_minor-c.current_payable_minor,0) WHEN e.kind='qualification_revoked_after_paid' AND e.reason='qualification_revoked_after_paid' THEN c.paid_minor ELSE 0 END) > COALESCE((SELECT SUM(recorded.delta_minor) FROM distribution_commission_adjustments recorded WHERE recorded.commission_id=c.id AND recorded.kind IN ('manual_recovery','merchant_liability')),0)),
		(e.status IN ('open','resolved') AND c.status NOT IN ('cancelled','zero_commission') AND (CASE WHEN e.kind='buyer_refund_after_paid' THEN GREATEST(c.paid_minor-c.current_payable_minor,0) WHEN e.kind='qualification_revoked_after_paid' AND e.reason='qualification_revoked_after_paid' THEN c.paid_minor ELSE 0 END) > COALESCE((SELECT SUM(recorded.delta_minor) FROM distribution_commission_adjustments recorded WHERE recorded.commission_id=c.id AND recorded.kind IN ('manual_recovery','merchant_liability')),0))
		FROM distribution_exceptions e JOIN distribution_commissions c ON c.id=e.commission_id JOIN distribution_distributors d ON d.id=c.distributor_id LEFT JOIN distribution_settlements s ON s.id=e.settlement_id WHERE e.id>$1 ORDER BY e.id LIMIT $2`, after, adminLimit(limit))
	if e != nil {
		return distributionport.AdminPage[distributionport.AdminException]{}, mapError(e)
	}
	defer rows.Close()
	p := distributionport.AdminPage[distributionport.AdminException]{}
	for rows.Next() {
		var x distributionport.AdminException
		if e = rows.Scan(&x.ExceptionID, &x.CommissionID, &x.DistributorPublicNo, &x.DistributorCustomerID, &x.OrderReference, &x.Kind, &x.Status, &x.UnpaidDueMinor, &x.AlreadyPaidMinor, &x.AmountMinor, &x.Currency, &x.Reason, &x.PaymentInstructionReference, &x.CreatedAt, &x.UpdatedAt, &x.Version, &x.ReconcileTarget, &x.CanReconcile, &x.CanRecordRecovery, &x.CanRecordMerchantLiability); e != nil {
			return p, mapError(e)
		}
		p.Items = append(p.Items, x)
	}
	if len(p.Items) == int(adminLimit(limit)) {
		p.NextCursor = strconv.FormatInt(p.Items[len(p.Items)-1].ExceptionID, 10)
	}
	return p, mapError(rows.Err())
}

func (r *Repository) EarningsByCustomer(ctx context.Context, customerID int64) (distributionport.Earnings, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return distributionport.Earnings{}, e
	}
	var x distributionport.Earnings
	e = tx.QueryRow(ctx, `SELECT
		COALESCE(SUM(c.original_item_paid_minor),0),
		COALESCE(SUM(c.successful_refund_minor),0),
		COALESCE(SUM(c.initial_minor),0),
		COALESCE(SUM(c.current_payable_minor-c.initial_minor),0),
		COALESCE(SUM(CASE WHEN c.status NOT IN ('paid','cancelled','zero_commission') THEN GREATEST(c.current_payable_minor-c.paid_minor,0) ELSE 0 END),0),
		COALESCE(SUM(c.paid_minor),0),
		COALESCE(SUM((SELECT SUM(a.delta_minor) FROM distribution_commission_adjustments a WHERE a.commission_id=c.id AND a.kind='manual_recovery')),0)
		FROM distribution_commissions c JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1`, customerID).Scan(&x.GrossPaidSalesMinor, &x.SuccessfulRefundsMinor, &x.InitialCommissionMinor, &x.CommissionAdjustmentsMinor, &x.UnsettledPayableMinor, &x.PaidCommissionMinor, &x.RecoveredMinor)
	x.Currency = "CNY"
	return x, mapError(e)
}
func (r *Repository) ListCommissionsByCustomer(ctx context.Context, customerID int64, status distributiondomain.CommissionStatus, cursor string, limit int32) (distributionport.CommissionPage, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return distributionport.CommissionPage{}, e
	}
	after, e := readModelCursor(cursor)
	if e != nil {
		return distributionport.CommissionPage{}, e
	}
	q := `SELECT c.id,c.order_id,a.product_name,c.initial_minor,c.current_payable_minor,c.paid_minor,c.status,c.hold_reason,c.cancel_reason,c.exception_reason,c.paid_confirmed_at,c.due_at,
		(SELECT MAX(ae.occurred_at)
			FROM distribution_settlements s
			JOIN distribution_audit_events ae ON ae.aggregate_type='commission' AND ae.aggregate_id=c.id
				AND ae.event_type='distribution.settlement_paid.v1'
				AND ae.payload->>'settlement_reference'=s.settlement_reference
			WHERE s.commission_id=c.id AND c.paid_minor>0),c.created_at,'CNY'
		FROM distribution_commissions c
		JOIN distribution_order_attributions a ON a.id=c.attribution_id
		JOIN distribution_distributors d ON d.id=c.distributor_id
		WHERE d.customer_id=$1 AND c.id>$2`
	args := []any{customerID, after}
	if status.Valid() {
		q += " AND c.status=$3"
		args = append(args, string(status))
	}
	q += " ORDER BY c.id LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, adminLimit(limit))
	rows, e := tx.Query(ctx, q, args...)
	if e != nil {
		return distributionport.CommissionPage{}, mapError(e)
	}
	defer rows.Close()
	p := distributionport.CommissionPage{}
	for rows.Next() {
		var x distributionport.CommissionListItem
		var id, orderID int64
		var commissionStatus string
		var paidAt *time.Time
		if e = rows.Scan(&id, &orderID, &x.ProductName, &x.InitialMinor, &x.CurrentPayableMinor, &x.PaidMinor, &commissionStatus, &x.HoldReason, &x.CancelReason, &x.ExceptionReason, &x.PaidConfirmedAt, &x.DueAt, &paidAt, &x.CreatedAt, &x.Currency); e != nil {
			return p, mapError(e)
		}
		x.CommissionID = strconv.FormatInt(id, 10)
		x.OrderReference = "order-" + strconv.FormatInt(orderID, 10)
		x.Status = commissionStatus
		if paidAt != nil {
			x.SettlementConfirmedAt = paidAt.UTC()
			x.PaidAt = x.SettlementConfirmedAt // deprecated compatibility alias; system split confirmation, not bank arrival.
		}
		p.Items = append(p.Items, x)
	}
	if len(p.Items) == int(adminLimit(limit)) {
		p.NextCursor = p.Items[len(p.Items)-1].CommissionID
	}
	return p, mapError(rows.Err())
}
