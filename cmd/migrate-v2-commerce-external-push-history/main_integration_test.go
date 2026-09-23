package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// frozenV1DeliveryRow and frozenV1Snapshot exactly mirror the 73cdaa legacy
// snapshot wire types. Keeping this fixture independent from deliveryRow
// proves old JSON has no effect_jobs member before the current decoder sees it.
type frozenV1DeliveryRow struct {
	ID             int64           `json:"id"`
	ConfigID       int64           `json:"config_id"`
	EventType      string          `json:"event_type"`
	DeliveryID     string          `json:"delivery_id"`
	TargetType     string          `json:"target_type"`
	TargetID       string          `json:"target_id"`
	OrderID        int64           `json:"order_id"`
	ProductID      int64           `json:"product_id"`
	Status         string          `json:"status"`
	AttemptCount   int             `json:"attempt_count"`
	RequestURL     string          `json:"request_url"`
	RequestHeaders json.RawMessage `json:"request_headers"`
	RequestBody    json.RawMessage `json:"request_body"`
	ResponseStatus *int            `json:"response_status,omitempty"`
	ResponseBody   string          `json:"response_body"`
	ErrorMessage   string          `json:"error_message"`
	NextRetryAt    *time.Time      `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	EffectJobID    *int64          `json:"effect_job_id,omitempty"`
	EffectState    *string         `json:"effect_state,omitempty"`
}

type frozenV1Snapshot struct {
	Manifest   manifest              `json:"manifest"`
	Configs    []configRow           `json:"configs"`
	Deliveries []frozenV1DeliveryRow `json:"deliveries"`
	Outbox     []outboxRow           `json:"domain_event_outbox"`
}

// OneID decision: not involved. Historical rows preserve legacy delivery
// evidence and Product source-map IDs only; they never resolve, create, or
// link a Customer. Persistence decision: one serializable Outbound-ledger
// transaction. No intent, EER, order-paid event, Provider action, or River job
// is created by extract, apply, replay, or reconciliation.
func TestCommerceExternalPushHistoryCLIExtractApplyReplayVerifyAndDriftPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, source, targetURL, sourceURL, cleanup := commercePushHistoryPools(t, ctx)
	defer cleanup()
	seedCommercePushHistorySource(t, ctx, source)
	if _, err := target.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_table,target_id) VALUES($1,'product','wechat_pay_products','101','products',1001)`, sourceSystem); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_DATABASE_URL", targetURL)
	t.Setenv("AICRM_SOURCE_DATABASE_URL", sourceURL)

	temp := t.TempDir()
	keyPath := filepath.Join(temp, "snapshot.key")
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(bytes32(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(temp, "commerce-push.sealed")
	revision := strings.Repeat("a", 40)
	if err := run(ctx, []string{"--mode=extract", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--source-revision=" + revision}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	s, digest, err := loadFile(snapshotPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Configs) != 1 || len(s.Deliveries) != 5 || len(s.Outbox) != 1 || s.Configs[0].Secret != "legacy-secret" {
		t.Fatalf("sealed source extraction shape=%#v", s)
	}
	primary := sourceDelivery(t, s, "delivery-old-2")
	simulated := sourceDelivery(t, s, "delivery-old-simulated-5")
	cancelled := sourceDelivery(t, s, "delivery-old-retryable-cancelled-7")
	multiple := sourceDelivery(t, s, "delivery-old-multiple-9")
	withoutJob := sourceDelivery(t, s, "delivery-old-no-job-10")
	if len(primary.EffectJobs) != 1 || primary.EffectJobs[0] != (effectJobRelation{ID: 4, EffectType: "webhook.order_paid.push", State: "succeeded"}) || len(simulated.EffectJobs) != 1 || simulated.EffectJobs[0] != (effectJobRelation{ID: 6, EffectType: "webhook.order_paid.push", State: "simulated"}) || simulated.ResponseStatus != nil || simulated.AttemptCount != 1 || len(cancelled.EffectJobs) != 1 || cancelled.EffectJobs[0] != (effectJobRelation{ID: 8, EffectType: "webhook.order_paid.push", State: "cancelled"}) || cancelled.ResponseStatus != nil || cancelled.AttemptCount != 1 || len(multiple.EffectJobs) != 2 || multiple.EffectJobs[0] != (effectJobRelation{ID: 9, EffectType: "webhook.order_paid.push", State: "succeeded"}) || multiple.EffectJobs[1] != (effectJobRelation{ID: 10, EffectType: "webhook.order_paid.push", State: "cancelled"}) || withoutJob.EffectJobs == nil || len(withoutJob.EffectJobs) != 0 {
		t.Fatalf("sealed source extraction did not preserve all delivery effect relations: %#v", s.Deliveries)
	}
	want := hex.EncodeToString(digest[:])
	if err = run(ctx, []string{"--mode=dry-run", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	assertCommercePushHistoryNoEffects(t, ctx, target)

	// The first apply fails atomically if the Outbound history ledger cannot
	// accept one row; no partial batch or current send is left behind.
	if _, err = target.Exec(ctx, `CREATE FUNCTION commerce_push_history_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'history failpoint'; END; $$; CREATE TRIGGER commerce_push_history_fail BEFORE INSERT ON outbound_commerce_push_history_rows FOR EACH ROW EXECUTE FUNCTION commerce_push_history_fail()`); err != nil {
		t.Fatal(err)
	}
	apply := []string{"--mode=apply", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want, "--confirm-apply"}
	if err = run(ctx, apply); err == nil {
		t.Fatal("history row failpoint accepted apply")
	}
	var batches, rows int
	if err = target.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_commerce_push_history_batches),(SELECT count(*) FROM outbound_commerce_push_history_rows)`).Scan(&batches, &rows); err != nil || batches != 0 || rows != 0 {
		t.Fatalf("failed apply left history batches=%d rows=%d err=%v", batches, rows, err)
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER commerce_push_history_fail ON outbound_commerce_push_history_rows; DROP FUNCTION commerce_push_history_fail()`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, apply); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertCommercePushHistoryLedger(t, ctx, target, "applied")
	assertCommercePushHistoryNoEffects(t, ctx, target)
	if err = run(ctx, apply); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	assertCommercePushHistoryLedger(t, ctx, target, "applied")
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertCommercePushHistoryLedger(t, ctx, target, "reconciled")

	// Keep the protected source digest unchanged but alter target facts. Every
	// source field, Product mapping, and read-only outcome is checked again.
	if _, err = target.Exec(ctx, `UPDATE outbound_commerce_push_history_rows SET source_state='failed',source_updated_at=source_updated_at+INTERVAL '1 second',target_product_id=999 WHERE source_kind='delivery' AND source_delivery_id='delivery-old-2'`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err == nil {
		t.Fatal("verify accepted delivery state/time/product mapping drift")
	}
	if _, err = target.Exec(ctx, `UPDATE outbound_commerce_push_history_rows SET source_state='success',source_updated_at='2026-09-06T12:02:00Z',target_product_id=1001 WHERE source_kind='delivery' AND source_delivery_id='delivery-old-2'`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err != nil {
		t.Fatalf("verify after restore: %v", err)
	}

	// The batch membership is protected evidence too. A row cannot be moved
	// under a batch while retaining the row's source facts and still reconcile.
	if _, err = target.Exec(ctx, `UPDATE outbound_commerce_push_history_batch_rows SET source_digest=decode(repeat('00',32),'hex')`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err == nil {
		t.Fatal("verify accepted membership source-digest drift")
	}
	if _, err = target.Exec(ctx, `UPDATE outbound_commerce_push_history_batch_rows membership SET source_digest=row.source_digest FROM outbound_commerce_push_history_rows row WHERE row.id=membership.source_row_id`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshotPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + want}); err != nil {
		t.Fatalf("verify after membership restore: %v", err)
	}

	// The batch is bound to the protected source revision and every row digest.
	drift := s
	drift.Deliveries = append([]deliveryRow(nil), s.Deliveries...)
	drift.Deliveries[0].Status = "failed"
	if err = populateManifest(&drift, revision, s.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}
	driftPath := filepath.Join(temp, "drift.sealed")
	driftDigest, err := sealToFile(drift, driftPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + driftPath, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + hex.EncodeToString(driftDigest[:]), "--confirm-apply"}); err == nil || !strings.Contains(err.Error(), "historical source row digest drift") {
		t.Fatalf("source digest drift=%v", err)
	}

	// A later protected capture from the same frozen donor revision is a new
	// batch, not a false revision collision. Overlapping V2 source rows retain
	// their single global ledger identity; only the three added source rows are
	// new. Replaying the second manifest does not duplicate either set.
	seedCommercePushHistorySourceExtension(t, ctx, source)
	if _, err = target.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_table,target_id) VALUES($1,'product','wechat_pay_products','102','products',1002)`, sourceSystem); err != nil {
		t.Fatal(err)
	}
	snapshotPath2 := filepath.Join(temp, "commerce-push-second.sealed")
	if err = run(ctx, []string{"--mode=extract", "--snapshot=" + snapshotPath2, "--snapshot-key-file=" + keyPath, "--source-revision=" + revision}); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	_, digest2, err := loadFile(snapshotPath2, keyPath)
	if err != nil || digest2 == digest {
		t.Fatalf("second protected manifest digest=%x first=%x err=%v", digest2, digest, err)
	}
	apply2 := []string{"--mode=apply", "--snapshot=" + snapshotPath2, "--snapshot-key-file=" + keyPath, "--manifest-sha256=" + hex.EncodeToString(digest2[:]), "--confirm-apply"}
	if err = run(ctx, apply2); err != nil {
		t.Fatalf("second snapshot apply: %v", err)
	}
	var allBatches, sourceRows, members int
	if err = target.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_commerce_push_history_batches),(SELECT count(*) FROM outbound_commerce_push_history_rows),(SELECT count(*) FROM outbound_commerce_push_history_batch_rows)`).Scan(&allBatches, &sourceRows, &members); err != nil || allBatches != 2 || sourceRows != 10 || members != 17 {
		t.Fatalf("overlap batches/source_rows/members=%d/%d/%d err=%v", allBatches, sourceRows, members, err)
	}
	if err = run(ctx, apply2); err != nil {
		t.Fatalf("second snapshot replay: %v", err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM outbound_commerce_push_history_rows`).Scan(&sourceRows); err != nil || sourceRows != 10 {
		t.Fatalf("second replay duplicated source rows=%d err=%v", sourceRows, err)
	}
	assertCommercePushHistoryNoEffects(t, ctx, target)
}

func TestLoadFileReadsFrozenV1EffectRelation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	effectID := int64(44)
	effectState := "succeeded"
	s := frozenV1Snapshot{Deliveries: []frozenV1DeliveryRow{{
		ID: 1, ConfigID: 1, EventType: "transaction.paid", DeliveryID: "legacy-delivery-1",
		TargetType: "product", TargetID: "101", OrderID: 1, ProductID: 101, Status: "success", AttemptCount: 1,
		RequestURL: "https://legacy.example/push", RequestHeaders: json.RawMessage(`{}`), RequestBody: json.RawMessage(`{}`),
		ResponseBody: "", ErrorMessage: "", CreatedAt: at, UpdatedAt: at, EffectJobID: &effectID, EffectState: &effectState,
	}}}
	s.Manifest = frozenV1Manifest(t, s, at)
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"effect_jobs"`) {
		t.Fatalf("73c V1 fixture unexpectedly contains effect_jobs: %s", raw)
	}
	wantDigest := sha256.Sum256(raw)
	key := bytes32(t)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	keyPath := filepath.Join(temp, "legacy.key")
	if err = os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(temp, "legacy.sealed")
	sealed := append(nonce, aead.Seal(nil, nonce, raw, []byte(legacySchemaVersion))...)
	if err = os.WriteFile(snapshotPath, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	got, digest, err := loadFile(snapshotPath, keyPath)
	if err != nil || digest != wantDigest || got.Manifest.SchemaVersion != legacySchemaVersion || len(got.Deliveries) != 1 || got.Deliveries[0].EffectJobID == nil || *got.Deliveries[0].EffectJobID != effectID || got.Deliveries[0].EffectState == nil || *got.Deliveries[0].EffectState != effectState || got.Deliveries[0].EffectJobs != nil {
		t.Fatalf("load frozen V1 snapshot err=%v digest=%x want=%x snapshot=%#v", err, digest, wantDigest, got)
	}
	legacyFacts := facts(got)
	if len(legacyFacts) != 1 || legacyFacts[0].effectID == nil || *legacyFacts[0].effectID != effectID || legacyFacts[0].effectState == nil || *legacyFacts[0].effectState != effectState {
		t.Fatalf("legacy effect projection=%#v", legacyFacts)
	}
}

func frozenV1Manifest(t *testing.T, s frozenV1Snapshot, at time.Time) manifest {
	t.Helper()
	sets := map[string]any{"configs": s.Configs, "deliveries": s.Deliveries, "domain_event_outbox": s.Outbox}
	digests := make(map[string]string, len(sets))
	counts := make(map[string]int, len(sets))
	for name, value := range sets {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		digests[name] = hex.EncodeToString(digest[:])
		switch rows := value.(type) {
		case []configRow:
			counts[name] = len(rows)
		case []frozenV1DeliveryRow:
			counts[name] = len(rows)
		case []outboxRow:
			counts[name] = len(rows)
		}
	}
	return manifest{SchemaVersion: legacySchemaVersion, SourceSystem: sourceSystem, SourceRevision: strings.Repeat("a", 40), SnapshotAt: at, Counts: counts, Digests: digests}
}

func sourceDelivery(t *testing.T, s snapshot, deliveryID string) deliveryRow {
	t.Helper()
	for _, row := range s.Deliveries {
		if row.DeliveryID == deliveryID {
			return row
		}
	}
	t.Fatalf("source delivery %q was not extracted", deliveryID)
	return deliveryRow{}
}

func bytes32(t *testing.T) []byte {
	t.Helper()
	out := make([]byte, 32)
	if _, err := rand.Read(out); err != nil {
		t.Fatal(err)
	}
	return out
}

func commercePushHistoryPools(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool, string, string, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping commerce push history PostgreSQL integration test")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	targetSchema := "aicrm_commerce_push_history_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{targetSchema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	sourceSchema := "aicrm_v2_commerce_push_source_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{sourceSchema}.Sanitize()); err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{targetSchema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	targetCfg := cfg.Copy()
	targetCfg.ConnConfig.RuntimeParams["search_path"] = targetSchema
	targetCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	target, err := pgxpool.NewWithConfig(ctx, targetCfg)
	if err != nil {
		t.Fatal(err)
	}
	sourceCfg := cfg.Copy()
	sourceCfg.ConnConfig.RuntimeParams["search_path"] = sourceSchema
	sourceCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	source, err := pgxpool.NewWithConfig(ctx, sourceCfg)
	if err != nil {
		target.Close()
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, commercePushHistoryTargetSchema(t)); err != nil {
		source.Close()
		target.Close()
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, commercePushHistorySourceSchema); err != nil {
		source.Close()
		target.Close()
		t.Fatal(err)
	}
	return target, source, withSearchPath(t, databaseURL, targetSchema), withSearchPath(t, databaseURL, sourceSchema), func() {
		source.Close()
		target.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{targetSchema}.Sanitize()+" CASCADE")
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{sourceSchema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
func withSearchPath(t *testing.T, url, schema string) string {
	t.Helper()
	return url + "&search_path=" + schema
}
func commercePushHistoryTargetSchema(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "0095_product_external_push.sql"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "-- The old external-push configuration"
	index := strings.Index(string(raw), marker)
	if index < 0 {
		t.Fatal("history migration segment not found")
	}
	return `CREATE TABLE config_definition_import_source_maps(source_system TEXT NOT NULL,domain TEXT NOT NULL,source_kind TEXT NOT NULL,source_key TEXT NOT NULL,target_table TEXT NOT NULL,target_id BIGINT NOT NULL); CREATE TABLE external_effects(id BIGINT PRIMARY KEY); CREATE TABLE river_job(id BIGINT PRIMARY KEY); CREATE TABLE outbound_commerce_push_intents(id BIGINT PRIMARY KEY);` + string(raw[index:])
}

const commercePushHistorySourceSchema = `CREATE TABLE external_push_config(id BIGINT PRIMARY KEY,target_type TEXT NOT NULL,target_id TEXT NOT NULL,event_type TEXT NOT NULL,enabled BOOLEAN NOT NULL,webhook_url TEXT NOT NULL,push_type TEXT NOT NULL,expires_at_ts BIGINT,day BIGINT,frequency BIGINT,remark TEXT NOT NULL,custom_params JSONB NOT NULL,secret TEXT NOT NULL,created_by TEXT NOT NULL,updated_by TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL); CREATE TABLE external_push_delivery(id BIGINT PRIMARY KEY,config_id BIGINT NOT NULL,event_type TEXT NOT NULL,delivery_id TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,order_id BIGINT NOT NULL,product_id BIGINT NOT NULL,status TEXT NOT NULL,attempt_count INTEGER NOT NULL,request_url TEXT NOT NULL,request_headers JSONB NOT NULL,request_body JSONB NOT NULL,response_status INTEGER,response_body TEXT NOT NULL,error_message TEXT NOT NULL,next_retry_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL); CREATE TABLE domain_event_outbox(id BIGINT PRIMARY KEY,event_type TEXT NOT NULL,aggregate_type TEXT NOT NULL,aggregate_id TEXT NOT NULL,payload JSONB NOT NULL,status TEXT NOT NULL,retry_count INTEGER NOT NULL,next_retry_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL); CREATE TABLE external_effect_job(id BIGINT PRIMARY KEY,target_type TEXT NOT NULL,target_id TEXT NOT NULL,effect_type TEXT NOT NULL,status TEXT NOT NULL);`

func seedCommercePushHistorySource(t *testing.T, ctx context.Context, source *pgxpool.Pool) {
	t.Helper()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	_, err := source.Exec(ctx, `INSERT INTO external_push_config VALUES(1,'product','101','transaction.paid',TRUE,'https://legacy.example/push?source=v2','member_open',NULL,30,1,'old remark','{"count":9007199254740993}'::jsonb,'legacy-secret','old-admin','new-admin',$1,$2); INSERT INTO external_push_delivery VALUES(2,1,'transaction.paid','delivery-old-2','product','101',44,101,'success',2,'https://legacy.example/push?source=v2','{"X-AICRM-Signature":"redacted"}'::jsonb,'{"phone_number":"sensitive"}'::jsonb,200,'accepted','',NULL,$1,$2); INSERT INTO external_push_delivery VALUES(5,1,'transaction.paid','delivery-old-simulated-5','product','101',46,101,'skipped',1,'https://legacy.example/push?source=v2','{}'::jsonb,'{}'::jsonb,NULL,'','',NULL,$1,$2); INSERT INTO domain_event_outbox VALUES(3,'transaction.paid','wechat_pay_order','44','{"order":"sensitive"}'::jsonb,'success',1,NULL,$1,$2); INSERT INTO external_effect_job VALUES(4,'external_push_delivery','delivery-old-2','webhook.order_paid.push','succeeded'); INSERT INTO external_effect_job VALUES(6,'external_push_delivery','delivery-old-simulated-5','webhook.order_paid.push','simulated')`, at, at.Add(2*time.Minute))
	if _, err = source.Exec(ctx, `INSERT INTO external_push_delivery VALUES(7,1,'transaction.paid','delivery-old-retryable-cancelled-7','product','101',47,101,'skipped',1,'https://legacy.example/push?source=v2','{}'::jsonb,'{}'::jsonb,NULL,'','',NULL,$1,$2); INSERT INTO external_push_delivery VALUES(9,1,'transaction.paid','delivery-old-multiple-9','product','101',48,101,'success',2,'https://legacy.example/push?source=v2','{}'::jsonb,'{}'::jsonb,200,'accepted','',NULL,$1,$2); INSERT INTO external_push_delivery VALUES(10,1,'transaction.paid','delivery-old-no-job-10','product','101',49,101,'pending',0,'https://legacy.example/push?source=v2','{}'::jsonb,'{}'::jsonb,NULL,'','',NULL,$1,$2); INSERT INTO external_effect_job VALUES(8,'external_push_delivery','delivery-old-retryable-cancelled-7','webhook.order_paid.push','cancelled'); INSERT INTO external_effect_job VALUES(9,'external_push_delivery','delivery-old-multiple-9','webhook.order_paid.push','succeeded'); INSERT INTO external_effect_job VALUES(10,'external_push_delivery','delivery-old-multiple-9','webhook.order_paid.push','cancelled')`, at, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
}
func seedCommercePushHistorySourceExtension(t *testing.T, ctx context.Context, source *pgxpool.Pool) {
	t.Helper()
	at := time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC)
	_, err := source.Exec(ctx, `INSERT INTO external_push_config VALUES(11,'product','102','transaction.paid',TRUE,'https://legacy.example/push?source=v2','member_renew',NULL,45,2,'next remark','{"state":"new"}'::jsonb,'legacy-secret-next','old-admin','new-admin',$1,$2); INSERT INTO external_push_delivery VALUES(12,11,'transaction.paid','delivery-old-12','product','102',45,102,'failed',3,'https://legacy.example/push?source=v2','{}'::jsonb,'{}'::jsonb,502,'gateway rejected','upstream timeout',NULL,$1,$2); INSERT INTO domain_event_outbox VALUES(13,'transaction.paid','wechat_pay_order','45','{}'::jsonb,'failed',2,NULL,$1,$2); INSERT INTO external_effect_job VALUES(14,'external_push_delivery','delivery-old-12','webhook.order_paid.push','failed')`, at, at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
}

func assertCommercePushHistoryLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want string) {
	t.Helper()
	var input, imported, pending, excluded int
	var status string
	if err := pool.QueryRow(ctx, `SELECT input_count,imported_count,pending_count,excluded_count,status FROM outbound_commerce_push_history_batches`).Scan(&input, &imported, &pending, &excluded, &status); err != nil {
		t.Fatal(err)
	}
	if input != 7 || imported != 6 || pending != 0 || excluded != 1 || status != want {
		t.Fatalf("history conservation input/imported/pending/excluded/status=%d/%d/%d/%d/%s", input, imported, pending, excluded, status)
	}
	var configTarget, deliveryTarget *int64
	var deliveryEffect *int64
	var deliveryState, deliveryError string
	var deliveryStatus *int
	var bodyProtected bool
	if err := pool.QueryRow(ctx, `SELECT target_product_id FROM outbound_commerce_push_history_rows WHERE source_kind='config'`).Scan(&configTarget); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT target_product_id,source_effect_job_id,source_state,source_response_status,source_error_message,source_response_body_protected FROM outbound_commerce_push_history_rows WHERE source_kind='delivery' AND source_delivery_id='delivery-old-2'`).Scan(&deliveryTarget, &deliveryEffect, &deliveryState, &deliveryStatus, &deliveryError, &bodyProtected); err != nil {
		t.Fatal(err)
	}
	if configTarget == nil || deliveryTarget == nil || *configTarget != 1001 || *deliveryTarget != 1001 || deliveryEffect == nil || *deliveryEffect != 4 || deliveryState != "success" || deliveryStatus == nil || *deliveryStatus != 200 || deliveryError != "" || !bodyProtected {
		t.Fatalf("target facts config=%v delivery=%v effect=%v state=%q response=%v error=%q protected=%t", configTarget, deliveryTarget, deliveryEffect, deliveryState, deliveryStatus, deliveryError, bodyProtected)
	}
	var simulatedAttempts int
	var simulatedEffectState string
	var simulatedStatus *int
	if err := pool.QueryRow(ctx, `SELECT source_attempt_count,COALESCE(source_effect_state,''),source_response_status FROM outbound_commerce_push_history_rows WHERE source_kind='delivery' AND source_delivery_id='delivery-old-simulated-5'`).Scan(&simulatedAttempts, &simulatedEffectState, &simulatedStatus); err != nil {
		t.Fatal(err)
	}
	if simulatedAttempts != 1 || simulatedEffectState != "simulated" || simulatedStatus != nil {
		t.Fatalf("simulated delivery source evidence attempts/state/status=%d/%q/%v", simulatedAttempts, simulatedEffectState, simulatedStatus)
	}
	// A one-to-many V2 target relation has no truthful singular target column.
	// The sealed snapshot retains both jobs, while this read-only projection is
	// explicitly ambiguous and cannot imply a Provider result.
	var multipleEffectID *int64
	var multipleEffectState string
	if err := pool.QueryRow(ctx, `SELECT source_effect_job_id,COALESCE(source_effect_state,'') FROM outbound_commerce_push_history_rows WHERE source_kind='delivery' AND source_delivery_id='delivery-old-multiple-9'`).Scan(&multipleEffectID, &multipleEffectState); err != nil {
		t.Fatal(err)
	}
	if multipleEffectID != nil || multipleEffectState != ambiguousEffectRelationState {
		t.Fatalf("multiple effect relation projection id/state=%v/%q", multipleEffectID, multipleEffectState)
	}
	var missingEffectID *int64
	var missingEffectState string
	if err := pool.QueryRow(ctx, `SELECT source_effect_job_id,COALESCE(source_effect_state,'') FROM outbound_commerce_push_history_rows WHERE source_kind='delivery' AND source_delivery_id='delivery-old-no-job-10'`).Scan(&missingEffectID, &missingEffectState); err != nil {
		t.Fatal(err)
	}
	if missingEffectID != nil || missingEffectState != "" {
		t.Fatalf("zero effect relation projection id/state=%v/%q", missingEffectID, missingEffectState)
	}
	// The old effect runtime permits failed_retryable -> cancelled. Retaining
	// that terminal record preserves a real ambiguity instead of inventing a
	// no-call fact from the final state or attempt counter.
	var cancelledAttempts int
	var cancelledEffectState string
	var cancelledStatus *int
	if err := pool.QueryRow(ctx, `SELECT source_attempt_count,COALESCE(source_effect_state,''),source_response_status FROM outbound_commerce_push_history_rows WHERE source_kind='delivery' AND source_delivery_id='delivery-old-retryable-cancelled-7'`).Scan(&cancelledAttempts, &cancelledEffectState, &cancelledStatus); err != nil {
		t.Fatal(err)
	}
	if cancelledAttempts != 1 || cancelledEffectState != "cancelled" || cancelledStatus != nil {
		t.Fatalf("cancelled retryable source evidence attempts/state/status=%d/%q/%v", cancelledAttempts, cancelledEffectState, cancelledStatus)
	}
}

func assertCommercePushHistoryNoEffects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var intents, effects, jobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_commerce_push_intents),(SELECT count(*) FROM external_effects),(SELECT count(*) FROM river_job)`).Scan(&intents, &effects, &jobs); err != nil {
		t.Fatal(err)
	}
	if intents != 0 || effects != 0 || jobs != 0 {
		t.Fatalf("history importer created current intents/effects/jobs=%d/%d/%d", intents, effects, jobs)
	}
}
