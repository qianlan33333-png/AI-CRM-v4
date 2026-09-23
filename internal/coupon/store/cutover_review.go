package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"sort"
)

// CutoverReviewDigest freezes every Owner field, target, claim count and audit
// history. Values remain inside the Owner; only the digest crosses the Port.
func (r *Repository) CutoverReviewDigest(ctx context.Context, id couponport.ID) ([32]byte, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return [32]byte{}, e
	}
	c, e := r.get(ctx, tx, id, true)
	if e != nil {
		return [32]byte{}, e
	}
	c.AvailabilityStatus = ""
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
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
	sort.Strings(c.TargetRefs)
	var slug string
	var claims int64
	var audit json.RawMessage
	e = tx.QueryRow(ctx, `SELECT COALESCE(public_slug,''),(SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM coupon_audit_events a WHERE coupon_id=$1),'[]'::jsonb) FROM coupon_rules WHERE id=$1`, id).Scan(&slug, &claims, &audit)
	if e != nil {
		return [32]byte{}, e
	}
	b, e := json.Marshal(struct {
		Coupon couponport.Coupon
		Slug   string
		Claims int64
		Audit  json.RawMessage
	}{c, slug, claims, audit})
	return sha256.Sum256(b), e
}
func (r *Repository) ImportReviewedCutoverDefinition(ctx context.Context, in couponport.CutoverDefinitionImport, before [32]byte) (couponport.Coupon, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, e
	}
	actual, e := r.CutoverReviewDigest(ctx, in.ExistingID)
	if e != nil {
		return couponport.Coupon{}, e
	}
	if before == ([32]byte{}) || actual != before {
		return couponport.Coupon{}, errCutoverConflict
	}
	c, e := r.get(ctx, tx, in.ExistingID, true)
	if e != nil {
		return c, e
	}
	var claims, otherAudit int64
	e = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1),(SELECT count(*) FROM coupon_audit_events WHERE coupon_id=$1 AND event_type<>'coupon.public_shared')`, c.ID).Scan(&claims, &otherAudit)
	if e != nil {
		return c, e
	}
	if in.Actor < 1 || claims != 0 || otherAudit != 0 || !sameCutoverDefinition(c, in.Coupon) || in.TotalIssueLimit < c.TotalIssueLimit || in.IssuedCount < c.IssuedCount || in.IssuedCount > in.TotalIssueLimit || in.PublicSlug == "" || !cutoverSlug.MatchString(in.PublicSlug) {
		return c, errCutoverConflict
	}
	var oldSlug string
	if e = tx.QueryRow(ctx, `SELECT COALESCE(public_slug,'') FROM coupon_rules WHERE id=$1`, c.ID).Scan(&oldSlug); e != nil {
		return c, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO coupon_audit_events(event_type,coupon_id,actor_admin_user_id,payload,occurred_at) VALUES('coupon.cutover_reviewed',$1,$2,jsonb_build_object('transaction_id',txid_current()::text,'old_slug',$3::text,'new_slug',$4::text,'old_version',$5::text,'before_sha256',$6::text),clock_timestamp())`, c.ID, in.Actor, oldSlug, in.PublicSlug, fmt.Sprint(c.Version), fmt.Sprintf("%x", before))
	if e != nil {
		return c, e
	}
	// The unique public_slug index rejects collisions; no existing link is stolen.
	_, e = tx.Exec(ctx, `UPDATE coupon_rules SET total_issue_limit=$2,issued_count=$3,public_slug=$4,version=version+1,updated_at=GREATEST(updated_at,$5) WHERE id=$1`, c.ID, in.TotalIssueLimit, in.IssuedCount, in.PublicSlug, in.UpdatedAt.UTC())
	if e != nil {
		return c, e
	}
	return r.get(ctx, tx, c.ID, false)
}
