package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"testing"
)

type commerceReaderDB struct {
	t  *testing.T
	tx *commerceReaderTx
}

func (d commerceReaderDB) BeginTx(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	if o.AccessMode != pgx.ReadOnly || o.IsoLevel != pgx.RepeatableRead {
		d.t.Fatal("unsafe source transaction")
	}
	return d.tx, nil
}

type commerceReaderTx struct {
	pgx.Tx
	t         *testing.T
	s         Snapshot
	queries   int
	committed bool
}
type commerceReaderRow struct{ value any }

func (r commerceReaderRow) Scan(dest ...any) error {
	switch d := dest[0].(type) {
	case *[]byte:
		*d = r.value.([]byte)
	default:
		raw, _ := json.Marshal(r.value)
		return json.Unmarshal(raw, d)
	}
	return nil
}
func (t *commerceReaderTx) QueryRow(_ context.Context, q string, _ ...any) pgx.Row {
	if q == "SELECT transaction_timestamp()" {
		return commerceReaderRow{t.s.Manifest.SnapshotAt}
	}
	t.queries++
	var rows any
	switch {
	case strings.Contains(q, "public.wechat_pay_products"):
		rows = t.s.Products
	case strings.Contains(q, "public.service_period_products"):
		rows = t.s.ServicePeriods
	case strings.Contains(q, "public.commerce_coupon_product_bindings"):
		rows = t.s.CouponBindings
	case strings.Contains(q, "public.commerce_coupons"):
		if !strings.Contains(q, "public_slug") || !strings.Contains(q, "issued_count") {
			t.t.Fatal("lost coupon facts in extraction")
		}
		rows = t.s.Coupons
	default:
		t.t.Fatal("queried excluded source table")
	}
	raw, _ := json.Marshal(rows)
	return commerceReaderRow{raw}
}
func (t *commerceReaderTx) Exec(_ context.Context, q string, _ ...any) (pgconn.CommandTag, error) {
	if q != "SET LOCAL statement_timeout='15s'" {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected command")
	}
	return pgconn.NewCommandTag("SET"), nil
}
func (t *commerceReaderTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *commerceReaderTx) Rollback(context.Context) error { return nil }
func TestCommerceExtractReadsOnlyFourTables(t *testing.T) {
	tx := &commerceReaderTx{t: t, s: commerceFixture(t)}
	got, err := ExtractCommerceFrom(context.Background(), commerceReaderDB{t, tx}, strings.Repeat("a", 40))
	if err != nil || !tx.committed || tx.queries != 4 || got.Manifest.Scope != "commerce-only" {
		t.Fatalf("extract %v queries=%d", err, tx.queries)
	}
}

func commerceFixture(t *testing.T) Snapshot {
	s := testSnapshot(t)
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	slug := "retained-coupon-slug"
	issued := int64(7)
	for i := range s.Coupons {
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	if err := PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestCommerceScopeRoundtripPreservesIssuanceAndSlug(t *testing.T) {
	s := commerceFixture(t)
	key := bytes.Repeat([]byte{1}, 32)
	sealed, d, err := Seal(s, key)
	if err != nil {
		t.Fatal(err)
	}
	got, gd, err := Load(sealed, key)
	if err != nil || d != gd {
		t.Fatalf("roundtrip %v", err)
	}
	if got.Manifest.Scope != "commerce-only" || *got.Coupons[0].PublicSlug != "retained-coupon-slug" || *got.Coupons[0].IssuedCount != 7 {
		t.Fatal("lost cutover facts")
	}
	if ValidateExpectedBaseline(got) == nil {
		t.Fatal("small commerce snapshot passed legacy baseline")
	}
}
func TestCommerceScopeRejectsExcludedRowsAndMissingFacts(t *testing.T) {
	s := commerceFixture(t)
	s.GroupPlans = testSnapshot(t).GroupPlans
	if PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt) == nil {
		t.Fatal("group row accepted")
	}
	s = commerceFixture(t)
	s.Coupons[0].IssuedCount = nil
	if PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt) == nil {
		t.Fatal("missing issued_count accepted")
	}
	s = commerceFixture(t)
	s.Manifest.Scope = ""
	if PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt) == nil {
		t.Fatal("commerce facts accepted as legacy snapshot")
	}
}
