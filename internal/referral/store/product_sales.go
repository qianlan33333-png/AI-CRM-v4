package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

const productSaleEventColumns = `id,campaign_id,order_id,order_version,product_id,product_type,product_code,product_name,product_version,COALESCE(participation_id,0),COALESCE(team_id,0),COALESCE(promoter_customer_id,0),COALESCE(promotion_credential_ref,''),COALESCE(policy_version,0),commission_rate_basis_points,wait_days,buyer_customer_id,beneficiary_customer_id,kind,amount_delta_minor,order_count_delta,source_paid_event_id,COALESCE(reverses_sale_event_id,0),refund_receipt_key,source_digest,occurred_at`

func (r *Repository) InsertProductActivityContextWithin(ctx context.Context, value referralport.ProductActivityContext) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if value.ContextDigest == ([sha256.Size]byte{}) || value.CampaignID < 1 || value.ProductID < 1 || (value.ProductType != "standard_product" && value.ProductType != "service_period") || !value.SalesMetric.Valid() || value.State != "active" || value.ExpiresAt.IsZero() || value.CreatedAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO referral_product_activity_contexts(context_digest,campaign_id,product_id,product_type,sales_metric,state,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, value.ContextDigest[:], value.CampaignID, value.ProductID, value.ProductType, string(value.SalesMetric), value.State, value.ExpiresAt.UTC(), value.CreatedAt.UTC())
	return mapError(err)
}

func (r *Repository) ReadProductSaleContextWithin(ctx context.Context, digest [sha256.Size]byte, productID int64, productType string, at time.Time) (referralport.ProductSaleContext, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referralport.ProductSaleContext{}, err
	}
	if digest == ([sha256.Size]byte{}) || productID < 1 || (productType != "standard_product" && productType != "service_period") || at.IsZero() {
		return referralport.ProductSaleContext{}, ErrInvalid
	}
	var value referralport.ProductSaleContext
	var metric string
	err = tx.QueryRow(ctx, `SELECT campaign_id,product_id,product_type,expires_at,sales_metric
FROM referral_product_activity_contexts
WHERE context_digest=$1 AND product_id=$2 AND product_type=$3 AND state='active' AND expires_at>$4`, digest[:], productID, productType, at.UTC()).Scan(&value.CampaignID, &value.ProductID, &value.ProductType, &value.ExpiresAt, &metric)
	if errors.Is(err, pgx.ErrNoRows) {
		return referralport.ProductSaleContext{}, referralport.ErrNotFound
	}
	if err != nil {
		return referralport.ProductSaleContext{}, mapError(err)
	}
	value.SalesMetric = referralport.SalesMetric(metric)
	if value.CampaignID < 1 || value.ProductID != productID || value.ProductType != productType || !value.SalesMetric.Valid() {
		return referralport.ProductSaleContext{}, referralport.ErrUnavailable
	}
	return value, nil
}

