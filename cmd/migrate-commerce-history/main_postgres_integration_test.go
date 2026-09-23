package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLCommerceHistoryCommandApplyReplayReconcile executes the real
// command against one isolated PostgreSQL schema. It deliberately has no
// importer/verifier substitute: the command composes the existing Identity,
// Order and Payment Owners, then each Owner verifies its own facts before the
// Order-owned run ledger is marked reconciled.
func TestPostgreSQLCommerceHistoryCommandApplyReplayReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := commerceHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	snapshot, digest := frozenCommerceHistoryManifest(t)
	apply := []string{"--mode=apply", "--snapshot=" + snapshot, "--manifest-sha256=" + digest, "--confirm-apply"}

	// This database-only failpoint happens after the Payment insert and history
	// receipt would have been staged. Its failure must roll back that entire
	// Payment-owned UoW; the command itself never writes a foreign Owner table.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION commerce_history_fail_payment_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'commerce history payment audit failpoint';
		END;
		$$;
		CREATE TRIGGER commerce_history_fail_payment_audit
		BEFORE INSERT ON payment_audit_events
		FOR EACH ROW WHEN (NEW.event_type = 'payment.history_imported')
		EXECUTE FUNCTION commerce_history_fail_payment_audit();`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, apply); err == nil {
		t.Fatal("payment failpoint allowed history import")
	}
	commerceHistoryAssertPaymentRollback(t, ctx, pool)
	if _, err := pool.Exec(ctx, `DROP TRIGGER commerce_history_fail_payment_audit ON payment_audit_events; DROP FUNCTION commerce_history_fail_payment_audit();`); err != nil {
		t.Fatal(err)
	}

	if err := run(ctx, apply); err != nil {
		t.Fatalf("real apply: %v", err)
	}
	commerceHistoryAssertAppliedConservation(t, ctx, pool)

	// A second invocation of the exact frozen manifest is a restart/replay, not
	// a second import. It must retain each Owner's prior source/target receipt.
	if err := run(ctx, apply); err != nil {
		t.Fatalf("real replay: %v", err)
	}
	commerceHistoryAssertAppliedConservation(t, ctx, pool)

	var sourceDigest []byte
	var originalTarget string
	if err := pool.QueryRow(ctx, `
		SELECT receipt.payload_digest,payment.provider_transaction_digest
		FROM payment_operation_receipts receipt
		JOIN payments payment ON payment.id=receipt.result_id
		WHERE receipt.operation='history_import' AND receipt.actor_scope='frozen-commerce-history-pg-001'
		  AND receipt.result_kind='payment'`).Scan(&sourceDigest, &originalTarget); err != nil {
		t.Fatal(err)
	}
	if len(sourceDigest) != 32 || originalTarget == "" {
		t.Fatalf("missing immutable payment source/target facts digest=%x target=%q", sourceDigest, originalTarget)
	}
	if _, err := pool.Exec(ctx, `UPDATE payments SET provider_transaction_digest='sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'`); err != nil {
		t.Fatal(err)
	}
	reconcile := []string{"--mode=reconcile", "--snapshot=" + snapshot, "--manifest-sha256=" + digest}
	if err := run(ctx, reconcile); err == nil {
		t.Fatal("reconcile accepted target drift while the source receipt remained intact")
	}
	var persistedSource []byte
	var runStatus string
	if err := pool.QueryRow(ctx, `
		SELECT receipt.payload_digest,run.status
		FROM payment_operation_receipts receipt
		JOIN order_import_runs run ON run.run_key=receipt.actor_scope
		WHERE receipt.operation='history_import' AND receipt.actor_scope='frozen-commerce-history-pg-001'
		  AND receipt.result_kind='payment'`).Scan(&persistedSource, &runStatus); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persistedSource, sourceDigest) || runStatus != "applied" {
		t.Fatalf("failed reconcile changed receipt or run digest=%x status=%q", persistedSource, runStatus)
	}
	if _, err := pool.Exec(ctx, `UPDATE payments SET provider_transaction_digest=$1`, originalTarget); err != nil {
		t.Fatal(err)
	}

	if err := run(ctx, reconcile); err != nil {
		t.Fatalf("real reconcile: %v", err)
	}
	commerceHistoryAssertReconciled(t, ctx, pool)
}

func frozenCommerceHistoryManifest(t *testing.T) (path, digest string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate frozen commerce manifest")
	}
	path = filepath.Join(filepath.Dir(source), "testdata", "frozen_commerce_history_full_v3.json")
	manifest, err := ordermigration.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, hex.EncodeToString(manifest.Digest[:])
}

func commerceHistoryAssertPaymentRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var identities, quarantines, orders, orderReceipts, payments, refunds, paymentReceipts, audits, outbox, effects int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity_history_import_receipts WHERE outcome='canonical'),
			(SELECT count(*) FROM identity_history_import_receipts WHERE outcome='quarantined'),
			(SELECT count(*) FROM orders WHERE source_system='commerce-history'),
			(SELECT count(*) FROM order_import_receipts),
			(SELECT count(*) FROM payments),
			(SELECT count(*) FROM payment_refunds),
			(SELECT count(*) FROM payment_operation_receipts WHERE operation='history_import'),
			(SELECT count(*) FROM payment_audit_events),
			(SELECT count(*) FROM payment_outbox),
			(SELECT count(*) FROM external_effects)`).Scan(&identities, &quarantines, &orders, &orderReceipts, &payments, &refunds, &paymentReceipts, &audits, &outbox, &effects); err != nil {
		t.Fatal(err)
	}
	if identities != 1 || quarantines != 1 || orders != 1 || orderReceipts != 1 || payments != 0 || refunds != 0 || paymentReceipts != 0 || audits != 0 || outbox != 0 || effects != 0 {
		t.Fatalf("payment failpoint rollback identities=%d quarantines=%d orders=%d order_receipts=%d payments=%d refunds=%d payment_receipts=%d audits=%d outbox=%d effects=%d", identities, quarantines, orders, orderReceipts, payments, refunds, paymentReceipts, audits, outbox, effects)
	}
}

