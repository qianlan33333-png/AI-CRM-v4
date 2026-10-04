package app

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The upgrade must accept preexisting poster receipts and retain immutable
// evidence while adding context issuance to the current owner contract.
func TestPostgreSQLProductActivityContextMigrationPreservesReceipts(t *testing.T) {
	pool, cleanup := referralPostgreSQLPoolWithContextReceipts(t, false)
	defer cleanup()
	ctx := context.Background()
	digest := sha256.Sum256([]byte("legacy-poster-receipt"))
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO referral_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('campaign_posters','admin:9001',$1,$1,'campaign',7,$2) RETURNING id`, digest[:], time.Now().UTC()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM referral_operation_receipts r WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate receipt migration")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0222_referral_product_activity_context_receipt.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("upgrade with historical receipt: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM referral_operation_receipts r WHERE id=$1`, id).Scan(&after); err != nil || before != after {
		t.Fatalf("historical receipt changed err=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE referral_operation_receipts SET result_id=result_id WHERE id=$1`, id); err == nil {
		t.Fatal("upgrade removed append-only protection")
	}
	newDigest := sha256.Sum256([]byte("context-receipt"))
	if _, err = pool.Exec(ctx, `INSERT INTO referral_operation_receipts(operation,actor_scope,key_digest,payload_digest,result_kind,result_id,created_at) VALUES('issue_product_activity_context','customer:101',$1,$1,'campaign',7,$2)`, newDigest[:], time.Now().UTC()); err != nil {
		t.Fatalf("context receipt not allowed after upgrade: %v", err)
	}
}