func (r *Repository) InsertProductSaleCheckoutWithin(ctx context.Context, value referralport.ProductSaleCheckoutSnapshot) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if value.OrderID < 1 || value.OrderVersion < 1 || value.CampaignID < 1 || value.ProductID < 1 || (value.ProductType != "standard_product" && value.ProductType != "service_period") || value.ProductCode == "" || len(value.ProductCode) > 200 || value.ProductName == "" || len(value.ProductName) > 500 || value.ProductVersion < 1 || value.PolicyVersion < 0 || value.CommissionRateBasisPoints < 0 || value.CommissionRateBasisPoints > 3000 || value.WaitDays < 0 || value.WaitDays > 29 || value.BuyerCustomerID < 1 || value.BeneficiaryCustomerID < 1 || value.ActivityContextDigest == ([sha256.Size]byte{}) || value.CreatedAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO referral_product_sale_checkouts(order_id,order_version,campaign_id,product_id,product_type,product_code,product_name,product_version,promoter_customer_id,promotion_credential_ref,policy_version,commission_rate_basis_points,wait_days,buyer_customer_id,beneficiary_customer_id,activity_context_digest,promotion_context_digest,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,0),NULLIF($10,''),NULLIF($11,0),$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT(order_id) DO NOTHING`, value.OrderID, value.OrderVersion, value.CampaignID, value.ProductID, value.ProductType, value.ProductCode, value.ProductName, value.ProductVersion, value.PromotionCustomerID, value.PromotionCredentialRef, value.PolicyVersion, value.CommissionRateBasisPoints, value.WaitDays, value.BuyerCustomerID, value.BeneficiaryCustomerID, value.ActivityContextDigest[:], value.PromotionContextDigest[:], value.CreatedAt.UTC())
	return mapError(err)
}

func (r *Repository) ReadProductSaleCheckoutWithin(ctx context.Context, orderID int64, lock bool) (referralport.ProductSaleCheckoutSnapshot, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referralport.ProductSaleCheckoutSnapshot{}, err
	}
	if orderID < 1 {
		return referralport.ProductSaleCheckoutSnapshot{}, ErrInvalid
	}
	query := `SELECT order_id,order_version,campaign_id,product_id,product_type,product_code,product_name,product_version,COALESCE(promoter_customer_id,0),COALESCE(promotion_credential_ref,''),COALESCE(policy_version,0),commission_rate_basis_points,wait_days,buyer_customer_id,beneficiary_customer_id,activity_context_digest,promotion_context_digest,created_at FROM referral_product_sale_checkouts WHERE order_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var value referralport.ProductSaleCheckoutSnapshot
	var activity, promotion []byte
	err = tx.QueryRow(ctx, query, orderID).Scan(&value.OrderID, &value.OrderVersion, &value.CampaignID, &value.ProductID, &value.ProductType, &value.ProductCode, &value.ProductName, &value.ProductVersion, &value.PromotionCustomerID, &value.PromotionCredentialRef, &value.PolicyVersion, &value.CommissionRateBasisPoints, &value.WaitDays, &value.BuyerCustomerID, &value.BeneficiaryCustomerID, &activity, &promotion, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referralport.ProductSaleCheckoutSnapshot{}, referralport.ErrNotFound
	}
	if err != nil {
		return referralport.ProductSaleCheckoutSnapshot{}, mapError(err)
	}
	if len(activity) != sha256.Size || len(promotion) != sha256.Size {
		return referralport.ProductSaleCheckoutSnapshot{}, referralport.ErrUnavailable
	}
	copy(value.ActivityContextDigest[:], activity)
	copy(value.PromotionContextDigest[:], promotion)
	if value.OrderID < 1 || value.OrderVersion < 1 || value.CampaignID < 1 || value.ProductID < 1 || (value.ProductType != "standard_product" && value.ProductType != "service_period") || value.ProductCode == "" || value.ProductName == "" || value.ProductVersion < 1 || value.BuyerCustomerID < 1 || value.BeneficiaryCustomerID < 1 || value.ActivityContextDigest == ([sha256.Size]byte{}) || value.CreatedAt.IsZero() {
		return referralport.ProductSaleCheckoutSnapshot{}, referralport.ErrUnavailable
	}
	return value, nil
}

func (r *Repository) ReadProductSaleCreditWithin(ctx context.Context, orderID, productID int64, lock bool) (referraldomain.ProductSaleEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ProductSaleEvent{}, err
	}
	if orderID < 1 || productID < 1 {
		return referraldomain.ProductSaleEvent{}, ErrInvalid
	}
	query := `SELECT ` + productSaleEventColumns + ` FROM referral_product_sale_events WHERE order_id=$1 AND product_id=$2 AND kind='credit'`
	_ = lock // the credit row is locked by the caller before this aggregate read
	return scanProductSaleEvent(tx.QueryRow(ctx, query, orderID, productID))
}

func (r *Repository) ReadProductSaleReversalByReceiptWithin(ctx context.Context, saleID int64, receiptKey string) (referraldomain.ProductSaleEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ProductSaleEvent{}, err
	}
	if saleID < 1 || receiptKey == "" {
		return referraldomain.ProductSaleEvent{}, ErrInvalid
	}
	return scanProductSaleEvent(tx.QueryRow(ctx, `SELECT `+productSaleEventColumns+` FROM referral_product_sale_events WHERE reverses_sale_event_id=$1 AND refund_receipt_key=$2`, saleID, receiptKey))
}

