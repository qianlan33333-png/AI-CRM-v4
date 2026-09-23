package main

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	channel "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestCustomerSyncAndWelcomeShareOneCustomerWithoutReplayingSend(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()
	var serverVersion int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if serverVersion < 160000 || serverVersion >= 170000 {
		t.Fatalf("PostgreSQL server_version_num=%d; want 16.x", serverVersion)
	}

	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1)}
	input, _ := fixture.acceptWelcome(t, "customer-welcome-joint-0001", fixture.now)
	_, stopWelcome := fixture.startRuntime(t, writer, nil, true)
	select {
	case <-writer.called:
	case <-time.After(5 * time.Second):
		stopWelcome()
		t.Fatal("welcome did not execute before customer creation")
	}
	fixture.waitEffect(t, input, "executed")
	stopWelcome()

	var identitiesBefore int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM customer_identities WHERE kind='wecom_external_userid' AND scope_key='wecom-corp:wx-corp' AND normalized_value='external-1'`).Scan(&identitiesBefore); err != nil || identitiesBefore != 0 {
		t.Fatalf("identities before ordinary callback=%d err=%v", identitiesBefore, err)
	}
	var beforeSource, beforeTarget, beforePayload, beforePolicy []byte
	var beforeEffectRef string
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT i.effect_ref,e.source_ref_digest,e.target_ref_digest,e.payload_digest,e.policy_version_hash FROM channel_welcome_intents i JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&beforeEffectRef, &beforeSource, &beforeTarget, &beforePayload, &beforePolicy); err != nil {
		t.Fatal(err)
	}

	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	customerStore := customerstore.NewPostgreSQL()
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	relationships := wecom.NewPostgreSQLFollowRelationshipStore()
	states := channel.NewPostgreSQLStore()
	processor := wecom.InboxProcessor{
		Enabled: true, CorpID: "wx-corp", Inbox: fixture.dispatch.Inbox, UOW: fixture.unit,
		Lifecycle: wecom.ExternalContactLifecycle{
			Identity: oneID, Relationships: relationships, States: states, Entrants: states,
			Actions: fixture.actions, Directory: customerStore, Outbox: platformoutbox.NewPostgreSQL(),
		},
		Receipts: wecom.NewPostgreSQLCallbackReceiptStore(), Audit: audit,
	}
	if processed, processErr := processor.ProcessOnce(fixture.ctx, "customer-welcome-joint", 1); processErr != nil || processed != 1 {
		t.Fatalf("ordinary callback processed=%d err=%v", processed, processErr)
	}

	workers := river.NewWorkers()
	insertWorker := wecom.NewCustomerSyncWorker()
	if err = river.AddWorkerSafely[wecom.CustomerSyncJobArgs](workers, insertWorker); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(fixture.native, workers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := wecom.NewRiverCustomerSyncEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	syncService := wecom.CustomerSyncService{
		Enabled: true, CorpID: "wx-corp", Provider: customerWelcomeDirectoryProvider{},
		Identity:   oneID,
		Projection: customerStore, Timeline: customerStore,
		Store: wecom.NewPostgreSQLCustomerSyncStore(), Outbox: platformoutbox.NewPostgreSQL(),
		Enqueuer: enqueuer, Audit: audit, UOW: fixture.unit,
	}
	if err = insertWorker.BindService(syncService); err != nil {
		t.Fatal(err)
	}
	run, _, err := syncService.CreateScheduled(fixture.ctx, "initial", "customer-welcome-joint-sync")
	if err != nil {
		t.Fatal(err)
	}
	stopSync := startCustomerSyncRuntime(t, fixture.ctx, fixture.native, syncService)
	waitCustomerSyncStatus(t, fixture.ctx, syncService, run.ID, wecom.SyncSucceeded)
	stopSync()

	var customerID int64
	var roots, relationshipCount int
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT min(customer_id),count(DISTINCT customer_id) FROM customer_identities WHERE kind='wecom_external_userid' AND scope_key='wecom-corp:wx-corp' AND normalized_value='external-1'`).Scan(&customerID, &roots); err != nil {
		t.Fatal(err)
	}
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM wecom_customer_owner_observations WHERE customer_id=$1 AND relationship_status='active'`, customerID).Scan(&relationshipCount); err != nil {
		t.Fatal(err)
	}
	if roots != 1 || relationshipCount != 2 {
		t.Fatalf("customer=%d roots=%d relationships=%d", customerID, roots, relationshipCount)
	}
	if err = fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
		t.Fatal(err)
	}
	_, stopRestarted := fixture.startRuntime(t, writer, nil, true)
	time.Sleep(300 * time.Millisecond)
	stopRestarted()

	var linkedCustomer int64
	var effectRef, effectState string
	var source, target, payload, policy []byte
	var attempts int
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT i.customer_id,i.effect_ref,e.state,e.source_ref_digest,e.target_ref_digest,e.payload_digest,e.policy_version_hash,(SELECT count(*) FROM external_effect_attempts a WHERE a.effect_id=e.id) FROM channel_welcome_intents i JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&linkedCustomer, &effectRef, &effectState, &source, &target, &payload, &policy, &attempts); err != nil {
		t.Fatal(err)
	}
	if linkedCustomer != customerID || effectRef != beforeEffectRef || effectState != "executed" || string(source) != string(beforeSource) || string(target) != string(beforeTarget) || string(payload) != string(beforePayload) || string(policy) != string(beforePolicy) || attempts != 1 || writer.calls() != 1 {
		t.Fatalf("linked=%d/%d effect=%s/%s state=%s attempts=%d sends=%d", linkedCustomer, customerID, effectRef, beforeEffectRef, effectState, attempts, writer.calls())
	}
}

type customerWelcomeDirectoryProvider struct{}

func (customerWelcomeDirectoryProvider) DirectoryReady() bool { return true }
func (customerWelcomeDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return []string{"employee-1", "employee-2"}, nil
}
func (customerWelcomeDirectoryProvider) BatchExternalContacts(_ context.Context, staffID, cursor string, _ int) (wecomport.ExternalContactPage, error) {
	if cursor != "" {
		return wecomport.ExternalContactPage{}, nil
	}
	return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{{ExternalUserID: "external-1", Name: "Joint Customer", Gender: 1, Type: 1, FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: staffID}}}}}, nil
}
