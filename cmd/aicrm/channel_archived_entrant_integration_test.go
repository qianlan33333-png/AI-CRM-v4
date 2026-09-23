package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	channel "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// TestArchivedRuntimeChannelCompletesEntrantWithoutEffects exercises the real
// PostgreSQL InboxProcessor path. It is the regression for a trusted callback
// whose action was formerly rolled back as callback_lifecycle merely because
// the channel was archived.
func TestArchivedRuntimeChannelCompletesEntrantWithoutEffects(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()

	channelID := fixture.ready.resolution.Asset.ChannelID
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE channels SET status='archived',archived_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, channelID); err != nil {
		t.Fatal(err)
	}
	input, _ := fixture.acceptWelcome(t, "archived-runtime-entrant-0001", fixture.now)
	callbackID := string(input.CallbackKey)

	var intentState, intentReason string
	var intentEffects int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT state,result_reason,(SELECT count(*) FROM external_effects) FROM channel_welcome_intents WHERE callback_id=$1`, callbackID).Scan(&intentState, &intentReason, &intentEffects); err != nil || intentState != "channel_unavailable" || intentReason != "channel_unavailable" || intentEffects != 0 {
		t.Fatalf("welcome intent state=%q reason=%q effects=%d err=%v", intentState, intentReason, intentEffects, err)
	}

	processor := archivedEntrantProcessor(t, fixture)
	if processed, err := processor.ProcessOnce(fixture.ctx, "archived-runtime-entrant", 1); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}

	var inboxStatus, resultCodes, errorCode string
	var processingReceipts, retryReceipts int
	var customers, identities, relationships, entrants, assignments, actions, tagCommands, effects int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT status,array_to_string(r.result_codes,',')
		FROM webhook_inbox i JOIN wecom_callback_receipts r ON r.inbox_id=i.id AND r.receipt_kind='processing'
		WHERE i.idempotency_key=$1`, callbackID).Scan(&inboxStatus, &resultCodes); err != nil {
		t.Fatal(err)
	}
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT COALESCE((SELECT error_code FROM wecom_callback_receipts WHERE inbox_id=i.id AND receipt_kind='processing'),''),
		(SELECT count(*) FROM wecom_callback_receipts WHERE inbox_id=i.id AND receipt_kind='processing'),
		(SELECT count(*) FROM wecom_callback_receipts WHERE inbox_id=i.id AND receipt_kind='retry_requested')
		FROM webhook_inbox i WHERE i.idempotency_key=$1`, callbackID).Scan(&errorCode, &processingReceipts, &retryReceipts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT
		(SELECT count(*) FROM customers),
		(SELECT count(*) FROM customer_identities WHERE kind='wecom_external_userid'),
		(SELECT count(*) FROM wecom_follow_relationships WHERE active),
		(SELECT count(*) FROM channel_acquisition_entrant_receipts WHERE status='channel_attributed' AND binding_id IS NOT NULL),
		(SELECT count(*) FROM channel_entrant_assignments),
		(SELECT count(*) FROM channel_entrant_actions),
		(SELECT count(*) FROM customer_tag_commands),
		(SELECT count(*) FROM external_effects)`).Scan(&customers, &identities, &relationships, &entrants, &assignments, &actions, &tagCommands, &effects); err != nil {
		t.Fatal(err)
	}
	if inboxStatus != "processed" || !containsAll(resultCodes, "channel_attributed", "ignored") ||
		customers != 1 || identities != 1 || relationships != 1 || entrants != 1 ||
		assignments != 0 || actions != 0 || tagCommands != 0 || effects != 0 ||
		errorCode != "" || processingReceipts != 1 || retryReceipts != 0 {
		t.Fatalf("inbox=%s results=%q error=%q processing_receipts=%d retry_receipts=%d customers=%d identities=%d relationships=%d entrants=%d assignments=%d actions=%d tag_commands=%d effects=%d", inboxStatus, resultCodes, errorCode, processingReceipts, retryReceipts, customers, identities, relationships, entrants, assignments, actions, tagCommands, effects)
	}

	// A later activation cannot turn the completed historic callback into an
	// action. The durable Inbox is already processed, and callback/welcome keys
	// make a duplicate delivery a no-op.
	reactivateArchivedFixtureChannel(t, fixture, channelID)
	if err := fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
		t.Fatal(err)
	}
	if processed, err := processor.ProcessOnce(fixture.ctx, "archived-runtime-entrant-replay", 1); err != nil || processed != 0 {
		t.Fatalf("duplicate processed=%d err=%v", processed, err)
	}
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT
		(SELECT count(*) FROM channel_entrant_assignments),
		(SELECT count(*) FROM channel_entrant_actions),
		(SELECT count(*) FROM customer_tag_commands),
		(SELECT count(*) FROM external_effects)`).Scan(&assignments, &actions, &tagCommands, &effects); err != nil || assignments != 0 || actions != 0 || tagCommands != 0 || effects != 0 {
		t.Fatalf("historic replay assignments=%d actions=%d tag_commands=%d effects=%d err=%v", assignments, actions, tagCommands, effects, err)
	}
}

// TestActiveRuntimeAndLegacyChannelsAcceptEntrantActions keeps the active
// behavior on the actual InboxProcessor path: a trusted callback is attributed,
// assigned, and has its entry-tag action accepted. The composition integration
// test separately proves that the same Channel action is accepted through the
// Customer TagCommand port when that port is wired at the composition root.
func TestActiveRuntimeAndLegacyChannelsAcceptEntrantActions(t *testing.T) {
	for _, scenario := range []struct {
		name, rawState string
		assetVersion   int64
		legacy         bool
	}{
		{name: "runtime", rawState: "active-runtime-state", assetVersion: 1},
		{name: "verified_legacy", rawState: "active-legacy-state", assetVersion: 2_000_000_019, legacy: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newChannelWelcomeRuntimeFixture(t)
			defer fixture.close()
			states := channel.NewPostgreSQLStore()
			digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
			if err != nil {
				t.Fatal(err)
			}
			channelFixture := seedChannelWelcomeFixture(t, fixture.ctx, fixture.unit, states, digester, 1, "active-"+scenario.name, scenario.rawState, false, scenario.assetVersion, 1)
			if scenario.legacy {
				if err = seedVerifiedLegacyAsset(fixture.ctx, fixture.native, channelFixture.resolution.Asset.ChannelID); err != nil {
					t.Fatal(err)
				}
			}
			key := mustChannelWelcomeKey(t, "wecom:external-contact:active-"+scenario.name+"-entrant-0001")
			input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(channelFixture.rawState), ReceivedAt: fixture.now}
			if err = fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
				t.Fatal(err)
			}
			processor := archivedEntrantProcessor(t, fixture)
			if processed, processErr := processor.ProcessOnce(fixture.ctx, "active-"+scenario.name+"-entrant", 1); processErr != nil || processed != 1 {
				t.Fatalf("processed=%d err=%v", processed, processErr)
			}
			var inboxStatus, resultCodes, actionState, actionEffectRef string
			var assignmentCount, actionCount int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT i.status,array_to_string(r.result_codes,',')
				FROM webhook_inbox i JOIN wecom_callback_receipts r ON r.inbox_id=i.id AND r.receipt_kind='processing'
				WHERE i.idempotency_key=$1`, string(key)).Scan(&inboxStatus, &resultCodes); err != nil {
				t.Fatal(err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM channel_entrant_assignments WHERE callback_id=$1`, string(key)).Scan(&assignmentCount); err != nil {
				t.Fatal(err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*),COALESCE(max(state),''),COALESCE(max(effect_ref),'') FROM channel_entrant_actions WHERE callback_id=$1 AND action_kind='entry_tag'`, string(key)).Scan(&actionCount, &actionState, &actionEffectRef); err != nil {
				t.Fatal(err)
			}
			if inboxStatus != "processed" || !containsAll(resultCodes, "channel_attributed") || strings.Contains(resultCodes, "ignored") || assignmentCount != 1 || actionCount != 1 || actionState != "queued" || actionEffectRef == "" {
				t.Fatalf("inbox=%s results=%q assignments=%d entry_tag_count=%d entry_tag_state=%q effect_ref=%q", inboxStatus, resultCodes, assignmentCount, actionCount, actionState, actionEffectRef)
			}
		})
	}
}

