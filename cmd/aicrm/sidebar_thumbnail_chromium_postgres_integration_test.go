package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// TestPostgreSQLSidebarThumbnailChromiumJourney exercises the deployed sidebar
// shell rather than a DOM fixture: Access owns the authenticated browser
// session, the sidebar bootstrap resolves an existing scoped OneID relation,
// Media owns the enabled image variant, and Chromium loads it through a blob
// object URL under the sidebar-only CSP relaxation.
//
// OneID decision: involved only through the existing scoped
// wecom_external_userid read path; the journey never provisions or merges a
// customer. Persistence decision: the isolated PostgreSQL fixture exercises
// profile CAS plus existing outbound intent acceptance/completion in their
// owning stores. External Effects decision: the journey verifies durable local
// acceptance, replay, and completion facts without a Provider business write;
// JSSDK ticket reads use the existing trusted read adapter.
func TestPostgreSQLSidebarThumbnailChromiumJourney(t *testing.T) {
	// Linux CI requires this journey. macOS runs the same HTTPS + DevTools
	// protocol when explicitly requested so a platform-specific startup issue is
	// diagnosed rather than hidden behind a skip.
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate sidebar Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	// compose resolves web/dist relative to the process directory. A package
	// test otherwise runs from cmd/aicrm and silently falls back to the frozen
	// shell, so Chrome never receives the staged sidebar Host it is meant to
	// exercise. Build the exact hashed release artifact first, as CI does.
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	provider := newCustomerTagChromiumProvider()
	defer provider.Close()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin,
		ReleaseSHA: "sidebar-thumbnail-chromium-journey", WorkerOwner: "sidebar-thumbnail-chromium-journey", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "sidebar-thumbnail-chromium-webhook-secret"},
		Survey:    platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "sidebar-browser-owner", Password: "sidebar-browser-owner-password", DisplayName: "Sidebar Browser Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: false},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "sidebar-thumbnail-context-key-32", APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "sidebar-browser-owner", Password: "sidebar-browser-owner-password", DisplayName: "Sidebar Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarThumbnailChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarBootstrapSurveySubmissions(ctx, application); err != nil {
		t.Fatal(err)
	}
	productID, serviceProductID, _, err := seedProductExternalPushChromiumJourney(ctx, application)
	if err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarStandardParityChromiumFacts(ctx, application, productID, serviceProductID); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarBusinessTimelineChromium(ctx, application); err != nil {
		t.Fatal(err)
	}
	assertSidebarSendHTTPReplayOmitsGrant(t, ctx, application, productID, serviceProductID)
	// Assert the same outer route Chromium will open. This makes a missing
	// repository-relative release artifact a deterministic test failure instead
	// of a generic DOM timeout after the browser starts.
	outerSidebar := httptest.NewRecorder()
	application.handler.ServeHTTP(outerSidebar, httptest.NewRequest(http.MethodGet, "/sidebar/bind-mobile", nil))
	if outerSidebar.Code != http.StatusOK || !bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarHost-`)) || !bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarStandardOverlay-`)) || !bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarImageResourceLoader-`)) || !bytes.Contains(outerSidebar.Body.Bytes(), []byte(`id="tabs"`)) || !bytes.Contains(outerSidebar.Body.Bytes(), []byte(`https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js`)) {
		t.Fatalf("outer composed sidebar status=%d sidebar_host=%t standard_overlay=%t image_loader=%t tabs=%t jssdk=%t", outerSidebar.Code, bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarHost-`)), bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarStandardOverlay-`)), bytes.Contains(outerSidebar.Body.Bytes(), []byte(`/sidebar-assets/sidebarImageResourceLoader-`)), bytes.Contains(outerSidebar.Body.Bytes(), []byte(`id="tabs"`)), bytes.Contains(outerSidebar.Body.Bytes(), []byte(`https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js`)))
	}
	server.Config.Handler = application.handler
	server.StartTLS()

	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "..", "..", "internal", "webshell", "sidebar_thumbnail_chromium.test.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_SIDEBAR_THUMBNAIL_TEST_URL="+server.URL,
		"AICRM_SIDEBAR_THUMBNAIL_TEST_USERNAME=sidebar-browser-owner",
		"AICRM_SIDEBAR_THUMBNAIL_TEST_PASSWORD=sidebar-browser-owner-password",
		"AICRM_SIDEBAR_JSSDK_FIXTURE="+filepath.Join(repository, "web", "v3", "sidebar", "testdata", "wecom-jweixin-1.0.0.js"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sidebar thumbnail Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "sidebar_thumbnail_chromium: PASS") {
		t.Fatalf("sidebar thumbnail Chromium journey did not report success: %q", output)
	}
	var profileSource, industry string
	var profileVersion int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT profile_source,industry,version FROM customer_sidebar_profiles WHERE customer_id=1`).Scan(&profileSource, &industry, &profileVersion); err != nil {
		t.Fatal(err)
	}
	if profileSource != "Chromium活动报名" || industry != "教育" || profileVersion != 2 {
		t.Fatalf("Chromium profile durable facts source=%q industry=%q version=%d", profileSource, industry, profileVersion)
	}
	writes, businessReads := provider.Counts()
	tokenReads, corpTicketReads, agentTicketReads := provider.JSSDKReadCounts()
	if writes != 0 || businessReads != 0 || tokenReads != 1 || corpTicketReads != 1 || agentTicketReads != 1 {
		t.Fatalf("sidebar handshake writes=%d business_reads=%d access_token_reads=%d corp_ticket_reads=%d agent_ticket_reads=%d", writes, businessReads, tokenReads, corpTicketReads, agentTicketReads)
	}
}