func commerceHistoryAssertAppliedConservation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var (
		runStatus                                               string
		input, imported                                         int64
		canonical, quarantines, orders, orderReceipts, payments int64
		refunds, paymentReceipts, orderAudits, orderOutbox      int64
		paymentAudits, paymentOutbox, effects                   int64
		orderStatus, paymentStatus, refundStatus                string
		refundedMinor                                           int64
		effectEligible                                          bool
		externalPayment, externalRefund                         *int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,input_count,imported_count
		FROM order_import_runs WHERE run_key='frozen-commerce-history-pg-001'`).Scan(&runStatus, &input, &imported); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity_history_import_receipts WHERE outcome='canonical'),
			(SELECT count(*) FROM identity_history_import_receipts WHERE outcome='quarantined'),
			(SELECT count(*) FROM orders WHERE source_system='commerce-history'),
			(SELECT count(*) FROM order_import_receipts),
			(SELECT count(*) FROM payments),
			(SELECT count(*) FROM payment_refunds),
			(SELECT count(*) FROM payment_operation_receipts WHERE operation='history_import'),
			(SELECT count(*) FROM order_audit_events WHERE event_type='order.history_imported'),
			(SELECT count(*) FROM order_outbox WHERE event_type='order.history_imported'),
			(SELECT count(*) FROM payment_audit_events WHERE event_type IN ('payment.history_imported','payment.refund_history_imported')),
			(SELECT count(*) FROM payment_outbox WHERE event_type IN ('payment.history_imported','payment.refund_history_imported')),
			(SELECT count(*) FROM external_effects)`).Scan(&canonical, &quarantines, &orders, &orderReceipts, &payments, &refunds, &paymentReceipts, &orderAudits, &orderOutbox, &paymentAudits, &paymentOutbox, &effects); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT o.status,o.refunded_minor,o.effect_eligible,p.status,p.external_effect_id,r.status,r.external_effect_id
		FROM orders o
		JOIN payments p ON p.order_id=o.id
		JOIN payment_refunds r ON r.payment_id=p.id
		WHERE o.source_system='commerce-history'`).Scan(&orderStatus, &refundedMinor, &effectEligible, &paymentStatus, &externalPayment, &refundStatus, &externalRefund); err != nil {
		t.Fatal(err)
	}
	if runStatus != "applied" || input != 4 || imported != 4 || canonical != 1 || quarantines != 1 || orders != 1 || orderReceipts != 1 || payments != 1 || refunds != 1 || paymentReceipts != 2 || orderAudits != 1 || orderOutbox != 1 || paymentAudits != 2 || paymentOutbox != 2 || effects != 0 || orderStatus != "partially_refunded" || refundedMinor != 400 || effectEligible || paymentStatus != "paid" || externalPayment != nil || refundStatus != "completed" || externalRefund != nil {
		t.Fatalf("history conservation run=%q input=%d imported=%d canonical=%d quarantines=%d orders=%d order_receipts=%d payments=%d refunds=%d payment_receipts=%d order_audits=%d order_outbox=%d payment_audits=%d payment_outbox=%d effects=%d order=%q refunded=%d effect_eligible=%v payment=%q payment_effect=%v refund=%q refund_effect=%v", runStatus, input, imported, canonical, quarantines, orders, orderReceipts, payments, refunds, paymentReceipts, orderAudits, orderOutbox, paymentAudits, paymentOutbox, effects, orderStatus, refundedMinor, effectEligible, paymentStatus, externalPayment, refundStatus, externalRefund)
	}
}

func commerceHistoryAssertReconciled(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM order_import_runs WHERE run_key='frozen-commerce-history-pg-001'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "reconciled" {
		t.Fatalf("run status=%q", status)
	}
	commerceHistoryAssertTerminalEffectsAbsent(t, ctx, pool)
}

func commerceHistoryAssertTerminalEffectsAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var effects, intents, callbacks, reconciliations int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM external_effects),
			(SELECT count(*) FROM payment_provider_intents),
			(SELECT count(*) FROM payment_callback_receipts),
			(SELECT count(*) FROM payment_reconciliations)`).Scan(&effects, &intents, &callbacks, &reconciliations); err != nil {
		t.Fatal(err)
	}
	if effects != 0 || intents != 0 || callbacks != 0 || reconciliations != 0 {
		t.Fatalf("historical import produced external work effects=%d intents=%d callbacks=%d reconciliations=%d", effects, intents, callbacks, reconciliations)
	}
}

