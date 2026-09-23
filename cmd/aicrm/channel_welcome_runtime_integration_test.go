package main

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	channel "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

func TestChannelWelcomeDedicatedRiverRuntimeJourney(t *testing.T) {
	t.Run("ordinary outbound workers cannot consume welcome window", func(t *testing.T) {
		fixture := newChannelWelcomeRuntimeFixture(t)
		defer fixture.close()
		blocker := newBlockedOutboundAdapter()
		writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 2)}
		runtime, stop := fixture.startRuntime(t, writer, blocker, false)
		defer stop()
		for index := 0; index < 4; index++ {
			label := strconv.Itoa(index)
			if _, _, err := fixture.effects.AcceptAndQueue(fixture.ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("channel-welcome-congestion", "receipt", label), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMessage, SourceRefDigest: effectport.Hash("channel-welcome-congestion", "source", label), TargetRefDigest: effectport.Hash("channel-welcome-congestion", "target", label), PayloadDigest: effectport.Hash("channel-welcome-congestion", "payload", label), PolicyVersionHash: effectport.Hash("channel-welcome-congestion", "policy")}}); err != nil {
				t.Fatal(err)
			}
		}
		for range 4 {
			select {
			case <-blocker.started:
			case <-time.After(5 * time.Second):
				t.Fatal("ordinary outbound workers did not become blocked")
			}
		}
		acceptedAt := time.Now()
		input, deadline := fixture.acceptWelcome(t, "runtime-congested-0001", fixture.now)
		if elapsed := time.Since(acceptedAt); elapsed > time.Second {
			t.Fatalf("callback acceptance under ordinary backlog took %s", elapsed)
		}
		select {
		case call := <-writer.called:
			if !call.at.Before(deadline) || !call.deadline.Equal(deadline) || call.code == "" {
				t.Fatalf("welcome call=%+v deadline=%s", call, deadline)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("welcome worker did not run while %s was saturated", platformjobqueue.OutboundQueue)
		}
		fixture.waitEffect(t, input, effectport.StateExecuted)
		blocker.releaseAll()
		_ = runtime
	})

	t.Run("restart executes once before deadline and expires without call after deadline", func(t *testing.T) {
		fixture := newChannelWelcomeRuntimeFixture(t)
		defer fixture.close()
		writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 3)}
		input, _ := fixture.acceptWelcome(t, "runtime-restart-0001", fixture.now)
		// There is no running River runtime when acceptance commits. Recreate the
		// repository/worker/runtime from the durable rows and execute once.
		_, stop := fixture.startRuntime(t, writer, nil, true)
		select {
		case <-writer.called:
		case <-time.After(5 * time.Second):
			stop()
			t.Fatal("restart did not recover the queued welcome effect")
		}
		fixture.waitEffect(t, input, effectport.StateExecuted)
		stop()
		_, stopAgain := fixture.startRuntime(t, writer, nil, true)
		time.Sleep(300 * time.Millisecond)
		stopAgain()
		if writer.calls() != 1 {
			t.Fatalf("restart replayed terminal welcome provider call count=%d", writer.calls())
		}

		expiredInput, _ := fixture.acceptWelcome(t, "runtime-expired-0001", fixture.now.Add(-25*time.Second))
		_, stopExpired := fixture.startRuntime(t, writer, nil, true)
		fixture.waitEffect(t, expiredInput, effectport.StateFinalFailed)
		stopExpired()
		if writer.calls() != 1 {
			t.Fatalf("expired welcome crossed provider boundary count=%d", writer.calls())
		}
		fixture.assertWelcomeOutcome(t, expiredInput, effectport.StateFinalFailed, "deadline_expired", false)

		// A Provider error after the actual write boundary remains an explicit
		// unknown outcome. It is distinguishable from expiry and never creates a
		// fresh effect or second call.
		unknownWriter := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1), err: wecomport.WrapProviderWriteError(errors.New("timeout"), true)}
		unknownInput, _ := fixture.acceptWelcome(t, "runtime-unknown-0001", fixture.now)
		_, stopUnknown := fixture.startRuntime(t, unknownWriter, nil, true)
		fixture.waitEffect(t, unknownInput, effectport.StateUnknown)
		stopUnknown()
		if unknownWriter.calls() != 1 {
			t.Fatalf("unknown welcome provider call count=%d", unknownWriter.calls())
		}
		fixture.assertWelcomeOutcome(t, unknownInput, effectport.StateUnknown, "outcome_unknown", true)

		// A completed strict Provider rejection is terminal, retains only its safe
		// numeric code, and must not be converted to unknown or retried.
		rejectedWriter := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1), err: wecomport.WrapProviderWriteDispositionWithCode(errors.New("provider rejected"), true, false, false, 40003)}
		rejectedInput, _ := fixture.acceptWelcome(t, "runtime-rejected-0001", fixture.now)
		_, stopRejected := fixture.startRuntime(t, rejectedWriter, nil, true)
		fixture.waitEffect(t, rejectedInput, effectport.StateFinalFailed)
		stopRejected()
		if rejectedWriter.calls() != 1 {
			t.Fatalf("rejected welcome provider call count=%d", rejectedWriter.calls())
		}
		fixture.assertWelcomeOutcome(t, rejectedInput, effectport.StateFinalFailed, "provider_rejected", true)
		var providerCode *int64
		if err := fixture.native.QueryRow(fixture.ctx, `SELECT provider_error_code FROM channel_welcome_intents WHERE callback_id=$1`, rejectedInput.CallbackKey).Scan(&providerCode); err != nil || providerCode == nil || *providerCode != 40003 {
			t.Fatalf("provider rejection code=%v err=%v", providerCode, err)
		}
		_, stopRejectedAgain := fixture.startRuntime(t, rejectedWriter, nil, true)
		time.Sleep(300 * time.Millisecond)
		stopRejectedAgain()
		if rejectedWriter.calls() != 1 {
			t.Fatalf("rejected terminal welcome replayed provider call count=%d", rejectedWriter.calls())
		}
	})
}

