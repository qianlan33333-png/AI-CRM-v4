package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type checkoutClaim struct {
	item couponport.CustomerCoupon
	rule couponport.Coupon
}
type checkoutRedemption struct {
	id, claimID                         int64
	orderReference, status, claimStatus string
	snapshot                            couponport.ReservationSnapshot
	validUntil                          *time.Time
}

func (r *Repository) ClaimCoupon(ctx context.Context, c couponport.ClaimCommand, key, payload [32]byte, at time.Time) (couponport.CustomerCoupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.CustomerCoupon{}, err
	}
	if err = lockCheckoutOperation(ctx, tx, "claim", c.ActorScope, key); err != nil {
		return couponport.CustomerCoupon{}, err
	}
	if prior, found, e := findClaimReceipt(ctx, tx, c.ActorScope, key, payload); e != nil || found {
		return prior, e
	}
	rule, e := r.get(ctx, tx, c.CouponID, true)
	if e != nil {
		return couponport.CustomerCoupon{}, e
	}
	if rule.Status != "published" || at.Before(rule.ClaimStartsAt) || !at.Before(rule.ClaimEndsAt) || rule.IssuedCount >= rule.TotalIssueLimit {
		return couponport.CustomerCoupon{}, couponapp.ErrNoEligibleCoupon
	}
	var held int64
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1 AND customer_id=$2`, rule.ID, c.HolderCustomerID).Scan(&held); e != nil {
		return couponport.CustomerCoupon{}, e
	}
	if held >= rule.PerUserIssueLimit {
		return couponport.CustomerCoupon{}, couponapp.ErrNoEligibleCoupon
	}
	from, until, e := couponClaimValidity(rule, at)
	if e != nil {
		return couponport.CustomerCoupon{}, e
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_rules SET issued_count=issued_count+1,updated_at=$2 WHERE id=$1 AND issued_count<total_issue_limit`, rule.ID, at)
	if e != nil {
		return couponport.CustomerCoupon{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.CustomerCoupon{}, couponapp.ErrNoEligibleCoupon
	}
	var id int64
	// The canonical claim projection keys native writes globally. Its key must
	// use the same actor scope as the receipt namespace; otherwise two valid
	// actors who happen to reuse a browser-generated key would collide.
	scopeDigest := sha256.Sum256([]byte(c.ActorScope))
	sourceKey := "claim:" + hex.EncodeToString(scopeDigest[:]) + ":" + hex.EncodeToString(key[:])
	e = tx.QueryRow(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,source_digest,created_at,updated_at) VALUES('native-coupon',$1,$2,$3,'available','',$4,$5,$6,$7,$4,$4) RETURNING id`, sourceKey, c.HolderCustomerID, rule.ID, at, from, until, payload[:]).Scan(&id)
	if e != nil {
		return couponport.CustomerCoupon{}, e
	}
	item := couponport.CustomerCoupon{ClaimID: id, CouponID: int64(rule.ID), Name: rule.Name, DiscountMinor: rule.DiscountAmountTotal, Currency: rule.Currency, Status: "available", ClaimedAt: at, ValidFrom: &from, ValidUntil: &until}
	raw, e := json.Marshal(item)
	if e != nil {
		return couponport.CustomerCoupon{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO coupon_claim_operation_receipts(operation,actor_scope,key_digest,payload_digest,claim_id,result_snapshot,created_at) VALUES('claim',$1,$2,$3,$4,$5::jsonb,$6)`, c.ActorScope, key[:], payload[:], id, raw, at); e != nil {
		return couponport.CustomerCoupon{}, e
	}
	if e = appendClaimFact(ctx, tx, id, 0, "claim", c.ActorScope, payload, key, at); e != nil {
		return couponport.CustomerCoupon{}, e
	}
	return item, nil
}