func seedSidebarThumbnailChromiumJourney(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	for _, statement := range []string{
		`UPDATE admin_users SET wecom_userid='fixture-staff' WHERE id=1`,
		`INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`,
		`INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES(1,'wecom_external_userid','wecom-corp:fixture-corp','sidebar-thumbnail-external','verified','sidebar_thumbnail_chromium_fixture',1,clock_timestamp())`,
		`INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES('sidebar-thumbnail-chromium-seed','manual','succeeded','wecom-corp:fixture-corp',jsonb_build_array('fixture-staff'),clock_timestamp(),clock_timestamp())`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	var runID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='sidebar-thumbnail-chromium-seed'`).Scan(&runID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES(1,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=1),'sidebar thumbnail customer with a deliberately long display name','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1)`, runID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('fixture-corp','fixture-staff',1,true)`); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at) VALUES(1,'active','sidebar thumbnail customer with a deliberately long display name','customer #1','active','sidebar_thumbnail_chromium_fixture',clock_timestamp(),clock_timestamp())`); err != nil {
		return err
	}
	content, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err = pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		return err
	}
	for index := 1; index <= 7; index++ {
		if _, err = pool.Exec(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,$2,$3,'Chromium pagination fixture','Chromium fixture','sidebar-fixture','image/png',$4,1,1,true,1,1)`, digest, fmt.Sprintf("sidebar-thumbnail-%02d.png", index), fmt.Sprintf("Chromium sidebar thumbnail %02d", index), len(content)); err != nil {
			return err
		}
	}
	return nil
}

func seedSidebarStandardParityChromiumFacts(ctx context.Context, application *composedApplication, productID, serviceProductID int64) error {
	pool := application.pool.Native()
	now := time.Now().UTC().Truncate(time.Second)
	orderDigest := sha256.Sum256([]byte("sidebar-standard-order"))
	var orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
VALUES('wechat_pay','sidebar-chromium','customer-order','SIDEBAR-ORDER-001','',1,1,9900,9900,'CNY','refunded','history',FALSE,$1,1,$2,$2) RETURNING id`, orderDigest[:], now.Add(-48*time.Hour)).Scan(&orderID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,$2,'browser-push-product','浏览器外推商品',9900,1,9900)`, orderID, productID); err != nil {
		return err
	}
	entitlementDigest := sha256.Sum256([]byte("sidebar-standard-entitlement"))
	if _, err := pool.Exec(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at)
VALUES('sidebar-chromium','customer-entitlement',1,$1,'浏览器周期外推商品','active',$2,$3,'31日真实服务周期',$4,$2,$2)`, serviceProductID, now.Add(-24*time.Hour), now.AddDate(0, 0, 30), entitlementDigest[:]); err != nil {
		return err
	}
	type couponSeed struct {
		name, slug    string
		starts, ends  time.Time
		limit, issued int
	}
	seeds := []couponSeed{
		{"Chromium可领取券", "chromium-active", now.Add(-time.Hour), now.Add(24 * time.Hour), 2, 0},
		{"Chromium未开始券", "chromium-future", now.Add(time.Hour), now.Add(24 * time.Hour), 2, 0},
		{"Chromium已结束券", "chromium-expired", now.Add(-48 * time.Hour), now.Add(-time.Hour), 2, 0},
		{"Chromium已领完券", "chromium-soldout", now.Add(-time.Hour), now.Add(24 * time.Hour), 2, 2},
		{"Chromium个人上限券", "chromium-limit", now.Add(-time.Hour), now.Add(24 * time.Hour), 1, 1},
		{"Chromium无链接券", "", now.Add(-time.Hour), now.Add(24 * time.Hour), 2, 0},
	}
	for index, seed := range seeds {
		var couponID int64
		var slug any
		if seed.slug != "" {
			slug = seed.slug
		}
		if err := pool.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,relative_validity_days,instructions,created_by,updated_by,created_at,updated_at,public_slug)
