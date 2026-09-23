package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type CommissionAdjustment struct {
	CommissionID          int64
	Kind                  string
	DeltaMinor            int64
	ResultingPayableMinor int64
	Reason, SourceRef     string
	OccurredAt            time.Time
}

func scanAttribution(row rowScanner) (distributiondomain.Attribution, error) {
	var attribution distributiondomain.Attribution
	var qualificationState string
	err := row.Scan(&attribution.ID, &attribution.OrderID, &attribution.OrderItemLine, &attribution.ProductCode, &attribution.ProductName, &attribution.DistributorID, &attribution.PromotionCredentialID, &attribution.QualificationEvidenceRef, &qualificationState, &attribution.PolicyVersion, &attribution.CommissionRateBasisPoints, &attribution.WaitDays, &attribution.AttributedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.Attribution{}, distributionport.ErrNotFound
	}
	if err != nil {
		return distributiondomain.Attribution{}, mapError(err)
	}
	attribution.QualificationState = distributiondomain.QualificationState(qualificationState)
	if !attribution.Valid() {
		return distributiondomain.Attribution{}, distributionport.ErrUnavailable
	}
	return attribution, nil
}

func (r *Repository) ReadAttributionByOrderItemWithin(ctx context.Context, orderID int64, line int32, lock bool) (distributiondomain.Attribution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Attribution{}, err
	}
	if orderID < 1 || line < 1 {
		return distributiondomain.Attribution{}, ErrInvalid
	}
	query := `SELECT id,order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_version,commission_rate_basis_points,wait_days,attributed_at FROM distribution_order_attributions WHERE order_id=$1 AND order_item_line=$2`
	if lock {
		query += " FOR UPDATE"
	}
	return scanAttribution(tx.QueryRow(ctx, query, orderID, line))
}

