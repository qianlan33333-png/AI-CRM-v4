package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type SettlementContext struct {
	Commission  distributiondomain.Commission
	Attribution distributiondomain.Attribution
	ProductID   int64
	ProductType distributiondomain.ProductType
}

// QualificationProductScope identifies an Order-owned purchase product whose
// refund may affect a distributor's current qualification. It deliberately
// omits customer identity: Distribution obtains canonical lineage through the
// Identity Port after it has locked its own rows.
type QualificationProductScope struct {
	ProductID   int64
	ProductType distributiondomain.ProductType
}

func (v QualificationProductScope) Valid() bool {
	return v.ProductID > 0 && v.ProductType.Valid()
}

// Every commission column must remain explicitly qualified: the attribution
// join has overlapping order/distributor identifiers, and prefixing only the
// first item in a comma-separated list produces an ambiguous PostgreSQL read.
const settlementContextColumns = `c.id,c.attribution_id,c.order_id,c.order_item_line,c.distributor_id,c.original_item_paid_minor,c.successful_refund_minor,c.initial_minor,c.current_payable_minor,c.paid_minor,c.commission_rate_basis_points,c.paid_confirmed_at,c.due_at,c.status,c.hold_reason,c.cancel_reason,c.exception_reason,c.version,c.created_at,c.updated_at,a.id,a.order_id,a.order_item_line,a.product_code,a.product_name,a.distributor_id,a.promotion_credential_id,a.qualification_evidence_reference,a.qualification_state,a.policy_version,a.commission_rate_basis_points,a.wait_days,a.attributed_at,p.product_id,p.product_type`

func scanSettlementContext(row rowScanner) (SettlementContext, error) {
	var result SettlementContext
	var productType string
	var qualificationState string
	err := row.Scan(&result.Commission.ID, &result.Commission.AttributionID, &result.Commission.OrderID, &result.Commission.OrderItemLine, &result.Commission.DistributorID, &result.Commission.OriginalItemPaidMinor, &result.Commission.SuccessfulRefundMinor, &result.Commission.InitialMinor, &result.Commission.CurrentPayableMinor, &result.Commission.PaidMinor, &result.Commission.CommissionRateBasisPoints, &result.Commission.PaidConfirmedAt, &result.Commission.DueAt, &result.Commission.Status, &result.Commission.HoldReason, &result.Commission.CancelReason, &result.Commission.ExceptionReason, &result.Commission.Version, &result.Commission.CreatedAt, &result.Commission.UpdatedAt, &result.Attribution.ID, &result.Attribution.OrderID, &result.Attribution.OrderItemLine, &result.Attribution.ProductCode, &result.Attribution.ProductName, &result.Attribution.DistributorID, &result.Attribution.PromotionCredentialID, &result.Attribution.QualificationEvidenceRef, &qualificationState, &result.Attribution.PolicyVersion, &result.Attribution.CommissionRateBasisPoints, &result.Attribution.WaitDays, &result.Attribution.AttributedAt, &result.ProductID, &productType)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementContext{}, distributionport.ErrNotFound
	}
	if err != nil {
		return SettlementContext{}, mapError(err)
	}
	result.Commission.Status = distributiondomain.CommissionStatus(string(result.Commission.Status))
	result.ProductType = distributiondomain.ProductType(productType)
	result.Attribution.QualificationState = distributiondomain.QualificationState(qualificationState)
	if !result.Attribution.Valid() || result.ProductID < 1 || !result.ProductType.Valid() || !result.Commission.Status.Valid() {
		return SettlementContext{}, distributionport.ErrUnavailable
	}
	return result, nil
}

type Settlement struct {
	ID                                                        int64
	CommissionID                                              int64
	Reference, OriginalPaymentReference, InstructionReference string
	EffectReference, State                                    string
	AmountMinor                                               int64
	Currency                                                  string
	DeadlineAt                                                *time.Time
	Version                                                   int64
	CreatedAt, UpdatedAt                                      time.Time
}

type Exception struct {
	ID                                    int64
	CommissionID, SettlementID            int64
	Kind, Status                          string
	UnpaidDueMinor, AlreadyPaidMinor      int64
	AmountMinor                           int64
	Reason, EvidenceReference, ActorScope string
	Version                               int64
	CreatedAt, UpdatedAt                  time.Time
}

