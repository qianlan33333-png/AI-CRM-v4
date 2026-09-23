package target

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CommercePreflight intentionally has no mutation path. A matching source map
// permits reuse of its target ID, never creation of a second definition.
type CommercePreflight struct {
	ApplyReady  bool           `json:"apply_ready"`
	Counts      map[string]int `json:"counts"`
	Rows        []CommerceRow  `json:"rows"`
	Limitations []string       `json:"limitations"`
}
type CommerceRow struct {
	Kind     string `json:"kind"`
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id,omitempty"`
	State    string `json:"state"`
}

func InspectCommerceTarget(ctx context.Context, pool *pgxpool.Pool, snap source.Snapshot, actor int64) (CommercePreflight, error) {
	report := CommercePreflight{Counts: map[string]int{}, Rows: []CommerceRow{}, Limitations: []string{"apply_revalidates_owner_definition_and_claim_counters"}}
	if pool == nil || actor < 1 || snap.Manifest.Scope != "commerce-only" || snap.Validate() != nil {
		return report, ErrInvalid
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, errors.New("begin commerce preflight")
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1 AND is_active)`, actor).Scan(&active); err != nil || !active {
		return report, ErrInvalid
	}
	check := func(kind string, id int64, row any, code string) error {
		raw, e := json.Marshal(row)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(raw)
		r := CommerceRow{Kind: kind, SourceID: id}
		var prior []byte
		var table string
		e = tx.QueryRow(ctx, `SELECT target_id,source_digest,target_table FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind=$2 AND source_key=$3`, snap.Manifest.SourceSystem, kind, fmt.Sprint(id)).Scan(&r.TargetID, &prior, &table)
		if errors.Is(e, pgx.ErrNoRows) {
			r.State = "new_source"
			if code != "" {
				var occupied bool
				if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE product_code=$1)`, code).Scan(&occupied); e != nil {
					return errors.New("inspect product code")
				}
				if occupied {
					r.State = "conflict_unmapped_product_code"
				}
			}
		} else if e != nil {
			return errors.New("inspect commerce source mapping")
		} else if string(prior) != string(digest[:]) {
			r.State = "conflict_source_drift"
			if coupon, ok := row.(source.Coupon); ok {
				if _, err := existingCouponMapping(platformpostgres.BindTransaction(ctx, tx), snap.Manifest.SourceSystem, coupon, false); err == nil {
					r.State = "candidate_coupon_delta_owner_check_required"
				}
			}
		} else {
			r.State = "mapped_source_equal"
			var version int64
			switch table {
			case "products":
				e = tx.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, r.TargetID).Scan(&version)
			case "coupon_rules":
				e = tx.QueryRow(ctx, `SELECT version FROM coupon_rules WHERE id=$1`, r.TargetID).Scan(&version)
			default:
				r.State = "conflict_target_owner"
			}
			if errors.Is(e, pgx.ErrNoRows) {
				r.State = "conflict_target_missing"
			} else if e != nil {
				return errors.New("inspect commerce target version")
			} else if version > 1 {
				r.State = "mapped_source_equal_target_version_changed"
			}
		}
		report.Counts[r.State]++
		report.Rows = append(report.Rows, r)
		return nil
	}
	for _, r := range snap.Products {
		if err = check("wechat_pay_products", r.ID, r, r.ProductCode); err != nil {
			return report, err
		}
	}
	for _, r := range snap.ServicePeriods {
		if err = check("service_period_products", r.ID, r, ""); err != nil {
			return report, err
		}
	}
	for _, r := range snap.Coupons {
		// Old protected v1 source maps predate these fields. Compare their exact
		// original row representation, while retaining new facts in the snapshot.
		r.PublicSlug = nil
		r.IssuedCount = nil
		if err = check("commerce_coupons", r.ID, r, ""); err != nil {
			return report, err
		}
	}
	for _, r := range snap.CouponBindings {
		if err = check("commerce_coupon_product_bindings", r.ID, r, ""); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return report, errors.New("finish commerce preflight")
	}
	return report, nil
}
