package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	channel "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// TestChannelWelcomePostgreSQLAcceptanceJourney covers the short, verified
// callback path with its real owner tables. It uses no Provider credentials or
// network writer: the only external boundary stays in outbound unit tests.
func TestChannelWelcomePostgreSQLAcceptanceJourney(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := wecom.NewWelcomeGrantCipher("channel-welcome-integration-secret")
	if err != nil {
		t.Fatal(err)
	}
	digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
	if err != nil {
		t.Fatal(err)
	}
	adminID := insertChannelWelcomeAdmin(t, ctx, native)
	states := channel.NewPostgreSQLStore()
	ready := seedChannelWelcomeFixture(t, ctx, unit, states, digester, adminID, "ready", "welcome-ready", false, 1, 0)
	materialUnavailable := seedChannelWelcomeFixture(t, ctx, unit, states, digester, adminID, "material", "welcome-material", true, 1, 0)
	missingAsset := seedChannelWelcomeFixture(t, ctx, unit, states, digester, adminID, "missing", "welcome-missing", false, 99, 0)

	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	actions := channel.NewEntrantActionStore(effects, nil)
	dispatcher := wecom.ExternalContactCallbackDispatcher{StateDigester: digester, Inbox: inbox, UOW: unit, WelcomeGrants: wecom.NewPostgreSQLWelcomeGrantStore(cipher), WelcomeActions: actions, States: states}
	now := time.Now().UTC().Truncate(time.Microsecond)
	key := mustChannelWelcomeKey(t, "wecom:external-contact:channel-welcome-pg-0001")
	input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(ready.rawState), ReceivedAt: now}
	if err = dispatcher.DispatchDecryptedEvent(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertChannelWelcomeCounts(t, ctx, native, 1, 1, 1, 1)
	var firstReceived, deadline time.Time
	var customerID *int64
	var effectRef, state, payload string
	if err = native.QueryRow(ctx, `SELECT first_received_at,send_deadline_at,customer_id,effect_ref,state,(SELECT payload::text FROM webhook_inbox WHERE idempotency_key=$1) FROM channel_welcome_intents WHERE callback_id=$1`, key).Scan(&firstReceived, &deadline, &customerID, &effectRef, &state, &payload); err != nil {
		t.Fatal(err)
	}
	if !firstReceived.Equal(now) || !deadline.Equal(now.Add(20*time.Second)) || customerID != nil || state != "queued" || effectRef == "" || containsRawWelcome(payload) {
		t.Fatalf("first=%s deadline=%s customer=%v state=%s effect=%q payload=%s", firstReceived, deadline, customerID, state, effectRef, payload)
	}
	var queue string
	if err = native.QueryRow(ctx, `SELECT queue FROM external_effect_jobs WHERE effect_id=$1`, effectIDNumber(t, effectRef)).Scan(&queue); err != nil || queue != platformjobqueue.OutboundWelcomeQueue {
		t.Fatalf("queue=%q err=%v", queue, err)
	}

	// Replays, including concurrent Provider deliveries, reuse the original
	// Inbox/grant/intent/effect and cannot extend the 20-second business window.
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			replay := input
			replay.ReceivedAt = now.Add(7 * time.Minute)
			errs <- dispatcher.DispatchDecryptedEvent(ctx, replay)
		}()
	}
	wait.Wait()
	close(errs)
	for replayErr := range errs {
		if replayErr != nil {
			t.Fatalf("concurrent replay=%v", replayErr)
		}
	}
	assertChannelWelcomeCounts(t, ctx, native, 1, 1, 1, 1)
	var replayDeadline time.Time
	if err = native.QueryRow(ctx, `SELECT send_deadline_at FROM channel_welcome_intents WHERE callback_id=$1`, key).Scan(&replayDeadline); err != nil || !replayDeadline.Equal(deadline) {
		t.Fatalf("replay deadline=%s want=%s err=%v", replayDeadline, deadline, err)
	}

	// An ordinary customer lifecycle can link afterwards without producing a
	// second welcome effect or modifying frozen references.
	var canonicalCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&canonicalCustomer); err != nil {
		t.Fatal(err)
	}
	// Customer lifecycle processing and a Provider replay may run together.
	// The lifecycle only attaches CustomerID; neither path creates a second
	// welcome effect or refreshes its frozen target/payload/deadline.
	parallel := make(chan error, 2)
	go func() {
		parallel <- dispatcher.DispatchDecryptedEvent(ctx, wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: input.Plaintext, ReceivedAt: now.Add(9 * time.Minute)})
	}()
	go func() {
		parallel <- unit.Within(ctx, func(tx context.Context) error {
			return actions.AcceptEntrantActions(tx, channelport.EntrantActionCommand{CallbackID: string(key), CustomerID: customerdomain.CustomerID(canonicalCustomer), Resolution: ready.resolution, OccurredAt: now})
		})
	}()
	for range 2 {
		if parallelErr := <-parallel; parallelErr != nil {
			t.Fatalf("parallel callback/lifecycle=%v", parallelErr)
		}
	}
	if err = native.QueryRow(ctx, `SELECT customer_id FROM channel_welcome_intents WHERE callback_id=$1`, key).Scan(&canonicalCustomer); err != nil {
		t.Fatal(err)
	}
	assertChannelWelcomeCounts(t, ctx, native, 1, 1, 1, 1)
	var anotherCustomer int64
	if err = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&anotherCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE channel_welcome_intents SET customer_id=$2 WHERE callback_id=$1`, key, anotherCustomer); err == nil {
		t.Fatal("later customer association rewrote the immutable welcome target")
	}
	if _, err = native.Exec(ctx, `UPDATE channel_welcome_intents SET source_ref_digest=$2 WHERE callback_id=$1`, key, effectport.Hash("tamper", "source")); err == nil {
		t.Fatal("welcome source digest was mutable after customer association")
	}
	// 0150 must reject every partial envelope combination. Disable only the
	// immutable guard in this disposable schema so the database CHECK, rather
	// than the guard, proves its own NULL behavior.
	if _, err = native.Exec(ctx, `ALTER TABLE channel_welcome_intents DISABLE TRIGGER channel_welcome_intents_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE channel_welcome_intents SET effect_payload_digest=NULL WHERE callback_id=$1`, key); err == nil {
		t.Fatal("partial immutable effect envelope was accepted")
	}
	if _, err = native.Exec(ctx, `ALTER TABLE channel_welcome_intents ENABLE TRIGGER channel_welcome_intents_guard`); err != nil {
		t.Fatal(err)
	}

	// Material unavailable and a now-unavailable configuration still persist a
	// normal Inbox delivery plus an explicit no-send reason; no effect is made.
	for _, fixture := range []channelWelcomeFixture{materialUnavailable, missingAsset} {
		fixtureKey := mustChannelWelcomeKey(t, "wecom:external-contact:"+fixture.name+"-pg-0001")
		if err = dispatcher.DispatchDecryptedEvent(ctx, wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: fixtureKey, Plaintext: channelWelcomePlaintext(fixture.rawState), ReceivedAt: now}); err != nil {
			t.Fatal(err)
		}
		var recordedState, recordedReason string
		if err = native.QueryRow(ctx, `SELECT state,result_reason FROM channel_welcome_intents WHERE callback_id=$1`, fixtureKey).Scan(&recordedState, &recordedReason); err != nil {
			t.Fatal(err)
		}
		want := "welcome_material_unavailable"
		if fixture.name == "missing" {
			want = "channel_unavailable"
		}
		if recordedState != want || recordedReason != want {
			t.Fatalf("fixture=%s state=%s reason=%s want=%s", fixture.name, recordedState, recordedReason, want)
		}
	}
	assertChannelWelcomeCounts(t, ctx, native, 3, 3, 3, 1)

	// A required acceptance failure rolls back the grant and Inbox together.
	failing := dispatcher
	failing.WelcomeActions = failingChannelWelcomeAccepter{}
	if err = failing.DispatchDecryptedEvent(ctx, wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: mustChannelWelcomeKey(t, "wecom:external-contact:welcome-failure-pg-0001"), Plaintext: channelWelcomePlaintext(ready.rawState), ReceivedAt: now}); err == nil {
		t.Fatal("required welcome acceptance failure was acknowledged")
	}
	assertChannelWelcomeCounts(t, ctx, native, 3, 3, 3, 1)

	// Reopening the repository represents a process restart after commit: the
	// one durable queued effect and its queue receipt remain recoverable.
	restarted, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	if projection, readErr := restarted.Get(ctx, effectRef); readErr != nil || projection.State != effectport.StateQueued {
		t.Fatalf("restart projection=%+v err=%v", projection, readErr)
	}
}

func TestChannelWelcomeMigrationReadinessRequiresIntentTable(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := channel.NewModuleRegistration().Readiness(ctx, native); err != nil {
		t.Fatalf("0066-ready channel module=%v", err)
	}
	if _, err := native.Exec(ctx, `DROP TABLE channel_welcome_message_snapshots`); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `DROP TABLE channel_welcome_intents`); err != nil {
		t.Fatal(err)
	}
	if err := channel.NewModuleRegistration().Readiness(ctx, native); err == nil {
		t.Fatal("channel module was ready without the 0066 welcome-intent table")
	}
}

type channelWelcomeFixture struct {
	name, rawState string
	resolution     channeldomain.StateResolution
}

func seedChannelWelcomeFixture(t *testing.T, ctx context.Context, unit *platformpostgres.UnitOfWork, states *channel.PostgreSQLStore, digester wecom.StateDigester, adminID int64, name, rawState string, material bool, assetVersion, entryTagID int64) channelWelcomeFixture {
	return seedChannelWelcomeFixtureWithAssigneeRatio(t, ctx, unit, states, digester, adminID, name, rawState, material, assetVersion, entryTagID, 100)
}

func seedChannelWelcomeFixtureWithAssigneeRatio(t *testing.T, ctx context.Context, unit *platformpostgres.UnitOfWork, states *channel.PostgreSQLStore, digester wecom.StateDigester, adminID int64, name, rawState string, material bool, assetVersion, entryTagID, assigneeRatio int64) channelWelcomeFixture {
	return seedChannelWelcomeFixtureWithWelcomeMessage(t, ctx, unit, states, digester, adminID, name, rawState, "welcome", material, assetVersion, entryTagID, assigneeRatio)
}

func seedChannelWelcomeFixtureWithWelcomeMessage(t *testing.T, ctx context.Context, unit *platformpostgres.UnitOfWork, states *channel.PostgreSQLStore, digester wecom.StateDigester, adminID int64, name, rawState, welcomeMessage string, material bool, assetVersion, entryTagID, assigneeRatio int64) channelWelcomeFixture {
	t.Helper()
	var channelID int64
	if err := unit.Within(ctx, func(tx context.Context) error {
		digest := sha256.Sum256([]byte("channel-welcome-config:" + name))
		transaction, transactionErr := platformpostgres.RequireTransaction(tx)
		if transactionErr != nil {
			return transactionErr
		}
		if transactionErr = transaction.QueryRow(tx, `INSERT INTO channels(code,status,current_config_version,created_at,updated_at) VALUES($1,'active',1,clock_timestamp(),clock_timestamp()) RETURNING id`, "welcome-"+name).Scan(&channelID); transactionErr != nil {
			return transactionErr
		}
		images := []int64{}
		if material {
			images = []int64{1}
		}
		entryTagName, entryTagGroupName := "", ""
		if entryTagID > 0 {
			entryTagName, entryTagGroupName = "fixture entry tag", "fixture entry tag group"
		}
		if _, transactionErr = transaction.Exec(tx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,welcome_message,welcome_image_ids,entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,config_digest,created_by,created_at) VALUES($1,1,'qrcode','qrcode',$2,$3,$4,NULLIF($5,0),$6,$7,'single_owner','ratio',$8,$9,clock_timestamp())`, channelID, "Welcome "+name, welcomeMessage, images, entryTagID, entryTagName, entryTagGroupName, digest[:], adminID); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = transaction.Exec(tx, `INSERT INTO channel_assignees(channel_id,config_version,staff_id,priority,ratio_percent,created_at) VALUES($1,1,$2,1,$3,clock_timestamp())`, channelID, adminID, assigneeRatio); transactionErr != nil {
			return transactionErr
		}
		if assetVersion == 1 {
			assetDigest := sha256.Sum256([]byte("channel-welcome-asset:" + name))
			assetEffectRef := fmt.Sprintf("eer_%d", channelID+10_000)
			assetAcceptRef := fmt.Sprintf("eerop_%d", channelID+10_000)
			assetQueueRef := fmt.Sprintf("eerop_%d", channelID+20_000)
			if _, transactionErr = transaction.Exec(tx, `INSERT INTO channel_acquisition_assets(channel_id,config_version,asset_version,kind,operation,source_ref_digest,operation_key_digest,request_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,provider_asset_ref,result_url,created_by) VALUES($1,1,1,'contact_way_qrcode','create',$2,$3,$4,$5,$6,$7,'executed','provider-asset','https://work.weixin.qq.com/qr', $8)`, channelID, effectport.Hash("channel-welcome-asset-source", name), assetDigest[:], assetDigest[:], assetEffectRef, assetAcceptRef, assetQueueRef, adminID); transactionErr != nil {
				return transactionErr
			}
		}
		stateDigest, digestErr := digester.DigestState("wx-corp", rawState)
		if digestErr != nil {
			return digestErr
		}
		bindingDigest := sha256.Sum256([]byte("channel-welcome-binding:" + name))
		_, _, transactionErr = states.PutBinding(tx, channel.StateBinding{CorpID: "wx-corp", DigestKeyVersion: 1, StateDigest: stateDigest, ChannelID: channelID, AssetKind: channel.AcquisitionAssetQRCode, AssetVersion: assetVersion, BindingDigest: bindingDigest, ActiveFrom: time.Unix(0, 0).UTC()})
		return transactionErr
	}); err != nil {
		t.Fatal(err)
	}
	return channelWelcomeFixture{name: name, rawState: rawState, resolution: channeldomain.StateResolution{Status: channeldomain.StateAttributed, Asset: channeldomain.AcquisitionAsset{ChannelID: channelID, Kind: "qrcode", AssetVersion: assetVersion}}}
}

