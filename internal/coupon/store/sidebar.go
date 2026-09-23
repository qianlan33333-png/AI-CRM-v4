package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ListCustomerCoupons(ctx context.Context, customerID int64, limit int32) (couponport.CustomerCouponPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.CustomerCouponPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.customer_id=$1 ORDER BY claim.claimed_at DESC,claim.id DESC LIMIT $2`, customerID, limit)
	if err != nil {
		return couponport.CustomerCouponPage{}, err
	}
	defer rows.Close()
	page := couponport.CustomerCouponPage{Items: []couponport.CustomerCoupon{}}
	for rows.Next() {
		var item couponport.CustomerCoupon
		if err = rows.Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE customer_id=$1`, customerID).Scan(&page.Total)
	return page, err
}

func (r *Repository) ListCouponClaims(ctx context.Context, couponID couponport.ID, limit, offset int32) (couponport.AdminCouponClaimPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.AdminCouponClaimPage{}, err
	}
	if couponID < 1 || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return couponport.AdminCouponClaimPage{}, couponapp.ErrInvalidCoupon
	}
	page := couponport.AdminCouponClaimPage{Items: []couponport.AdminCouponClaim{}, Limit: limit, Offset: offset}
	rows, err := tx.Query(ctx, `SELECT id,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at FROM coupon_customer_claims WHERE coupon_id=$1 ORDER BY claimed_at DESC,id DESC LIMIT $2 OFFSET $3`, couponID, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item couponport.AdminCouponClaim
		if err = rows.Scan(&item.ClaimID, &item.CustomerID, &item.CouponID, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1`, couponID).Scan(&page.Total)
	return page, err
}

func (r *Repository) ImportHistoricalCustomerCoupon(ctx context.Context, input couponport.HistoricalCustomerCoupon) (couponport.CustomerCoupon, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.CustomerCoupon{}, false, err
	}
	var item couponport.CustomerCoupon
	var digest []byte
	query := `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at,claim.source_digest FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.source_system=$1 AND claim.source_key=$2 FOR UPDATE OF claim`
	err = tx.QueryRow(ctx, query, input.SourceSystem, input.SourceKey).Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt, &digest)
	if err == nil {
		if len(digest) != 32 || string(digest) != string(input.SourceDigest[:]) {
			return item, false, couponapp.ErrConflict
		}
		return item, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return item, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, input.SourceSystem, input.SourceKey, input.CustomerID, input.CouponID, input.Status, input.ClaimNoMasked, input.ClaimedAt, input.ValidFrom, input.ValidUntil, input.RedeemedAt, input.SourceDigest[:], input.CreatedAt, input.UpdatedAt).Scan(&id)
	if err != nil {
		return item, false, err
	}
	err = tx.QueryRow(ctx, `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.id=$1`, id).Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt)
	return item, err == nil, err
}

var _ couponapp.CustomerCouponStore = (*Repository)(nil)
var _ couponapp.CouponClaimAdminStore = (*Repository)(nil)

// ListSidebarClaimable reads Coupon definitions for the scoped sidebar
// customer. It does not filter on availability or the customer's claim count:
// the caller needs both facts to distinguish a visible directory item from a
// currently unavailable public claim. Public slugs remain optional because
// only the explicit Coupon share command may create one.
func (r *Repository) ListSidebarClaimable(ctx context.Context, customerID int64, limit, offset int32) (couponapp.SidebarClaimableRecordPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponapp.SidebarClaimableRecordPage{}, err
	}
	if customerID < 1 || limit < 1 || limit > couponport.SidebarClaimableMaximumLimit || offset < 0 || offset > couponport.SidebarClaimableMaximumOffset {
		return couponapp.SidebarClaimableRecordPage{}, couponapp.ErrInvalidCoupon
	}
	page := couponapp.SidebarClaimableRecordPage{Items: []couponapp.SidebarClaimableRecord{}, Limit: limit, Offset: offset}
	rows, err := tx.Query(ctx, `SELECT `+couponColumns+`,COALESCE(rule.public_slug,''),(SELECT count(*) FROM coupon_customer_claims claim WHERE claim.customer_id=$1 AND claim.coupon_id=rule.id)
		FROM coupon_rules rule
		WHERE rule.status<>'draft' AND rule.status<>'archived'
		ORDER BY rule.updated_at DESC,rule.id DESC
		LIMIT $2 OFFSET $3`, customerID, limit, offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var record couponapp.SidebarClaimableRecord
		var mode string
		if err = rows.Scan(&record.Coupon.ID, &record.Coupon.Name, &record.Coupon.DiscountAmountTotal, &record.Coupon.Currency, &record.Coupon.Status, &record.Coupon.TotalIssueLimit, &record.Coupon.PerUserIssueLimit, &record.Coupon.IssuedCount, &record.Coupon.ClaimStartsAt, &record.Coupon.ClaimEndsAt, &mode, &record.Coupon.UseStartsAt, &record.Coupon.UseEndsAt, &record.Coupon.RelativeValidityDays, &record.Coupon.Instructions, &record.Coupon.CreatedBy, &record.Coupon.UpdatedBy, &record.Coupon.Version, &record.Coupon.CreatedAt, &record.Coupon.UpdatedAt, &record.PublicSlug, &record.ClaimCount); err != nil {
			rows.Close()
			return page, err
		}
		record.Coupon.ValidityMode = couponport.ValidityMode(mode)
		page.Items = append(page.Items, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	rows.Close()
	for index := range page.Items {
		page.Items[index].Coupon.TargetRefs, err = r.targets(ctx, tx, page.Items[index].Coupon.ID)
		if err != nil {
			return page, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_rules WHERE status<>'draft' AND status<>'archived'`).Scan(&page.Total)
	return page, err
}

