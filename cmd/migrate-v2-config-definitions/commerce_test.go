package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	configtarget "github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/target"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestCommerceUnsupportedModesAndUnconfirmedApplyFailBeforeDatabaseAccess(t *testing.T) {
	if err := run(context.Background(), []string{"--commerce-only", "--mode=apply"}); err == nil || !strings.Contains(err.Error(), "requires --confirm-apply") {
		t.Fatalf("unconfirmed apply: %v", err)
	}
	for _, mode := range []string{"verify", "history-extract", "history-apply"} {
		if err := run(context.Background(), []string{"--commerce-only", "--mode=" + mode}); err == nil || !strings.Contains(err.Error(), "supports extract, inspect, dry-run and apply only") {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
}

func TestCommercePreflightReusesMappingsAndReportsDrift(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	legacy := configMigrationFixture(t, strings.Repeat("b", 40))
	d, err := legacy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = configMigrationRunner(t, pool).Apply(ctx, legacy, d, actor); err != nil {
		t.Fatal(err)
	}
	s := legacy
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	for i := range s.Coupons {
		slug := "public-coupon"
		issued := int64(i)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	refresh := func() {
		t.Helper()
		if err := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	report, err := configtarget.InspectCommerceTarget(ctx, pool.Native(), s, actor)
	if err != nil {
		t.Fatal(err)
	}
	if report.ApplyReady || report.Counts["mapped_source_equal"] != 63 {
		t.Fatalf("unsafe preflight %#v", report)
	}
	var before int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_definition_import_source_maps`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	s.Products[0].Name = "source changed"
	fresh := s.Products[1]
	fresh.ID = 100000
	fresh.ProductCode = "new-cutover-product"
	s.Products = append(s.Products, fresh)
	if _, err = pool.Native().Exec(ctx, `UPDATE products SET version=version+1 WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key=$1)`, "2"); err != nil {
		t.Fatal(err)
	}
	refresh()
	report, err = configtarget.InspectCommerceTarget(ctx, pool.Native(), s, actor)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["conflict_source_drift"] < 1 || report.Counts["new_source"] != 1 || report.Counts["mapped_source_equal_target_version_changed"] < 1 {
		t.Fatalf("missing conflicts %#v", report)
	}
	var after int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_definition_import_source_maps`).Scan(&after); err != nil || after != before {
		t.Fatalf("preflight mutated mappings %d %d %v", before, after, err)
	}
}

func TestCommerceSafeApplyPreservesFactsAndReplays(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	legacy := configMigrationFixture(t, strings.Repeat("c", 40))
	runner := configMigrationRunner(t, pool)
	d, _ := legacy.CanonicalDigest()
	if _, err := runner.Apply(ctx, legacy, d, actor); err != nil {
		t.Fatal(err)
	}
	s := legacy
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	for i := range s.Coupons {
		slug := fmt.Sprintf("cp-cutover-%d_Legacy", i)
		issued := int64(5)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	p := s.Products[0]
	p.ID = 100001
	p.ProductCode = "cutover-new-product"
	s.Products = append(s.Products, p)
	c := s.Coupons[0]
	c.ID = 100001
	slug := "cp-cutover-new"
	c.PublicSlug = &slug
	s.Coupons = append(s.Coupons, c)
	b := s.CouponBindings[0]
	b.ID = 100001
	b.CouponID = c.ID
	b.TradeProductID = p.ID
	s.CouponBindings = append(s.CouponBindings, b)
	apply := func() (configtarget.Result, error) {
		t.Helper()
		if err := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); err != nil {
			t.Fatal(err)
		}
		d, _ := s.CanonicalDigest()
		return runner.Apply(ctx, s, d, actor)
	}
	out, err := apply()
	if err != nil {
		t.Fatal(err)
	}
	if out.Products != 1 || out.Coupons != 1 || out.GroupOps != 0 || out.Automation != 0 {
		t.Fatalf("unexpected mutations %+v", out)
	}
	var rules int
	var minimum, maximum int64
	if err = pool.Native().QueryRow(ctx, `SELECT count(*),min(issued_count),max(issued_count) FROM coupon_rules`).Scan(&rules, &minimum, &maximum); err != nil {
		t.Fatal(err)
	}
	if rules != 16 || minimum != 5 || maximum != 5 {
		t.Fatalf("coupon facts %d %d %d", rules, minimum, maximum)
	}
	var slugs int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM coupon_rules WHERE public_slug LIKE 'cp-cutover-%'`).Scan(&slugs); err != nil || slugs != 16 {
		t.Fatalf("slug count=%d err=%v", slugs, err)
	}
	again, err := apply()
	if err != nil || !again.NoOp || again.Products != 0 || again.Coupons != 0 {
		t.Fatalf("replay %+v %v", again, err)
	}
	// Claims imported independently must not exceed the preserved source total.
	var customerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claimed_at,source_digest,created_at,updated_at) SELECT 'cutover-test',n::text,$1,(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1'),'claimed',now(),decode(repeat('00',32),'hex'),now(),now() FROM generate_series(1,6) n`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = apply(); err == nil {
		t.Fatal("claimed count larger than source issuance accepted")
	}
	if _, err = pool.Native().Exec(ctx, `DELETE FROM coupon_customer_claims WHERE source_system='cutover-test'`); err != nil {
		t.Fatal(err)
	}
	// Changed issuance cannot erase target activity, even with equal old digests.
	changed := int64(6)
	s.Coupons[0].IssuedCount = &changed
	if _, err = apply(); err != nil {
		t.Fatal("audited monotone source counter delta rejected", err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE coupon_rules SET issued_count=7 WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1')`); err != nil {
		t.Fatal(err)
	}
	if _, err = apply(); err == nil {
		t.Fatal("target native claim drift overwritten")
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE coupon_rules SET issued_count=6 WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1')`); err != nil {
		t.Fatal(err)
	}
	// An earlier new row must roll back if a later definition conflicts.
	p.ID++
	p.ProductCode = "must-rollback-product"
	s.Products = append(s.Products, p)
	s.Coupons[0].Name = "source-definition-drift"
	if _, err = apply(); err == nil {
		t.Fatal("source drift accepted")
	}
	var count int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM products WHERE product_code='must-rollback-product'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback count=%d err=%v", count, err)
	}
}

func TestCommerceAuditedCouponDeltaPreservesProductOperatorConfig(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	s := configMigrationFixture(t, strings.Repeat("d", 40))
	s.Coupons[0].TotalIssueLimit = 20
	s.Coupons[0].UpdatedAt = s.Coupons[0].UpdatedAt.In(time.FixedZone("source", 8*3600))
	if e := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); e != nil {
		t.Fatal(e)
	}
	oldDigest, _ := s.CanonicalDigest()
	runner := configMigrationRunner(t, pool)
	if _, e := runner.Apply(ctx, s, oldDigest, actor); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Native().Exec(ctx, `UPDATE products SET version=4,images='["https://example.test/retained.png"]'::jsonb,legacy_admin_projection=legacy_admin_projection||'{"operator_extra":"retain"}'::jsonb WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key='2')`); e != nil {
		t.Fatal(e)
	}
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	for i := range s.Coupons {
		slug := fmt.Sprintf("cp-delta-%d", i)
		issued := int64(0)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	count := int64(27)
	s.Coupons[0].IssuedCount = &count
	s.Coupons[0].TotalIssueLimit = 10000
	s.Coupons[0].UpdatedAt = s.Coupons[0].UpdatedAt.Add(time.Hour)
	if e := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); e != nil {
		t.Fatal(e)
	}
	d, _ := s.CanonicalDigest()
	report, e := configtarget.InspectCommerceTarget(ctx, pool.Native(), s, actor)
	if e != nil {
		t.Fatal(e)
	}
	if report.Counts["candidate_coupon_delta_owner_check_required"] != 1 || report.Counts["mapped_source_equal_target_version_changed"] != 1 {
		t.Fatalf("preflight %+v", report.Counts)
	}
	for i := 0; i < 2; i++ {
		if _, e = runner.Apply(ctx, s, d, actor); e != nil {
			t.Fatal(e)
		}
	}
	var limit, issued, version int64
	var extra, images string
	if e = pool.Native().QueryRow(ctx, `SELECT total_issue_limit,issued_count FROM coupon_rules WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1')`).Scan(&limit, &issued); e != nil || limit != 10000 || issued != 27 {
		t.Fatalf("coupon facts %d/%d %v", limit, issued, e)
	}
	if e = pool.Native().QueryRow(ctx, `SELECT version,legacy_admin_projection->>'operator_extra',images::text FROM products WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key='2')`).Scan(&version, &extra, &images); e != nil || version != 4 || extra != "retain" || !strings.Contains(images, "retained.png") {
		t.Fatalf("operator config changed %d %s %s %v", version, extra, images, e)
	}
	var revisions int
	if e = pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_definition_commerce_revisions`).Scan(&revisions); e != nil || revisions != 15 {
		t.Fatalf("revision count %d %v", revisions, e)
	}
	// Historical claims copy already-counted issuance; importing and replaying
	// all 27 claims must not allocate another 27 coupons.
	var couponID int64
	if e = pool.Native().QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1'`).Scan(&couponID); e != nil {
		t.Fatal(e)
	}
	importer := runner.Coupons.(couponport.HistoricalCustomerCouponImporter)
	from := s.Coupons[0].ClaimStartsAt
	until := from.Add(7 * 24 * time.Hour)
	claims := make([]couponport.HistoricalCustomerCoupon, 27)
	for i := range claims {
		var customerID int64
		if e = pool.Native().QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); e != nil {
			t.Fatal(e)
		}
		claims[i] = couponport.HistoricalCustomerCoupon{SourceSystem: "cutover-claim-test", SourceKey: fmt.Sprint(i), CustomerID: customerID, CouponID: couponID, Status: "claimed", ClaimedAt: from, ValidFrom: &from, ValidUntil: &until, SourceDigest: [32]byte{byte(i + 1)}, CreatedAt: from, UpdatedAt: from}
	}
	for pass := 0; pass < 2; pass++ {
		e = runner.UOW.Within(ctx, func(bound context.Context) error {
			for _, claim := range claims {
				item, added, err := importer.ImportHistoricalCustomerCoupon(bound, claim)
				if err != nil {
					return err
				}
				if added != (pass == 0) || item.DiscountMinor != s.Coupons[0].DiscountAmountTotal || item.Currency != "CNY" || item.ValidFrom == nil || !item.ValidFrom.Equal(from) || item.ValidUntil == nil || !item.ValidUntil.Equal(until) {
					return fmt.Errorf("historical coupon readback mismatch")
				}
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = runner.Apply(ctx, s, d, actor); e != nil {
		t.Fatal("post-claim definition reconciliation", e)
	}
	var imported int64
	if e = pool.Native().QueryRow(ctx, `SELECT issued_count,(SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1) FROM coupon_rules WHERE id=$1`, couponID).Scan(&issued, &imported); e != nil || issued != 27 || imported != 27 {
		t.Fatalf("double counted issuance issued=%d imported=%d error=%v", issued, imported, e)
	}
	// A target operator price change is not presentation-only and must conflict.
	if _, e = pool.Native().Exec(ctx, `UPDATE products SET price_minor=price_minor+1,version=version+1 WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key='2')`); e != nil {
		t.Fatal(e)
	}
	if _, e = runner.Apply(ctx, s, d, actor); e == nil {
		t.Fatal("target commercial fact drift accepted")
	}
}

func TestCommerceExplicitReviewedCouponCASAndReplay(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	s := configMigrationFixture(t, strings.Repeat("e", 40))
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	for i := range s.Coupons {
		slug := ""
		issued := int64(0)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	binding := s.CouponBindings[0]
	// Keep the old binding mapping, then replace its source key below.
	s.Coupons[0].TotalIssueLimit = 20
	if e := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); e != nil {
		t.Fatal(e)
	}
	d, _ := s.CanonicalDigest()
	runner := configMigrationRunner(t, pool)
	if _, e := runner.Apply(ctx, s, d, actor); e != nil {
		t.Fatal(e)
	}
	var id, productID int64
	if e := pool.Native().QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='commerce_coupons' AND source_key='1'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if e := pool.Native().QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key=$1`, fmt.Sprint(binding.TradeProductID)).Scan(&productID); e != nil {
		t.Fatal(e)
	}

	if _, e := pool.Native().Exec(ctx, `UPDATE coupon_rules SET public_slug='target-test-link',version=2,updated_at=updated_at+interval '2 days' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	binding.ID = 100001
	s.CouponBindings[0] = binding
	for i := range s.Coupons {
		slug := fmt.Sprintf("source-public-%d", i)
		issued := int64(0)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	issued := int64(27)
	s.Coupons[0].IssuedCount = &issued
	s.Coupons[0].TotalIssueLimit = 10000
	s.Coupons[0].UpdatedAt = s.Coupons[0].UpdatedAt.Add(time.Hour)
	if e := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); e != nil {
		t.Fatal(e)
	}
	d, _ = s.CanonicalDigest()
	if _, e := runner.Apply(ctx, s, d, actor); e == nil {
		t.Fatal("unreviewed source drift accepted")
	}
	owner := runner.Coupons.(couponport.ReviewedCutoverImporter)
	var before [32]byte
	if e := runner.UOW.Within(ctx, func(bound context.Context) error {
		var e error
		before, e = owner.CutoverReviewDigest(bound, couponport.ID(id))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	runner.ReviewCouponSourceID = 1
	runner.ReviewCouponBefore = before
	// Even inside the accepted transaction the audit cannot authorize a
	// second slug replacement: old slug/version no longer match.
	second := runner
	second.UOW = cutoverAfterUOW{inner: runner.UOW, after: func(bound context.Context) error {
		tx, e := platformpostgres.RequireTransaction(bound)
		if e != nil {
			return e
		}
		_, e = tx.Exec(bound, `UPDATE coupon_rules SET public_slug='second-same-transaction',version=version+1 WHERE id=$1`, id)
		return e
	}}
	if _, e := second.Apply(ctx, s, d, actor); e == nil {
		t.Fatal("same transaction audit reused")
	}
	bad := runner
	bad.ReviewCouponBefore[0] ^= 1
	if _, e := bad.Apply(ctx, s, d, actor); e == nil {
		t.Fatal("wrong before accepted")
	}
	for i := 0; i < 2; i++ {
		if _, e := runner.Apply(ctx, s, d, actor); e != nil {
			t.Fatal(e)
		}
	}
	var total, count, version, reviews, bindings int64
	var slug string
	if e := pool.Native().QueryRow(ctx, `SELECT total_issue_limit,issued_count,version,public_slug,(SELECT count(*) FROM config_definition_commerce_reviews),(SELECT count(*) FROM config_definition_import_source_maps WHERE source_kind='commerce_coupon_product_bindings' AND source_key=$2) FROM coupon_rules WHERE id=$1`, id, fmt.Sprint(binding.ID)).Scan(&total, &count, &version, &slug, &reviews, &bindings); e != nil || total != 10000 || count != 27 || version != 3 || slug != "source-public-0" || reviews != 1 || bindings != 1 {
		t.Fatalf("review result %d %d %d %s %d %d %v", total, count, version, slug, reviews, bindings, e)
	}
	// A prior review never authorizes another transaction, another coupon,
	// or an ordinary update path to replace a published link.
	for _, target := range []int64{id, id + 1} {
		if _, e := pool.Native().Exec(ctx, `UPDATE coupon_rules SET public_slug='unauthorized-replacement',version=version+1 WHERE id=$1`, target); e == nil {
			t.Fatal("old review reused for ordinary/cross-coupon update")
		}
	}
	if _, e := pool.Native().Exec(ctx, `UPDATE coupon_rules SET version=version+1 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := runner.Apply(ctx, s, d, actor); e == nil {
		t.Fatal("post-review target edit overwritten")
	}
}

type cutoverAfterUOW struct {
	inner platformport.UnitOfWork
	after func(context.Context) error
}

func (u cutoverAfterUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return u.inner.Within(ctx, func(bound context.Context) error {
		if e := fn(bound); e != nil {
			return e
		}
		return u.after(bound)
	})
}