// InsertExceptionWithin records an auditable unresolved money fact. The
// caller already locks the Commission, so a terminal transition cannot create
// duplicate exception rows on a retry. Manual actions use their own receipt
// and append a later handling fact rather than rewriting this record.
func (r *Repository) InsertExceptionWithin(ctx context.Context, value Exception) (Exception, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Exception{}, err
	}
	if value.CommissionID < 1 || value.SettlementID < 0 || !validExceptionKind(value.Kind) || value.Status != "open" || value.UnpaidDueMinor < 0 || value.AlreadyPaidMinor < 0 || value.AmountMinor < 0 || value.Reason != strings.TrimSpace(value.Reason) || value.Reason == "" || len(value.Reason) > 500 || value.EvidenceReference != strings.TrimSpace(value.EvidenceReference) || len(value.EvidenceReference) > 500 || value.ActorScope != strings.TrimSpace(value.ActorScope) || value.ActorScope == "" || len(value.ActorScope) > 200 || value.Version != 1 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return Exception{}, ErrInvalid
	}
	return scanException(tx.QueryRow(ctx, `INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,NULLIF($2,0),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id,commission_id,COALESCE(settlement_id,0),kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at`, value.CommissionID, value.SettlementID, value.Kind, value.Status, value.UnpaidDueMinor, value.AlreadyPaidMinor, value.AmountMinor, value.Reason, value.EvidenceReference, value.ActorScope, value.Version, value.CreatedAt.UTC(), value.UpdatedAt.UTC()))
}

// FindOpenExceptionWithin reads the single warning/exception fact for a
// commission kind under the commission lock held by its caller. It is used for
// idempotent operational warnings such as the split-channel deadline window;
// recovery and refund adjustments remain append-only records instead.
func (r *Repository) FindOpenExceptionWithin(ctx context.Context, commissionID int64, kind string) (Exception, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Exception{}, err
	}
	if commissionID < 1 || !validExceptionKind(kind) {
		return Exception{}, ErrInvalid
	}
	return scanException(tx.QueryRow(ctx, `SELECT id,commission_id,COALESCE(settlement_id,0),kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at FROM distribution_exceptions WHERE commission_id=$1 AND kind=$2 AND status='open' ORDER BY id DESC LIMIT 1 FOR UPDATE`, commissionID, kind))
}

// FindExceptionByEvidenceWithin finds the immutable source fact regardless of
// its handling status. It lets a replay of the same Provider instruction stay
// idempotent after an administrator has resolved the operational exception,
// while a distinct evidence reference can still be recorded later.
func (r *Repository) FindExceptionByEvidenceWithin(ctx context.Context, commissionID int64, kind, evidence string) (Exception, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Exception{}, err
	}
	if commissionID < 1 || !validExceptionKind(kind) || evidence == "" || evidence != strings.TrimSpace(evidence) {
		return Exception{}, ErrInvalid
	}
	return scanException(tx.QueryRow(ctx, `SELECT id,commission_id,COALESCE(settlement_id,0),kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at FROM distribution_exceptions WHERE commission_id=$1 AND kind=$2 AND evidence_reference=$3 ORDER BY id DESC LIMIT 1 FOR UPDATE`, commissionID, kind, evidence))
}