VALUES($1,1000,'CNY','published',$2,$2,$3,$4,$5,'relative_days',30,'Chromium sidebar fixture',1,1,$6,$6,$7) RETURNING id`, seed.name, seed.limit, seed.issued, seed.starts, seed.ends, now.Add(time.Duration(index)*time.Second), slug).Scan(&couponID); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,$2,0)`, couponID, fmt.Sprintf("standard_product:%d", productID)); err != nil {
			return err
		}
		if seed.name == "Chromium个人上限券" {
			claimDigest := sha256.Sum256([]byte(seed.name))
			if _, err := pool.Exec(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claimed_at,valid_from,valid_until,source_digest,created_at,updated_at)
VALUES('sidebar-chromium','limit-claim',1,$1,'claimed',$2,$2,$3,$4,$2,$2)`, couponID, now.Add(-time.Minute), now.AddDate(0, 0, 30), claimDigest[:]); err != nil {
				return err
			}
		}
	}
	return nil
}

func assertSidebarSendHTTPReplayOmitsGrant(t *testing.T, ctx context.Context, application *composedApplication, productID, serviceProductID int64) {
	t.Helper()
	contextToken, err := (wecom.ContextTokenService{CorpID: "fixture-corp", SigningKey: []byte("sidebar-thumbnail-context-key-32")}).Issue(ctx, wecom.SidebarPrincipal{CorpID: "fixture-corp", EmployeeID: "fixture-staff"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	type acceptance struct {
		IntentID int64           `json:"intent_id"`
		EffectID string          `json:"effect_id"`
		State    string          `json:"state"`
		Grant    string          `json:"grant"`
		Payload  json.RawMessage `json:"payload"`
		Replayed bool            `json:"replayed"`
	}
	send := func(key string, resourceID int64, productType string) acceptance {
		request := httptest.NewRequest(http.MethodPost, "/api/sidebar/v2/send-intents", strings.NewReader(fmt.Sprintf(`{"resource_kind":"product","resource_id":"%d","product_type":%q}`, resourceID, productType)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Sidebar-Context-Token", contextToken)
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("sidebar send accept status=%d body=%s", response.Code, response.Body.String())
		}
		var result acceptance
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, replay := send("sidebar-chromium-replay-contract", productID, "standard"), send("sidebar-chromium-replay-contract", productID, "standard")
	if first.IntentID < 1 || first.State != "queued" || first.Grant == "" || len(first.Payload) == 0 || first.Replayed ||
		replay.IntentID != first.IntentID || replay.State != "queued" || replay.Grant != "" || len(replay.Payload) == 0 || !replay.Replayed {
		t.Fatalf("sidebar real HTTP replay first=%+v replay=%+v", first, replay)
	}
	var intents, grants int
	if err := application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_sidebar_send_intents WHERE id=$1),(SELECT count(*) FROM outbound_sidebar_send_grants WHERE intent_id=$1)`, first.IntentID).Scan(&intents, &grants); err != nil {
		t.Fatal(err)
	}
	if intents != 1 || grants != 1 {
		t.Fatalf("sidebar replay durable rows intents=%d grants=%d", intents, grants)
	}
	outcomeRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/sidebar/v2/send-intents/%d/outcome", first.IntentID), strings.NewReader(fmt.Sprintf(`{"grant":%q,"outcome":"final_failed","evidence":"replay-contract-not-executed"}`, first.Grant)))
	outcomeRequest.Header.Set("Content-Type", "application/json")
	outcomeRequest.Header.Set("X-Sidebar-Context-Token", contextToken)
	outcomeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(outcomeResponse, outcomeRequest)
	if outcomeResponse.Code != http.StatusOK {
		t.Fatalf("sidebar replay original-scope completion status=%d body=%s", outcomeResponse.Code, outcomeResponse.Body.String())
	}
	var state string
	if err := application.pool.Native().QueryRow(ctx, `SELECT state FROM outbound_sidebar_send_intents WHERE id=$1`, first.IntentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "final_failed" {
		t.Fatalf("sidebar replay original-scope completion state=%q", state)
	}

	expiring := send("sidebar-chromium-expiry-contract", serviceProductID, "service_period")
	if expiring.IntentID < 1 || expiring.EffectID == "" || expiring.State != "queued" || expiring.Grant == "" || expiring.Replayed {
		t.Fatalf("sidebar expiry acceptance=%+v", expiring)
	}
	expiry := outbound.SidebarJSSDKExpiry{}
	envelope := effectport.Envelope{Kind: effectport.KindSidebarJSSDKSend, PayloadDigest: effectport.Hash("sidebar-expiry-contract", expiring.EffectID)}
	result, err := expiry.Execute(ctx, envelope, effectport.Attempt{EffectID: expiring.EffectID, Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = uow.Within(ctx, func(txctx context.Context) error {
			return expiry.CompleteEffect(txctx, expiring.EffectID, envelope, effectport.Attempt{EffectID: expiring.EffectID, Number: 1}, result)
		}); err != nil {
			t.Fatal(err)
		}
	}
	var expireAudits, expireOutbox int
	var expirePayload json.RawMessage
	if err = application.pool.Native().QueryRow(ctx, `SELECT intent.state,(SELECT count(*) FROM outbound_sidebar_send_audit_events audit WHERE audit.intent_id=intent.id AND audit.operation='expire'),(SELECT count(*) FROM outbound_sidebar_send_outbox event WHERE event.intent_id=intent.id AND event.event_type='outbound.sidebar_send.expired.v1'),(SELECT payload FROM outbound_sidebar_send_outbox event WHERE event.intent_id=intent.id AND event.event_type='outbound.sidebar_send.expired.v1') FROM outbound_sidebar_send_intents intent WHERE intent.id=$1`, expiring.IntentID).Scan(&state, &expireAudits, &expireOutbox, &expirePayload); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		IntentID int64  `json:"intent_id"`
		EffectID string `json:"effect_id"`
		State    string `json:"state"`
	}
	if err = json.Unmarshal(expirePayload, &payload); err != nil {
		t.Fatal(err)
	}
	if state != "outcome_unknown" || expireAudits != 1 || expireOutbox != 1 || payload.IntentID != expiring.IntentID || payload.EffectID != expiring.EffectID || payload.State != "outcome_unknown" {
		t.Fatalf("sidebar expiry durable facts state=%q audits=%d outbox=%d payload=%+v", state, expireAudits, expireOutbox, payload)
	}
	reloadedExpiry := send("sidebar-chromium-expiry-reload", serviceProductID, "service_period")
	if !reloadedExpiry.Replayed || reloadedExpiry.IntentID != expiring.IntentID || reloadedExpiry.State != "outcome_unknown" || reloadedExpiry.Grant != "" {
		t.Fatalf("sidebar expiry reload must replay unresolved intent result=%+v", reloadedExpiry)
	}
}