func insertChannelWelcomeAdmin(t *testing.T, ctx context.Context, native *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled) VALUES('channel-welcome','$argon2id$channel-welcome','Channel Welcome','channel-welcome',true,false) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func channelWelcomePlaintext(state string) []byte {
	return []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID><State><![CDATA[` + state + `]]></State><WelcomeCode><![CDATA[welcome-secret]]></WelcomeCode></xml>`)
}

func mustChannelWelcomeKey(t *testing.T, value string) idempotency.Key {
	t.Helper()
	key, err := idempotency.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func assertChannelWelcomeCounts(t *testing.T, ctx context.Context, native *pgxpool.Pool, inboxes, grants, intents, effects int) {
	t.Helper()
	var gotInboxes, gotGrants, gotIntents, gotEffects int
	if err := native.QueryRow(ctx, `SELECT (SELECT count(*) FROM webhook_inbox),(SELECT count(*) FROM wecom_welcome_grants),(SELECT count(*) FROM channel_welcome_intents),(SELECT count(*) FROM external_effects)`).Scan(&gotInboxes, &gotGrants, &gotIntents, &gotEffects); err != nil || gotInboxes != inboxes || gotGrants != grants || gotIntents != intents || gotEffects != effects {
		t.Fatalf("inboxes=%d grants=%d intents=%d effects=%d err=%v", gotInboxes, gotGrants, gotIntents, gotEffects, err)
	}
}

func effectIDNumber(t *testing.T, value string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(value, "eer_%d", &id); err != nil || id < 1 {
		t.Fatalf("effect ref=%q err=%v", value, err)
	}
	return id
}

func containsRawWelcome(payload string) bool {
	var value map[string]any
	return json.Unmarshal([]byte(payload), &value) != nil || payload == "" || strings.Contains(payload, "welcome-secret")
}

type failingChannelWelcomeAccepter struct{}

func (failingChannelWelcomeAccepter) AcceptCallbackWelcome(context.Context, channelport.CallbackWelcomeCommand) error {
	return errors.New("required channel welcome acceptance failed")
}

func channelWelcomeIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Channel Welcome PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "channel_welcome_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Channel Welcome test")
	}
	base := filepath.Join(filepath.Dir(source), "..", "..", "migrations")
	for _, migration := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0006_wecom_callback_channel_acquisition.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0029_channel_center.sql", "0031_channel_history_import.sql", "0032_channel_acquisition_assets.sql", "0033_wecom_welcome_grants.sql", "0034_channel_entrant_actions.sql", "0035_channel_acquisition_links.sql", "0059_channel_v1_semantic_repair.sql", "0065_channel_legacy_asset_retirement.sql", "0066_channel_welcome_intents.sql", "0086_wecom_profile_primary_owner.sql", "0093_customer_tag_commands.sql", "0125_outbound_material_preparation.sql", "0148_channel_archive_edit.sql", "0150_channel_welcome_message_snapshots.sql", "0153_wecom_customer_detail_projection.sql", "0170_wecom_contact_description_effect.sql", "0171_wecom_contact_description_source_coverage.sql", "0164_channel_welcome_provider_rejections.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(base, migration))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", migration, execErr)
		}
	}
	if err = ensureAccessLoginFixtureSchema(ctx, native); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

type channelBusyTagSubmitter struct{}

func (channelBusyTagSubmitter) SubmitTagCommand(context.Context, customerport.TagCommand) (customerport.TagCommandResult, error) {
	return customerport.TagCommandResult{}, customerport.ErrTagCommandConflict
}
func (channelBusyTagSubmitter) SubmitTagCommandWithin(context.Context, customerport.TagCommand) (customerport.TagCommandResult, error) {
	return customerport.TagCommandResult{}, customerport.ErrTagCommandConflict
}

func TestChannelEntrantBusyTagCommandKeepsAssignmentAndRecordsRejectedActionPostgreSQL(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
	if err != nil {
		t.Fatal(err)
	}
	adminID := insertChannelWelcomeAdmin(t, ctx, native)
	states := channel.NewPostgreSQLStore()
	ready := seedChannelWelcomeFixture(t, ctx, unit, states, digester, adminID, "tag-busy", "tag-busy-state", false, 1, 7)
	var customerID int64
	if err = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	actions := channel.NewEntrantActionStore(effects, nil)
	if err = actions.SetTagCommandSubmitter(channelBusyTagSubmitter{}); err != nil {
		t.Fatal(err)
	}
	callbackID := "channel-tag-busy-0001"
	if err = unit.Within(ctx, func(tx context.Context) error {
		return actions.AcceptEntrantActions(tx, channelport.EntrantActionCommand{CallbackID: callbackID, CustomerID: customerdomain.CustomerID(customerID), Resolution: ready.resolution, OccurredAt: time.Now().UTC()})
	}); err != nil {
		t.Fatalf("entrant acceptance=%v", err)
	}
	var assignments, actionsCount, refs int
	var state, reason string
	if err = native.QueryRow(ctx, `SELECT count(*) FROM channel_entrant_assignments WHERE callback_id=$1`, callbackID).Scan(&assignments); err != nil || assignments != 1 {
		t.Fatalf("assignments=%d err=%v", assignments, err)
	}
	if err = native.QueryRow(ctx, `SELECT state,result_reason,(effect_ref IS NOT NULL)::int+(accept_receipt_ref IS NOT NULL)::int+(queue_receipt_ref IS NOT NULL)::int FROM channel_entrant_actions WHERE callback_id=$1 AND action_kind='entry_tag'`, callbackID).Scan(&state, &reason, &refs); err != nil || state != "rejected" || reason != "customer_tag_busy" || refs != 0 {
		t.Fatalf("action state=%q reason=%q refs=%d err=%v", state, reason, refs, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM channel_entrant_actions WHERE callback_id=$1`, callbackID).Scan(&actionsCount); err != nil || actionsCount != 1 {
		t.Fatalf("actions=%d err=%v", actionsCount, err)
	}
	var effectCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil || effectCount != 0 {
		t.Fatalf("effects=%d err=%v", effectCount, err)
	}
}