// InsertAttributionWithin freezes a checkout decision. An existing row for an
// order item is replayed only if the exact same immutable distributor and
// credential are present; callers must treat a different promotion as a
// checkout conflict rather than overwrite it.
func (r *Repository) InsertAttributionWithin(ctx context.Context, attribution distributiondomain.Attribution, policyID int64) (distributiondomain.Attribution, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Attribution{}, false, err
	}
	if !attribution.ValidForInsert() || policyID < 1 {
		return distributiondomain.Attribution{}, false, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(order_id,order_item_line) DO NOTHING RETURNING id`, attribution.OrderID, attribution.OrderItemLine, attribution.ProductCode, attribution.ProductName, attribution.DistributorID, attribution.PromotionCredentialID, attribution.QualificationEvidenceRef, string(attribution.QualificationState), policyID, attribution.PolicyVersion, attribution.CommissionRateBasisPoints, attribution.WaitDays, attribution.AttributedAt.UTC()).Scan(&id)
	if err == nil {
		attribution.ID = id
		return attribution, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.Attribution{}, false, mapError(err)
	}
	existing, err := r.ReadAttributionByOrderItemWithin(ctx, attribution.OrderID, attribution.OrderItemLine, true)
	if err != nil {
		return distributiondomain.Attribution{}, false, err
	}
	if existing.ProductCode != attribution.ProductCode || existing.ProductName != attribution.ProductName || existing.DistributorID != attribution.DistributorID || existing.PromotionCredentialID != attribution.PromotionCredentialID || existing.QualificationEvidenceRef != attribution.QualificationEvidenceRef || existing.QualificationState != attribution.QualificationState || existing.PolicyVersion != attribution.PolicyVersion || existing.CommissionRateBasisPoints != attribution.CommissionRateBasisPoints || existing.WaitDays != attribution.WaitDays {
		return distributiondomain.Attribution{}, false, distributionport.ErrConflict
	}
	return existing, false, nil
}

func scanCommission(row rowScanner) (distributiondomain.Commission, error) {
	var commission distributiondomain.Commission
	var status string
	err := row.Scan(&commission.ID, &commission.AttributionID, &commission.OrderID, &commission.OrderItemLine, &commission.DistributorID, &commission.OriginalItemPaidMinor, &commission.SuccessfulRefundMinor, &commission.InitialMinor, &commission.CurrentPayableMinor, &commission.PaidMinor, &commission.CommissionRateBasisPoints, &commission.PaidConfirmedAt, &commission.DueAt, &status, &commission.HoldReason, &commission.CancelReason, &commission.ExceptionReason, &commission.Version, &commission.CreatedAt, &commission.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.Commission{}, distributionport.ErrNotFound
	}
	if err != nil {
		return distributiondomain.Commission{}, mapError(err)
	}
	commission.Status = distributiondomain.CommissionStatus(status)
	if commission.ID < 1 || commission.AttributionID < 1 || commission.OrderID < 1 || commission.OrderItemLine < 1 || commission.DistributorID < 1 || commission.OriginalItemPaidMinor < 0 || commission.SuccessfulRefundMinor < 0 || commission.SuccessfulRefundMinor > commission.OriginalItemPaidMinor || commission.InitialMinor < 0 || commission.CurrentPayableMinor < 0 || commission.PaidMinor < 0 || commission.CommissionRateBasisPoints < 0 || commission.CommissionRateBasisPoints > distributiondomain.MaximumCommissionRateBasisPoints || !commission.Status.Valid() || commission.Version < 1 || commission.PaidConfirmedAt.IsZero() || commission.DueAt.Before(commission.PaidConfirmedAt) || commission.CreatedAt.IsZero() || commission.UpdatedAt.Before(commission.CreatedAt) {
		return distributiondomain.Commission{}, distributionport.ErrUnavailable
	}
	return commission, nil
}

const commissionColumns = `id,attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at`

func (r *Repository) InsertCommissionWithin(ctx context.Context, commission distributiondomain.Commission) (distributiondomain.Commission, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Commission{}, false, err
	}
	if commission.AttributionID < 1 || commission.OrderID < 1 || commission.OrderItemLine < 1 || commission.DistributorID < 1 || commission.OriginalItemPaidMinor < 0 || commission.SuccessfulRefundMinor != 0 || commission.InitialMinor < 0 || commission.CurrentPayableMinor < 0 || commission.PaidMinor != 0 || commission.CommissionRateBasisPoints < 0 || commission.CommissionRateBasisPoints > distributiondomain.MaximumCommissionRateBasisPoints || !commission.Status.Valid() || commission.Version != 1 || commission.PaidConfirmedAt.IsZero() || commission.DueAt.Before(commission.PaidConfirmedAt) || commission.CreatedAt.IsZero() || commission.UpdatedAt.Before(commission.CreatedAt) {
		return distributiondomain.Commission{}, false, ErrInvalid
	}
	query := `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19) ON CONFLICT(attribution_id) DO NOTHING RETURNING ` + commissionColumns
	created, err := scanCommission(tx.QueryRow(ctx, query, commission.AttributionID, commission.OrderID, commission.OrderItemLine, commission.DistributorID, commission.OriginalItemPaidMinor, commission.SuccessfulRefundMinor, commission.InitialMinor, commission.CurrentPayableMinor, commission.PaidMinor, commission.CommissionRateBasisPoints, commission.PaidConfirmedAt.UTC(), commission.DueAt.UTC(), string(commission.Status), commission.HoldReason, commission.CancelReason, commission.ExceptionReason, commission.Version, commission.CreatedAt.UTC(), commission.UpdatedAt.UTC()))
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, distributionport.ErrNotFound) {
		return distributiondomain.Commission{}, false, err
	}
	existing, err := r.ReadCommissionByAttributionWithin(ctx, commission.AttributionID, true)
	if err != nil {
		return distributiondomain.Commission{}, false, err
	}
	if existing.OrderID != commission.OrderID || existing.OrderItemLine != commission.OrderItemLine || existing.DistributorID != commission.DistributorID || existing.OriginalItemPaidMinor != commission.OriginalItemPaidMinor || existing.InitialMinor != commission.InitialMinor || existing.CommissionRateBasisPoints != commission.CommissionRateBasisPoints || !existing.PaidConfirmedAt.Equal(commission.PaidConfirmedAt) {
		return distributiondomain.Commission{}, false, distributionport.ErrConflict
	}
	return existing, false, nil
}

func (r *Repository) ReadCommissionByAttributionWithin(ctx context.Context, attributionID int64, lock bool) (distributiondomain.Commission, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Commission{}, err
	}
	if attributionID < 1 {
		return distributiondomain.Commission{}, ErrInvalid
	}
	query := `SELECT ` + commissionColumns + ` FROM distribution_commissions WHERE attribution_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanCommission(tx.QueryRow(ctx, query, attributionID))
}