// Only local owner facts are seeded. No customer Provider write is needed to
// prove the rendered business feed, and sync audit rows remain stored.
func seedSidebarBusinessTimelineChromium(ctx context.Context, app *composedApplication) error {
	tx, err := app.pool.Native().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var channelID, runID, radarID, identityID, sessionID int64
	now := time.Now().UTC().Add(-10 * time.Second)
	if err = tx.QueryRow(ctx, `INSERT INTO channels(code,status,created_at,updated_at) VALUES('chromium.timeline','active',$1,$1) RETURNING id`, now).Scan(&channelID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,assignment_mode,assignment_strategy,config_digest,created_by,created_at) VALUES($1,1,'qrcode','qrcode','Chromium渠道活动','single_owner','ratio',decode(repeat('08',32),'hex'),1,$2)`, channelID, now); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state,completed_at) VALUES('chromium-timeline-history',decode(repeat('09',32),'hex'),$1,decode(repeat('10',32),'hex'),'completed',$1) RETURNING id`, now).Scan(&runID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_history_contacts(import_run_id,channel_id,source_contact_id,customer_id,first_entered_at,last_entered_at,enter_count) VALUES($1,$2,1,1,$3,$3,1)`, runID, channelID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customer_timeline_projection(customer_id,source_domain,source_event_id,event_type,title,occurred_at) VALUES(1,'wecom','chromium-sync-noise','customer.profile_synced','企微客户资料已同步',$1)`, now); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM customer_identities WHERE customer_id=1 AND assurance='verified' ORDER BY id LIMIT 1`).Scan(&identityID); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at) VALUES('rd_timeline12345678','Chromium雷达介绍','Chromium雷达介绍','link','https://example.com/timeline','unionid_required','enabled',1,1,$1,$1) RETURNING id`, now).Scan(&radarID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at) VALUES(decode(repeat('11',32),'hex'),$1,1,$2,1,'resolved',decode(repeat('12',32),'hex'),$3,$4) RETURNING id`, radarID, identityID, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at) VALUES('chromium-timeline-radar',$1,1,$2,'content_opened','resolved',$3,1,decode(repeat('13',32),'hex'),decode(repeat('14',32),'hex'),$4,$4)`, radarID, sessionID, identityID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
