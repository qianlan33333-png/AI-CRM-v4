package store

import (
	"context"
	"strings"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ couponport.DefinitionImporter = (*Repository)(nil)

// ImportDefinition adds one normalized configuration rule to the caller's
// transaction. Historical issuance is deliberately rejected rather than
// copied into a definition-only migration.
func (r *Repository) ImportDefinition(ctx context.Context, input couponport.DefinitionImport) (couponport.Coupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.Coupon{}, err
	}
	v := input.Coupon
	if input.Actor < 1 || v.ID != 0 || v.IssuedCount != 0 || strings.TrimSpace(v.Name) != v.Name || v.Name == "" || len(v.Name) > 45 ||
		v.DiscountAmountTotal < 1 || v.Currency != "CNY" || v.TotalIssueLimit < 1 || v.PerUserIssueLimit < 1 || v.PerUserIssueLimit > v.TotalIssueLimit ||
		v.ClaimStartsAt.IsZero() || v.ClaimEndsAt.IsZero() || !v.ClaimEndsAt.After(v.ClaimStartsAt) || len(v.Instructions) > 200 ||
		input.CreatedAt.IsZero() || input.UpdatedAt.IsZero() || input.UpdatedAt.Before(input.CreatedAt) {
		return couponport.Coupon{}, couponapp.ErrInvalidCoupon
	}
	if v.Status != "draft" && v.Status != "published" && v.Status != "stopped" && v.Status != "archived" {
		return couponport.Coupon{}, couponapp.ErrInvalidCoupon
	}
	if (v.ValidityMode == couponport.ValidityFixedRange && (v.UseStartsAt == nil || v.UseEndsAt == nil || !v.UseEndsAt.After(*v.UseStartsAt) || v.RelativeValidityDays != nil)) ||
		(v.ValidityMode == couponport.ValidityRelativeDays && (v.UseStartsAt != nil || v.UseEndsAt != nil || v.RelativeValidityDays == nil || *v.RelativeValidityDays < 1)) {
		return couponport.Coupon{}, couponapp.ErrInvalidCoupon
	}
	if v.ValidityMode != couponport.ValidityFixedRange && v.ValidityMode != couponport.ValidityRelativeDays {
		return couponport.Coupon{}, couponapp.ErrInvalidCoupon
	}
	var id couponport.ID
	err = tx.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,use_ends_at,relative_validity_days,instructions,created_by,updated_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10,$11,$12,$13,$14,$14,$15,$16) RETURNING id`,
		v.Name, v.DiscountAmountTotal, v.Currency, v.Status, v.TotalIssueLimit, v.PerUserIssueLimit, v.ClaimStartsAt.UTC(), v.ClaimEndsAt.UTC(),
		v.ValidityMode, v.UseStartsAt, v.UseEndsAt, v.RelativeValidityDays, v.Instructions, input.Actor, input.CreatedAt.UTC(), input.UpdatedAt.UTC()).Scan(&id)
	if err != nil {
		return couponport.Coupon{}, err
	}
	if err = r.replaceTargets(ctx, tx, id, v.TargetRefs); err != nil {
		return couponport.Coupon{}, err
	}
	return r.get(ctx, tx, id, false)
}