func TestChannelWelcomeProviderRejectionCompletionRollsBackAtomically(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()
	input, _ := fixture.acceptWelcome(t, "runtime-rejection-rollback-0001", fixture.now)
	var effectRef string
	var jobID int64
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT i.effect_ref,j.river_job_id FROM channel_welcome_intents i JOIN external_effect_jobs j ON j.effect_id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&effectRef, &jobID); err != nil {
		t.Fatal(err)
	}
	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1), err: wecomport.WrapProviderWriteDispositionWithCode(errors.New("provider rejected"), true, false, false, 40003)}
	reader := channelEntrantActionReaderAdapter{uow: fixture.unit, source: fixture.actions}
	provider := outbound.NewChannelEntrantProvider(reader, reader, fixture.unit, fixture.grants, nil, nil, writer)
	completion, err := outbound.NewChannelEntrantCompletionSink(fixture.actions)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.effects.SetCompletionSink(channelWelcomeCompletionRouter{welcome: completion, failAfter: true}); err != nil {
		t.Fatal(err)
	}
	if err = fixture.effects.RunAttempt(fixture.ctx, effectIDNumber(t, effectRef), 1, jobID, provider); err == nil {
		t.Fatal("expected completion transaction failure")
	}
	if writer.calls() != 1 {
		t.Fatalf("provider calls=%d", writer.calls())
	}
	var effectState, intentState string
	var providerCode *int64
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT e.state,i.state,i.provider_error_code FROM channel_welcome_intents i JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&effectState, &intentState, &providerCode); err != nil || effectState != "attempted" || intentState != "queued" || providerCode != nil {
		t.Fatalf("completion rollback effect=%q intent=%q code=%v err=%v", effectState, intentState, providerCode, err)
	}

	// River re-delivery after the failed completion sees the committed attempted
	// lease. It cannot cross the Provider boundary again; it waits for recovery.
	replayErr := fixture.effects.RunAttempt(fixture.ctx, effectIDNumber(t, effectRef), 1, jobID, provider)
	var snooze *river.JobSnoozeError
	if !errors.As(replayErr, &snooze) || snooze.Duration <= 0 || writer.calls() != 1 {
		t.Fatalf("completion retry err=%v snooze=%+v provider calls=%d", replayErr, snooze, writer.calls())
	}
	if _, err = fixture.native.Exec(fixture.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, effectIDNumber(t, effectRef)); err != nil {
		t.Fatal(err)
	}
	if err = fixture.effects.RunAttempt(fixture.ctx, effectIDNumber(t, effectRef), 1, jobID, provider); err != nil || writer.calls() != 1 {
		t.Fatalf("expired recovery err=%v provider calls=%d", err, writer.calls())
	}
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT e.state,i.state,i.provider_error_code FROM channel_welcome_intents i JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&effectState, &intentState, &providerCode); err != nil || effectState != "outcome_unknown" || intentState != "queued" || providerCode != nil {
		t.Fatalf("recovered effect=%q intent=%q code=%v err=%v", effectState, intentState, providerCode, err)
	}
}

