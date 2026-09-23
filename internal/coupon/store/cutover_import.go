package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ couponport.CutoverDefinitionImporter = (*Repository)(nil)
var errCutoverConflict = errors.New("coupon cutover definition conflict")
var cutoverSlug = regexp.MustCompile(`^[A-Za-z0-9_-]{6,120}$`)

// ImportCutoverDefinition never creates claims or dispatches effects. Source
// totals can fill an imported zero counter, or advance a counter whose target
// still equals the prior audited import. Native target claiming diverges from
// that baseline and fails closed. Business fields are never blindly replaced.
func (r *Repository) ImportCutoverDefinition(ctx context.Context, in couponport.CutoverDefinitionImport) (couponport.Coupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.Coupon{}, err
	}
	desired := in.Coupon
	if in.Actor < 1 || desired.ID != 0 || desired.IssuedCount < 0 || desired.IssuedCount > desired.TotalIssueLimit || (in.PublicSlug != "" && !cutoverSlug.MatchString(in.PublicSlug)) {
		return couponport.Coupon{}, errCutoverConflict
	}
	var rule couponport.Coupon
	if in.ExistingID == 0 {
		definition := in.DefinitionImport
		definition.IssuedCount = 0
		rule, err = r.ImportDefinition(ctx, definition)
	} else {
		rule, err = r.get(ctx, tx, in.ExistingID, true)
		if err == nil && (!sameCutoverDefinition(rule, desired) || (rule.TotalIssueLimit != desired.TotalIssueLimit && (!in.AllowSourceLimitIncrease || rule.TotalIssueLimit != in.ExpectedTotalIssueLimit || desired.TotalIssueLimit < rule.TotalIssueLimit))) {
			err = errCutoverConflict
		}
	}
	if err != nil {
		return couponport.Coupon{}, err
	}
	var existingSlug string
	var claims int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(public_slug,''),(SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1) FROM coupon_rules WHERE id=$1 FOR UPDATE`, rule.ID).Scan(&existingSlug, &claims); err != nil {
		return couponport.Coupon{}, err
	}
	counterConflict := rule.IssuedCount != 0 && rule.IssuedCount != desired.IssuedCount
	if in.ExpectedIssuedCount != nil {
		counterConflict = rule.IssuedCount != *in.ExpectedIssuedCount || desired.IssuedCount < *in.ExpectedIssuedCount
	}
	if claims > desired.IssuedCount || counterConflict || (existingSlug != "" && existingSlug != in.PublicSlug) {
		return couponport.Coupon{}, errCutoverConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE coupon_rules SET public_slug=NULLIF($2,''),issued_count=$3,total_issue_limit=$4,version=version+CASE WHEN total_issue_limit<>$4 THEN 1 ELSE 0 END,updated_at=CASE WHEN total_issue_limit<>$4 THEN GREATEST(updated_at,$5) ELSE updated_at END WHERE id=$1`, rule.ID, in.PublicSlug, desired.IssuedCount, desired.TotalIssueLimit, in.UpdatedAt.UTC()); err != nil {
		return couponport.Coupon{}, err
	}
	return r.get(ctx, tx, rule.ID, false)
}

func sameCutoverDefinition(a, b couponport.Coupon) bool {
	// Exclude only owner metadata, derived availability and the separately
	// reconciled counter. Every business definition field and target is compared.
	clean := func(c couponport.Coupon) []byte {
		c.ID = 0
		c.Version = 0
		c.CreatedBy = 0
		c.UpdatedBy = 0
		c.CreatedAt = b.CreatedAt
		c.UpdatedAt = b.UpdatedAt
		c.ClaimStartsAt = c.ClaimStartsAt.UTC()
		c.ClaimEndsAt = c.ClaimEndsAt.UTC()
		if c.UseStartsAt != nil {
			v := c.UseStartsAt.UTC()
			c.UseStartsAt = &v
		}
		if c.UseEndsAt != nil {
			v := c.UseEndsAt.UTC()
			c.UseEndsAt = &v
		}
		c.AvailabilityStatus = ""
		c.IssuedCount = 0
		c.TotalIssueLimit = 0
		c.TargetRefs = append([]string{}, c.TargetRefs...)
		sort.Strings(c.TargetRefs)
		raw, _ := json.Marshal(c)
		return raw
	}
	return string(clean(a)) == string(clean(b))
}