// HasExceptionWithReasonWithin is a history read for a narrowly scoped,
// affirmative durable business fact. It deliberately does not depend on the
// current exception status: a later qualifying purchase must not revive the
// commission that was already revoked while its original split instruction was
// outstanding. Callers must use a controlled reason; a same-kind unavailable
// or conflicted check is not proof of revocation.
func (r *Repository) HasExceptionWithReasonWithin(ctx context.Context, commissionID int64, kind, reason string) (bool, error) {
	if commissionID < 1 || !validExceptionKind(kind) || reason == "" || reason != strings.TrimSpace(reason) {
		return false, distributionport.ErrConflict
	}
	tx, err := transaction(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM distribution_exceptions WHERE commission_id=$1 AND kind=$2 AND reason=$3)`, commissionID, kind, reason).Scan(&found)
	if err != nil {
		return false, mapError(err)
	}
	return found, nil
}

// ResolveOpenBusinessExceptionsWithin closes only the post-submission buyer
// refund and qualification-revocation facts once the same commission has
// reached its durable non-payable terminal state.  It deliberately leaves
// provider, reserve, and unrelated operational exceptions untouched.
func (r *Repository) ResolveOpenBusinessExceptionsWithin(ctx context.Context, commissionID int64, at time.Time) ([]Exception, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if commissionID < 1 || at.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `UPDATE distribution_exceptions
		SET status='resolved',version=version+1,updated_at=$2
		WHERE commission_id=$1 AND status='open'
		  AND kind IN ('buyer_refund_after_paid','qualification_revoked_after_paid')
		RETURNING id,commission_id,COALESCE(settlement_id,0),kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at`, commissionID, at.UTC())
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := []Exception{}
	for rows.Next() {
		value, scanErr := scanException(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func scanException(row rowScanner) (Exception, error) {
	var value Exception
	err := row.Scan(&value.ID, &value.CommissionID, &value.SettlementID, &value.Kind, &value.Status, &value.UnpaidDueMinor, &value.AlreadyPaidMinor, &value.AmountMinor, &value.Reason, &value.EvidenceReference, &value.ActorScope, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Exception{}, distributionport.ErrNotFound
	}
	if err != nil {
		return Exception{}, mapError(err)
	}
	if value.ID < 1 || value.CommissionID < 1 || !validExceptionKind(value.Kind) || value.Status == "" || value.UnpaidDueMinor < 0 || value.AlreadyPaidMinor < 0 || value.AmountMinor < 0 || value.Reason == "" || value.ActorScope == "" || value.Version < 1 || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Exception{}, distributionport.ErrUnavailable
	}
	return value, nil
}

func validExceptionKind(value string) bool {
	switch value {
	case "settlement_unknown", "settlement_not_paid", "settlement_deadline", "settlement_deadline_imminent", "receiver_unavailable", "qualification_revoked_after_paid", "buyer_refund_after_paid", "unfreeze_final_failed", "merchant_liability", "recovery":
		return true
	default:
		return false
	}
}

func (r *Repository) ReadSettlementContextWithin(ctx context.Context, commissionID int64) (SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return SettlementContext{}, err
	}
	if commissionID < 1 {
		return SettlementContext{}, ErrInvalid
	}
	return scanSettlementContext(tx.QueryRow(ctx, `SELECT `+settlementContextColumns+` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE c.id=$1 FOR UPDATE`, commissionID))
}

// ListSettlementContextsByOrderWithin finds only Distribution's own frozen
// commission facts for a successfully refunded order. It never touches Order
// tables; Order has already committed the authoritative refund transition.
func (r *Repository) ListSettlementContextsByOrderWithin(ctx context.Context, orderID int64) ([]SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if orderID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+settlementContextColumns+` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE c.order_id=$1 ORDER BY c.id FOR UPDATE`, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]SettlementContext, 0, 1)
	for rows.Next() {
		value, scanErr := scanSettlementContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

// ListSettlementContextsByQualificationCustomerProductWithin finds every
// commission made by one distributor for the exact product whose qualifying
// self-purchase has just been successfully refunded. The product ID/type and
// canonical customer are supplied by the trusted Order settlement event.
func (r *Repository) ListSettlementContextsByQualificationCustomerProductWithin(ctx context.Context, customerID, productID int64, productType distributiondomain.ProductType) ([]SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if customerID < 1 || productID < 1 || !productType.Valid() {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+settlementContextColumns+` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id JOIN distribution_distributors d ON d.id=c.distributor_id WHERE d.customer_id=$1 AND p.product_id=$2 AND p.product_type=$3 ORDER BY c.id FOR UPDATE`, customerID, productID, string(productType))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]SettlementContext, 0)
	for rows.Next() {
		value, scanErr := scanSettlementContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

// ListRefundRecheckContextsWithin acquires every commission touched by one
// buyer refund or qualification-product recheck in one globally ordered query.
// The worker filters the product rows through locked canonical lineage before
// changing them. Taking this single commission lock set avoids the deadlock
// pattern of first locking buyer rows and then taking same-product rows in a
// second query.
func (r *Repository) ListRefundRecheckContextsWithin(ctx context.Context, buyerOrderID int64, scopes []QualificationProductScope) ([]SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if buyerOrderID < 1 {
		return nil, ErrInvalid
	}
	clauses := []string{"c.order_id=$1"}
	args := []any{buyerOrderID}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if !scope.Valid() {
			return nil, ErrInvalid
		}
		key := strconv.FormatInt(scope.ProductID, 10) + ":" + string(scope.ProductType)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		productPos := len(args) + 1
		typePos := productPos + 1
		clauses = append(clauses, "(p.product_id=$"+strconv.Itoa(productPos)+" AND p.product_type=$"+strconv.Itoa(typePos)+")")
		args = append(args, scope.ProductID, string(scope.ProductType))
	}
	query := `SELECT ` + settlementContextColumns + ` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE ` + strings.Join(clauses, " OR ") + ` ORDER BY c.id FOR UPDATE`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]SettlementContext, 0)
	for rows.Next() {
		value, scanErr := scanSettlementContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

// ListSettlementContextsByQualificationEvidenceOrderWithin locates frozen
// attribution facts which expressly relied on an order as the promoter's
// qualification purchase. This is also the safe historical-order path: Order
// may have no native checkout snapshot, but a previously verified history
// evidence reference still names its exact source order. The caller must
// re-check current qualification before changing any commission.
func (r *Repository) ListSettlementContextsByQualificationEvidenceOrderWithin(ctx context.Context, orderID int64) ([]SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if orderID < 1 {
		return nil, ErrInvalid
	}
	prefix := "order:" + strconv.FormatInt(orderID, 10) + ":item:%"
	rows, err := tx.Query(ctx, `SELECT `+settlementContextColumns+` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE a.qualification_evidence_reference LIKE $1 ORDER BY c.id FOR UPDATE`, prefix)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]SettlementContext, 0)
	for rows.Next() {
		value, scanErr := scanSettlementContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

const settlementColumns = `id,commission_id,settlement_reference,amount_minor,currency,original_payment_reference,payment_instruction_reference,payment_effect_reference,state,provider_deadline_at,version,created_at,updated_at`

func scanSettlement(row rowScanner) (Settlement, error) {
	var value Settlement
	err := row.Scan(&value.ID, &value.CommissionID, &value.Reference, &value.AmountMinor, &value.Currency, &value.OriginalPaymentReference, &value.InstructionReference, &value.EffectReference, &value.State, &value.DeadlineAt, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settlement{}, distributionport.ErrNotFound
	}
	if err != nil {
		return Settlement{}, mapError(err)
	}
	if value.ID < 1 || value.CommissionID < 1 || value.Reference == "" || value.AmountMinor < 1 || value.Currency != "CNY" || value.OriginalPaymentReference == "" || value.State == "" || value.Version < 1 || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Settlement{}, distributionport.ErrUnavailable
	}
	return value, nil
}

func (r *Repository) InsertSettlementWithin(ctx context.Context, value Settlement) (Settlement, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Settlement{}, false, err
	}
	if value.CommissionID < 1 || value.Reference != strings.TrimSpace(value.Reference) || len(value.Reference) < 8 || len(value.Reference) > 200 || value.AmountMinor < 1 || value.Currency != "CNY" || value.OriginalPaymentReference != strings.TrimSpace(value.OriginalPaymentReference) || value.OriginalPaymentReference == "" || value.State != "planned" || value.Version != 1 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return Settlement{}, false, ErrInvalid
	}
	created, err := scanSettlement(tx.QueryRow(ctx, `INSERT INTO distribution_settlements(commission_id,settlement_reference,amount_minor,currency,original_payment_reference,state,provider_deadline_at,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(settlement_reference) DO NOTHING RETURNING `+settlementColumns, value.CommissionID, value.Reference, value.AmountMinor, value.Currency, value.OriginalPaymentReference, value.State, value.DeadlineAt, value.Version, value.CreatedAt.UTC(), value.UpdatedAt.UTC()))
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, distributionport.ErrNotFound) {
		return Settlement{}, false, err
	}
	existing, err := r.ReadSettlementByReferenceWithin(ctx, value.Reference, true)
	if err != nil {
		return Settlement{}, false, err
	}
	if existing.CommissionID != value.CommissionID || existing.AmountMinor != value.AmountMinor || existing.OriginalPaymentReference != value.OriginalPaymentReference {
		return Settlement{}, false, distributionport.ErrConflict
	}
	return existing, false, nil
}

func (r *Repository) ReadSettlementByReferenceWithin(ctx context.Context, reference string, lock bool) (Settlement, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Settlement{}, err
	}
	if reference == "" {
		return Settlement{}, ErrInvalid
	}
	query := `SELECT ` + settlementColumns + ` FROM distribution_settlements WHERE settlement_reference=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanSettlement(tx.QueryRow(ctx, query, reference))
}

func (r *Repository) ReadLatestSettlementByCommissionWithin(ctx context.Context, commissionID int64, lock bool) (Settlement, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Settlement{}, err
	}
	if commissionID < 1 {
		return Settlement{}, ErrInvalid
	}
	query := `SELECT ` + settlementColumns + ` FROM distribution_settlements WHERE commission_id=$1 ORDER BY id DESC LIMIT 1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanSettlement(tx.QueryRow(ctx, query, commissionID))
}

func (r *Repository) AcceptSettlementWithin(ctx context.Context, value Settlement, expectedVersion int64) (Settlement, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Settlement{}, err
	}
	if value.ID < 1 || expectedVersion < 1 || value.Version != expectedVersion+1 || value.State != "accepted" || value.InstructionReference == "" || value.EffectReference == "" || value.UpdatedAt.IsZero() {
		return Settlement{}, ErrInvalid
	}
	return scanSettlement(tx.QueryRow(ctx, `UPDATE distribution_settlements SET payment_instruction_reference=$2,payment_effect_reference=$3,state=$4,provider_deadline_at=$5,version=$6,updated_at=$7 WHERE id=$1 AND version=$8 RETURNING `+settlementColumns, value.ID, value.InstructionReference, value.EffectReference, value.State, value.DeadlineAt, value.Version, value.UpdatedAt.UTC(), expectedVersion))
}

// UpdateSettlementStateWithin records a Payment-reconciled result against the
// original immutable instruction. It never changes amount, receiver, original
// payment reference, or instruction reference; a timeout therefore remains
// queryable under the same provider operation instead of creating a second
// payout number.
func (r *Repository) UpdateSettlementStateWithin(ctx context.Context, value Settlement, expectedVersion int64) (Settlement, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Settlement{}, err
	}
	if value.ID < 1 || expectedVersion < 1 || value.Version != expectedVersion+1 || value.State != strings.TrimSpace(value.State) || !validSettlementState(value.State) || value.UpdatedAt.IsZero() {
		return Settlement{}, ErrInvalid
	}
	return scanSettlement(tx.QueryRow(ctx, `UPDATE distribution_settlements SET state=$2,provider_deadline_at=$3,version=$4,updated_at=$5 WHERE id=$1 AND version=$6 RETURNING `+settlementColumns, value.ID, value.State, value.DeadlineAt, value.Version, value.UpdatedAt.UTC(), expectedVersion))
}

func validSettlementState(value string) bool {
	switch value {
	case "planned", "accepted", "attempted", "outcome_unknown", "receiver_succeeded", "cancelled", "exception":
		return true
	default:
		return false
	}
}

// ListSettlementContextsByQualificationProductWithin returns frozen
// commission facts for one exact product. Refund recheck workers use it only
// after Order has supplied a trusted native checkout or verified-history
// product mapping, then re-run each distributor's QualificationService. This
// intentionally does not infer a distributor from raw payer/beneficiary IDs.
func (r *Repository) ListSettlementContextsByQualificationProductWithin(ctx context.Context, productID int64, productType distributiondomain.ProductType) ([]SettlementContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if productID < 1 || !productType.Valid() {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+settlementContextColumns+` FROM distribution_commissions c JOIN distribution_order_attributions a ON a.id=c.attribution_id JOIN distribution_product_policies p ON p.id=a.policy_id WHERE p.product_id=$1 AND p.product_type=$2 ORDER BY c.id FOR UPDATE`, productID, string(productType))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]SettlementContext, 0)
	for rows.Next() {
		value, scanErr := scanSettlementContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}
