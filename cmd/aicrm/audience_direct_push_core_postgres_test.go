package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	automation "github.com/qianlan33333-png/AI-CRM-v3/internal/automation"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type directPushCoreNoopJobs struct{}

func (directPushCoreNoopJobs) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	return 1, nil
}

func (directPushCoreNoopJobs) EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error) {
	return 1, nil
}

// TestAudienceDirectPushCoreSignedReplayBudgetAndOneIDMergePostgreSQL exercises
// the actual signed HTTP entry, OneID resolver/link/merge, Segment eligibility
// Port, Automation owner, frozen Media content, Outbound intent, and EER store
// against an isolated PostgreSQL schema. No real Provider is
// started automatically. The effect worker is invoked synchronously against a
// loopback Provider; thumbnail readiness and open-source observations are fixtures.
func TestAudienceDirectPushCoreSignedReplayBudgetAndOneIDMergePostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	native, cleanup := automationAudienceRuntimePool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	segmentRepo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	automationRepo, err := automationstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaRepo, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	staffID := automationAudienceInsertProviderStaff(t, ctx, native)
	materials := automationAudienceCreateMedia(t, ctx, mediaRepo, staffID)

	const unionScope = "wechat-open-platform:direct-push-core-test"
	identities := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	makeFact := func(kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
		t.Helper()
		fact, factErr := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
			Kind: kind, Scope: scope, Value: value, Source: "direct-push-core-postgres-test",
		})
		if factErr != nil {
			t.Fatal(factErr)
		}
		return fact
	}
	unionA := makeFact(identitydomain.KindUnionID, unionScope, "direct-push-union-alias-a")
	wecomB := makeFact(identitydomain.KindWeComExternalUserID, "wecom-corp:direct-push-core-test", "direct-push-wecom-root-b")
	var customerA, customerB identityport.ProvisionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var provisionErr error
		customerA, provisionErr = identities.ProvisionCustomerFromVerifiedIdentity(tx, unionA)
		if provisionErr != nil {
			return provisionErr
		}
		customerB, provisionErr = identities.ProvisionCustomerFromVerifiedIdentity(tx, wecomB)
		return provisionErr
	}); err != nil {
		t.Fatalf("provision OneID roots: %v", err)
	}

	packageID := automationAudiencePackageWithDefinition(t, ctx, uow, segmentRepo, time.Now().UTC(), "direct-push-core", json.RawMessage(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`))
	automationService := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepo, mediaRepo, mediaRepo, mediaRepo, mediaRepo, automationRepo)
	agent := automationAudiencePublishedAgent(t, ctx, automationService, staffID, materials)
	publishedAgent, found, err := automationService.PublishedAgent(ctx, agent.ID)
	if err != nil || !found {
		t.Fatalf("published agent=%+v found=%t err=%v", publishedAgent, found, err)
	}
	execution, err := segmentapp.NewExecutionService(uow, segmentRepo, automationService, automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}, true)
	if err != nil {
		t.Fatal(err)
	}
	combined := automationAudienceCombinedDigest(publishedAgent.ContentDigest, publishedAgent.MaterialsDigest)
	if binding, bindErr := execution.PutBinding(ctx, segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: 2, AgentID: agent.ID, ExpectedPublishedVersion: publishedAgent.PublishedVersion, ExpectedAgentDigest: combined, Actor: staffID, IdempotencyKey: "direct-push-binding-0001"}); bindErr != nil || binding.ID < 1 {
		t.Fatalf("bind package execution: binding=%+v err=%v", binding, bindErr)
	}
	if _, err = execution.ReplaceSenders(ctx, segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: 3, ProviderMemberIDs: []string{"sender-a"}, Actor: staffID, IdempotencyKey: "direct-push-senders-0001"}); err != nil {
		t.Fatalf("configure Segment sender set: %v", err)
	}

	source := &automationAudienceSource{}
	source.Set([]customerdomain.CustomerID{customerA.CustomerID, customerB.CustomerID})
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, source, segmentadapter.CanonicalCustomers{
		UoW: uow, Resolver: canonicalCustomerAdapter{reader: identityquery.NewPostgreSQL()},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, segmentRepo, evaluator, directPushCoreNoopJobs{}, directPushCoreNoopJobs{})
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "direct-push-snapshot-0001", ReferenceTime: time.Now().UTC()})
	if err != nil {
		t.Fatalf("accept Segment snapshot: %v", err)
	}
	if err = snapshots.ProcessRefresh(ctx, refresh.ID); err != nil {
		t.Fatalf("publish Segment snapshot: %v", err)
	}
	facade := segmentapp.NewRuntimeFacade(segmentapp.NewService(uow, segmentRepo), snapshots, execution)
	if active, activeErr := facade.TransitionPackage(ctx, segmentapp.VersionCommand{ID: packageID, ExpectedVersion: 4, Actor: staffID, IdempotencyKey: "direct-push-activate-0001"}, segmentdomain.Active); activeErr != nil || active.Lifecycle != segmentdomain.Active {
		t.Fatalf("activate eligible package: package=%+v err=%v", active, activeErr)
	}

	workers := river.NewWorkers()
	eerKindOnlyWorker := externaleffects.NewWorker(nil, nil)
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, eerKindOnlyWorker); err != nil {
		t.Fatal(err)
	}
	observeWorker := automation.NewDirectPushObserveWorker()
	if err = river.AddWorkerSafely[automation.DirectPushObserveArgs](workers, observeWorker); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := outbound.NewMessageService(native, uow, effects, automationRepo)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := automationapp.NewDirectPushRuntime(
		uow,
		automationRepo,
		audienceDirectPushTargetResolver{uow: uow, identities: identities, staff: accessstore.NewPostgreSQL(), unionScope: unionScope},
		directPushEligibilityAdapter{reader: segmentRepo},
		automationOutboundContentFreezer{capturer: mediaRepo, materials: mediaRepo},
		messages,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.BindCanonicalLineages(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	config, err := runtime.ConfigureDirectPush(ctx, automationport.DirectPushConfigCommand{PackageID: packageID, Enabled: true, MaxPerCustomer24h: 1, ExpectedVersion: 0, Actor: staffID, IdempotencyKey: "direct-push-config-0001"})
	if err != nil || !config.Enabled || config.WebhookReference == "" {
		t.Fatalf("configure webhook: config=%+v err=%v", config, err)
	}

	const signingKey = "0123456789abcdef0123456789abcdef"
	now := time.Now().UTC().Truncate(time.Second)
	protocol := &audienceDirectPushAuthenticator{key: []byte(signingKey), now: func() time.Time { return now }}
	handler, err := automationhttp.NewDirectPushHandler(runtime, protocol)
	if err != nil {
		t.Fatal(err)
	}

	bodyA, err := json.Marshal(map[string]any{"items": []automationport.DirectPushInput{{UnionID: "direct-push-union-alias-a", Text: "Frozen audience message", MiniProgramID: materials.miniID, SenderUserID: "sender-a", ClientReference: "source-a"}}})
	if err != nil {
		t.Fatal(err)
	}
	invalidSignature := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte("wrong-signing-key"), "direct-push-event-bad-signature", bodyA, now)
	if invalidSignature.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status=%d", invalidSignature.Code)
	}
	// Actual EER INSERT failure must roll back the push, Outbound intent,
	// receipt and Outbox together; the identical event can then be retried.
	if _, err = native.Exec(ctx, `CREATE FUNCTION direct_push_fail_effect() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated EER insert failure'; END $$; CREATE TRIGGER direct_push_fail_effect BEFORE INSERT ON external_effects FOR EACH ROW EXECUTE FUNCTION direct_push_fail_effect()`); err != nil {
		t.Fatal(err)
	}
	failed := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000001", bodyA, now)
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("EER insert failure status=%d", failed.Code)
	}
	var partialWrites int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_audience_push_batches)+(SELECT count(*) FROM automation_audience_push_items)+(SELECT count(*) FROM automation_audience_push_outbox)+(SELECT count(*) FROM outbound_message_intents)+(SELECT count(*) FROM external_effects)`).Scan(&partialWrites); err != nil || partialWrites != 0 {
		t.Fatalf("failed acceptance leaked %d durable rows, err=%v", partialWrites, err)
	}
	if _, err = native.Exec(ctx, `DROP TRIGGER direct_push_fail_effect ON external_effects; DROP FUNCTION direct_push_fail_effect()`); err != nil {
		t.Fatal(err)
	}
	first := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000001", bodyA, now)
	if first.Code != http.StatusAccepted {
		t.Fatalf("signed first acceptance status=%d body=%s", first.Code, first.Body.String())
	}
	var firstResult automationport.DirectPushBatchResult
	if err = json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil || firstResult.AcceptedCount != 1 || firstResult.RejectedCount != 0 || len(firstResult.Items) != 1 || firstResult.Items[0].State != "accepted" {
		t.Fatalf("first accepted result=%+v err=%v body=%s", firstResult, err, first.Body.String())
	}
	pushID := firstResult.Items[0].PushID
	if pushID == "" {
		t.Fatal("first acceptance omitted push ID")
	}

	bodyASecond := bytes.Replace(bodyA, []byte("source-a"), []byte("source-a-second"), 1)
	preMergeLimit := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000002", bodyASecond, now)
	if preMergeLimit.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same-root 24h budget status=%d body=%s; want 422 rate_limit_24h", preMergeLimit.Code, preMergeLimit.Body.String())
	}
	var preMergeLimitResult automationport.DirectPushBatchResult
	if err = json.Unmarshal(preMergeLimit.Body.Bytes(), &preMergeLimitResult); err != nil || preMergeLimitResult.AcceptedCount != 0 || len(preMergeLimitResult.Items) != 1 || preMergeLimitResult.Items[0].Code != "rate_limit_24h" {
		t.Fatalf("same-root budget result=%+v err=%v", preMergeLimitResult, err)
	}

	// The same original receipt must replay even after OneID changes its root.
	attachForMerge := identitydomain.LinkEvidence{Type: "direct_push_merge", Strength: identitydomain.EvidenceStrong, Source: "direct-push-core-postgres-test", EventID: "merge-a-to-b", Digest: "sha256:merge-a-to-b", PolicyVersion: "direct-push-test-v1"}
	var candidate identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var linkErr error
		candidate, linkErr = identities.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: customerA.CustomerID, Target: wecomB, Evidence: attachForMerge})
		return linkErr
	}); err != nil || candidate.Candidate == nil || candidate.Status != identityapp.LinkCandidate {
		t.Fatalf("create real OneID merge candidate: candidate=%+v err=%v", candidate, err)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var mergeErr error
		merged, mergeErr = identities.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: customerB.CustomerID, Operator: "direct-push-core-postgres-test"})
		return mergeErr
	}); err != nil || merged.Status != identityapp.LinkMerged || merged.Merge == nil || merged.Merge.ToCustomerID != customerB.CustomerID {
		t.Fatalf("confirm actual A-to-B OneID merge: result=%+v err=%v", merged, err)
	}
	var resolvedA, resolvedB identityport.ResolveResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var resolveErr error
		resolvedA, resolveErr = identities.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: unionScope, Value: "direct-push-union-alias-a", Assurance: identitydomain.AssuranceDeclared, Source: "direct-push-core-postgres-test"})
		if resolveErr != nil {
			return resolveErr
		}
		resolvedB, resolveErr = identities.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:direct-push-core-test", Value: "direct-push-wecom-root-b", Assurance: identitydomain.AssuranceDeclared, Source: "direct-push-core-postgres-test"})
		return resolveErr
	}); err != nil || resolvedA.CustomerID != customerB.CustomerID || resolvedB.CustomerID != customerB.CustomerID {
		t.Fatalf("OneID aliases did not resolve to B after merge: A=%+v B=%+v err=%v", resolvedA, resolvedB, err)
	}

	replay := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000001", bodyA, now)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("same event replay after merge status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayResult automationport.DirectPushBatchResult
	if err = json.Unmarshal(replay.Body.Bytes(), &replayResult); err != nil || replayResult.BatchID != firstResult.BatchID || replayResult.AcceptedCount != 1 || len(replayResult.Items) != 1 || replayResult.Items[0].PushID != pushID {
		t.Fatalf("replay changed original result: first=%+v replay=%+v err=%v", firstResult, replayResult, err)
	}
	bodyDrift := bytes.Replace(bodyA, []byte("Frozen audience message"), []byte("Drifted message"), 1)
	drift := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000001", bodyDrift, now)
	if drift.Code != http.StatusConflict {
		t.Fatalf("same event payload drift status=%d body=%s; want conflict", drift.Code, drift.Body.String())
	}

	bodyB, err := json.Marshal(map[string]any{"items": []automationport.DirectPushInput{{UnionID: "direct-push-union-alias-a", Text: "Frozen audience message", MiniProgramID: materials.miniID, SenderUserID: "sender-a", ClientReference: "source-b"}}})
	if err != nil {
		t.Fatal(err)
	}
	postMerge := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), "direct-push-event-00000003", bodyB, now)
	if postMerge.Code != http.StatusUnprocessableEntity {
		t.Errorf("per-canonical-customer 24h budget was bypassed after A→B merge: second root event status=%d body=%s; expected 422 rate_limit_24h", postMerge.Code, postMerge.Body.String())
	}
	var postMergeResult automationport.DirectPushBatchResult
	if json.Unmarshal(postMerge.Body.Bytes(), &postMergeResult) == nil && postMerge.Code == http.StatusAccepted {
		t.Errorf("post-merge B request created push %q despite A already using the 24h allowance", postMergeResult.Items[0].PushID)
	}

	var batches, items, itemsForA, itemsForB, intents, queuedEffects, acceptedOutbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM automation_audience_push_batches),
		(SELECT count(*) FROM automation_audience_push_items),
		(SELECT count(*) FROM automation_audience_push_items WHERE customer_id=$1),
		(SELECT count(*) FROM automation_audience_push_items WHERE customer_id=$2),
		(SELECT count(*) FROM outbound_message_intents WHERE source_kind='audience_direct_push'),
		(SELECT count(*) FROM external_effects WHERE owner='outbound' AND state='queued'),
		(SELECT count(*) FROM automation_audience_push_outbox WHERE event_type='automation.audience_push.accepted.v1')`, customerA.CustomerID, customerB.CustomerID).Scan(&batches, &items, &itemsForA, &itemsForB, &intents, &queuedEffects, &acceptedOutbox); err != nil {
		t.Fatal(err)
	}
	t.Logf("persisted direct-push facts after first/replay/drift/merge/root-B attempt: batches=%d items=%d native_customer_A_items=%d canonical_customer_B_items=%d outbound_intents=%d queued_EER_effects=%d acceptance_outbox=%d; no EER worker or WeCom Provider was run", batches, items, itemsForA, itemsForB, intents, queuedEffects, acceptedOutbox)
	if itemsForA != 1 || intents != items || queuedEffects != items || acceptedOutbox != batches {
		t.Errorf("replay/drift/effect atomicity counts inconsistent: A=%d items=%d intents=%d queued_effects=%d accepted_outbox=%d batches=%d", itemsForA, items, intents, queuedEffects, acceptedOutbox, batches)
	}
	if itemsForB != 0 || items != 1 {
		t.Errorf("canonical OneID root already consumed the 24h allowance, but native-ID budget persisted A=%d and B=%d items (total=%d); expected one original item", itemsForA, itemsForB, items)
	}

	var storedCustomerID, storedIdentityID, storedStaffID, storedMiniProgramID int64
	var pushState, effectID string
	var itemSnapshot, outboundSnapshot, itemSnapshotDigest, outboundSnapshotDigest []byte
	if err = native.QueryRow(ctx, `SELECT item.customer_id,item.identity_id,item.sender_staff_id,item.miniprogram_id,item.send_state,item.effect_id,item.content_snapshot,item.content_snapshot_digest,intent.content_snapshot,intent.content_snapshot_digest
		FROM automation_audience_push_items item JOIN outbound_message_intents intent ON intent.id=item.outbound_intent_id WHERE item.id=$1`, pushNumericID(t, pushID)).Scan(&storedCustomerID, &storedIdentityID, &storedStaffID, &storedMiniProgramID, &pushState, &effectID, &itemSnapshot, &itemSnapshotDigest, &outboundSnapshot, &outboundSnapshotDigest); err != nil {
		t.Fatal(err)
	}
	if storedCustomerID != int64(customerA.CustomerID) || storedIdentityID != customerA.IdentityID || storedStaffID != staffID || storedMiniProgramID != materials.miniID || pushState != "queued" || effectID == "" {
		t.Errorf("frozen target/effect binding drifted: customer=%d identity=%d staff=%d mini=%d state=%s effect=%q", storedCustomerID, storedIdentityID, storedStaffID, storedMiniProgramID, pushState, effectID)
	}
	var itemJSON, outboundJSON any
	if json.Unmarshal(itemSnapshot, &itemJSON) != nil || json.Unmarshal(outboundSnapshot, &outboundJSON) != nil || !reflect.DeepEqual(itemJSON, outboundJSON) || !bytes.Equal(itemSnapshotDigest, outboundSnapshotDigest) {
		t.Errorf("frozen Automation/Outbound content snapshots or digests differ")
	}
	var frozen struct {
		ContentText string `json:"content_text"`
		Sources     []struct {
			AppID    string `json:"appid"`
			PagePath string `json:"pagepath"`
			Title    string `json:"title"`
		} `json:"sources"`
	}
	if err = json.Unmarshal(itemSnapshot, &frozen); err != nil || frozen.ContentText != "Frozen audience message" || len(frozen.Sources) != 1 || frozen.Sources[0].AppID != "wx-runtime" || frozen.Sources[0].PagePath != "pages/runtime" || frozen.Sources[0].Title != "Runtime card" {
		t.Errorf("stored mini-program/text snapshot is not frozen as submitted: snapshot=%s err=%v", itemSnapshot, err)
	}
	var observedEffects []string
	rows, err := native.Query(ctx, `SELECT item.send_state || '/' || intent.state || '/' || effect.state FROM automation_audience_push_items item JOIN outbound_message_intents intent ON intent.id=item.outbound_intent_id JOIN external_effects effect ON effect.id=split_part(item.effect_id,'_',2)::bigint ORDER BY item.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var state string
		if err = rows.Scan(&state); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		observedEffects = append(observedEffects, state)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	t.Logf("persisted status projection send_state/outbound_intent_state/EER_state=%v (queued is acceptance only; Provider delivery was not exercised)", observedEffects)
	// Two new event keys compete for exactly one remaining canonical allowance.
	if _, err = runtime.ConfigureDirectPush(ctx, automationport.DirectPushConfigCommand{PackageID: packageID, Enabled: true, MaxPerCustomer24h: 2, ExpectedVersion: 1, Actor: staffID, IdempotencyKey: "direct-push-config-0002"}); err != nil {
		t.Fatal(err)
	}
	responses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			response := postSignedDirectPushCore(t, handler, config.WebhookReference, []byte(signingKey), fmt.Sprintf("direct-push-event-concurrent-%d", i), bodyB, now)
			responses <- response.Code
		}(i)
	}
	codes := map[int]int{}
	for i := 0; i < 2; i++ {
		select {
		case code := <-responses:
			codes[code]++
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if codes[http.StatusAccepted] != 1 || codes[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("concurrent canonical allowance responses=%v", codes)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_audience_push_items WHERE customer_id=$1`, customerB.CustomerID).Scan(&itemsForB); err != nil || itemsForB != 1 {
		t.Fatalf("new writes must use the current root, count=%d err=%v", itemsForB, err)
	}

	// Execute the original frozen intent through its real EER and Outbound
	// owners. Provider requests are restricted to this loopback server.
	var providerCalls atomic.Int32
	sentAt := now.Add(-25 * time.Hour) // synthetic source clock: observation is due
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			var body struct {
				External []string `json:"external_userid"`
				Sender   string   `json:"sender"`
				Text     struct {
					Content string `json:"content"`
				} `json:"text"`
				Attachments []struct {
					Type string                                          `json:"msgtype"`
					Mini struct{ Title, AppID, Page, PicMediaID string } `json:"miniprogram"`
				} `json:"attachments"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.External) != 1 || body.External[0] != "direct-push-wecom-root-b" || body.Sender != "sender-a" || body.Text.Content != "Frozen audience message" || len(body.Attachments) != 1 || body.Attachments[0].Type != "miniprogram" || body.Attachments[0].Mini.Title != "Runtime card" || body.Attachments[0].Mini.AppID != "wx-runtime" || body.Attachments[0].Mini.Page != "pages/runtime" {
				t.Error("loopback Provider received an unexpected frozen message")
				http.Error(w, "invalid fixture request", 400)
				return
			}
			providerCalls.Add(1)
			_, _ = w.Write([]byte(`{"errcode":0,"msgid":"direct-push-fixture-message"}`))
		case "/cgi-bin/externalcontact/get_groupmsg_send_result":
			_, _ = fmt.Fprintf(w, `{"errcode":0,"send_list":[{"external_userid":"direct-push-wecom-root-b","userid":"sender-a","status":1,"send_time":%d}]}`, sentAt.Unix())
		default:
			t.Errorf("unexpected loopback Provider endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	providerClient, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "direct-push-core-test", ContactSecret: "synthetic-fixture-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	preparedThumbnail := &legacyPreparedStub{}
	messageProvider, err := outbound.NewMessageProvider(outbound.MessageProviderConfig{
		Enabled: true, CorpScope: "wecom-corp:direct-push-core-test", Executions: messages,
		Identities: outboundIdentityAdapter{uow: uow, reader: identityquery.NewPostgreSQL()},
		Staff:      automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}, Content: automationService,
		Payloads: automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{sources: mediaRepo, preparer: preparedThumbnail, scopeDigest: "sha256:" + strings.Repeat("b", 64)}}, Writer: providerClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(messages); err != nil {
		t.Fatal(err)
	}
	var jobID, effectNumericID, generation int64
	if err = native.QueryRow(ctx, `SELECT id,(args->>'effect_id')::bigint,(args->>'generation')::bigint FROM river_job WHERE kind='external_effect.execute.v1' AND (args->>'effect_id')::bigint=split_part($1,'_',2)::bigint`, effectID).Scan(&jobID, &effectNumericID, &generation); err != nil {
		t.Fatal(err)
	}
	effectJob := &river.Job[externaleffects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: jobID}, Args: externaleffects.EffectJobArgs{EffectID: effectNumericID, Generation: generation}}
	effectWorker := externaleffects.NewWorker(effects, messageProvider)
	if err = effectWorker.Work(ctx, effectJob); err != nil {
		t.Fatal(err)
	}
	if err = effectWorker.Work(ctx, effectJob); err != nil {
		t.Fatal(err)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("effect replay called loopback Provider %d times", providerCalls.Load())
	}
	statuses, err := runtime.DirectPushStatuses(ctx, config.WebhookReference, []string{pushID})
	if err != nil || len(statuses) != 1 || statuses[0].SendState != "provider_accepted" {
		t.Fatalf("effect completion statuses=%+v err=%v", statuses, err)
	}

	scheduler, err := automation.NewRiverDirectPushObservationScheduler(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	opens := &directPushCoreOpenFixture{result: automationport.DirectPushOpenResult{State: "unavailable", Reason: "coverage_incomplete"}}
	if err = runtime.BindObservation(audienceDirectPushDeliveryAdapter{receipts: messages, provider: providerClient}, opens, scheduler); err != nil {
		t.Fatal(err)
	}
	if err = observeWorker.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	if err = runtime.ReconcileDirectPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.ReconcileDirectPushes(ctx); err != nil {
		t.Fatal(err)
	}
	var observationJobs int
	var observationJobID int64
	var scheduledAt time.Time
	if err = native.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='automation.audience-direct-push-observe.v1' AND (args->>'item_id')::bigint=$1`, pushNumericID(t, pushID)).Scan(&observationJobs); err != nil || observationJobs != 1 {
		t.Fatalf("reconcile replay observation jobs=%d err=%v", observationJobs, err)
	}
	if err = native.QueryRow(ctx, `SELECT id,scheduled_at FROM river_job WHERE kind='automation.audience-direct-push-observe.v1' AND (args->>'item_id')::bigint=$1`, pushNumericID(t, pushID)).Scan(&observationJobID, &scheduledAt); err != nil || !scheduledAt.Equal(sentAt.Add(24*time.Hour)) {
		t.Fatalf("observation due=%v err=%v", scheduledAt, err)
	}
	observeJob := &river.Job[automation.DirectPushObserveArgs]{JobRow: &rivertype.JobRow{ID: observationJobID}, Args: automation.DirectPushObserveArgs{ItemID: pushNumericID(t, pushID)}}
	if err = observeWorker.Work(ctx, observeJob); err == nil {
		t.Fatal("incomplete source coverage prematurely completed observation")
	}
	statuses, err = runtime.DirectPushStatuses(ctx, config.WebhookReference, []string{pushID})
	if err != nil || statuses[0].SendState != "delivery_proven" || statuses[0].ObservationState != "observing" {
		t.Fatalf("retryable observation statuses=%+v err=%v", statuses, err)
	}
	openedAt := sentAt.Add(time.Hour)
	opens.result = automationport.DirectPushOpenResult{State: "opened", OpenedAt: &openedAt}
	if err = observeWorker.Work(ctx, observeJob); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return automationRepo.RecordDirectPushObservation(tx, pushNumericID(t, pushID), opens.result, now)
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err = runtime.DirectPushStatuses(ctx, config.WebhookReference, []string{pushID})
	if err != nil || statuses[0].ObservationState != "opened" {
		t.Fatalf("completed observation statuses=%+v err=%v", statuses, err)
	}
	var observedAudits int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_audience_push_audit_events WHERE event_type='automation.audience_push.observed.v1'`).Scan(&observedAudits); err != nil || observedAudits != 1 {
		t.Fatalf("observation replay audit count=%d err=%v", observedAudits, err)
	}
	t.Log("real EER/Outbound completion and replay, loopback WeCom send/result HTTP, owner delivery CAS, unique durable observation job, incomplete-source snooze and observation receipt passed; thumbnail readiness/open evidence are fixtures, no real Provider acceptance claimed")

}

func postSignedDirectPushCore(t *testing.T, handler http.Handler, reference string, key []byte, event string, body []byte, now time.Time) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.test/api/automation/audience/webhooks/"+reference, bytes.NewReader(body))
	timestamp := fmt.Sprint(now.UTC().Unix())
	request.Header.Set(automationhttp.AudienceWebhookClientIDHeader, automationhttp.AudienceWebhookClientID)
	request.Header.Set(automationhttp.AudienceWebhookTimestampHeader, timestamp)
	request.Header.Set(automationhttp.AudienceWebhookEventIDHeader, event)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	request.Header.Set(automationhttp.AudienceWebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func pushNumericID(t *testing.T, value string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(value, "ap_%d", &id); err != nil || id < 1 {
		t.Fatalf("invalid DirectPush ID %q: %v", value, err)
	}
	return id
}

type directPushCoreOpenFixture struct {
	result automationport.DirectPushOpenResult
}

func (s *directPushCoreOpenFixture) ObserveDirectPushOpen(context.Context, customerdomain.CustomerID, json.RawMessage, time.Time, time.Time) (automationport.DirectPushOpenResult, error) {
	return s.result, nil
}

var _ outboundport.MaterialPreparer = (*legacyPreparedStub)(nil)
