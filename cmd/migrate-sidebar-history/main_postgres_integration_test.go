package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/cutoverproof"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLSidebarHistoryAllianceApplyReplayReconcile drives the actual
// migration command over PostgreSQL. The fixture preserves three donor states:
// no admin_alliance fact, an explicit donor clear, and a non-empty fact. The
// command must not invent a value for the first state, replay must not insert
// a second entitlement, and reconcile must reject a target-side alliance
// alteration even when the immutable source digest remains unchanged.
func TestPostgreSQLSidebarHistoryAllianceApplyReplayReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	manifestPath, digest := sidebarHistoryAllianceManifest(t)
	seedSidebarHistoryIdentitiesAndDefinition(t, ctx, pool)
	applyArgs := []string{"--mode=apply", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest, "--confirm-apply"}

	// The Order-owned entitlement write, its historical receipt and source-map
	// follow-up must not leave a target row when its UoW fails. The existing
	// command performs no direct Order-table write itself.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION sidebar_history_fail_entitlement_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'sidebar history entitlement failpoint'; END;
		$$;
		CREATE TRIGGER sidebar_history_fail_entitlement_insert
		BEFORE INSERT ON order_service_entitlements
		FOR EACH ROW EXECUTE FUNCTION sidebar_history_fail_entitlement_insert();`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil {
		t.Fatal("entitlement failpoint allowed sidebar history apply")
	}
	var failedEntitlements, failedMaps int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM sidebar_history_migration_source_map)`).Scan(&failedEntitlements, &failedMaps); err != nil {
		t.Fatal(err)
	}
	if failedEntitlements != 0 || failedMaps != 0 {
		t.Fatalf("failed apply leaked Order target rows entitlements=%d maps=%d", failedEntitlements, failedMaps)
	}
	var interruptedStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001'`).Scan(&interruptedStatus); err != nil || interruptedStatus != "applying" {
		t.Fatalf("interrupted apply status=%q err=%v", interruptedStatus, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER sidebar_history_fail_entitlement_insert ON order_service_entitlements; DROP FUNCTION sidebar_history_fail_entitlement_insert()`); err != nil {
		t.Fatal(err)
	}

	// The failed command's session lease was released on close, so the exact
	// frozen snapshot may recover the applying batch. While another command
	// holds the real PostgreSQL lease, a parallel re-entry must stop before it
	// can race the receipts or overwrite this batch's counters.
	m, err := load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := acquireSidebarHistoryApplyLease(ctx, pool, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil || !strings.Contains(err.Error(), "already in progress") {
		lease.Release()
		t.Fatalf("parallel sidebar history apply error=%v", err)
	}
	lease.Release()

	// An interruption can happen after the historical owner has committed some
	// rows and their source receipts, rather than only before the first write.
	// Make the second entitlement write fail: the first source must survive,
	// and recovery must replay it before importing the remaining sources.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION sidebar_history_fail_second_entitlement_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF (SELECT count(*) FROM order_service_entitlements WHERE source_system='ai-crm-production:150.158.82.186/openclaw_wecom') >= 1 THEN
				RAISE EXCEPTION 'sidebar history second entitlement failpoint';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER sidebar_history_fail_second_entitlement_insert
		BEFORE INSERT ON order_service_entitlements
		FOR EACH ROW EXECUTE FUNCTION sidebar_history_fail_second_entitlement_insert();`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil {
		t.Fatal("second entitlement failpoint allowed sidebar history apply")
	}
	var partialEntitlements, partialMaps, partialQuarantines, partialImported, partialReplayed, partialBatchQuarantines int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
			(SELECT count(*) FROM sidebar_history_migration_source_map WHERE source_kind='service_period_entitlement'),
			(SELECT count(*) FROM sidebar_history_migration_quarantine WHERE source_kind='service_period_entitlement'),
			imported_count,replayed_count,quarantined_count
		FROM sidebar_history_migration_batches
		WHERE run_key='sidebar-alliance-pg-001'`, productionSourceSystem).Scan(&partialEntitlements, &partialMaps, &partialQuarantines, &partialImported, &partialReplayed, &partialBatchQuarantines); err != nil {
		t.Fatal(err)
	}
	if partialEntitlements != 1 || partialMaps != 1 || partialQuarantines != 0 || partialImported != 0 || partialReplayed != 0 || partialBatchQuarantines != 0 {
		t.Fatalf("partial apply conservation entitlements=%d maps=%d quarantines=%d batch=%d/%d/%d", partialEntitlements, partialMaps, partialQuarantines, partialImported, partialReplayed, partialBatchQuarantines)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER sidebar_history_fail_second_entitlement_insert ON order_service_entitlements; DROP FUNCTION sidebar_history_fail_second_entitlement_insert()`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("recover partially interrupted sidebar history apply: %v", err)
	}
	sidebarHistoryAssertAllianceTargets(t, ctx, pool)
	if err := pool.QueryRow(ctx, `SELECT imported_count,replayed_count,quarantined_count FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001'`).Scan(&partialImported, &partialReplayed, &partialBatchQuarantines); err != nil {
		t.Fatal(err)
	}
	if partialImported != 2 || partialReplayed != 1 || partialBatchQuarantines != 1 {
		t.Fatalf("recovered partial apply outcome imported=%d replayed=%d quarantined=%d", partialImported, partialReplayed, partialBatchQuarantines)
	}

	// Re-running the exact protected file exercises the production replay path.
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("real sidebar history replay: %v", err)
	}
	sidebarHistoryAssertAllianceTargets(t, ctx, pool)

	var sourceDigest []byte
	if err := pool.QueryRow(ctx, `
		SELECT source_digest FROM sidebar_history_migration_source_map
		WHERE source_kind='service_period_entitlement' AND source_key='9'`).Scan(&sourceDigest); err != nil {
		t.Fatal(err)
	}
	if len(sourceDigest) != 32 {
		t.Fatalf("source digest length=%d", len(sourceDigest))
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET alliance='目标漂移' WHERE source_system=$1 AND source_key='9'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	reconcileArgs := []string{"--mode=reconcile", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest}
	if err := run(ctx, reconcileArgs); err == nil {
		t.Fatal("reconcile accepted alliance target drift")
	}
	var persistedDigest []byte
	var runStatus string
	if err := pool.QueryRow(ctx, `
		SELECT source_digest,(SELECT status FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001')
		FROM sidebar_history_migration_source_map
		WHERE source_kind='service_period_entitlement' AND source_key='9'`).Scan(&persistedDigest, &runStatus); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceDigest, persistedDigest) || runStatus != "applied" {
		t.Fatalf("failed reconcile changed immutable source or status digest=%x status=%q", persistedDigest, runStatus)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET alliance='联盟甲' WHERE source_system=$1 AND source_key='9'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("real sidebar history reconcile: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001'`).Scan(&runStatus); err != nil || runStatus != "reconciled" {
		t.Fatalf("reconcile status=%q err=%v", runStatus, err)
	}
}

