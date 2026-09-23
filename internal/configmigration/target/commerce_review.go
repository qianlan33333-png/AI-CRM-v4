package target

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r Runner) applyReviewedCoupon(ctx context.Context, system string, row source.Coupon, in couponport.CutoverDefinitionImport, batch int64, manifest [32]byte) (couponport.Coupon, couponMapping, error) {
	var cm couponMapping
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return couponport.Coupon{}, cm, e
	}
	e = tx.QueryRow(ctx, `SELECT id,target_id,source_digest FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind='commerce_coupons' AND source_key=$2 AND target_table='coupon_rules' FOR UPDATE`, system, fmt.Sprint(row.ID)).Scan(&cm.MapID, &cm.ID, &cm.Prior)
	if e != nil {
		return couponport.Coupon{}, cm, e
	}
	cm.Found = true
	owner, ok := r.Coupons.(couponport.ReviewedCutoverImporter)
	if !ok {
		return couponport.Coupon{}, cm, ErrInvalid
	}
	in.ExistingID = couponport.ID(cm.ID)
	full, _ := json.Marshal(row)
	fullDigest := sha256.Sum256(full)
	var before, after, md, sd []byte
	e = tx.QueryRow(ctx, `SELECT before_digest,after_digest,manifest_digest,full_source_digest FROM config_definition_commerce_reviews WHERE source_map_id=$1 AND batch_id=$2`, cm.MapID, batch).Scan(&before, &after, &md, &sd)
	if e == nil {
		current, err := owner.CutoverReviewDigest(ctx, in.ExistingID)
		if err != nil {
			return couponport.Coupon{}, cm, err
		}
		if string(before) != string(r.ReviewCouponBefore[:]) || string(after) != string(current[:]) || string(md) != string(manifest[:]) || string(sd) != string(fullDigest[:]) {
			return couponport.Coupon{}, cm, ErrDrift
		}
		return couponport.Coupon{ID: in.ExistingID}, cm, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return couponport.Coupon{}, cm, e
	}
	out, e := owner.ImportReviewedCutoverDefinition(ctx, in, r.ReviewCouponBefore)
	if e != nil {
		return out, cm, e
	}
	final, e := owner.CutoverReviewDigest(ctx, in.ExistingID)
	if e != nil {
		return out, cm, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO config_definition_commerce_reviews(source_map_id,batch_id,basis,manifest_digest,full_source_digest,before_digest,after_digest) VALUES($1,$2,'source_authoritative_review',$3,$4,$5,$6)`, cm.MapID, batch, manifest[:], fullDigest[:], r.ReviewCouponBefore[:], final[:])
	return out, cm, e
}