func (r *Repository) ReadCommissionWithin(ctx context.Context, commissionID int64, lock bool) (distributiondomain.Commission, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Commission{}, err
	}
	if commissionID < 1 {
		return distributiondomain.Commission{}, ErrInvalid
	}
	query := `SELECT ` + commissionColumns + ` FROM distribution_commissions WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanCommission(tx.QueryRow(ctx, query, commissionID))
}

func (r *Repository) UpdateCommissionWithin(ctx context.Context, commission distributiondomain.Commission, expectedVersion int64) (distributiondomain.Commission, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Commission{}, err
	}
	if commission.ID < 1 || expectedVersion < 1 || commission.Version != expectedVersion+1 || commission.SuccessfulRefundMinor < 0 || commission.SuccessfulRefundMinor > commission.OriginalItemPaidMinor || commission.CurrentPayableMinor < 0 || commission.PaidMinor < 0 || !commission.Status.Valid() || commission.UpdatedAt.IsZero() {
		return distributiondomain.Commission{}, ErrInvalid
	}
	return scanCommission(tx.QueryRow(ctx, `UPDATE distribution_commissions SET successful_refund_minor=$2,current_payable_minor=$3,paid_minor=$4,status=$5,hold_reason=$6,cancel_reason=$7,exception_reason=$8,version=$9,updated_at=$10 WHERE id=$1 AND version=$11 RETURNING `+commissionColumns, commission.ID, commission.SuccessfulRefundMinor, commission.CurrentPayableMinor, commission.PaidMinor, string(commission.Status), strings.TrimSpace(commission.HoldReason), strings.TrimSpace(commission.CancelReason), strings.TrimSpace(commission.ExceptionReason), commission.Version, commission.UpdatedAt.UTC(), expectedVersion))
}

func (r *Repository) AppendCommissionAdjustmentWithin(ctx context.Context, adjustment CommissionAdjustment) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if adjustment.CommissionID < 1 || adjustment.Kind == "" || len(adjustment.Kind) > 100 || adjustment.ResultingPayableMinor < 0 || adjustment.Reason != strings.TrimSpace(adjustment.Reason) || adjustment.Reason == "" || len(adjustment.Reason) > 200 || adjustment.SourceRef != strings.TrimSpace(adjustment.SourceRef) || len(adjustment.SourceRef) > 200 || adjustment.OccurredAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO distribution_commission_adjustments(commission_id,kind,delta_minor,resulting_payable_minor,reason,source_reference,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, adjustment.CommissionID, adjustment.Kind, adjustment.DeltaMinor, adjustment.ResultingPayableMinor, adjustment.Reason, adjustment.SourceRef, adjustment.OccurredAt.UTC())
	return mapError(err)
}

// AttributionQualificationContext is the frozen attribution plus the
// Distribution-owned policy/product and distributor customer references needed
// to re-check qualification when Order reports payment success. It does not
// expose or query Order, Payment or Identity tables.
type AttributionQualificationContext struct {
	Attribution           distributiondomain.Attribution
	DistributorCustomerID int64
	ProductID             int64
	ProductType           distributiondomain.ProductType
}

func (r *Repository) ReadAttributionQualificationContextByOrderItemWithin(ctx context.Context, orderID int64, line int32, lock bool) (AttributionQualificationContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return AttributionQualificationContext{}, err
	}
	if orderID < 1 || line < 1 {
		return AttributionQualificationContext{}, ErrInvalid
	}
	query := `SELECT a.id,a.order_id,a.order_item_line,a.product_code,a.product_name,a.distributor_id,a.promotion_credential_id,a.qualification_evidence_reference,a.qualification_state,a.policy_version,a.commission_rate_basis_points,a.wait_days,a.attributed_at,d.customer_id,p.product_id,p.product_type FROM distribution_order_attributions a JOIN distribution_distributors d ON d.id=a.distributor_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE a.order_id=$1 AND a.order_item_line=$2`
	if lock {
		query += ` FOR UPDATE OF a,d,p`
	}
	var value AttributionQualificationContext
	var state, productType string
	err = tx.QueryRow(ctx, query, orderID, line).Scan(&value.Attribution.ID, &value.Attribution.OrderID, &value.Attribution.OrderItemLine, &value.Attribution.ProductCode, &value.Attribution.ProductName, &value.Attribution.DistributorID, &value.Attribution.PromotionCredentialID, &value.Attribution.QualificationEvidenceRef, &state, &value.Attribution.PolicyVersion, &value.Attribution.CommissionRateBasisPoints, &value.Attribution.WaitDays, &value.Attribution.AttributedAt, &value.DistributorCustomerID, &value.ProductID, &productType)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttributionQualificationContext{}, distributionport.ErrNotFound
	}
	if err != nil {
		return AttributionQualificationContext{}, mapError(err)
	}
	value.Attribution.QualificationState = distributiondomain.QualificationState(state)
	value.ProductType = distributiondomain.ProductType(productType)
	if !value.Attribution.Valid() || value.DistributorCustomerID < 1 || value.ProductID < 1 || !value.ProductType.Valid() {
		return AttributionQualificationContext{}, distributionport.ErrUnavailable
	}
	return value, nil
}