// ReadSidebarClaimable is the exact-item counterpart to the paged directory.
// It reads a definition plus the scoped claim-count fact, but has no write
// path: sending a link cannot issue a coupon or occupy an issue slot.
func (r *Repository) ReadSidebarClaimable(ctx context.Context, customerID int64, couponID couponport.ID) (couponapp.SidebarClaimableRecord, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponapp.SidebarClaimableRecord{}, err
	}
	if customerID < 1 || couponID < 1 {
		return couponapp.SidebarClaimableRecord{}, couponapp.ErrInvalidCoupon
	}
	var record couponapp.SidebarClaimableRecord
	var mode string
	err = tx.QueryRow(ctx, `SELECT `+couponColumns+`,COALESCE(rule.public_slug,''),(SELECT count(*) FROM coupon_customer_claims claim WHERE claim.customer_id=$1 AND claim.coupon_id=rule.id)
		FROM coupon_rules rule WHERE rule.id=$2 AND rule.status<>'draft' AND rule.status<>'archived'`, customerID, couponID).Scan(
		&record.Coupon.ID, &record.Coupon.Name, &record.Coupon.DiscountAmountTotal, &record.Coupon.Currency, &record.Coupon.Status, &record.Coupon.TotalIssueLimit, &record.Coupon.PerUserIssueLimit, &record.Coupon.IssuedCount, &record.Coupon.ClaimStartsAt, &record.Coupon.ClaimEndsAt, &mode, &record.Coupon.UseStartsAt, &record.Coupon.UseEndsAt, &record.Coupon.RelativeValidityDays, &record.Coupon.Instructions, &record.Coupon.CreatedBy, &record.Coupon.UpdatedBy, &record.Coupon.Version, &record.Coupon.CreatedAt, &record.Coupon.UpdatedAt, &record.PublicSlug, &record.ClaimCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return couponapp.SidebarClaimableRecord{}, couponapp.ErrNotFound
	}
	if err != nil {
		return couponapp.SidebarClaimableRecord{}, err
	}
	record.Coupon.ValidityMode = couponport.ValidityMode(mode)
	record.Coupon.TargetRefs, err = r.targets(ctx, tx, record.Coupon.ID)
	if err != nil {
		return couponapp.SidebarClaimableRecord{}, err
	}
	return record, nil
}

var _ couponapp.SidebarClaimableCatalogStore = (*Repository)(nil)
