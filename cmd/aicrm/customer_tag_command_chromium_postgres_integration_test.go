package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLCustomerTagCommandChromiumJourney uses the actual Access
// session/CSRF path, the rendered customer Host, PostgreSQL/River and the
// Composition-owned WeCom Provider adapter. OneID resolves two seeded trusted
// external identities; the fixture only observes opaque test targets. The
// second write deliberately returns plain-text HTTP 500 without a trusted
// Provider errcode, so the durable result remains outcome_unknown rather than
// pretending it was rejected.
//
// OneID decision: involved through the scoped wecom_external_userid read Port;
// the journey seeds identities but never provisions or merges a customer.
// Persistence decision: command, EER receipt, River job and completion share
// PostgreSQL transactions; mark_tag/readback are controlled external fixture calls.
func TestPostgreSQLCustomerTagCommandChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate customer tag Chromium script")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	// The actual customer Host now waits for the manifest-built V3 standard
	// component before it reads the tag directory. Compose must therefore use
	// the repository root, just as the dedicated picker journey does; a raw
	// cmd/aicrm working directory leaves Runtime assets empty and makes the
	// real page fail closed.
	t.Chdir(repository)
	prepareTagPickerChromiumArtifacts(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	provider := newCustomerTagChromiumProvider()
	defer provider.Close()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin,
		ReleaseSHA: "customer-tag-chromium-journey", WorkerOwner: "customer-tag-chromium-journey", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "customer-tag-chromium-webhook-secret"},
		Survey:    platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "browser-owner", Password: "browser-owner-password", DisplayName: "Browser Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: true},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "customer-tag-chromium-context-key-32", CustomerTagProviderEnabled: true, APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "browser-owner", Password: "browser-owner-password", DisplayName: "Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedCustomerTagChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- application.effectsRuntime.Run(workerCtx) }()
	defer func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("effects runtime: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("effects runtime did not stop")
		}
	}()

	server.Config.Handler = application.handler
	server.StartTLS()
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "customer_tag_command_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_CUSTOMER_TAG_TEST_URL="+server.URL, "AICRM_CUSTOMER_TAG_TEST_USERNAME=browser-owner", "AICRM_CUSTOMER_TAG_TEST_PASSWORD=browser-owner-password")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("customer tag Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "customer_tag_command_chromium: PASS") {
		t.Fatalf("customer tag Chromium journey did not report success: %q", output)
	}
	var executed, unknown, writes, reads int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='executed'),count(*) FILTER (WHERE state='outcome_unknown') FROM customer_tag_command_lines`).Scan(&executed, &unknown); err != nil {
		t.Fatal(err)
	}
	var providerTagID, observedName, observationStatus string
	if err = application.pool.Native().QueryRow(ctx, `SELECT provider_tag_id,observed_name,observation_status FROM wecom_customer_tag_observations WHERE customer_id=1 AND observation_status='active'`).Scan(&providerTagID, &observedName, &observationStatus); err != nil {
		t.Fatal(err)
	}
	writes, reads = provider.Counts()
	if executed != 1 || unknown != 1 || writes != 2 || reads < 1 || providerTagID != "fixture-provider-add" || observedName != "fixture observed" || observationStatus != "active" {
		t.Fatalf("durable outcomes executed=%d unknown=%d provider_tag_id=%q observed_name=%q observation_status=%q provider_writes=%d provider_reads=%d", executed, unknown, providerTagID, observedName, observationStatus, writes, reads)
	}
}

type customerTagChromiumProvider struct {
	server                                           *httptest.Server
	mu                                               sync.Mutex
	writes, reads                                    int
	jssdkTokenReads, jssdkCorpReads, jssdkAgentReads int
}

func newCustomerTagChromiumProvider() *customerTagChromiumProvider {
	fixture := &customerTagChromiumProvider{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			fixture.mu.Lock()
			fixture.jssdkTokenReads++
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/get_jsapi_ticket":
			fixture.mu.Lock()
			fixture.jssdkCorpReads++
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"ticket":"fixture-corp-ticket","expires_in":7200}`))
		case "/cgi-bin/ticket/get":
			if r.URL.Query().Get("type") != "agent_config" {
				http.Error(w, "unexpected JSSDK ticket type", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.jssdkAgentReads++
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"ticket":"fixture-agent-ticket","expires_in":7200}`))
		case "/cgi-bin/externalcontact/mark_tag":
			var request struct {
				ExternalUserID string   `json:"external_userid"`
				UserID         string   `json:"userid"`
				AddTag         []string `json:"add_tag"`
				RemoveTag      []string `json:"remove_tag"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if request.UserID != "fixture-staff" || len(request.AddTag) != 1 || request.AddTag[0] != "fixture-provider-add" || len(request.RemoveTag) != 1 || request.RemoveTag[0] != "fixture-provider-remove" {
				http.Error(w, "unexpected tag mutation", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.writes++
			fixture.mu.Unlock()
			if request.ExternalUserID == "fixture-external-two" {
				http.Error(w, "fixture plain response failure", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/get":
			fixture.mu.Lock()
			fixture.reads++
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"fixture-external-one","name":"fixture","type":1,"gender":0},"follow_user":[{"userid":"fixture-staff","tags":[{"tag_id":"fixture-provider-add","name":"fixture observed","type":1}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return fixture
}
func (f *customerTagChromiumProvider) URL() string          { return f.server.URL }
func (f *customerTagChromiumProvider) Client() *http.Client { return f.server.Client() }
func (f *customerTagChromiumProvider) Close()               { f.server.Close() }
func (f *customerTagChromiumProvider) Counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes, f.reads
}

// JSSDKReadCounts separates the access-token and both signing-ticket reads from
// business-provider observations so the Chromium journey also proves cache reuse.
func (f *customerTagChromiumProvider) JSSDKReadCounts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jssdkTokenReads, f.jssdkCorpReads, f.jssdkAgentReads
}

func seedCustomerTagChromiumJourney(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	_, err := pool.Exec(ctx, `UPDATE admin_users SET wecom_userid='fixture-staff' WHERE id=1;
		INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active');
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES
		(1,'wecom_external_userid','wecom-corp:fixture-corp','fixture-external-one','verified','chromium_fixture',1,clock_timestamp()),
		(2,'wecom_external_userid','wecom-corp:fixture-corp','fixture-external-two','verified','chromium_fixture',1,clock_timestamp());
		INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES('customer-tag-chromium-seed','manual','succeeded','wecom-corp:fixture-corp',jsonb_build_array('fixture-staff'),clock_timestamp(),clock_timestamp());`)
	if err != nil {
		return err
	}
	var runID int64
	if err = pool.QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='customer-tag-chromium-seed'`).Scan(&runID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES
		(1,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=1),'fixture one','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1),
		(2,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=2),'fixture two','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1)`, runID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('fixture-corp','fixture-staff',1,true),('fixture-corp','fixture-staff',2,true);
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at) VALUES
		(1,'active','fixture one','customer #1','active','chromium_fixture',clock_timestamp(),clock_timestamp()),
		(2,'active','fixture two','customer #2','active','chromium_fixture',clock_timestamp(),clock_timestamp());
		INSERT INTO tag_groups(group_name,sort_order) VALUES('fixture group',0);
		INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES((SELECT id FROM tag_groups WHERE group_name='fixture group'),'fixture add',0),((SELECT id FROM tag_groups WHERE group_name='fixture group'),'fixture remove',1);
		INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('fixture-provider-add',(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture add')),('fixture-provider-remove',(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture remove'));`)
	return err
}