func commerceHistoryCommandDatabase(t *testing.T, ctx context.Context) (string, *pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping commerce-history command PostgreSQL journey")
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
	schema := "commerce_history_command_" + hex.EncodeToString(random[:])
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
	if err = commerceHistoryCommandMigrate(ctx, pool); err != nil {
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

func commerceHistoryCommandMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{
		"0001_platform.sql",
		"0002_identity.sql",
		"0005_external_effects.sql",
		"0020_order.sql",
		"0021_payment.sql",
		"0024_order_product_version.sql",
		"0025_payment_reconciliation.sql",
		"0026_identity_history_receipts.sql",
		"0061_product_public_purchase.sql",
		"0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql",
		"0129_order_history_source_delta.sql",
		"0134_payment_history_source_delta.sql",
		"0156_distribution_profit_sharing_payment.sql",
		"0161_payment_paid_confirmation_time.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		// The deployed migration intentionally owns a public immutable trigger
		// function. This isolated schema substitutes an equivalent local function
		// so parallel PostgreSQL tests cannot race over public.pg_proc.
		statement := strings.ReplaceAll(string(raw), "public.external_effects_reject_delete", "external_effects_reject_delete")
		if _, err = pool.Exec(ctx, statement); err != nil {
			return errors.New(name + ": " + err.Error())
		}
	}
	return nil
}

func TestPostgreSQLHistoricalDeltaAtomicPaymentAndReplay(t *testing.T) {
	ctx := context.Background()
	db, pool, cleanup := commerceHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", db)
	file, _ := frozenCommerceHistoryManifest(t)
	current, err := ordermigration.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	write := func(m ordermigration.Manifest) (string, string) {
		t.Helper()
		raw, e := json.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		p := filepath.Join(t.TempDir(), "manifest.json")
		if e = os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		parsed, e := ordermigration.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		return p, hex.EncodeToString(parsed.Digest[:])
	}
	old := current
	old.RunKey = "delta-before"
	old.Orders = append([]ordermigration.OrderRow(nil), current.Orders...)
	old.Orders[0].Status = "pending_payment"
	old.Orders[0].ProviderTransactionNo = ""
	old.Refunds = nil
	oldFile, oldHash := write(old)
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + oldFile, "--manifest-sha256=" + oldHash, "--confirm-apply"}); err != nil {
		t.Fatal(err)
	}
	current.RunKey = "delta-after"
	afterFile, afterHash := write(current)
	proof := map[string]any{"manifest_sha256": afterHash, "orders": map[string]orderport.HistoricalDeltaPrecondition{old.Orders[0].SourceKey: {SourceDigest: ordermigration.HistoricalOrderDigest(old.Orders[0]), Version: 1}}}
	raw, _ := json.Marshal(proof)
	proofFile := filepath.Join(t.TempDir(), "preconditions.json")
	os.WriteFile(proofFile, raw, 0600)
	args := []string{"--mode=apply", "--snapshot=" + afterFile, "--manifest-sha256=" + afterHash, "--history-delta-preconditions=" + proofFile, "--confirm-apply"}
	// The source amount and item snapshot remain frozen even with valid CAS.
	bad := current
	bad.RunKey = "delta-bad-commercial-change"
	bad.Orders = append([]ordermigration.OrderRow(nil), current.Orders...)
	bad.Orders[0].Items = append([]ordermigration.ItemRow(nil), current.Orders[0].Items...)
	bad.Orders[0].AmountMinor++
	bad.Orders[0].Items[0].LineAmountMinor++
	bad.Orders[0].Items[0].UnitAmountMinor++
	badFile, badHash := write(bad)
	proof["manifest_sha256"] = badHash
	badRaw, _ := json.Marshal(proof)
	badProof := filepath.Join(t.TempDir(), "bad-proof.json")
	os.WriteFile(badProof, badRaw, 0600)
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + badFile, "--manifest-sha256=" + badHash, "--history-delta-preconditions=" + badProof, "--confirm-apply"}); err == nil {
		t.Fatal("commercial facts overwritten")
	}

	if _, err = pool.Exec(ctx, `CREATE FUNCTION delta_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'failpoint'; END;$$; CREATE TRIGGER delta_fail BEFORE INSERT ON payment_audit_events FOR EACH ROW EXECUTE FUNCTION delta_fail()`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, args); err == nil {
		t.Fatal("failpoint accepted")
	}
	var version, deltas, payments, refunds int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT version FROM orders LIMIT 1),(SELECT count(*) FROM order_history_source_deltas),(SELECT count(*) FROM payments),(SELECT count(*) FROM payment_refunds)`).Scan(&version, &deltas, &payments, &refunds); err != nil {
		t.Fatal(err)
	}
	if version != 1 || deltas != 0 || payments != 0 || refunds != 0 {
		t.Fatal("split delta transaction")
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER delta_fail ON payment_audit_events; DROP FUNCTION delta_fail()`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, args); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, args); err != nil {
		t.Fatal("replay", err)
	}
	if err = run(ctx, []string{"--mode=reconcile", "--snapshot=" + afterFile, "--manifest-sha256=" + afterHash}); err != nil {
		t.Fatal("reconcile", err)
	}
	var effects int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_history_source_deltas),(SELECT count(*) FROM external_effects)`).Scan(&deltas, &effects); err != nil {
		t.Fatal(err)
	}
	if deltas != 1 || effects != 0 {
		t.Fatal("unexpected delta/effects")
	}
}
