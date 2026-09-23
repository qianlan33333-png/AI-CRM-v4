package wecom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestPostgreSQLArchiveCallbackReplayKeepsFirstReceipt(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	crypto, err := NewCallbackCrypto("archive-replay-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-archive-replay")
	if err != nil {
		t.Fatal(err)
	}
	providerNow := time.Unix(1_788_336_000, 0).UTC()
	crypto.now = func() time.Time { return providerNow }
	arrival := providerNow.Add(5 * time.Second)
	handler := CallbackHandler{
		Enabled: true, Crypto: crypto, Inbox: inbox, UOW: unit,
		Dispatcher: CallbackEventDispatcher{Archive: ArchiveCallbackDispatcher{Inbox: inbox, UOW: unit, ExpectedAgentID: 1000002}},
		Now:        func() time.Time { return arrival },
	}
	plain := []byte("<xml><ToUserName>wx-archive-replay</ToUserName><FromUserName>sys</FromUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><AgentID>1000002</AgentID><Event>msgaudit_notify</Event></xml>")
	if status := serveArchiveCallback(t, handler, crypto, plain); status != http.StatusOK {
		t.Fatalf("first callback status=%d", status)
	}
	var firstReceived time.Time
	var firstPayload []byte
	if err = pool.Native().QueryRow(ctx, `SELECT received_at,payload FROM webhook_inbox WHERE provider=$1`, archiveCallbackProvider).Scan(&firstReceived, &firstPayload); err != nil {
		t.Fatal(err)
	}
	var canonical map[string]string
	if err = json.Unmarshal(firstPayload, &canonical); err != nil || canonical["corp_id"] != "wx-archive-replay" || canonical["event"] != "msgaudit_notify" || len(canonical) != 2 {
		t.Fatalf("canonical payload=%s", firstPayload)
	}
	arrival = arrival.Add(3 * time.Second)
	if status := serveArchiveCallback(t, handler, crypto, plain); status != http.StatusOK {
		t.Fatalf("replayed callback status=%d", status)
	}
	var count int
	var replayReceived time.Time
	if err = pool.Native().QueryRow(ctx, `SELECT count(*),min(received_at) FROM webhook_inbox WHERE provider=$1`, archiveCallbackProvider).Scan(&count, &replayReceived); err != nil || count != 1 || !replayReceived.Equal(firstReceived) {
		t.Fatalf("rows=%d received=%s first=%s err=%v", count, replayReceived, firstReceived, err)
	}
	drift := []byte("<xml><ToUserName>wx-archive-replay</ToUserName><FromUserName>sys</FromUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><AgentID>1000003</AgentID><Event>msgaudit_notify</Event></xml>")
	if status := serveArchiveCallback(t, handler, crypto, drift); status == http.StatusOK {
		t.Fatal("canonical provider-fact drift was acknowledged")
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM webhook_inbox WHERE provider=$1`, archiveCallbackProvider).Scan(&count); err != nil || count != 1 {
		t.Fatalf("drift inbox rows=%d err=%v", count, err)
	}
}

func serveArchiveCallback(t *testing.T, handler CallbackHandler, crypto *CallbackCrypto, plain []byte) int {
	t.Helper()
	encrypted := encryptForTest(t, crypto, plain)
	timestamp := "1788336000"
	query := "?msg_signature=" + callbackSignature("archive-replay-token", timestamp, "replay-nonce", encrypted) + "&timestamp=" + timestamp + "&nonce=replay-nonce"
	body := "<xml><ToUserName>wx-archive-replay</ToUserName><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader(body)))
	return response.Code
}

func TestPostgreSQLWeComStoresIntegration(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("oauth state nonce and transaction boundary", func(t *testing.T) {
		store := NewPostgreSQLOAuthStateStore()
		now := time.Now().UTC()
		stateDigest := sha256.Sum256([]byte("state"))
		nonceDigest := sha256.Sum256([]byte("nonce"))
		state := OAuthState{Purpose: OAuthSidebar, Redirect: "/sidebar", ExpiresAt: now.Add(time.Minute)}
		if err := store.Create(ctx, state, stateDigest, nonceDigest); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("transaction boundary error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, state, stateDigest, nonceDigest)
		}); err != nil {
			t.Fatal(err)
		}
		wrongNonce := sha256.Sum256([]byte("wrong nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, wrongNonce, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("wrong nonce error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			consumed, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			if consumeErr == nil && consumed.Redirect != "/sidebar" {
				return errors.New("unexpected consumed redirect")
			}
			return consumeErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("state replay error=%v", err)
		}
		expiredState := sha256.Sum256([]byte("expired state"))
		expiredNonce := sha256.Sum256([]byte("expired nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, OAuthState{Purpose: OAuthAdmin, Redirect: "/admin", ExpiresAt: now.Add(time.Second)}, expiredState, expiredNonce)
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthAdmin, expiredState, expiredNonce, now.Add(2*time.Second))
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("expired state error=%v", err)
		}
	})

	t.Run("follow relationships are employee scoped and same-second deletion wins", func(t *testing.T) {
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		store := NewPostgreSQLFollowRelationshipStore()
		base := time.Unix(1_788_336_000, 0).UTC()
		first := CallbackFollowRelationship{CallbackID: "callback-100", CorpID: "wx-corp", EmployeeID: "employee-one", CustomerID: customerdomain.CustomerID(customerID), Active: true, OccurredAt: base}
		if _, err := store.ApplyCallbackEvent(ctx, first); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("relationship transaction boundary error=%v", err)
		}
		var firstApplication FollowRelationshipApplication
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var applyErr error
			firstApplication, applyErr = store.ApplyCallbackEvent(txContext, first)
			return applyErr
		}); err != nil || !firstApplication.Applied || !firstApplication.Active {
			t.Fatalf("first application=%+v err=%v", firstApplication, err)
		}
		second := first
		second.CallbackID = "callback-101"
		second.EmployeeID = "employee-two"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, second)
			if applyErr == nil && (!application.Applied || !application.Active) {
				return errors.New("second employee was not activated")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}

		deleteOne := first
		deleteOne.CallbackID = "callback-200"
		deleteOne.Active = false
		deleteOne.OccurredAt = base.Add(20 * time.Second)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, deleteOne)
			if applyErr == nil && (!application.Applied || application.Active) {
				return errors.New("employee-one was not deactivated")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		lateAdd := first
		lateAdd.CallbackID = "callback-150"
		lateAdd.OccurredAt = base.Add(10 * time.Second)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, lateAdd)
			if applyErr == nil && (application.Applied || application.Active) {
				return errors.New("older add reactivated employee-one")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := store.IsActive(txContext, "wx-corp", "employee-two", customerdomain.CustomerID(customerID))
			if activeErr == nil && !active {
				return errors.New("employee-one deletion changed employee-two")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}

		sameSecondDelete := second
		sameSecondDelete.CallbackID = "callback-delete-same-second"
		sameSecondDelete.Active = false
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, sameSecondDelete)
			if applyErr == nil && (!application.Applied || application.Active) {
				return errors.New("same-second deletion did not win")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		sameSecondAdd := second
		sameSecondAdd.CallbackID = "zzzz-callback-add-hash-order-must-not-win"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, sameSecondAdd)
			if applyErr == nil && (application.Applied || application.Active) {
				return errors.New("same-second callback hash reactivated relationship")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown delete cursor rejects older add before OneID exists", func(t *testing.T) {
		store := NewPostgreSQLFollowRelationshipStore()
		identityDigest := sha256.Sum256([]byte("protected external identity"))
		deleted := CallbackExternalContactEvent{CallbackID: "delete-newer", CorpID: "wx-corp", EmployeeID: "employee-one", ExternalIdentityDigest: identityDigest, Active: false, OccurredAt: time.Unix(400, 0).UTC()}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			admission, admitErr := store.AdmitExternalContactEvent(txContext, deleted)
			if admitErr == nil && (!admission.Admitted || !admission.Advanced || admission.Active) {
				return errors.New("unknown delete cursor was not retained")
			}
			return admitErr
		}); err != nil {
			t.Fatal(err)
		}
		olderAdd := deleted
		olderAdd.CallbackID = "add-older"
		olderAdd.Active = true
		olderAdd.OccurredAt = time.Unix(300, 0).UTC()
		if err := unit.Within(ctx, func(txContext context.Context) error {
			admission, admitErr := store.AdmitExternalContactEvent(txContext, olderAdd)
			if admitErr == nil && (admission.Admitted || admission.Active) {
				return errors.New("older activation passed delete cursor")
			}
			return admitErr
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("first identity cursor insert is concurrent idempotent", func(t *testing.T) {
		store := NewPostgreSQLFollowRelationshipStore()
		identityDigest := sha256.Sum256([]byte("concurrent protected identity"))
		const workers = 16
		errorsFound := make(chan error, workers)
		var group sync.WaitGroup
		for index := 0; index < workers; index++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				event := CallbackExternalContactEvent{
					CallbackID: "concurrent-callback-" + strconv.Itoa(worker), CorpID: "wx-corp",
					EmployeeID: "employee-concurrent", ExternalIdentityDigest: identityDigest,
					Active: true, OccurredAt: time.Unix(500, 0).UTC(),
				}
				errorsFound <- unit.Within(ctx, func(txContext context.Context) error {
					admission, admitErr := store.AdmitExternalContactEvent(txContext, event)
					if admitErr == nil && !admission.Admitted {
						return errors.New("equivalent same-second event was not admitted")
					}
					return admitErr
				})
			}(index)
		}
		group.Wait()
		close(errorsFound)
		for err := range errorsFound {
			if err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_external_contact_event_cursors WHERE corp_id='wx-corp' AND employee_id='employee-concurrent' AND external_identity_digest=$1`, identityDigest[:]).Scan(&count); err != nil || count != 1 {
			t.Fatalf("cursor rows=%d err=%v", count, err)
		}
	})

	t.Run("callback receipts are immutable concurrent-idempotent and retry intent is exact", func(t *testing.T) {
		store := NewPostgreSQLCallbackReceiptStore()
		processedInboxID := insertWeComInbox(t, ctx, pool, "receipt-processed", "processing", 1)
		processed := AppendCallbackProcessingReceipt{
			InboxID: processedInboxID, AttemptNumber: 1,
			CommandDigest: sha256.Sum256([]byte("processed-command")),
			EventType:     "change_external_contact", ChangeType: "add_external_contact",
			ResultingInboxStatus: "processed",
			ResultCodes:          []CallbackResultCode{CallbackRelationshipActivated, CallbackCustomerCreated},
		}
		if _, _, err := store.AppendProcessing(ctx, processed); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("processing receipt transaction boundary error=%v", err)
		}
		var processedReceipt CallbackReceipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var created bool
			var appendErr error
			processedReceipt, created, appendErr = store.AppendProcessing(txContext, processed)
			if appendErr == nil && !created {
				return errors.New("first processing receipt was a replay")
			}
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		if processedReceipt.EventType != "change_external_contact" {
			t.Fatalf("event type=%q", processedReceipt.EventType)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, appendErr := store.AppendProcessing(txContext, processed)
			if appendErr == nil && created {
				return errors.New("processing replay inserted a second fact")
			}
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		drift := processed
		drift.ResultCodes = []CallbackResultCode{CallbackCustomerResolved}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, appendErr := store.AppendProcessing(txContext, drift)
			return appendErr
		}); !errors.Is(err, ErrCallbackReceiptConflict) {
			t.Fatalf("processing drift error=%v", err)
		}

		failedInboxID := insertWeComInbox(t, ctx, pool, "receipt-failed", "failed", 2)
		failed := AppendCallbackProcessingReceipt{
			InboxID: failedInboxID, AttemptNumber: 2,
			CommandDigest: sha256.Sum256([]byte("failed-command")),
			EventType:     "change_external_contact", ChangeType: "edit_external_contact",
			ResultingInboxStatus: "failed", ResultCodes: []CallbackResultCode{CallbackFailedTerminal},
			ErrorCode: "identity_conflict_terminal",
		}
		var failedReceipt CallbackReceipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var appendErr error
			failedReceipt, _, appendErr = store.AppendProcessing(txContext, failed)
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		actorID := insertAdminUser(t, ctx, pool)
		retry := BeginCallbackRetry{
			TargetReceiptID: failedReceipt.ID, ExpectedAttempt: 2,
			ExpectedInboxStatus: "failed", ActorAdminUserID: actorID,
			Reason:             "configuration corrected",
			OperationKeyDigest: sha256.Sum256([]byte("retry-key")),
			CommandDigest:      sha256.Sum256([]byte("retry-command")),
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, beginErr := store.BeginRetry(txContext, retry)
			if beginErr == nil && !created {
				return errors.New("first retry intent was a replay")
			}
			// Production composition must call platform webhook Retry CAS here in
			// this same UOW. This Store test exercises only the WeCom-owned fact.
			return beginErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, beginErr := store.BeginRetry(txContext, retry)
			if beginErr == nil && created {
				return errors.New("retry replay inserted a second operation")
			}
			return beginErr
		}); err != nil {
			t.Fatal(err)
		}
		retryDrift := retry
		retryDrift.Reason = "different reason"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, beginErr := store.BeginRetry(txContext, retryDrift)
			return beginErr
		}); !errors.Is(err, ErrCallbackReceiptConflict) {
			t.Fatalf("retry drift error=%v", err)
		}

		concurrentInboxID := insertWeComInbox(t, ctx, pool, "receipt-concurrent", "processing", 1)
		concurrent := processed
		concurrent.InboxID = concurrentInboxID
		concurrent.CommandDigest = sha256.Sum256([]byte("concurrent-command"))
		type concurrentResult struct {
			created bool
			err     error
		}
		results := make(chan concurrentResult, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				var created bool
				err := unit.Within(ctx, func(txContext context.Context) error {
					var appendErr error
					_, created, appendErr = store.AppendProcessing(txContext, concurrent)
					return appendErr
				})
				results <- concurrentResult{created: created, err: err}
			}()
		}
		wait.Wait()
		close(results)
		createdCount := 0
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.created {
				createdCount++
			}
		}
		if createdCount != 1 {
			t.Fatalf("concurrent created count=%d", createdCount)
		}
		if _, err := pool.Native().Exec(ctx, `UPDATE wecom_callback_receipts SET error_code='tampered' WHERE id=$1`, processedReceipt.ID); err == nil {
			t.Fatal("append-only callback receipt accepted UPDATE")
		}
		if _, err := pool.Native().Exec(ctx, `DELETE FROM wecom_callback_receipts WHERE id=$1`, processedReceipt.ID); err == nil {
			t.Fatal("append-only callback receipt accepted DELETE")
		}
		if _, err := pool.Native().Exec(ctx, `TRUNCATE wecom_callback_receipts`); err == nil {
			t.Fatal("append-only callback receipt accepted TRUNCATE")
		}
	})

	t.Run("processor commits relationship receipt audit and Inbox together", func(t *testing.T) {
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
		if err != nil {
			t.Fatal(err)
		}
		auditor, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
		if err != nil {
			t.Fatal(err)
		}
		key, _ := idempotency.Parse("wecom:external-contact:processor-integration-0001")
		payload := json.RawMessage(`{"corp_id":"wx-corp","to_user_name":"wx-corp","msg_type":"event","event":"change_external_contact","change_type":"add_external_contact","external_userid":"processor-external","userid":"processor-employee","state_present":false,"create_time":1788336000,"msg_id_present":false,"welcome_code_present":false,"source_present":false,"fail_reason_present":false}`)
		if err = unit.Within(ctx, func(txContext context.Context) error {
			_, ingestErr := inbox.Ingest(txContext, webhook.Ingest{Provider: callbackProvider, IdempotencyKey: key, Payload: payload})
			return ingestErr
		}); err != nil {
			t.Fatal(err)
		}
		relationships := NewPostgreSQLFollowRelationshipStore()
		processor := InboxProcessor{
			Enabled: true, CorpID: "wx-corp", Inbox: inbox, UOW: unit,
			Lifecycle: ExternalContactLifecycle{
				Identity:      canonicalLifecycleIdentity{customerID: customerdomain.CustomerID(customerID)},
				Relationships: relationships, States: &lifecycleStates{}, Entrants: &lifecycleReceipts{},
			},
			Receipts: NewPostgreSQLCallbackReceiptStore(), Audit: auditor,
		}
		if count, processErr := processor.ProcessOnce(ctx, "processor-integration", 1); processErr != nil || count != 1 {
			t.Fatalf("processed=%d err=%v", count, processErr)
		}
		var status string
		var receiptCount, auditCount int
		if err = pool.Native().QueryRow(ctx, `SELECT status FROM webhook_inbox WHERE idempotency_key=$1`, key).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_callback_receipts receipt JOIN webhook_inbox inbox ON inbox.id=receipt.inbox_id WHERE inbox.idempotency_key=$1 AND receipt.receipt_kind='processing'`, key).Scan(&receiptCount); err != nil {
			t.Fatal(err)
		}
		if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='wecom.callback_processed'`).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if status != string(webhook.StatusProcessed) || receiptCount != 1 || auditCount != 1 {
			t.Fatalf("status=%s receipts=%d audits=%d", status, receiptCount, auditCount)
		}
		if err = unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := relationships.IsActive(txContext, "wx-corp", "processor-employee", customerdomain.CustomerID(customerID))
			if activeErr == nil && !active {
				return errors.New("follow relationship is not active")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgreSQLAudienceContactsUseRelationshipFacts(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	var runID, firstIdentityID, secondIdentityID int64
	if err := pool.Native().QueryRow(ctx, `
		INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at)
		VALUES('audience-contact-fixture','manual','succeeded','wecom-corp:test',$1)
		RETURNING id`, now).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:test','external-first','verified','test-fixture',1,$2)
		RETURNING id`, first, now).Scan(&firstIdentityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.Native().QueryRow(ctx, `
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:test','external-second','verified','test-fixture',1,$2)
		RETURNING id`, second, now).Scan(&secondIdentityID); err != nil {
		t.Fatal(err)
	}
	profileDigest := sha256.Sum256([]byte("audience-contact-profile"))
	if _, err := pool.Native().Exec(ctx, `
		INSERT INTO wecom_external_contact_profiles(
			customer_id,corp_scope,external_identity_id,activation_status,profile_digest,
			last_seen_run_id,fetched_at,stale_at,updated_at
		) VALUES
			($1,'wecom-corp:test',$2,'active',$3,$4,$5,NULL,$5),
			($6,'wecom-corp:test',$7,'stale',$3,$4,$5,$5,$5)`,
		first, firstIdentityID, profileDigest[:], runID, now, second, secondIdentityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active,created_at,updated_at) VALUES('wx','owner-a',$1,true,$2,$3),('wx','owner-b',$4,true,$2,$3)`, first, now.Add(-48*time.Hour), now, second); err != nil {
		t.Fatal(err)
	}
	var facts []wecomport.AudienceContact
	if err := unit.Within(ctx, func(tx context.Context) error {
		var e error
		facts, e = PostgreSQLFollowRelationshipStore{}.AudienceContacts(tx, now.Add(time.Hour))
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 || facts[0].CustomerID != customerdomain.CustomerID(first) || facts[0].OwnerUserID != "owner-a" || facts[0].Status != "active" || !facts[0].ObservedAt.Equal(now.Add(-48*time.Hour)) || facts[1].Status != "deleted" {
		t.Fatalf("facts=%+v", facts)
	}
	// Recognition does not require an active follow relationship, but remains
	// scope-bound and excludes conflicting profiles.
	if _, err := pool.Native().Exec(ctx, `DELETE FROM wecom_follow_relationships WHERE customer_id IN ($1,$2)`, first, second); err != nil {
		t.Fatal(err)
	}
	if err := unit.Within(ctx, func(tx context.Context) error {
		ids, e := (PostgreSQLFollowRelationshipStore{}).AudienceRecognizedContacts(tx, "wecom-corp:test", now.Add(time.Hour))
		if e != nil {
			return e
		}
		if len(ids) != 2 {
			t.Fatalf("recognized=%d", len(ids))
		}
		ids, e = (PostgreSQLFollowRelationshipStore{}).AudienceRecognizedContacts(tx, "wecom-corp:other", now.Add(time.Hour))
		if e != nil {
			return e
		}
		if len(ids) != 0 {
			t.Fatal("cross-corp recognition")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Native().Exec(ctx, `UPDATE wecom_external_contact_profiles SET activation_status='conflict',stale_at=NULL WHERE customer_id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if err := unit.Within(ctx, func(tx context.Context) error {
		ids, e := (PostgreSQLFollowRelationshipStore{}).AudienceRecognizedContacts(tx, "wecom-corp:test", now.Add(time.Hour))
		if e != nil {
			return e
		}
		if len(ids) != 1 || int64(ids[0]) != first {
			t.Fatal("conflict must not qualify")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

}

func TestPostgreSQLAudiencePrimaryOwnersUseCompletedTrustedFollowScopes(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgreSQLCustomerSyncStore()
	tooMany := make([]customerdomain.CustomerID, maximumAudiencePrimaryOwnerBatch+1)
	if err = unit.Within(ctx, func(tx context.Context) error {
		_, readErr := store.AudiencePrimaryOwners(tx, tooMany)
		return readErr
	}); !errors.Is(err, ErrSyncCAS) {
		t.Fatalf("oversized audience owner batch=%v", err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	var primaryCustomer, unknownCustomer, conflictingCustomer int64
	for _, destination := range []*int64{&primaryCustomer, &unknownCustomer, &conflictingCustomer} {
		if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}

	insertRun := func(key, scope string, status CustomerSyncStatus, completedAt *time.Time) int64 {
		t.Helper()
		var id int64
		if err = pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at)
			VALUES($1,'manual',$2,$3,$4) RETURNING id`, key, status, scope, completedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertIdentity := func(customerID int64, value string) int64 {
		t.Helper()
		var id int64
		if err = pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'wecom_external_userid','wecom-corp:test',$2,'verified','test-fixture',1,$3) RETURNING id`, customerID, value, now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertProfile := func(customerID, identityID, runID int64, scope string) {
		t.Helper()
		digest := sha256.Sum256([]byte("primary-owner-profile-" + strconv.FormatInt(customerID, 10)))
		if _, err = pool.Native().Exec(ctx, `INSERT INTO wecom_external_contact_profiles(
			customer_id,corp_scope,external_identity_id,activation_status,profile_digest,last_seen_run_id,fetched_at,updated_at
		) VALUES($1,$2,$3,'active',$4,$5,$6,$6)`, customerID, scope, identityID, digest[:], runID, now); err != nil {
			t.Fatal(err)
		}
	}

	scope := "wecom-corp:test"
	primaryRun := insertRun("primary-owner-run-1", scope, SyncReconciling, nil)
	insertProfile(primaryCustomer, insertIdentity(primaryCustomer, "primary-owner-external"), primaryRun, scope)
	if err = unit.Within(ctx, func(tx context.Context) error {
		// The request employee is intentionally absent: only the provider's
		// trusted follow_user records participate in the primary selection.
		if e := store.UpsertProfileObservations(tx, primaryRun, scope, customerdomain.CustomerID(primaryCustomer), []wecomport.ExternalContactFollowInfo{{EmployeeID: "zara"}, {EmployeeID: "bob"}}, now); e != nil {
			return e
		}
		if e := store.ReconcileProfileObservations(tx, primaryRun, now); e != nil {
			return e
		}
		if e := store.RefreshProfilePrimaryOwners(tx, primaryRun, now); e != nil {
			return e
		}
		return store.Complete(tx, primaryRun, 1, 0)
	}); err != nil {
		t.Fatal(err)
	}
	var profilePrimary string
	var primaryRunID int64
	if err = pool.Native().QueryRow(ctx, `SELECT primary_owner_userid,primary_owner_run_id FROM wecom_external_contact_profiles WHERE customer_id=$1`, primaryCustomer).Scan(&profilePrimary, &primaryRunID); err != nil {
		t.Fatal(err)
	}
	if profilePrimary != "bob" || primaryRunID != primaryRun {
		t.Fatalf("profile primary=%q run=%d", profilePrimary, primaryRunID)
	}

	// A later complete scope with no provider follow users stales its
	// observations but preserves the old primary, exactly as the donor does.
	secondRun := insertRun("primary-owner-run-2", scope, SyncReconciling, nil)
	if _, err = pool.Native().Exec(ctx, `UPDATE wecom_external_contact_profiles SET last_seen_run_id=$2 WHERE customer_id=$1`, primaryCustomer, secondRun); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		if e := store.ReconcileProfileObservations(tx, secondRun, now.Add(time.Hour)); e != nil {
			return e
		}
		if e := store.RefreshProfilePrimaryOwners(tx, secondRun, now.Add(time.Hour)); e != nil {
			return e
		}
		return store.Complete(tx, secondRun, 1, 0)
	}); err != nil {
		t.Fatal(err)
	}

	completed := now.Add(-time.Hour)
	unknownRun := insertRun("primary-owner-legacy", scope, SyncSucceeded, &completed)
	insertProfile(unknownCustomer, insertIdentity(unknownCustomer, "unknown-owner-external"), unknownRun, scope)

	conflictRunOne := insertRun("primary-owner-conflict-one", scope, SyncSucceeded, &completed)
	insertProfile(conflictingCustomer, insertIdentity(conflictingCustomer, "conflict-owner-external"), conflictRunOne, scope)
	if _, err = pool.Native().Exec(ctx, `UPDATE wecom_external_contact_profiles SET primary_owner_userid='bob',primary_owner_run_id=$2 WHERE customer_id=$1`, conflictingCustomer, conflictRunOne); err != nil {
		t.Fatal(err)
	}
	conflictRunTwo := insertRun("primary-owner-conflict-two", "wecom-corp:other", SyncSucceeded, &completed)
	if _, err = pool.Native().Exec(ctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,primary_owner_userid)
		VALUES($1,$2,'zoe','active',$3,$4,'zoe')`, conflictingCustomer, "wecom-corp:other", conflictRunTwo, now); err != nil {
		t.Fatal(err)
	}

	var facts []wecomport.AudiencePrimaryOwner
	if err = unit.Within(ctx, func(tx context.Context) error {
		var readErr error
		facts, readErr = store.AudiencePrimaryOwners(tx, []customerdomain.CustomerID{customerdomain.CustomerID(primaryCustomer), customerdomain.CustomerID(unknownCustomer), customerdomain.CustomerID(conflictingCustomer)})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 || facts[0].CustomerID != customerdomain.CustomerID(primaryCustomer) || facts[0].CorpScope != scope || facts[0].OwnerUserID != "bob" || facts[0].Status != "known" || facts[0].VersionDigest == ([32]byte{}) || facts[1].CustomerID != customerdomain.CustomerID(unknownCustomer) || facts[1].Status != "unknown" || facts[1].VersionDigest != ([32]byte{}) || facts[2].CustomerID != customerdomain.CustomerID(conflictingCustomer) || facts[2].OwnerUserID != "" || facts[2].Status != "ambiguous" || facts[2].VersionDigest != ([32]byte{}) {
		t.Fatalf("primary facts=%+v", facts)
	}
}

func insertWeComInbox(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, suffix, status string, attempts int) int64 {
	t.Helper()
	digest := sha256.Sum256([]byte("payload-" + suffix))
	var id int64
	err := pool.Native().QueryRow(ctx, `
		INSERT INTO webhook_inbox (
			provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at
		) VALUES ('wecom.external_contact', $1, $2, '{}'::jsonb, $3, $4, 8, clock_timestamp())
		RETURNING id`, "wecom:test:"+suffix, digest[:], status, attempts).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAdminUser(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.Native().QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, display_name)
		VALUES ('callback_operator', '$argon2id$fixture', 'Callback Operator')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func wecomIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open PostgreSQL integration database")
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping PostgreSQL integration database")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_wecom_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range wecomMigrationPaths(t) {
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal("apply WeCom integration migration")
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func wecomMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate WeCom integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0003_access.sql"),
		filepath.Join(root, "migrations", "0004_wecom.sql"),
		filepath.Join(root, "migrations", "0005_external_effects.sql"),
		filepath.Join(root, "migrations", "0006_wecom_callback_channel_acquisition.sql"),
		filepath.Join(root, "migrations", "0009_customer_activation.sql"),
		filepath.Join(root, "migrations", "0022_customer_profile_sections.sql"),
		filepath.Join(root, "migrations", "0086_wecom_profile_primary_owner.sql"),
		filepath.Join(root, "migrations", "0093_customer_tag_commands.sql"),
		filepath.Join(root, "migrations", "0153_wecom_customer_detail_projection.sql"),
		filepath.Join(root, "migrations", "0171_wecom_contact_description_source_coverage.sql"),
	}
}