// TestPostgreSQLSidebarHistoryCaptureAllianceExpressionPreservesSourceFacts
// executes the production capture expression against PostgreSQL. The source
// stream must preserve key absence, JSON null, and the untrimmed Unicode text;
// only the v2 Go parser later applies the donor's Python whitespace rule.
func TestPostgreSQLSidebarHistoryCaptureAllianceExpressionPreservesSourceFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate capture script")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	script, err := os.ReadFile(filepath.Join(root, "scripts", "capture-sidebar-history-source.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, []byte("encode(convert_to((jsonb_build_object(")) || !bytes.Contains(script, []byte("jsonb_build_object('alliance', e.metadata_json->'admin_alliance')")) {
		t.Fatal("capture script no longer contains the tested Alliance JSON expression")
	}

	rows, err := pool.Query(ctx, `
		WITH source(source_id,metadata_json) AS (
			VALUES
				(1,'{}'::jsonb),
				(2,'{"admin_alliance":null}'::jsonb),
				(3,jsonb_build_object('admin_alliance', chr(160) || '联盟' || chr(28)))
		)
		SELECT encode(convert_to((jsonb_build_object('source_id',source_id) || CASE
			WHEN metadata_json ? 'admin_alliance'
				THEN jsonb_build_object('alliance', metadata_json->'admin_alliance')
			ELSE '{}'::jsonb
		END)::text,'UTF8'),'hex')
		FROM source
		ORDER BY source_id`)
	if err != nil {
		t.Fatalf("execute capture alliance SQL expression: %v", err)
	}
	defer rows.Close()
	var payloads []map[string]json.RawMessage
	for rows.Next() {
		var encoded string
		if err = rows.Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		raw, decodeErr := hex.DecodeString(encoded)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		var payload map[string]json.RawMessage
		if err = json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 3 {
		t.Fatalf("capture expression rows=%d", len(payloads))
	}
	if _, exists := payloads[0]["alliance"]; exists {
		t.Fatal("missing source key was emitted as an alliance fact")
	}
	if string(payloads[1]["alliance"]) != "null" {
		t.Fatalf("source JSON null=%s", payloads[1]["alliance"])
	}
	var alliance string
	if err = json.Unmarshal(payloads[2]["alliance"], &alliance); err != nil {
		t.Fatal(err)
	}
	if alliance != "\u00a0联盟\u001c" {
		t.Fatalf("capture expression altered raw Unicode alliance=%q", alliance)
	}
}

