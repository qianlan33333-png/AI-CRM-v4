package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// GetPublicCouponBySlug reads the Coupon-owned public projection. A stopped or
// archived rule retains its stable link so the host can render its terminal
// state; drafts are never public.
func (r *Repository) GetPublicCouponBySlug(ctx context.Context, slug string) (couponport.PublicCoupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.PublicCoupon{}, err
	}
	coupon, err := scanCoupon(tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupon_rules WHERE public_slug=$1 AND status<>'draft'`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return couponport.PublicCoupon{}, couponapp.ErrNotFound
	}
	if err != nil {
		return couponport.PublicCoupon{}, err
	}
	coupon.TargetRefs, err = r.targets(ctx, tx, coupon.ID)
	if err != nil {
		return couponport.PublicCoupon{}, err
	}
	return couponport.PublicCoupon{Coupon: coupon, PublicSlug: slug}, nil
}

// PublicCouponClaimState is the small holder-scoped count required by the
// frozen page. It does not expose claim IDs or checkouts and stays inside the
// caller's UoW.
func (r *Repository) PublicCouponClaimState(ctx context.Context, holderCustomerID int64, couponID couponport.ID) (couponport.PublicCouponClaimState, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.PublicCouponClaimState{}, err
	}
	if holderCustomerID < 1 || couponID < 1 {
		return couponport.PublicCouponClaimState{}, couponapp.ErrInvalidCoupon
	}
	var result couponport.PublicCouponClaimState
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE customer_id=$1 AND coupon_id=$2`, holderCustomerID, couponID).Scan(&result.ClaimCount); err != nil {
		return couponport.PublicCouponClaimState{}, err
	}
	return result, nil
}

// ListAvailableCouponClaims is deliberately scoped by the payment session's
// canonical holder. It has no customer lookup path and it never derives an
// amount: Order will validate the selected claim again while reserving it.
func (r *Repository) ListAvailableCouponClaims(ctx context.Context, holderCustomerID int64, targetRef string, at time.Time) ([]couponport.CustomerCoupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at
		FROM coupon_customer_claims claim
		JOIN coupon_rules rule ON rule.id=claim.coupon_id
		JOIN coupon_rule_targets target ON target.coupon_id=rule.id
		WHERE claim.customer_id=$1
		  AND claim.status IN ('available','claimed')
		  AND claim.valid_from<=$2 AND claim.valid_until>$2
		  AND rule.status='published' AND rule.currency='CNY'
		  AND target.target_ref=$3
		ORDER BY rule.discount_amount_total DESC,claim.valid_until ASC,claim.claimed_at ASC,claim.id ASC`, holderCustomerID, at, targetRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []couponport.CustomerCoupon{}
	for rows.Next() {
		var item couponport.CustomerCoupon
		if err := rows.Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// EnsurePublicCouponSlug serializes on the rule row. It accepts a random
// candidate from the application, makes it immutable, and records the local
// public-share fact with Coupon audit/outbox rows in this same UoW.
func (r *Repository) EnsurePublicCouponSlug(ctx context.Context, couponID couponport.ID, proposedSlug string, actorID int64, at time.Time) (couponport.PublicCoupon, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	coupon, err := r.get(ctx, tx, couponID, true)
	if err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	if coupon.Status != "published" {
		return couponport.PublicCoupon{}, false, couponapp.ErrConflict
	}
	var existing *string
	if err = tx.QueryRow(ctx, `SELECT public_slug FROM coupon_rules WHERE id=$1 FOR KEY SHARE`, couponID).Scan(&existing); err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	if existing != nil {
		return couponport.PublicCoupon{Coupon: coupon, PublicSlug: *existing}, false, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE coupon_rules SET public_slug=$2,updated_by=$3,updated_at=$4,version=version+1 WHERE id=$1 AND public_slug IS NULL`, couponID, proposedSlug, actorID, at); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return couponport.PublicCoupon{}, false, couponapp.ErrConflict
		}
		return couponport.PublicCoupon{}, false, err
	}
	payload, err := json.Marshal(map[string]any{"coupon_id": couponID, "actor": actorID, "public_slug": proposedSlug})
	if err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coupon_audit_events(event_type,coupon_id,actor_admin_user_id,payload,occurred_at) VALUES('coupon.public_shared',$1,$2,$3::jsonb,$4)`, couponID, actorID, payload, at); err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coupon_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('coupon.public_shared',$1,$2,$3::jsonb,$4)`, fmt.Sprintf("coupon:public-share:%d", couponID), couponID, payload, at); err != nil {
		return couponport.PublicCoupon{}, false, err
	}
	coupon.Version++
	coupon.UpdatedAt = at
	coupon.UpdatedBy = actorID
	return couponport.PublicCoupon{Coupon: coupon, PublicSlug: proposedSlug}, true, nil
}

var _ couponapp.PublicStore = (*Repository)(nil)