func (r *Repository) ReserveCoupon(ctx context.Context, c couponport.ReserveCommand, key, payload [32]byte, at time.Time) (couponport.ReservationSnapshot, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = lockCheckoutOperation(ctx, tx, "reserve", c.ActorScope, key); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if prior, found, e := findRedemptionReceipt(ctx, tx, "reserve", c.ActorScope, key, payload); e != nil || found {
		return prior, e
	}
	var existing int64
	e = tx.QueryRow(ctx, `SELECT id FROM coupon_order_redemptions WHERE order_reference=$1 FOR UPDATE`, c.OrderReference).Scan(&existing)
	if e == nil {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return couponport.ReservationSnapshot{}, e
	}
	row, e := selectReservationClaim(ctx, tx, c, c.ProductType+":"+fmt.Sprint(c.ProductID))
	if errors.Is(e, pgx.ErrNoRows) {
		if c.ClaimID == 0 {
			// Auto-selection is optional. A checkout without an eligible coupon is
			// a successful original-price checkout; no claim or redemption is
			// touched and Order persists this explicit no-coupon snapshot.
			return couponport.ReservationSnapshot{ProductID: c.ProductID, ProductType: c.ProductType, ProductCode: c.ProductCode, GrossAmountMinor: c.GrossAmountMinor, PayableAmountMinor: c.GrossAmountMinor, Currency: c.Currency}, nil
		}
		return couponport.ReservationSnapshot{}, couponapp.ErrNoEligibleCoupon
	}
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if row.item.DiscountMinor <= 0 || row.item.DiscountMinor >= c.GrossAmountMinor || row.item.Currency != c.Currency {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_customer_claims SET status='reserved',reserved_at=$2,updated_at=$2 WHERE id=$1 AND status IN ('available','claimed')`, row.item.ClaimID, at)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	payable := c.GrossAmountMinor - row.item.DiscountMinor
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO coupon_order_redemptions(claim_id,order_reference,product_id,product_type,product_code,rule_version,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,status,reserved_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'reserved',$11,$11,$11) RETURNING id`, row.item.ClaimID, c.OrderReference, c.ProductID, c.ProductType, c.ProductCode, row.rule.Version, c.GrossAmountMinor, row.item.DiscountMinor, payable, c.Currency, at).Scan(&id)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	s := couponport.ReservationSnapshot{CouponApplied: true, ReservationRef: redemptionRef(id), ClaimID: row.item.ClaimID, CouponID: couponport.ID(row.item.CouponID), ProductID: c.ProductID, ProductType: c.ProductType, ProductCode: c.ProductCode, RuleVersion: row.rule.Version, GrossAmountMinor: c.GrossAmountMinor, DiscountAmountMinor: row.item.DiscountMinor, PayableAmountMinor: payable, Currency: c.Currency}
	if e = recordRedemptionReceipt(ctx, tx, "reserve", c.ActorScope, key, payload, id, s, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = appendClaimFact(ctx, tx, row.item.ClaimID, id, "reserve", c.ActorScope, payload, key, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	return s, nil
}

func (r *Repository) ConsumeCoupon(ctx context.Context, c couponport.ConsumeCommand, key, payload [32]byte, at time.Time) (couponport.ReservationSnapshot, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = lockCheckoutOperation(ctx, tx, "consume", c.ActorScope, key); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if prior, found, e := findRedemptionReceipt(ctx, tx, "consume", c.ActorScope, key, payload); e != nil || found {
		return prior, e
	}
	row, e := readRedemption(ctx, tx, c.ReservationRef)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if row.orderReference != c.OrderReference || row.snapshot.PayableAmountMinor != c.SettledAmountMinor || row.snapshot.Currency != c.SettledCurrency {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	if row.status == "consumed" {
		if row.claimStatus != "redeemed" {
			return couponport.ReservationSnapshot{}, couponapp.ErrConflict
		}
		return row.snapshot, nil
	}
	if row.status != "reserved" || row.claimStatus != "reserved" {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_order_redemptions SET status='consumed',consumed_at=$2,updated_at=$2 WHERE id=$1 AND status='reserved'`, row.id, at)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	tag, e = tx.Exec(ctx, `UPDATE coupon_customer_claims SET status='redeemed',redeemed_at=$2,updated_at=$2 WHERE id=$1 AND status='reserved'`, row.claimID, at)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	if e = recordRedemptionReceipt(ctx, tx, "consume", c.ActorScope, key, payload, row.id, row.snapshot, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = appendClaimFact(ctx, tx, row.claimID, row.id, "consume", c.ActorScope, payload, key, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	return row.snapshot, nil
}

func (r *Repository) ReleaseCoupon(ctx context.Context, c couponport.ReleaseCommand, key, payload [32]byte, at time.Time) (couponport.ReservationSnapshot, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = lockCheckoutOperation(ctx, tx, "release", c.ActorScope, key); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if prior, found, e := findRedemptionReceipt(ctx, tx, "release", c.ActorScope, key, payload); e != nil || found {
		return prior, e
	}
	row, e := readRedemption(ctx, tx, c.ReservationRef)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if row.orderReference != c.OrderReference {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	if row.status == "released" {
		return row.snapshot, nil
	}
	if row.status != "reserved" || row.claimStatus != "reserved" {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	next := "available"
	if row.validUntil == nil || !at.Before(*row.validUntil) {
		next = "expired"
	}
	tag, e := tx.Exec(ctx, `UPDATE coupon_order_redemptions SET status='released',released_at=$2,release_reason=$3,updated_at=$2 WHERE id=$1 AND status='reserved'`, row.id, at, c.CloseReason)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	tag, e = tx.Exec(ctx, `UPDATE coupon_customer_claims SET status=$2,expired_at=CASE WHEN $2='expired' THEN $3 ELSE expired_at END,updated_at=$3 WHERE id=$1 AND status='reserved'`, row.claimID, next, at)
	if e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if tag.RowsAffected() != 1 {
		return couponport.ReservationSnapshot{}, couponapp.ErrConflict
	}
	if e = recordRedemptionReceipt(ctx, tx, "release", c.ActorScope, key, payload, row.id, row.snapshot, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	if e = appendClaimFact(ctx, tx, row.claimID, row.id, "release", c.ActorScope, payload, key, at); e != nil {
		return couponport.ReservationSnapshot{}, e
	}
	return row.snapshot, nil
}

func findClaimReceipt(ctx context.Context, tx pgx.Tx, scope string, key, payload [32]byte) (couponport.CustomerCoupon, bool, error) {
	var prior, raw []byte
	var item couponport.CustomerCoupon
	e := tx.QueryRow(ctx, `SELECT payload_digest,result_snapshot FROM coupon_claim_operation_receipts WHERE operation='claim' AND actor_scope=$1 AND key_digest=$2`, scope, key[:]).Scan(&prior, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return item, false, nil
	}
	if e != nil {
		return item, false, e
	}
	if len(prior) != 32 || string(prior) != string(payload[:]) || json.Unmarshal(raw, &item) != nil {
		return item, false, couponapp.ErrConflict
	}
	return item, true, nil
}
func findRedemptionReceipt(ctx context.Context, tx pgx.Tx, op, scope string, key, payload [32]byte) (couponport.ReservationSnapshot, bool, error) {
	var prior, raw []byte
	var item couponport.ReservationSnapshot
	e := tx.QueryRow(ctx, `SELECT payload_digest,result_snapshot FROM coupon_redemption_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, op, scope, key[:]).Scan(&prior, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return item, false, nil
	}
	if e != nil {
		return item, false, e
	}
	if len(prior) != 32 || string(prior) != string(payload[:]) || json.Unmarshal(raw, &item) != nil {
		return item, false, couponapp.ErrConflict
	}
	return item, true, nil
}
func selectReservationClaim(ctx context.Context, tx pgx.Tx, c couponport.ReserveCommand, target string) (checkoutClaim, error) {
	var x checkoutClaim
	var mode string
	q := `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at,rule.id,rule.name,rule.discount_amount_total,rule.currency,rule.status,rule.total_issue_limit,rule.per_user_issue_limit,rule.issued_count,rule.claim_starts_at,rule.claim_ends_at,rule.validity_mode,rule.use_starts_at,rule.use_ends_at,rule.relative_validity_days,rule.instructions,rule.created_by,rule.updated_by,rule.version,rule.created_at,rule.updated_at FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id JOIN coupon_rule_targets target ON target.coupon_id=rule.id WHERE claim.customer_id=$1 AND ($2=0 OR claim.id=$2) AND claim.status IN ('available','claimed') AND claim.valid_from<=$3 AND claim.valid_until>$3 AND rule.currency=$4 AND target.target_ref=$5 AND rule.status='published' AND rule.discount_amount_total>0 AND rule.discount_amount_total<$6 ORDER BY rule.discount_amount_total DESC,claim.valid_until ASC,claim.claimed_at ASC,claim.id ASC FOR UPDATE OF claim SKIP LOCKED LIMIT 1`
	e := tx.QueryRow(ctx, q, c.HolderCustomerID, c.ClaimID, c.ReservedAt, c.Currency, target, c.GrossAmountMinor).Scan(&x.item.ClaimID, &x.item.CouponID, &x.item.Name, &x.item.DiscountMinor, &x.item.Currency, &x.item.Status, &x.item.ClaimNoMasked, &x.item.ClaimedAt, &x.item.ValidFrom, &x.item.ValidUntil, &x.item.RedeemedAt, &x.rule.ID, &x.rule.Name, &x.rule.DiscountAmountTotal, &x.rule.Currency, &x.rule.Status, &x.rule.TotalIssueLimit, &x.rule.PerUserIssueLimit, &x.rule.IssuedCount, &x.rule.ClaimStartsAt, &x.rule.ClaimEndsAt, &mode, &x.rule.UseStartsAt, &x.rule.UseEndsAt, &x.rule.RelativeValidityDays, &x.rule.Instructions, &x.rule.CreatedBy, &x.rule.UpdatedBy, &x.rule.Version, &x.rule.CreatedAt, &x.rule.UpdatedAt)
	x.rule.ValidityMode = couponport.ValidityMode(mode)
	return x, e
}
func readRedemption(ctx context.Context, tx pgx.Tx, ref string) (checkoutRedemption, error) {
	id, e := parseRedemptionRef(ref)
	if e != nil {
		return checkoutRedemption{}, couponapp.ErrNotFound
	}
	var x checkoutRedemption
	e = tx.QueryRow(ctx, `SELECT redemption.id,redemption.claim_id,redemption.order_reference,redemption.status,redemption.product_id,redemption.product_type,redemption.product_code,claim.coupon_id,redemption.rule_version,redemption.gross_amount_minor,redemption.discount_amount_minor,redemption.payable_amount_minor,redemption.currency,claim.status,claim.valid_until FROM coupon_order_redemptions redemption JOIN coupon_customer_claims claim ON claim.id=redemption.claim_id WHERE redemption.id=$1 FOR UPDATE OF redemption,claim`, id).Scan(&x.id, &x.claimID, &x.orderReference, &x.status, &x.snapshot.ProductID, &x.snapshot.ProductType, &x.snapshot.ProductCode, &x.snapshot.CouponID, &x.snapshot.RuleVersion, &x.snapshot.GrossAmountMinor, &x.snapshot.DiscountAmountMinor, &x.snapshot.PayableAmountMinor, &x.snapshot.Currency, &x.claimStatus, &x.validUntil)
	if errors.Is(e, pgx.ErrNoRows) {
		return x, couponapp.ErrNotFound
	}
	if e != nil {
		return x, e
	}
	x.snapshot.ReservationRef = redemptionRef(x.id)
	x.snapshot.ClaimID = x.claimID
	x.snapshot.CouponApplied = true
	return x, nil
}
func recordRedemptionReceipt(ctx context.Context, tx pgx.Tx, op, scope string, key, payload [32]byte, id int64, s couponport.ReservationSnapshot, at time.Time) error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO coupon_redemption_operation_receipts(operation,actor_scope,key_digest,payload_digest,redemption_id,result_snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7)`, op, scope, key[:], payload[:], id, raw, at)
	return e
}
func lockCheckoutOperation(ctx context.Context, tx pgx.Tx, operation, scope string, key [32]byte) error {
	// The lock is held until the enclosing UoW commits or rolls back. It closes
	// the read-no-receipt / write-receipt race and lets the later caller replay
	// the committed immutable snapshot instead of observing a consumed quota.
	value := "coupon:" + operation + ":" + scope + ":" + hex.EncodeToString(key[:])
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, value)
	return err
}
func appendClaimFact(ctx context.Context, tx pgx.Tx, claimID, redemptionID int64, op, scope string, payload, key [32]byte, at time.Time) error {
	actor := sha256.Sum256([]byte(scope))
	if _, e := tx.Exec(ctx, `INSERT INTO coupon_claim_audit_events(claim_id,redemption_id,operation,actor_scope_digest,payload_digest,occurred_at) VALUES($1,NULLIF($2,0),$3,$4,$5,$6)`, claimID, redemptionID, op, actor[:], payload[:], at); e != nil {
		return e
	}
	event := map[string]string{"claim": "coupon.claimed.v1", "reserve": "coupon.reserved.v1", "consume": "coupon.consumed.v1", "release": "coupon.released.v1"}[op]
	raw, e := json.Marshal(map[string]any{"claim_id": claimID, "redemption_id": redemptionID, "operation": op})
	if e != nil {
		return e
	}
	d := sha256.Sum256(append(append([]byte(op+"\x00"+scope+"\x00"), key[:]...), payload[:]...))
	_, e = tx.Exec(ctx, `INSERT INTO coupon_claim_outbox(event_type,claim_id,redemption_id,payload,idempotency_digest,occurred_at) VALUES($1,$2,NULLIF($3,0),$4::jsonb,$5,$6)`, event, claimID, redemptionID, raw, d[:], at)
	return e
}
func couponClaimValidity(r couponport.Coupon, at time.Time) (time.Time, time.Time, error) {
	if r.ValidityMode == couponport.ValidityFixedRange {
		if r.UseStartsAt == nil || r.UseEndsAt == nil {
			return time.Time{}, time.Time{}, couponapp.ErrConflict
		}
		from := at
		if r.UseStartsAt.After(from) {
			from = *r.UseStartsAt
		}
		if !from.Before(*r.UseEndsAt) {
			return time.Time{}, time.Time{}, couponapp.ErrNoEligibleCoupon
		}
		return from.UTC(), r.UseEndsAt.UTC(), nil
	}
	if r.ValidityMode != couponport.ValidityRelativeDays || r.RelativeValidityDays == nil || *r.RelativeValidityDays < 1 {
		return time.Time{}, time.Time{}, couponapp.ErrConflict
	}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	local := at.In(loc)
	until := time.Date(local.Year(), local.Month(), local.Day()+int(*r.RelativeValidityDays), 0, 0, 0, 0, loc).UTC()
	if !at.Before(until) {
		return time.Time{}, time.Time{}, couponapp.ErrNoEligibleCoupon
	}
	return at.UTC(), until, nil
}
func redemptionRef(id int64) string { return fmt.Sprintf("cr_%d", id) }
func parseRedemptionRef(v string) (int64, error) {
	var id int64
	_, e := fmt.Sscanf(v, "cr_%d", &id)
	if e != nil || id < 1 || redemptionRef(id) != v {
		return 0, errors.New("invalid redemption reference")
	}
	return id, nil
}

var _ couponapp.CheckoutStore = (*Repository)(nil)