// TestUnavailableAssigneeStaysRetryableWhenChannelIsInactive verifies that an
// inactive/archived status is not enough to suppress an untrusted execution
// path. The legacy asset is verified and unretired, but its configuration has
// no selectable assignee, so the existing callback_lifecycle handling remains.
func TestUnavailableAssigneeStaysRetryableWhenChannelIsInactive(t *testing.T) {
	for _, channelStatus := range []string{"inactive", "archived"} {
		t.Run(channelStatus, func(t *testing.T) {
			fixture := newChannelWelcomeRuntimeFixture(t)
			defer fixture.close()
			states := channel.NewPostgreSQLStore()
			digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
			if err != nil {
				t.Fatal(err)
			}
			legacy := seedChannelWelcomeFixtureWithAssigneeRatio(t, fixture.ctx, fixture.unit, states, digester, 1, "unavailable-assignee-"+channelStatus, "unavailable-assignee-state-"+channelStatus, false, 2_000_000_019, 1, 1)
			if err = seedVerifiedLegacyAsset(fixture.ctx, fixture.native, legacy.resolution.Asset.ChannelID); err != nil {
				t.Fatal(err)
			}
			if _, err = fixture.native.Exec(fixture.ctx, `UPDATE channels SET status=$2,archived_at=CASE WHEN $2='archived' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE id=$1`, legacy.resolution.Asset.ChannelID, channelStatus); err != nil {
				t.Fatal(err)
			}
			// With this fixture's ratio=1, the precomputed key hashes to a point
			// above 1 (inactive=18; archived=94), so no staff is selectable.
			key := mustChannelWelcomeKey(t, "wecom:external-contact:unavailable-assignee-"+channelStatus+"-1")
			input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(legacy.rawState), ReceivedAt: fixture.now}
			if err = fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
				t.Fatal(err)
			}
			processor := archivedEntrantProcessor(t, fixture)
			if processed, processErr := processor.ProcessOnce(fixture.ctx, "unavailable-assignee-"+channelStatus, 1); processErr != nil || processed != 1 {
				t.Fatalf("processed=%d err=%v", processed, processErr)
			}
			var status, errorCode string
			var customers, entrants int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT i.status,COALESCE(r.error_code,'')
				FROM webhook_inbox i JOIN wecom_callback_receipts r ON r.inbox_id=i.id AND r.receipt_kind='processing'
				WHERE i.idempotency_key=$1`, string(key)).Scan(&status, &errorCode); err != nil {
				t.Fatal(err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM channel_acquisition_entrant_receipts)`).Scan(&customers, &entrants); err != nil {
				t.Fatal(err)
			}
			if status != "retryable" || errorCode != "callback_lifecycle" || customers != 0 || entrants != 0 {
				t.Fatalf("status=%s error=%s customers=%d entrants=%d", status, errorCode, customers, entrants)
			}
		})
	}
}