func (r *Repository) SumProductSaleReversalsWithin(ctx context.Context, saleID int64, lock bool) (int64, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, 0, err
	}
	if saleID < 1 {
		return 0, 0, ErrInvalid
	}
	query := `SELECT COALESCE(-SUM(amount_delta_minor),0),COALESCE(-SUM(order_count_delta),0) FROM referral_product_sale_events WHERE reverses_sale_event_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var amount, count int64
	if err = tx.QueryRow(ctx, query, saleID).Scan(&amount, &count); err != nil {
		return 0, 0, mapError(err)
	}
	return amount, count, nil
}

func (r *Repository) InsertProductSaleEventWithin(ctx context.Context, value referraldomain.ProductSaleEvent) (referraldomain.ProductSaleEvent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.ProductSaleEvent{}, err
	}
	// Database-assigned events are inserted with ID zero.  Domain Valid
	// deliberately requires a positive persisted ID, so validate an otherwise
	// identical candidate with the sentinel ID and reject caller-supplied IDs.
	if value.ID != 0 {
		return referraldomain.ProductSaleEvent{}, ErrInvalid
	}
	valid := value
	valid.ID = 1
	if !valid.Valid() {
		return referraldomain.ProductSaleEvent{}, ErrInvalid
	}
	return scanProductSaleEvent(tx.QueryRow(ctx, `INSERT INTO referral_product_sale_events(campaign_id,order_id,order_version,product_id,product_type,product_code,product_name,product_version,participation_id,team_id,promoter_customer_id,promotion_credential_ref,policy_version,commission_rate_basis_points,wait_days,buyer_customer_id,beneficiary_customer_id,kind,amount_delta_minor,order_count_delta,source_paid_event_id,reverses_sale_event_id,refund_receipt_key,source_digest,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,0),NULLIF($11,0),NULLIF($12,''),NULLIF($13,0),$14,$15,$16,$17,$18,$19,$20,$21,NULLIF($22,0),$23,$24,$25)
RETURNING `+productSaleEventColumns, value.CampaignID, value.OrderID, value.OrderVersion, value.ProductID, value.ProductType, value.ProductCode, value.ProductName, value.ProductVersion, value.ParticipationID, value.TeamID, value.PromoterCustomerID, value.PromotionCredentialRef, value.PolicyVersion, value.CommissionRateBasisPoints, value.WaitDays, value.BuyerCustomerID, value.BeneficiaryCustomerID, string(value.Kind), value.AmountDeltaMinor, value.OrderCountDelta, value.SourcePaidEventID, value.ReversesSaleEventID, value.RefundReceiptKey, value.SourceDigest[:], value.OccurredAt.UTC()))
}

func scanProductSaleEvent(row rowScanner) (referraldomain.ProductSaleEvent, error) {
	var value referraldomain.ProductSaleEvent
	var kind string
	var digest []byte
	err := row.Scan(&value.ID, &value.CampaignID, &value.OrderID, &value.OrderVersion, &value.ProductID, &value.ProductType, &value.ProductCode, &value.ProductName, &value.ProductVersion, &value.ParticipationID, &value.TeamID, &value.PromoterCustomerID, &value.PromotionCredentialRef, &value.PolicyVersion, &value.CommissionRateBasisPoints, &value.WaitDays, &value.BuyerCustomerID, &value.BeneficiaryCustomerID, &kind, &value.AmountDeltaMinor, &value.OrderCountDelta, &value.SourcePaidEventID, &value.ReversesSaleEventID, &value.RefundReceiptKey, &digest, &value.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.ProductSaleEvent{}, referralport.ErrNotFound
	}
	if err != nil {
		return referraldomain.ProductSaleEvent{}, mapError(err)
	}
	value.Kind = referraldomain.ProductSaleEventKind(kind)
	value.Currency = "CNY"
	if len(digest) != sha256.Size {
		return referraldomain.ProductSaleEvent{}, referralport.ErrUnavailable
	}
	copy(value.SourceDigest[:], digest)
	if !value.Valid() {
		return referraldomain.ProductSaleEvent{}, referralport.ErrUnavailable
	}
	return value, nil
}
