package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLReferralChromiumJourney exercises real host/session/SQL facts;
// payment is composed only to provide the existing trusted browser session.
// No distributor, qualification purchase, or receiver is created by this fixture.
func TestPostgreSQLReferralChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate referral journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key, cert := distributionFixturePaymentCredentials(t)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg := platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: strings.Repeat("1", 40), WorkerOwner: "referral-browser", WorkerLimit: 1,
		Referral:  platformconfig.Referral{TokenDataKey: dataKey},
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "referral-fixture-secret"},
		Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey, OAuthEnabled: true, OAuthAppID: "wx-referral-fixture-h5", OAuthSecret: "fixture-secret", OAuthOpenPlatformID: "referral-fixture-platform", OAuthScope: "snsapi_userinfo"},
		WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-referral-fixture", AppSecret: "fixture-secret", AppScope: "wechat-app:referral-fixture", H5OAuthEnabled: true, H5AppID: "wx-referral-fixture-h5", H5AppSecret: "fixture-secret", H5AppScope: "wechat-app:wx-referral-fixture-h5", OrderContactDataKey: dataKey, MerchantID: "fixture-mch", MerchantSerial: "fixture-serial", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: "0123456789abcdef0123456789abcdef"},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "referral-admin", Password: "referral-admin-password", DisplayName: "活动管理员"},
	}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, cfg.Bootstrap); err != nil {
		t.Fatal(err)
	}
	type fixtureActor struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Session string `json:"session"`
	}
	actors := []fixtureActor{}
	now := time.Now().UTC()
	for i := 0; i < 53; i++ {
		name := fmt.Sprintf("分页成员 %02d", i)
		if i == 0 {
			name = "队长阿青"
		} else if i == 1 {
			name = "队长小夏"
		} else if i == 2 {
			name = "超长昵称的活动参与者用于手机页面换行验证"
		}
		actor := fixtureActor{Name: name, Session: fmt.Sprintf("dist_%043d", i)}
		var identityID int64
		if err = application.pool.Native().QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&actor.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,source,updated_at) VALUES($1,'active',$2,'referral-browser',$3)`, actor.ID, name, now); err != nil {
			t.Fatal(err)
		}
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:referral-fixture',$2,'verified','referral-browser',1,$3) RETURNING id`, actor.ID, fmt.Sprintf("referral-fixture-%d", i), now).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(actor.Session))
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO distribution_browser_sessions(token_digest,customer_id,identity_id,channel,app_id,app_scope,expires_at,created_at) VALUES($1,$2,$3,'mini_program','wx-referral-fixture','wechat-app:referral-fixture',$4,$5)`, digest[:], actor.ID, identityID, now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
		actors = append(actors, actor)
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	fixture, _ := json.Marshal(actors)
	command := exec.CommandContext(ctx, "node", filepath.Join(repository, "cmd/aicrm/referral_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_REFERRAL_BROWSER_URL="+server.URL, "AICRM_REFERRAL_BROWSER_ACTORS="+string(fixture))
	output, runErr := command.CombinedOutput()
	if runErr != nil || !strings.Contains(string(output), "referral_chromium: PASS") {
		t.Fatalf("referral journey err=%v output=%s", runErr, output)
	}
	var referrer, version, historyCount int64
	if err = application.pool.Native().QueryRow(ctx, "SELECT referrer_customer_id, version FROM referral_current_relationships WHERE customer_id=$1", actors[2].ID).Scan(&referrer, &version); err != nil {
		t.Fatal(err)
	}
	if referrer != actors[1].ID || version != 2 {
		t.Fatalf("repeat acceptance changed current relation: referrer=%d version=%d", referrer, version)
	}
	if err = application.pool.Native().QueryRow(ctx, "SELECT count(*) FROM referral_relationship_history WHERE customer_id=$1", actors[2].ID).Scan(&historyCount); err != nil || historyCount != 2 {
		t.Fatalf("relationship history count=%d err=%v", historyCount, err)
	}
	var effects int
	if err = application.pool.Native().QueryRow(ctx, "SELECT count(*) FROM payment_profit_sharing_provider_intents").Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if effects != 0 {
		t.Fatal("invitation journey unexpectedly created payment effects")
	}
}