func TestChannelWelcomeProviderRejectionCodeConstraintPostgreSQL(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixture(t)
	defer fixture.close()
	input, _ := fixture.acceptWelcome(t, "runtime-provider-rejection-constraint-0001", fixture.now)
	var reason *string
	var code *int64
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT result_reason,provider_error_code FROM channel_welcome_intents WHERE callback_id=$1`, input.CallbackKey).Scan(&reason, &code); err != nil || reason != nil || code != nil {
		t.Fatalf("initial NULL/NULL reason=%v code=%v err=%v", reason, code, err)
	}
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE channel_welcome_intents SET state='final_failed',result_reason='provider_rejected',provider_error_code=40003 WHERE callback_id=$1`, input.CallbackKey); err != nil {
		t.Fatalf("provider_rejected/nonzero rejected: %v", err)
	}
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE channel_welcome_intents SET result_reason=NULL,provider_error_code=40003 WHERE callback_id=$1`, input.CallbackKey); err == nil {
		t.Fatal("NULL reason with nonzero Provider code passed constraint")
	}
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE channel_welcome_intents SET result_reason='provider_rejected',provider_error_code=NULL WHERE callback_id=$1`, input.CallbackKey); err == nil {
		t.Fatal("provider_rejected with NULL code passed constraint")
	}
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE channel_welcome_intents SET result_reason=NULL,provider_error_code=NULL WHERE callback_id=$1`, input.CallbackKey); err != nil {
		t.Fatalf("final NULL/NULL rejected: %v", err)
	}
}

func TestChannelWelcomePlainTextFreezesWithoutCustomerDirectoryRead(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixtureWithWelcomeMessage(t, "欢迎来到测试渠道")
	defer fixture.close()
	var customerID int64
	if err := fixture.native.QueryRow(fixture.ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	input, _ := fixture.acceptWelcome(t, "runtime-plain-text-0001", fixture.now)
	if err := fixture.unit.Within(fixture.ctx, func(tx context.Context) error {
		return fixture.actions.AcceptEntrantActions(tx, channelport.EntrantActionCommand{
			CallbackID: string(input.CallbackKey), CustomerID: customerdomain.CustomerID(customerID), Resolution: fixture.ready.resolution, OccurredAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	readingFailure := &failingWelcomeDirectoryReader{}
	messageCipher, err := wecom.NewChannelWelcomeMessageCipher("channel-welcome-runtime-secret-0001")
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.actions.SetWelcomeMessageDependencies(readingFailure, messageCipher); err != nil {
		t.Fatal(err)
	}
	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1)}
	_, stop := fixture.startRuntime(t, writer, nil, true)
	defer stop()
	select {
	case call := <-writer.called:
		if call.message != "欢迎来到测试渠道" {
			t.Fatalf("provider message=%q", call.message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plain-text welcome did not reach the local Provider boundary")
	}
	fixture.waitEffect(t, input, effectport.StateExecuted)
	if readingFailure.calls != 0 {
		t.Fatalf("plain text unexpectedly read customer directory calls=%d", readingFailure.calls)
	}
}

func TestChannelWelcomeTemplateFreezesOneRenderedBodyBeforeProviderRetry(t *testing.T) {
	fixture := newChannelWelcomeRuntimeFixtureWithWelcomeMessage(t, "您好{{客户名}}，{{客户名}}")
	defer fixture.close()
	var customerID int64
	if err := fixture.native.QueryRow(fixture.ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.native.Exec(fixture.ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,updated_at)
		VALUES($1,'active','初始客户名','fixture','active','channel-welcome-template',1,clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	input, _ := fixture.acceptWelcome(t, "runtime-template-0001", fixture.now)
	if err := fixture.unit.Within(fixture.ctx, func(tx context.Context) error {
		return fixture.actions.AcceptEntrantActions(tx, channelport.EntrantActionCommand{
			CallbackID: string(input.CallbackKey), CustomerID: customerdomain.CustomerID(customerID), Resolution: fixture.ready.resolution, OccurredAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	var effectRef string
	var source, target, payload, policy effectport.Digest
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT effect_ref,source_ref_digest,effect_target_digest,effect_payload_digest,effect_policy_digest
		FROM channel_welcome_intents WHERE callback_id=$1`, input.CallbackKey).Scan(&effectRef, &source, &target, &payload, &policy); err != nil {
		t.Fatal(err)
	}
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelWelcome, SourceRefDigest: source, TargetRefDigest: target, PayloadDigest: payload, PolicyVersionHash: policy}
	request := channelport.WelcomeMessageFreezeRequest{EffectRef: effectRef, Envelope: envelope}
	reader := channelEntrantActionReaderAdapter{uow: fixture.unit, source: fixture.actions}
	type freezeResult struct {
		message string
		err     error
	}
	results := make(chan freezeResult, 2)
	for range 2 {
		go func() {
			message, err := reader.FreezePublishedWelcomeMessage(fixture.ctx, request)
			results <- freezeResult{message: message, err: err}
		}()
	}
	for range 2 {
		result := <-results
		if result.err != nil || result.message != "您好初始客户名，初始客户名" {
			t.Fatalf("concurrent frozen result=%+v", result)
		}
	}
	var cipherText []byte
	var snapshots int
	if err := fixture.native.QueryRow(fixture.ctx, `SELECT ciphertext,(SELECT count(*) FROM channel_welcome_message_snapshots) FROM channel_welcome_message_snapshots`).Scan(&cipherText, &snapshots); err != nil || snapshots != 1 {
		t.Fatalf("snapshot count=%d err=%v", snapshots, err)
	}
	if bytes.Contains(cipherText, []byte("初始客户名")) || bytes.Contains(cipherText, []byte("{{客户名}}")) {
		t.Fatal("snapshot stored a plaintext template or customer name")
	}
	if _, err := fixture.native.Exec(fixture.ctx, `UPDATE customer_directory_projection SET display_name='后续客户名' WHERE customer_id=$1`, customerID); err != nil {
		t.Fatal(err)
	}
	frozenAfterNameChange, err := reader.FreezePublishedWelcomeMessage(fixture.ctx, request)
	if err != nil || frozenAfterNameChange != "您好初始客户名，初始客户名" {
		t.Fatalf("frozen message drifted after customer update message=%q err=%v", frozenAfterNameChange, err)
	}
	var stableSource, stableTarget, stablePayload, stablePolicy effectport.Digest
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT source_ref_digest,effect_target_digest,effect_payload_digest,effect_policy_digest FROM channel_welcome_intents WHERE callback_id=$1`, input.CallbackKey).Scan(&stableSource, &stableTarget, &stablePayload, &stablePolicy); err != nil || stableSource != source || stableTarget != target || stablePayload != payload || stablePolicy != policy {
		t.Fatalf("effect envelope drift source=%q/%q target=%q/%q payload=%q/%q policy=%q/%q err=%v", stableSource, source, stableTarget, target, stablePayload, payload, stablePolicy, policy, err)
	}
	writer := &runtimeWelcomeWriter{called: make(chan runtimeWelcomeCall, 1)}
	_, stop := fixture.startRuntime(t, writer, nil, true)
	defer stop()
	select {
	case call := <-writer.called:
		if call.message != "您好初始客户名，初始客户名" {
			t.Fatalf("Provider received non-frozen template body %q", call.message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("frozen template did not reach local Provider boundary")
	}
	fixture.waitEffect(t, input, effectport.StateExecuted)
}

type channelWelcomeRuntimeFixture struct {
	ctx      context.Context
	native   *pgxpool.Pool
	cleanup  func()
	pool     *platformpostgres.Pool
	unit     *platformpostgres.UnitOfWork
	effects  *externaleffects.Repository
	insert   *river.Client[pgx.Tx]
	dispatch wecom.ExternalContactCallbackDispatcher
	ready    channelWelcomeFixture
	now      time.Time
	actions  *channel.EntrantActionStore
	grants   *wecom.PostgreSQLWelcomeGrantStore
}

func newChannelWelcomeRuntimeFixture(t *testing.T) *channelWelcomeRuntimeFixture {
	return newChannelWelcomeRuntimeFixtureWithWelcomeMessage(t, "welcome")
}

func newChannelWelcomeRuntimeFixtureWithWelcomeMessage(t *testing.T, welcomeMessage string) *channelWelcomeRuntimeFixture {
	t.Helper()
	native, cleanup := channelWelcomeIntegrationPool(t)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	// This insert client needs the job kind registered before it can accept an
	// effect. The runtime worker receives its bound repository below.
	worker := externaleffects.NewWorker(nil, nil)
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, worker); err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	cipher, err := wecom.NewWelcomeGrantCipher("channel-welcome-runtime-secret-0001")
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	messageCipher, err := wecom.NewChannelWelcomeMessageCipher("channel-welcome-runtime-secret-0001")
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	digester, err := wecom.NewHMACStateDigester([]byte("12345678901234567890123456789012"))
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	adminID := insertChannelWelcomeAdmin(t, ctx, native)
	states := channel.NewPostgreSQLStore()
	ready := seedChannelWelcomeFixtureWithWelcomeMessage(t, ctx, unit, states, digester, adminID, "runtime", "welcome-runtime", welcomeMessage, false, 1, 0, 100)
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		cancel()
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	actions := channel.NewEntrantActionStore(effects, nil)
	if err = actions.SetWelcomeMessageDependencies(customerstore.PostgreSQL{}, messageCipher); err != nil {
		cancel()
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	grants := wecom.NewPostgreSQLWelcomeGrantStore(cipher)
	return &channelWelcomeRuntimeFixture{
		ctx: ctx, native: native, cleanup: func() { cancel(); pool.Close(); cleanup() }, pool: pool, unit: unit, effects: effects, insert: insert,
		dispatch: wecom.ExternalContactCallbackDispatcher{StateDigester: digester, Inbox: inbox, UOW: unit, WelcomeGrants: grants, WelcomeActions: actions, States: states},
		ready:    ready, now: time.Now().UTC().Truncate(time.Microsecond), actions: actions, grants: grants,
	}
}

func (fixture *channelWelcomeRuntimeFixture) close() {
	if fixture != nil && fixture.cleanup != nil {
		fixture.cleanup()
	}
}

func (fixture *channelWelcomeRuntimeFixture) acceptWelcome(t *testing.T, suffix string, receivedAt time.Time) (wecom.DecryptedCallbackEvent, time.Time) {
	t.Helper()
	key := mustChannelWelcomeKey(t, "wecom:external-contact:"+suffix)
	input := wecom.DecryptedCallbackEvent{CorpID: "wx-corp", CallbackKey: key, Plaintext: channelWelcomePlaintext(fixture.ready.rawState), ReceivedAt: receivedAt}
	if err := fixture.dispatch.DispatchDecryptedEvent(fixture.ctx, input); err != nil {
		t.Fatal(err)
	}
	return input, receivedAt.Add(20 * time.Second)
}

func (fixture *channelWelcomeRuntimeFixture) startRuntime(t *testing.T, writer *runtimeWelcomeWriter, ordinary effectport.ProviderAdapter, reconstruct bool) (*platformjobqueue.Runtime, func()) {
	t.Helper()
	reader := channelEntrantActionReaderAdapter{uow: fixture.unit, source: fixture.actions}
	grants := fixture.grants
	effects := fixture.effects
	if reconstruct {
		var err error
		effects, err = externaleffects.NewRepository(fixture.native, fixture.insert)
		if err != nil {
			t.Fatal(err)
		}
	}
	provider := outbound.NewChannelEntrantProvider(reader, reader, fixture.unit, grants, nil, nil, writer)
	adapter := channelWelcomeRuntimeAdapter{welcome: provider, ordinary: ordinary}
	workers := river.NewWorkers()
	worker := externaleffects.NewWorker(effects, adapter)
	if err := river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, worker); err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewChannelEntrantCompletionSink(fixture.actions)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(channelWelcomeCompletionRouter{welcome: completion}); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(fixture.native, workers, platformjobqueue.OutboundQueue, platformjobqueue.OutboundWelcomeQueue)
	if err != nil {
		t.Fatal(err)
	}
	runContext, cancel := context.WithCancel(fixture.ctx)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runContext) }()
	return runtime, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("River runtime did not stop")
		}
	}
}

func (fixture *channelWelcomeRuntimeFixture) waitEffect(t *testing.T, input wecom.DecryptedCallbackEvent, want effectport.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		err := fixture.native.QueryRow(fixture.ctx, `SELECT e.state FROM channel_welcome_intents i JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint WHERE i.callback_id=$1`, input.CallbackKey).Scan(&state)
		if err == nil && effectport.State(state) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("effect callback=%s did not reach %s", input.CallbackKey, want)
}

func (fixture *channelWelcomeRuntimeFixture) assertWelcomeOutcome(t *testing.T, input wecom.DecryptedCallbackEvent, wantState effectport.State, wantReason string, wantAttempted bool) {
	t.Helper()
	var state, reason string
	var attempted bool
	err := fixture.native.QueryRow(fixture.ctx, `SELECT i.state,COALESCE(i.result_reason,''),a.call_attempted
		FROM channel_welcome_intents i
		JOIN external_effects e ON e.id=substring(i.effect_ref FROM 5)::bigint
		JOIN external_effect_attempts a ON a.effect_id=e.id
		WHERE i.callback_id=$1
		ORDER BY a.number DESC LIMIT 1`, input.CallbackKey).Scan(&state, &reason, &attempted)
	if err != nil || effectport.State(state) != wantState || reason != wantReason || attempted != wantAttempted {
		t.Fatalf("welcome outcome state=%s reason=%s attempted=%t err=%v; want state=%s reason=%s attempted=%t", state, reason, attempted, err, wantState, wantReason, wantAttempted)
	}
}

type channelWelcomeRuntimeAdapter struct {
	welcome  effectport.ProviderAdapter
	ordinary effectport.ProviderAdapter
}

func (adapter channelWelcomeRuntimeAdapter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Kind == effectport.KindChannelWelcome && adapter.welcome != nil {
		return adapter.welcome.Execute(ctx, envelope, attempt)
	}
	if adapter.ordinary != nil {
		return adapter.ordinary.Execute(ctx, envelope, attempt)
	}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel-welcome-runtime-unhandled", string(envelope.Kind))}, nil
}

type channelWelcomeCompletionRouter struct {
	welcome   *outbound.ChannelEntrantCompletionSink
	failAfter bool
}

func (router channelWelcomeCompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if envelope.Kind == effectport.KindChannelWelcome {
		if err := router.welcome.CompleteEffect(ctx, effectRef, envelope, attempt, result); err != nil {
			return err
		}
		if router.failAfter {
			return errors.New("forced channel welcome completion failure")
		}
	}
	return nil
}

type runtimeWelcomeCall struct {
	at, deadline time.Time
	code         string
	message      string
}

type runtimeWelcomeWriter struct {
	mu     sync.Mutex
	count  int
	called chan runtimeWelcomeCall
	err    error
}

func (writer *runtimeWelcomeWriter) SendWelcomeMessage(ctx context.Context, code, message string, _ []wecomport.WelcomeAttachment) error {
	writer.mu.Lock()
	writer.count++
	writer.mu.Unlock()
	deadline, _ := ctx.Deadline()
	writer.called <- runtimeWelcomeCall{at: time.Now().UTC(), deadline: deadline, code: code, message: message}
	return writer.err
}

type failingWelcomeDirectoryReader struct{ calls int }

func (reader *failingWelcomeDirectoryReader) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	reader.calls++
	return nil, errors.New("fixture customer directory unavailable")
}

func (*runtimeWelcomeWriter) AddContactTag(context.Context, string, string, string) error { return nil }
func (writer *runtimeWelcomeWriter) calls() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.count
}

type blockedOutboundAdapter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockedOutboundAdapter() *blockedOutboundAdapter {
	return &blockedOutboundAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
}
func (adapter *blockedOutboundAdapter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	adapter.started <- struct{}{}
	select {
	case <-adapter.release:
	case <-ctx.Done():
	}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel-welcome-blocked-outbound", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)))}, nil
}
func (adapter *blockedOutboundAdapter) releaseAll() {
	adapter.once.Do(func() { close(adapter.release) })
}