// TestArchivedVerifiedLegacyChannelCompletesEntrantWithoutEffects proves the
// same rule for the synthetic State-binding path. Its asset_version deliberately
// differs from the legacy asset row: legacy fallback is authorized by the
// verified, unretired asset rather than numerical ID similarity.
func TestArchivedVerifiedLegacyChannelCompletesEntrantWithoutEffects(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()

	states := channel.NewPostgreSQLStore()
	digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := seedChannelWelcomeFixture(t, fixture.ctx, fixture.unit, states, digester, 1, "archived-legacy", "archived-legacy-state", false, 2_000_000_019, 1)
	if err = seedVerifiedLegacyAsset(fixture.ctx, fixture.native, legacy.resolution.Asset.ChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.native.Exec(fixture.ctx, `UPDATE channels SET status='archived',archived_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, legacy.resolution.Asset.ChannelID); err != nil {
		t.Fatal(err)
	}
	key := mustChannelWelcomeKey(t, "wecom:external-contact:archived-legacy-entrant-0001")
	input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(legacy.rawState), ReceivedAt: fixture.now}
	if err = fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
		t.Fatal(err)
	}
	processor := archivedEntrantProcessor(t, fixture)
	if processed, processErr := processor.ProcessOnce(fixture.ctx, "archived-legacy-entrant", 1); processErr != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, processErr)
	}
	var inboxStatus, resultCodes string
	var assignments, actions, tagCommands, effects int
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT i.status,array_to_string(r.result_codes,',')
		FROM webhook_inbox i JOIN wecom_callback_receipts r ON r.inbox_id=i.id AND r.receipt_kind='processing'
		WHERE i.idempotency_key=$1`, string(key)).Scan(&inboxStatus, &resultCodes); err != nil {
		t.Fatal(err)
	}
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM channel_entrant_assignments),(SELECT count(*) FROM channel_entrant_actions),(SELECT count(*) FROM customer_tag_commands),(SELECT count(*) FROM external_effects)`).Scan(&assignments, &actions, &tagCommands, &effects); err != nil {
		t.Fatal(err)
	}
	if inboxStatus != "processed" || !containsAll(resultCodes, "channel_attributed", "ignored") || assignments != 0 || actions != 0 || tagCommands != 0 || effects != 0 {
		t.Fatalf("inbox=%s results=%q assignments=%d actions=%d tag_commands=%d effects=%d", inboxStatus, resultCodes, assignments, actions, tagCommands, effects)
	}
}

// TestUnavailableAssetStaysRetryableEvenWhenChannelIsInactive proves that only
// the explicitly typed inactive/archived result is swallowed. A real Channel
// store with an active State binding that has neither a runtime nor verified
// legacy asset still goes through the original retryable callback_lifecycle path.
func TestUnavailableAssetStaysRetryableEvenWhenChannelIsInactive(t *testing.T) {
	for _, channelStatus := range []string{"active", "inactive", "archived"} {
		t.Run(channelStatus, func(t *testing.T) {
			fixture := newChannelWelcomeRuntimeFixture(t)
			defer fixture.close()

			states := channel.NewPostgreSQLStore()
			digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
			if err != nil {
				t.Fatal(err)
			}
			missing := seedChannelWelcomeFixture(t, fixture.ctx, fixture.unit, states, digester, 1, "missing-asset-"+channelStatus, "missing-asset-state-"+channelStatus, false, 2, 0)
			if _, err = fixture.native.Exec(fixture.ctx, `UPDATE channels SET status=$2,archived_at=CASE WHEN $2='archived' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE id=$1`, missing.resolution.Asset.ChannelID, channelStatus); err != nil {
				t.Fatal(err)
			}
			key := mustChannelWelcomeKey(t, "wecom:external-contact:missing-asset-"+channelStatus+"-0001")
			input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(missing.rawState), ReceivedAt: fixture.now}
			if err = fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
				t.Fatal(err)
			}
			processor := archivedEntrantProcessor(t, fixture)
			if processed, processErr := processor.ProcessOnce(fixture.ctx, "missing-asset-"+channelStatus, 1); processErr != nil || processed != 1 {
				t.Fatalf("processed=%d err=%v", processed, processErr)
			}
			var status, errorCode string
			var customers, entrants int
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT i.status,COALESCE(r.error_code,'')
				FROM webhook_inbox i JOIN wecom_callback_receipts r ON r.inbox_id=i.id AND r.receipt_kind='processing'
				WHERE i.idempotency_key=$1`, string(key)).Scan(&status, &errorCode); err != nil {
				t.Fatal(err)
			}
			if err = fixture.native.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM channel_acquisition_entrant_receipts)`).Scan(&customers, &entrants); err != nil {
				t.Fatal(err)
			}
			if status != "retryable" || errorCode != "callback_lifecycle" || customers != 0 || entrants != 0 {
				t.Fatalf("status=%s error=%s customers=%d entrants=%d", status, errorCode, customers, entrants)
			}
		})
	}
}

func archivedEntrantProcessor(t *testing.T, fixture *channelWelcomeRuntimeFixture) wecom.InboxProcessor {
	t.Helper()
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	states := channel.NewPostgreSQLStore()
	return wecom.InboxProcessor{
		Enabled: true, CorpID: "wx-corp", Inbox: fixture.dispatch.Inbox, UOW: fixture.unit,
		Lifecycle: wecom.ExternalContactLifecycle{
			Identity:      identityapp.OneIDService{Store: identitystore.NewPostgresStore()},
			Relationships: wecom.NewPostgreSQLFollowRelationshipStore(), States: states, Entrants: states,
			Actions: fixture.actions,
		},
		Receipts: wecom.NewPostgreSQLCallbackReceiptStore(), Audit: audit,
	}
}

func reactivateArchivedFixtureChannel(t *testing.T, fixture *channelWelcomeRuntimeFixture, channelID int64) {
	t.Helper()
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	store := channel.NewPostgreSQLCatalogStore()
	events, err := channel.NewChannelCatalogEventAppender(audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service := channel.NewCatalogService(fixture.unit, store, store, events, nil, nil, archivedEntrantStaffReader{})
	current, err := service.Get(fixture.ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := service.Update(fixture.ctx, channelID, channel.CatalogMutation{
		ActorID: 1, IdempotencyKey: "archived-entrant-reactivate-0001",
		Update: channeldomain.UpdateChannel{ExpectedVersion: current.Version, Code: current.Code, Status: channeldomain.StatusActive, Config: current.Config},
	})
	if err != nil || active.Status != channeldomain.StatusActive || active.Version != current.Version+1 {
		t.Fatalf("reactivate=%+v err=%v", active, err)
	}
}

type archivedEntrantStaffReader struct{}

func (archivedEntrantStaffReader) ReadChannelStaff(_ context.Context, ids []int64) ([]channelport.StaffSnapshot, error) {
	result := make([]channelport.StaffSnapshot, len(ids))
	for index, id := range ids {
		result[index] = channelport.StaffSnapshot{ID: id, Active: true}
	}
	return result, nil
}

func seedVerifiedLegacyAsset(ctx context.Context, native *pgxpool.Pool, channelID int64) error {
	var importRunID int64
	if err := native.QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at)
		VALUES('archived-legacy-fixture',decode(repeat('00',32),'hex'),clock_timestamp(),decode(repeat('00',32),'hex'),'reconciled',clock_timestamp()) RETURNING id`).Scan(&importRunID); err != nil {
		return err
	}
	return native.QueryRow(ctx, `INSERT INTO channel_legacy_acquisition_assets(import_run_id,source_asset_id,channel_id,config_version,asset_version,kind,provider_asset_ref,result_url,source_status,verification_status,source_digest,provider_readback_digest,verified_at)
		VALUES($1,1,$2,1,1000000009,'contact_way_qrcode','legacy-provider-ref','https://wework.example/legacy.png','active','legacy_verified_active',decode(repeat('00',32),'hex'),'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',clock_timestamp()) RETURNING id`, importRunID, channelID).Scan(new(int64))
}

func containsAll(values string, required ...string) bool {
	for _, value := range required {
		if !strings.Contains(","+values+",", ","+value+",") {
			return false
		}
	}
	return true
}
