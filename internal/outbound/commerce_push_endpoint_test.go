package outbound

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type endpointTargets map[string]CommercePushTarget

func (endpointTargets) CommercePushProviderEnabled() bool { return true }
func (m endpointTargets) CommercePushTarget(_ context.Context, r string) (CommercePushTarget, bool, error) {
	v, ok := m[r]
	return v, ok, nil
}
func TestCommerceEndpointURLSecurity(t *testing.T) {
	for _, s := range []string{"http://example.com", "https://localhost/x", "https://127.0.0.1", "https://10.0.0.1", "https://user:secret@example.com", "https://example.com/#x", "https://example.com:444"} {
		if editableCommerceEndpoint(s) {
			t.Fatalf("unsafe URL accepted")
		}
	}
	if !editableCommerceEndpoint("https://hooks.example.com/path?token=secret") {
		t.Fatal("valid endpoint rejected")
	}
}
func TestCommerceEndpointPostgreSQLIsolationRollbackAndPolicy(t *testing.T) {
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	schema := fmt.Sprintf("endpoint_test_%d", time.Now().UnixNano())
	if _, err = tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SET LOCAL search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile("../../migrations/0145_outbound_commerce_push_endpoints.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	runtime := endpointTargets{"shared": {Reference: "shared", Endpoint: "https://original.example.com", SigningKey: []byte("private-key"), Version: "1"}}
	s := NewCommercePushEndpoints(pool, runtime, []string{"shared"})
	c := platformpostgres.BindTransaction(ctx, tx)
	ref, err := s.SaveCommercePushEndpointWithin(c, "wechat_pay", 1, "", "https://one.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ref2, err := s.SaveCommercePushEndpointWithin(c, "wechat_pay", 2, "shared", "https://two.example.com")
	if err != nil || ref == ref2 {
		t.Fatal("owner references not isolated", err)
	}
	target, ok, err := s.CommercePushTarget(c, ref)
	if err != nil || !ok || target.Endpoint != "https://one.example.com" || target.Slot != ref || string(target.SigningKey) != "private-key" {
		t.Fatal("runtime policy not retained", err)
	}
	if runtime["shared"].Endpoint != "https://original.example.com" {
		t.Fatal("shared endpoint mutated")
	}
	if _, err = s.SaveCommercePushEndpointWithin(c, "wechat_pay", 2, ref, "https://evil.example.com"); err == nil {
		t.Fatal("cross owner reference accepted")
	}
	if value, err := s.ReadCommercePushEndpointWithin(c, "wechat_pay", 1, ""); err != nil || value != "https://one.example.com" {
		t.Fatal("disabled URL lost", err)
	}
	nested, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCommercePushEndpointWithin(platformpostgres.BindTransaction(ctx, nested), "wechat_pay", 1, ref, "https://changed.example.com"); err != nil {
		t.Fatal(err)
	}
	if err = nested.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.CommercePushTarget(c, ref)
	if err != nil || after.Endpoint != target.Endpoint {
		t.Fatal("rollback did not preserve target", err)
	}
	if _, err = s.SaveCommercePushEndpointWithin(c, "wechat_pay", 1, ref, "https://changed.example.com"); err != nil {
		t.Fatal(err)
	}
	changed, _, err := s.CommercePushTarget(c, ref)
	if err != nil || changed.policyDigest() == target.policyDigest() {
		t.Fatal("changed URL did not revoke frozen policy", err)
	}
	ambiguous := NewCommercePushEndpoints(pool, runtime, []string{"shared", "another"})
	if _, err = ambiguous.SaveCommercePushEndpointWithin(c, "wechat_pay", 3, "", "https://three.example.com"); err == nil {
		t.Fatal("ambiguous template default accepted")
	}
	delete(runtime, "shared")
	if _, ok, err = s.CommercePushTarget(c, ref); err != nil || ok {
		t.Fatal("removed runtime policy did not fail closed")
	}
	if _, err = s.SaveCommercePushEndpointWithin(c, "wechat_pay", 1, ref, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = s.CommercePushTarget(c, ref); err != nil || ok {
		t.Fatal("cleared endpoint still resolves")
	}
}

func TestCommerceEndpointEquivalentDefaultTemplate(t *testing.T) {
	base := CommercePushTarget{Reference: "z", Slot: "shared-slot", Endpoint: "https://z.example.com", SigningKey: []byte("key"), Version: "v1"}
	other := base
	other.Reference = "a"
	other.Slot = "other-original-slot"
	other.Endpoint = "https://a.example.com"
	targets := endpointTargets{"z": base, "a": other}
	manager := NewCommercePushEndpoints(nil, targets, []string{"z", "a"})
	if ref, err := manager.equivalentDefaultTemplate(context.Background()); err != nil || ref != "a" {
		t.Fatal("equivalent targets not stable", err)
	}
	for _, change := range []func(*CommercePushTarget){
		func(v *CommercePushTarget) { v.SigningKey = []byte("other-key") },
		func(v *CommercePushTarget) { v.AllowLoopbackHTTP = true },
		func(v *CommercePushTarget) { v.TenantID = "other" },
		func(v *CommercePushTarget) { v.BuyerPhone.Scope = "other" },
		func(v *CommercePushTarget) { v.CustomParams = map[string]any{"x": "y"} },
	} {
		changed := other
		change(&changed)
		targets["a"] = changed
		if _, err := manager.equivalentDefaultTemplate(context.Background()); err == nil {
			t.Fatal("unequal policy default accepted")
		}
	}
}
