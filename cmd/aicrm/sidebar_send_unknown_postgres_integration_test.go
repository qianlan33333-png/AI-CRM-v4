package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// OneID decision: this test uses existing canonical customers only; it does
// not resolve, provision, or merge identity. Persistence decision: the test
// exercises the existing PostgreSQL Outbound + External Effects UoW. External
// Effects decision: the real worker path closes a missing client receipt as
// outcome_unknown with no Provider call, then a fresh key can only replay it.
func TestPostgreSQLSidebarSendUnknownSurvivesFreshKeyAndExpiry(t *testing.T) {
	ctx := context.Background()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	defer pool.Close()
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
	expiry := outbound.SidebarJSSDKExpiry{}
	if err = effects.SetCompletionSink(expiry); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := outbound.NewSidebarSendService(uow, effects)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active')`); err != nil {
		t.Fatal(err)
	}

	standard := sidebarSendFixturePayload(t, "https://crm.test.invalid/p/standard-7")
	first, err := sidebarSendAccept(ctx, service, 1, "staff-one", "product", "7", standard, "sidebar-unknown-first")
	if err != nil || first.IntentID < 1 || first.EffectID == "" || first.Grant == "" || first.State != "queued" || first.Replayed {
		t.Fatalf("first acceptance=%+v err=%v", first, err)
	}
	firstEffectID, err := strconv.ParseInt(strings.TrimPrefix(first.EffectID, "eer_"), 10, 64)
	if err != nil || firstEffectID < 1 {
		t.Fatalf("parse effect id=%q err=%v", first.EffectID, err)
	}
	var firstJobID int64
	if err = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, firstEffectID).Scan(&firstJobID); err != nil {
		t.Fatal(err)
	}
	// This is the actual External Effects worker/CompletionSink path. The
	// adapter makes no Provider request; the lost browser receipt is still
	// outcome_unknown because the server cannot prove SDK non-execution.
	if err = effects.RunAttempt(ctx, firstEffectID, 1, firstJobID, expiry); err != nil {
		t.Fatal(err)
	}
	var intentState, effectState string
	var callAttempted, realExternalCall bool
	if err = native.QueryRow(ctx, `SELECT intent.state,effect.state,attempt.call_attempted,attempt.real_external_call_executed FROM outbound_sidebar_send_intents intent JOIN external_effects effect ON effect.id=$2 JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.number=1 WHERE intent.id=$1`, first.IntentID, firstEffectID).Scan(&intentState, &effectState, &callAttempted, &realExternalCall); err != nil {
		t.Fatal(err)
	}
	if intentState != "outcome_unknown" || effectState != "outcome_unknown" || callAttempted || realExternalCall {
		t.Fatalf("expiry result intent=%q effect=%q call_attempted=%t real_external_call=%t", intentState, effectState, callAttempted, realExternalCall)
	}

	reloaded, err := sidebarSendAccept(ctx, service, 1, "staff-one", "product", "7", standard, "sidebar-unknown-after-reload")
	if err != nil || !reloaded.Replayed || reloaded.IntentID != first.IntentID || reloaded.State != "outcome_unknown" || reloaded.Grant != "" {
		t.Fatalf("fresh key after unknown=%+v err=%v", reloaded, err)
	}
	var sends, effectsCount int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_sidebar_send_intents),(SELECT count(*) FROM external_effects)`).Scan(&sends, &effectsCount); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || effectsCount != 1 {
		t.Fatalf("unknown fresh key created durable rows sends=%d effects=%d", sends, effectsCount)
	}

	// A process can die after recording EER attempted but before the browser
	// expiry adapter finishes. Its expired lease must project both facts to
	// outcome_unknown without ever representing a server-side Provider call.
	stale, err := sidebarSendAccept(ctx, service, 1, "staff-one", "material", "stale-101", sidebarSendFixturePayload(t, ""), "sidebar-stale-first")
	if err != nil || stale.Grant == "" || stale.EffectID == "" || stale.State != "queued" {
		t.Fatalf("stale fixture acceptance=%+v err=%v", stale, err)
	}
	staleEffectID, err := strconv.ParseInt(strings.TrimPrefix(stale.EffectID, "eer_"), 10, 64)
	if err != nil || staleEffectID < 1 {
		t.Fatalf("parse stale effect id=%q err=%v", stale.EffectID, err)
	}
	var staleJobID int64
	if err = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, staleEffectID).Scan(&staleJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE external_effects SET state='attempted',attempt_count=1,lease_fence=1,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, staleEffectID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,1,1,1,'attempted')`, staleEffectID); err != nil {
		t.Fatal(err)
	}
	if err = effects.RunAttempt(ctx, staleEffectID, 1, staleJobID, expiry); err != nil {
		t.Fatal(err)
	}
	var staleIntentState, staleEffectState string
	var staleCallAttempted, staleRealExternalCall bool
	if err = native.QueryRow(ctx, `SELECT intent.state,effect.state,attempt.call_attempted,attempt.real_external_call_executed FROM outbound_sidebar_send_intents intent JOIN external_effects effect ON effect.id=$2 JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.number=1 WHERE intent.id=$1`, stale.IntentID, staleEffectID).Scan(&staleIntentState, &staleEffectState, &staleCallAttempted, &staleRealExternalCall); err != nil {
		t.Fatal(err)
	}
	if staleIntentState != "outcome_unknown" || staleEffectState != "outcome_unknown" || staleCallAttempted || staleRealExternalCall {
		t.Fatalf("stale lease result intent=%q effect=%q call_attempted=%t real_external_call=%t", staleIntentState, staleEffectState, staleCallAttempted, staleRealExternalCall)
	}
	staleReloaded, err := sidebarSendAccept(ctx, service, 1, "staff-one", "material", "stale-101", sidebarSendFixturePayload(t, ""), "sidebar-stale-reload")
	if err != nil || !staleReloaded.Replayed || staleReloaded.IntentID != stale.IntentID || staleReloaded.State != "outcome_unknown" || staleReloaded.Grant != "" {
		t.Fatalf("stale lease fresh key=%+v err=%v", staleReloaded, err)
	}

	// Same numeric ID is valid independently in the standard and service-period
	// product stores. The frozen /p/ and /s/ bindings must not collide.
	period := sidebarSendFixturePayload(t, "https://crm.test.invalid/s/service-period-7")
	periodAcceptance, err := sidebarSendAccept(ctx, service, 1, "staff-one", "product", "7", period, "sidebar-service-period-same-id")
	if err != nil || periodAcceptance.Replayed || periodAcceptance.IntentID == first.IntentID || periodAcceptance.Grant == "" {
		t.Fatalf("same numeric product id across types=%+v err=%v", periodAcceptance, err)
	}
	otherCustomer, err := sidebarSendAccept(ctx, service, 2, "staff-one", "product", "7", standard, "sidebar-customer-isolation")
	if err != nil || otherCustomer.Replayed || otherCustomer.Grant == "" {
		t.Fatalf("customer isolation=%+v err=%v", otherCustomer, err)
	}

	// Explicit failure is terminal and permits the user's later intentional
	// re-share; outcome_unknown above remains locked.
	if _, err = service.CompleteSidebarSend(ctx, outboundport.SidebarSendOutcomeCommand{IntentID: periodAcceptance.IntentID, CustomerID: 1, EmployeeID: "staff-one", Grant: periodAcceptance.Grant, Outcome: "final_failed", EvidenceDigest: sha256.Sum256([]byte("client-declared-final-failure"))}); err != nil {
		t.Fatal(err)
	}
	afterFinalFailure, err := sidebarSendAccept(ctx, service, 1, "staff-one", "product", "7", period, "sidebar-after-final-failure")
	if err != nil || afterFinalFailure.Replayed || afterFinalFailure.Grant == "" {
		t.Fatalf("terminal failure must permit re-share acceptance=%+v err=%v", afterFinalFailure, err)
	}

	// Different browser keys racing before either page receives a grant are also
	// serialized. Exactly one gets a grant; the other can only read it back.
	material := sidebarSendFixturePayload(t, "")
	type result struct {
		acceptance outboundport.SidebarSendAcceptance
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"sidebar-concurrent-a", "sidebar-concurrent-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			<-start
			acceptance, acceptErr := sidebarSendAccept(ctx, service, 2, "staff-one", "material", "99", material, key)
			results <- result{acceptance: acceptance, err: acceptErr}
		}(key)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, replayed := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent accept=%+v", result.err)
		}
		if result.acceptance.Replayed && result.acceptance.Grant == "" {
			replayed++
		} else if !result.acceptance.Replayed && result.acceptance.Grant != "" {
			accepted++
		} else {
			t.Fatalf("concurrent acceptance=%+v", result.acceptance)
		}
	}
	if accepted != 1 || replayed != 1 {
		t.Fatalf("concurrent grant/replay accepted=%d replayed=%d", accepted, replayed)
	}
}

func sidebarSendFixturePayload(t *testing.T, link string) []byte {
	t.Helper()
	if link == "" {
		return []byte(`{"msgtype":"image","image":{"mediaid":"fixture-media"}}`)
	}
	payload, err := json.Marshal(map[string]any{"msgtype": "news", "news": map[string]string{"link": link, "title": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func sidebarSendAccept(ctx context.Context, service *outbound.SidebarSendService, customerID int64, employeeID, resourceKind, resourceID string, payload []byte, idempotencyKey string) (outboundport.SidebarSendAcceptance, error) {
	return service.AcceptSidebarSend(ctx, outboundport.SidebarSendCommand{CustomerID: customerID, EmployeeID: employeeID, ResourceKind: resourceKind, ResourceID: resourceID, ContentDigest: sha256.Sum256(payload), Payload: payload, IdempotencyKey: idempotencyKey})
}
