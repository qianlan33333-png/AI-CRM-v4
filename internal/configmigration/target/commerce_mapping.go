package target

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func existingCommerceMapping(ctx context.Context, system, kind string, sourceID int64, row any, wantTable string) (int64, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var id int64
	var prior []byte
	var table string
	err = tx.QueryRow(ctx, `SELECT target_id,source_digest,target_table FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind=$2 AND source_key=$3 FOR UPDATE`, system, kind, fmt.Sprint(sourceID)).Scan(&id, &prior, &table)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	raw, err := json.Marshal(row)
	if err != nil {
		return 0, false, err
	}
	d := sha256.Sum256(raw)
	if table != wantTable || string(prior) != string(d[:]) {
		return 0, false, ErrDrift
	}
	var version int64
	switch table {
	case "products":
		err = tx.QueryRow(ctx, `SELECT version FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&version)
	case "coupon_rules":
		err = tx.QueryRow(ctx, `SELECT version FROM coupon_rules WHERE id=$1 FOR UPDATE`, id).Scan(&version)
	default:
		return 0, false, ErrDrift
	}
	if err != nil || version < 1 {
		return 0, false, ErrDrift
	}
	return id, true, nil
}

// Every historical commerce source mapping must remain represented. Removed
// source definitions/bindings require an explicit reconciliation decision.
func verifyCommerceCoverage(ctx context.Context, system string, keys map[string][]string, reviewedCouponID int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	for kind, ids := range keys {
		var missing bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM config_definition_import_source_maps old WHERE old.source_system=$1 AND old.source_kind=$2 AND NOT (old.source_key=ANY($3::text[])) AND NOT ($2='commerce_coupon_product_bindings' AND EXISTS(SELECT 1 FROM config_definition_import_source_maps coupon WHERE coupon.source_system=old.source_system AND coupon.source_kind='commerce_coupons' AND coupon.target_id=old.target_id AND (coupon.source_key=$4 OR EXISTS(SELECT 1 FROM config_definition_commerce_reviews review WHERE review.source_map_id=coupon.id AND review.basis='source_authoritative_review')))))`, system, kind, ids, fmt.Sprint(reviewedCouponID)).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrDrift
		}
	}
	return nil
}

// Coupon deltas may change only source updated_at and monotonically increase
// total_issue_limit. Reconstructing the prior row must reproduce its digest;
// matching a few current fields is never enough to authorize a source delta.
type couponMapping struct {
	ID, MapID          int64
	Found              bool
	Prior              []byte
	ExpectedIssued     *int64
	ExpectedLimit      int64
	AllowLimitIncrease bool
}

func existingCouponMapping(ctx context.Context, system string, row source.Coupon, lock bool) (couponMapping, error) {
	var result couponMapping
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return result, e
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var table string
	e = tx.QueryRow(ctx, `SELECT id,target_id,source_digest,target_table FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind='commerce_coupons' AND source_key=$2`+suffix, system, fmt.Sprint(row.ID)).Scan(&result.MapID, &result.ID, &result.Prior, &table)
	if errors.Is(e, pgx.ErrNoRows) {
		return result, nil
	}
	if e != nil {
		return result, e
	}
	if table != "coupon_rules" {
		return result, ErrDrift
	}
	result.Found = true
	var limit int64
	var updated time.Time
	var issued int64
	e = tx.QueryRow(ctx, `SELECT source_digest,total_issue_limit,source_updated_at,issued_count FROM config_definition_commerce_revisions WHERE source_map_id=$1 ORDER BY id DESC LIMIT 1`, result.MapID).Scan(&result.Prior, &limit, &updated, &issued)
	if e == nil {
		result.ExpectedIssued = &issued
	} else if errors.Is(e, pgx.ErrNoRows) {
		e = tx.QueryRow(ctx, `SELECT total_issue_limit,updated_at FROM coupon_rules WHERE id=$1`+suffix, result.ID).Scan(&limit, &updated)
	}
	if e != nil {
		return result, e
	}
	current, _ := json.Marshal(row)
	digest := sha256.Sum256(current)
	if string(result.Prior) == string(digest[:]) {
		result.ExpectedLimit = row.TotalIssueLimit
		return result, nil
	}
	if row.TotalIssueLimit < limit || row.UpdatedAt.Before(updated) {
		return result, ErrDrift
	}
	row.TotalIssueLimit = limit
	// PostgreSQL preserves instants, not the JSON timestamp offset from the
	// source snapshot. Try its current source offset and UTC, accepting only
	// an exact historical digest; this does not relax any field comparison.
	locations := []*time.Location{row.UpdatedAt.Location(), time.UTC}
	matched := false
	for _, location := range locations {
		row.UpdatedAt = updated.In(location)
		previous, _ := json.Marshal(row)
		priorDigest := sha256.Sum256(previous)
		if string(result.Prior) == string(priorDigest[:]) {
			matched = true
			break
		}
	}
	if !matched {
		return result, ErrDrift
	}
	result.ExpectedLimit = limit
	result.AllowLimitIncrease = true
	return result, nil
}
func recordCouponRevision(ctx context.Context, batch int64, system string, row source.Coupon, issued int64, prior []byte) error {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(row)
	d := sha256.Sum256(raw)
	if len(prior) == 0 {
		prior = d[:]
	}
	_, e = tx.Exec(ctx, `INSERT INTO config_definition_commerce_revisions(source_map_id,batch_id,prior_source_digest,source_digest,source_updated_at,total_issue_limit,issued_count) SELECT id,$1,$2,$3,$4,$5,$6 FROM config_definition_import_source_maps WHERE source_system=$7 AND source_kind='commerce_coupons' AND source_key=$8 ON CONFLICT(source_map_id,batch_id) DO NOTHING`, batch, prior, d[:], row.UpdatedAt.UTC(), row.TotalIssueLimit, issued, system, fmt.Sprint(row.ID))
	return e
}