// TestPostgreSQLSidebarHistoryReconcileVerifiesEveryImportedTargetFact is the
// command-level proof that preserved source digests alone cannot make a batch
// reconciled. It uses the real Order and Coupon importers with mixed terminal
// facts, then changes each target category while leaving the protected source
// rows and receipts untouched.
func TestPostgreSQLSidebarHistoryReconcileVerifiesEveryImportedTargetFact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	manifestPath, digest := sidebarHistoryFullFactsManifest(t)
	sidebarHistorySeedFullFacts(t, ctx, pool)
	applyArgs := []string{"--mode=apply", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest, "--confirm-apply"}
	reconcileArgs := []string{"--mode=reconcile", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("apply full sidebar history: %v", err)
	}
	var entitlements, claims, orders, grants int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 3 || claims != 3 || orders != 0 || grants != 0 {
		t.Fatalf("initial history targets entitlements=%d claims=%d orders=%d grants=%d", entitlements, claims, orders, grants)
	}

	// Entitlement status, period, and product mapping are target facts, not
	// merely an import digest. Each mutation must leave the batch applied.
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET status='expired' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET status='active' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET end_at=end_at + interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET end_at=end_at - interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET start_at=start_at + interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET start_at=start_at - interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET remark='目标漂移' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET remark='待续' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET service_product_id=501 WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET service_product_id=502 WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}

	// Coupon mapping, lifecycle status, and redemption time all come from the
	// protected row and must match the Coupon-owned target projection.
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET coupon_id=(SELECT id FROM coupon_rules ORDER BY id DESC LIMIT 1) WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET coupon_id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain='coupon' AND source_kind='commerce_coupons' AND source_key='61') WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET status='expired' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET status='claimed' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claim_no_masked='历史券号漂移' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claim_no_masked='' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claimed_at=claimed_at + interval '1 second' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claimed_at=claimed_at - interval '1 second' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET valid_until=valid_until + interval '1 second' WHERE source_system=$1 AND source_key='203'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET valid_until=valid_until - interval '1 second' WHERE source_system=$1 AND source_key='203'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET redeemed_at=redeemed_at + interval '1 second' WHERE source_system=$1 AND source_key='202'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET redeemed_at=redeemed_at - interval '1 second' WHERE source_system=$1 AND source_key='202'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}

	// A missing target cannot be repaired by retaining its map. Restore the
	// exact target row in this fixture, then prove the protected batch can pass.
	var deleted sidebarHistoryCouponTarget
	if err := pool.QueryRow(ctx, `SELECT id,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at FROM coupon_customer_claims WHERE source_system=$1 AND source_key='203'`, productionSourceSystem).Scan(&deleted.id, &deleted.customerID, &deleted.couponID, &deleted.status, &deleted.claimNoMasked, &deleted.claimedAt, &deleted.validFrom, &deleted.validUntil, &deleted.redeemedAt, &deleted.sourceDigest, &deleted.createdAt, &deleted.updatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM coupon_customer_claims WHERE id=$1`, deleted.id); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `INSERT INTO coupon_customer_claims(id,source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at)
		OVERRIDING SYSTEM VALUE VALUES($1,$2,'203',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, deleted.id, productionSourceSystem, deleted.customerID, deleted.couponID, deleted.status, deleted.claimNoMasked, deleted.claimedAt, deleted.validFrom, deleted.validUntil, deleted.redeemedAt, deleted.sourceDigest, deleted.createdAt, deleted.updatedAt); err != nil {
		t.Fatal(err)
	}

	// The source map and its target could be changed together while retaining
	// the protected row digest. Reconciliation must bind the scoped, verified
	// UnionID through OneID in the same Serializable transaction rather than
	// trusting that two altered customer_id columns agree.
	var originalCustomerID, wrongCustomerID int64
	if err := pool.QueryRow(ctx, `SELECT customer_id FROM order_service_entitlements WHERE source_system=$1 AND source_key='101'`, productionSourceSystem).Scan(&originalCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&wrongCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET customer_id=$1 WHERE source_system=$2 AND source_key='101'`, wrongCustomerID, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_source_map SET customer_id=$1 WHERE source_kind='service_period_entitlement' AND source_key='101'`, wrongCustomerID); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET customer_id=$1 WHERE source_system=$2 AND source_key='101'`, originalCustomerID, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_source_map SET customer_id=$1 WHERE source_kind='service_period_entitlement' AND source_key='101'`, originalCustomerID); err != nil {
		t.Fatal(err)
	}

	// Quarantine has no target, but its protected reason is still an observable
	// source result. A change must fail and a restored receipt can reconcile.
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_quarantine SET reason='identity_conflict' WHERE source_kind='service_period_entitlement' AND source_key='104'`); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_quarantine SET reason='identity_not_found' WHERE source_kind='service_period_entitlement' AND source_key='104'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile restored full facts: %v", err)
	}

	// Replay does not turn historical facts into new entitlement grants, orders,
	// coupon redemptions, or additional target rows.
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("replay full sidebar history: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 3 || claims != 3 || orders != 0 || grants != 0 {
		t.Fatalf("replay created effects entitlements=%d claims=%d orders=%d grants=%d", entitlements, claims, orders, grants)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile replayed full facts: %v", err)
	}

	// Both remaining receipts have an identity_not_found outcome. Once their
	// protected subject becomes verifiably resolvable, the existing Order and
	// Coupon import paths add maps and atomically remove the old quarantines.
	// A subsequent replay must retain exactly one outcome per source row.
	var recoveredCustomerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&recoveredCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'unionid','wechat-open-platform:primary','sidebar-full-unresolved','verified','provider-history:sidebar-full-recovery',1,clock_timestamp())`, recoveredCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("apply resolved prior quarantines: %v", err)
	}
	var maps, quarantines, imported, replayed int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		imported_count,replayed_count,status
		FROM sidebar_history_migration_batches WHERE run_key='sidebar-full-facts-pg-001'`).Scan(&maps, &quarantines, &imported, &replayed, &status); err != nil {
		t.Fatal(err)
	}
	if maps != 8 || quarantines != 0 || imported != 2 || replayed != 6 || status != "applied" {
		t.Fatalf("resolved quarantine receipts maps=%d quarantines=%d imported=%d replayed=%d status=%q", maps, quarantines, imported, replayed, status)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile resolved prior quarantines: %v", err)
	}
	// A later loss of identity proof cannot convert an already mapped receipt
	// back into a second quarantine outcome. Apply stops instead, retaining the
	// map for reconciliation to report as evidence drift.
	if _, err := pool.Exec(ctx, `UPDATE customer_identities SET status='retired' WHERE normalized_value='sidebar-full-unresolved'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil {
		t.Fatal("apply converted an existing map into a second quarantine")
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine)`).Scan(&maps, &quarantines); err != nil {
		t.Fatal(err)
	}
	if maps != 8 || quarantines != 0 {
		t.Fatalf("failed replay changed source outcomes maps=%d quarantines=%d", maps, quarantines)
	}
	if _, err := pool.Exec(ctx, `UPDATE customer_identities SET status='active' WHERE normalized_value='sidebar-full-unresolved'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("replay resolved prior quarantines: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &maps, &quarantines, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 4 || claims != 4 || maps != 8 || quarantines != 0 || orders != 0 || grants != 0 {
		t.Fatalf("resolved quarantine replay conservation entitlements=%d claims=%d maps=%d quarantines=%d orders=%d grants=%d", entitlements, claims, maps, quarantines, orders, grants)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile replayed resolved quarantines: %v", err)
	}
}

