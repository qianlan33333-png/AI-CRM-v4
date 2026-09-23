package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func orderReconciliationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping commerce reconciliation PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_commerce_reconcile_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := callerFile()
	if !ok {
		t.Fatal("locate commerce reconciliation test")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0020_order.sql", "0024_order_product_version.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}

func callerFile() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	return file, ok
}

func TestPostgreSQLOrderOnlyReconciliationRejectsPerRowOrderDrift(t *testing.T) {
	pool, cleanup := orderReconciliationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 2, 3, 4, 0, time.UTC)
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		RunKey:        "order-only-reconcile-001",
		Coverage:      Coverage{WeChatPayOrders: true},
		Orders: []OrderRow{{
			Provider: orderdomain.ProviderWeChatPay, SourceKey: "floating-order-1", MerchantOrderNo: "floating-merchant-1", ProviderTransactionNo: "floating-transaction-1", AmountMinor: 100, Currency: "CNY", Status: string(orderdomain.StatusPaid),
			Items: []ItemRow{{LineNo: 1, ProductCode: "legacy", ProductName: "历史订单", UnitAmountMinor: 100, Quantity: 1, LineAmountMinor: 100}}, CreatedAt: now, UpdatedAt: now,
		}},
	}
	if err := ValidateOrderOnly(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Digest = sha256.Sum256([]byte("approved-order-only-manifest"))
	order := manifest.Orders[0]
	digest := HistoricalOrderDigest(order)
	schemaDigest := sha256.Sum256([]byte(SchemaVersion))
	var runID, orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status,input_count,imported_count,replayed_count) VALUES($1,$2,$3,'applied',1,1,0) RETURNING id`, manifest.RunKey, manifest.Digest[:], schemaDigest[:]).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at) VALUES('wechat_pay','commerce-history',$1,$2,$3,100,0,'CNY','paid','history',false,$4,1,$5,$5) RETURNING id`, order.SourceKey, order.MerchantOrderNo, order.ProviderTransactionNo, digest[:], now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,NULL,NULL,'legacy','历史订单',100,1,100)`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,NULL,'paid',0,1,$2,$3)`, orderID, "migration:"+manifest.RunKey, now); err != nil {
		t.Fatal(err)
	}
	orderPayload := `{"order_id":` + strconv.FormatInt(orderID, 10) + `,"record_origin":"history","status":"paid","version":1}`
	if _, err := pool.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES('order.history_imported',$1,$2,$3::jsonb,$4)`, orderID, "migration:"+manifest.RunKey, orderPayload, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.history_imported',$1,$2,$3::jsonb,$4)`, "order.history_imported:"+strconv.FormatInt(orderID, 10)+":1", orderID, orderPayload, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,'commerce-history',$2,$3,'imported',$4)`, runID, order.SourceKey, digest[:], orderID); err != nil {
		t.Fatal(err)
	}
	store := PostgreSQLRuns{Pool: pool}
	matched, err := store.VerifyOrderOnly(ctx, manifest)
	if err != nil || !matched.Matched || matched.Orders != 1 || matched.Floating != 1 || matched.AmountMinor != 100 || matched.EffectEligible != 0 {
		t.Fatalf("matched=%+v err=%v", matched, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET provider_transaction_no='drifted-transaction' WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.VerifyOrderOnly(ctx, manifest); !errors.Is(err, ErrReconciliationMismatch) {
		t.Fatalf("per-row floating order drift err=%v", err)
	}
}