type sidebarHistoryCouponTarget struct {
	id, customerID, couponID          int64
	status, claimNoMasked             string
	claimedAt                         time.Time
	validFrom, validUntil, redeemedAt *time.Time
	sourceDigest                      []byte
	createdAt, updatedAt              time.Time
}

func sidebarHistoryReconcileRejects(t *testing.T, ctx context.Context, pool *pgxpool.Pool, args []string, runKey string) {
	t.Helper()
	if err := run(ctx, args); err == nil {
		t.Fatal("reconcile accepted target or quarantine drift")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE run_key=$1`, runKey).Scan(&status); err != nil || status != "applied" {
		t.Fatalf("failed reconcile status=%q err=%v", status, err)
	}
}

func sidebarHistoryFullFactsManifest(t *testing.T) (string, string) {
	t.Helper()
	at := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alliance := "联盟甲"
	from, until := at.AddDate(0, -1, 0), at.AddDate(0, 1, 0)
	redeemedAt := at.Add(-48 * time.Hour)
	m := manifest{
		SchemaVersion: currentSchemaVersion, RunKey: "sidebar-full-facts-pg-001", SourceSystem: productionSourceSystem,
		UnionIDScope: "wechat-open-platform:primary", CapturedAt: at,
		Entitlements: []sourceEntitlement{
			{SourceID: 101, UnionID: "sidebar-full-union-1", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "待续", Alliance: &alliance, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 102, UnionID: "sidebar-full-union-2", ServiceProductID: 12, ProductName: "季度服务", Status: "expired", StartAt: at.AddDate(0, -4, 0), EndAt: at.AddDate(0, -1, 0), Remark: "过期", CreatedAt: at.AddDate(0, -4, 0), UpdatedAt: at},
			{SourceID: 103, UnionID: "sidebar-full-union-3", ServiceProductID: 11, ProductName: "年度服务", Status: "refunded", StartAt: at.AddDate(0, -3, 0), EndAt: at.AddDate(0, -2, 0), Remark: "退款", CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
			{SourceID: 104, UnionID: "sidebar-full-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "隔离", CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{
			{SourceID: 201, UnionID: "sidebar-full-union-1", CouponID: 61, Status: "available", ClaimedAt: at.AddDate(0, -1, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 202, UnionID: "sidebar-full-union-2", CouponID: 62, Status: "consumed", ClaimedAt: at.AddDate(0, -2, 0), ValidFrom: &from, ValidUntil: &until, RedeemedAt: &redeemedAt, CreatedAt: at.AddDate(0, -2, 0), UpdatedAt: at},
			{SourceID: 203, UnionID: "sidebar-full-union-3", CouponID: 61, Status: "expired", ClaimedAt: at.AddDate(0, -3, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
			{SourceID: 204, UnionID: "sidebar-full-unresolved", CouponID: 61, Status: "expired", ClaimedAt: at.AddDate(0, -3, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
		},
	}
	return writeSidebarHistoryManifest(t, m)
}

func sidebarHistorySeedFullFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, unionID := range []string{"sidebar-full-union-1", "sidebar-full-union-2", "sidebar-full-union-3"} {
		var customerID int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'unionid','wechat-open-platform:primary',$2,'verified','provider-history:sidebar-full',1,clock_timestamp())`, customerID, unionID); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	insertCoupon := func(name string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,use_ends_at,instructions,created_by,updated_by,version,created_at,updated_at)
			VALUES($1,100,'CNY','published',100,10,0,$2,$3,'fixed_range',$2,$3,'',1,1,1,$2,$2) RETURNING id`, name, at.AddDate(0, -2, 0), at.AddDate(0, 2, 0)).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	couponOne, couponTwo := insertCoupon("历史券甲"), insertCoupon("历史券乙")
	if _, err := pool.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id) VALUES
		($1,'product','service_period_products','11',501),
		($1,'product','service_period_products','12',502),
		($1,'coupon','commerce_coupons','61',$2),
		($1,'coupon','commerce_coupons','62',$3)`, productionSourceSystem, couponOne, couponTwo); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLSidebarHistoryLegacyNoAllianceDigestReplayAndQuarantine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	unresolvedAlliance := "不应写入"
	m := manifest{
		SchemaVersion: 1, RunKey: "sidebar-alliance-legacy-pg-001", SourceSystem: productionSourceSystem,
		UnionIDScope: "wechat-open-platform:primary", CapturedAt: at,
		Entitlements: []sourceEntitlement{
			// This is the protected layout from before alliance existed. It must
			// serialize with no alliance key and therefore retain its old receipt.
			{SourceID: 17, UnionID: "sidebar-legacy-union", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "旧快照", CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			// An unresolved new-source row proves Reconcile validates quarantine
			// facts rather than requiring every entitlement to have a target.
			{SourceID: 18, UnionID: "sidebar-legacy-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &unresolvedAlliance, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{},
	}
	path, digest := writeSidebarHistoryManifest(t, m)
	legacyRaw, err := json.Marshal(m.Entitlements[0])
	if err != nil || bytes.Contains(legacyRaw, []byte(`"alliance"`)) {
		t.Fatalf("legacy digest row=%s err=%v", legacyRaw, err)
	}
	legacyDigest := sha256.Sum256(legacyRaw)

	var customerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'unionid','wechat-open-platform:primary','sidebar-legacy-union','verified','provider-history:sidebar-alliance',1,clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id)
		VALUES($1,'product','service_period_products','11',501)`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	var targetID int64
	if err = pool.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,alliance,source_digest,created_at,updated_at)
		VALUES($1,'17',$2,501,'年度服务','active',$3,$4,'旧快照',NULL,$5,$3,$6) RETURNING id`, productionSourceSystem, customerID, at.AddDate(0, -1, 0), at.AddDate(0, 11, 0), legacyDigest[:], at).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestRaw)
	var batchID int64
	if err = pool.QueryRow(ctx, `INSERT INTO sidebar_history_migration_batches(run_key,manifest_digest,source_system,input_count,status)
		VALUES($1,$2,$3,2,'applying') RETURNING id`, m.RunKey, manifestDigest[:], m.SourceSystem).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO sidebar_history_migration_source_map(batch_id,source_kind,source_key,source_digest,customer_id,target_table,target_id,disposition)
		VALUES($1,'service_period_entitlement','17',$2,$3,'order_service_entitlements',$4,'imported')`, batchID, legacyDigest[:], customerID, targetID); err != nil {
		t.Fatal(err)
	}

	applyArgs := []string{"--mode=apply", "--snapshot=" + path, "--manifest-sha256=" + digest, "--confirm-apply"}
	if err = run(ctx, applyArgs); err != nil {
		t.Fatalf("legacy protected replay: %v", err)
	}
	var targets, mappings, quarantines int64
	var targetDigest []byte
	var alliance *string
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		(SELECT source_digest FROM order_service_entitlements WHERE id=$2),
		(SELECT alliance FROM order_service_entitlements WHERE id=$2)`, productionSourceSystem, targetID).Scan(&targets, &mappings, &quarantines, &targetDigest, &alliance); err != nil {
		t.Fatal(err)
	}
	if targets != 1 || mappings != 1 || quarantines != 1 || !bytes.Equal(targetDigest, legacyDigest[:]) || alliance != nil {
		t.Fatalf("legacy replay targets=%d maps=%d quarantines=%d digest=%x alliance=%v", targets, mappings, quarantines, targetDigest, alliance)
	}

	if err = run(ctx, []string{"--mode=reconcile", "--snapshot=" + path, "--manifest-sha256=" + digest}); err != nil {
		t.Fatalf("legacy replay reconcile: %v", err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE id=$1`, batchID).Scan(&status); err != nil || status != "reconciled" {
		t.Fatalf("legacy replay status=%q err=%v", status, err)
	}
}

func sidebarHistoryAssertAllianceTargets(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT source_key,alliance FROM order_service_entitlements
		WHERE source_system=$1 ORDER BY source_key`, productionSourceSystem)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type target struct {
		key      string
		alliance *string
	}
	got := []target{}
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.key, &item.alliance); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].key != "7" || got[0].alliance != nil || got[1].key != "8" || got[1].alliance == nil || *got[1].alliance != "" || got[2].key != "9" || got[2].alliance == nil || *got[2].alliance != "联盟甲" {
		t.Fatalf("alliance facts not preserved: %#v", got)
	}
	var mappings, entitlementCount, quarantines int64
	var quarantineReason string
	if err = pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM sidebar_history_migration_source_map WHERE source_kind='service_period_entitlement'),
			(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
			(SELECT count(*) FROM sidebar_history_migration_quarantine WHERE source_kind='service_period_entitlement'),
			(SELECT reason FROM sidebar_history_migration_quarantine WHERE source_kind='service_period_entitlement' AND source_key='10')`, productionSourceSystem).Scan(&mappings, &entitlementCount, &quarantines, &quarantineReason); err != nil {
		t.Fatal(err)
	}
	if mappings != 3 || entitlementCount != 3 || quarantines != 1 || quarantineReason != "identity_not_found" {
		t.Fatalf("replay conservation maps=%d entitlements=%d quarantines=%d reason=%q", mappings, entitlementCount, quarantines, quarantineReason)
	}
}

func sidebarHistoryAllianceManifest(t *testing.T) (string, string) {
	t.Helper()
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	empty := ""
	value := "联盟甲"
	m := manifest{
		SchemaVersion: 1,
		RunKey:        "sidebar-alliance-pg-001",
		SourceSystem:  productionSourceSystem,
		UnionIDScope:  "wechat-open-platform:primary",
		CapturedAt:    at,
		Entitlements: []sourceEntitlement{
			{SourceID: 7, UnionID: "sidebar-union-7", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: nil, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 8, UnionID: "sidebar-union-8", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &empty, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 9, UnionID: "sidebar-union-9", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &value, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			// This row is deliberately unresolved. Reconcile must validate its
			// immutable quarantine receipt rather than demanding a target map.
			{SourceID: 10, UnionID: "sidebar-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &value, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{},
	}
	return writeSidebarHistoryManifest(t, m)
}

func writeSidebarHistoryManifest(t *testing.T, m manifest) (string, string) {
	t.Helper()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(t.TempDir(), "frozen-sidebar-alliance.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the exact protected-file digest construction obvious at the test
	// boundary rather than accepting a hard-coded confirmation hash.
	digest := sha256.Sum256(raw)
	return path, hex.EncodeToString(digest[:])
}

func seedSidebarHistoryIdentitiesAndDefinition(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, unionID := range []string{"sidebar-union-7", "sidebar-union-8", "sidebar-union-9"} {
		var customerID int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'unionid','wechat-open-platform:primary',$2,'verified','provider-history:sidebar-alliance',1,clock_timestamp())`, customerID, unionID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id)
		VALUES($1,'product','service_period_products','11',501)`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
}

func sidebarHistoryCommandDatabase(t *testing.T, ctx context.Context) (string, *pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping sidebar-history command PostgreSQL journey")
	}
	adminConfig, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "sidebar_history_command_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err = sidebarHistoryCommandMigrate(ctx, pool); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func sidebarHistoryCommandMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{
		"0002_identity.sql",
		"0020_order.sql",
		"0024_order_product_version.sql",
		"0049_order_history_attribution.sql",
		"0011_coupon_rules.sql",
		"0055_order_service_entitlements.sql",
		"0056_coupon_customer_claims.sql",
		"0070_service_period_entitlement_fulfillment.sql",
		"0076_order_checkout_snapshots.sql",
		"0088_order_service_entitlement_alliance.sql",
		"0058_sidebar_history_migration.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			return errors.New(name + ": " + err.Error())
		}
	}
	// The production definition-import migration owns this immutable mapping.
	// The command only reads these five columns; a narrow local projection keeps
	// this test focused on the command while avoiding GroupOps setup unrelated to
	// an entitlement import.
	_, err := pool.Exec(ctx, `
		CREATE TABLE config_definition_import_source_maps (
			source_system TEXT NOT NULL,
			domain TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_key TEXT NOT NULL,
			target_id BIGINT NOT NULL CHECK (target_id > 0),
			UNIQUE(source_system,domain,source_kind,source_key)
		)`)
	return err
}

func TestPostgreSQLSidebarHistoryFreshRunReusesOwnerReceipts(t *testing.T) {
	ctx := context.Background()
	dsn, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", dsn)
	path, digest := sidebarHistoryFullFactsManifest(t)
	sidebarHistorySeedFullFacts(t, ctx, pool)
	if err := run(ctx, []string{"--mode=apply", "--snapshot=" + path, "--manifest-sha256=" + digest, "--confirm-apply"}); err != nil {
		t.Fatal(err)
	}
	m, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.RunKey = "sidebar-new-independent-run"
	m.CapturedAt = m.CapturedAt.Add(time.Hour)
	other := filepath.Join(t.TempDir(), "fresh.json")
	if err = save(other, m); err != nil {
		t.Fatal(err)
	}
	m, err = load(other)
	if err != nil {
		t.Fatal(err)
	}
	sha := hex.EncodeToString(m.rawDigest[:])
	args := []string{"--mode=apply", "--snapshot=" + other, "--manifest-sha256=" + sha, "--confirm-apply"}
	if err = run(ctx, args); err != nil {
		t.Fatal(err)
	}
	var ent, claims int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM coupon_customer_claims)`).Scan(&ent, &claims); err != nil {
		t.Fatal(err)
	}
	if ent != 3 || claims != 3 {
		t.Fatal("fresh run duplicated owner history")
	}
	if err = run(ctx, []string{"--mode=reconcile", "--snapshot=" + other, "--manifest-sha256=" + sha}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLSidebarHistoryExternalProofResolvesOnlyExistingRoots(t *testing.T) {
	ctx := context.Background()
	dsn, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", dsn)
	path, digest := sidebarHistoryFullFactsManifest(t)
	sidebarHistorySeedFullFacts(t, ctx, pool)
	// Synthetic fixture owns all identities; production code only calls Resolve.
	if _, err := pool.Exec(ctx, `UPDATE customer_identities SET kind='wecom_external_userid',scope_key='wecom-corp:test'`); err != nil {
		t.Fatal(err)
	}
	m, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := proof.Snapshot{Version: 2, ResolutionMode: proof.ExistingWecomOnly, Scopes: proof.Scopes{CorpID: "test"}, CapturedAt: time.Now().UTC(), Rows: []proof.Row{}}
	seen := map[string]bool{}
	for _, r := range m.Entitlements {
		if seen[r.UnionID] {
			continue
		}
		seen[r.UnionID] = true
		s.Rows = append(s.Rows, proof.Row{UnionID: r.UnionID, CRMStatus: "active", PrimaryExternalID: r.UnionID, Evidence: []proof.Evidence{{MapID: int64(len(s.Rows) + 1), ProviderOK: true, CorpID: "test", ExternalID: r.UnionID, UnionID: r.UnionID, Status: "active", RawUnionID: r.UnionID, RawExternalID: r.UnionID}}})
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	enc := filepath.Join(dir, "proof.enc")
	out := filepath.Join(dir, "derived.json")
	if err = os.WriteFile(key, []byte(base64.RawStdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := proof.Seal(s, enc, key)
	if err != nil {
		t.Fatal(err)
	}
	binding := []string{"--external-proof=" + enc, "--external-proof-key-file=" + key, "--external-proof-sha256=" + hex.EncodeToString(d[:]), "--corp-id=test", "--confirm-matched-corp"}
	if err = run(ctx, append([]string{"--mode=bind-external-proof", "--snapshot=" + path, "--manifest-sha256=" + digest, "--output-snapshot=" + out}, binding...)); err != nil {
		t.Fatal(err)
	}
	derived, err := load(out)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(m.Entitlements)
	after, _ := json.Marshal(derived.Entitlements)
	if !bytes.Equal(before, after) || !derived.CapturedAt.Equal(m.CapturedAt) {
		t.Fatal("source row/timestamp changed")
	}
	args := append([]string{"--snapshot=" + out, "--manifest-sha256=" + hex.EncodeToString(derived.rawDigest[:])}, binding...)
	if err = run(ctx, append(args, "--mode=apply", "--confirm-apply")); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, append(args, "--mode=reconcile")); err != nil {
		t.Fatal(err)
	}
	var unions, ent int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_identities WHERE kind='unionid'),(SELECT count(*) FROM order_service_entitlements)`).Scan(&unions, &ent); err != nil {
		t.Fatal(err)
	}
	if unions != 0 || ent != 3 {
		t.Fatalf("unexpected identity or entitlement writes %d %d", unions, ent)
	}
}
