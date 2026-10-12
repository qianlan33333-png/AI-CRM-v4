package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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
	if !platformconfig.ChromiumJourneyRequired() && *domesticReleaseSmokeInputFile == "" {
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
	if *domesticReleaseSmokeInputFile == "" {
		prepareProductExternalPushChromiumArtifacts(t, repository)
	}
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
	if *domesticReleaseSmokeInputFile != "" {
		inputs, binary, releaseRoot := referralInstalledArtifact(t)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		origin := cfg.PublicOrigin
		environment := referralPosterInstalledEnvironment(databaseURL, inputs.InstalledSHA, address, origin)
		process := startDomesticSmokeProcess(t, binary, releaseRoot, environment)
		defer func() {
			if !process.finished {
				_ = process.command.Process.Signal(os.Interrupt)
				select {
				case err := <-process.done:
					process.finished = true
					if err != nil {
						t.Errorf("installed poster API did not stop: %T", err)
					}
				case <-time.After(8 * time.Second):
					_ = process.command.Process.Kill()
					<-process.done
					process.finished = true
					t.Error("installed poster API stop timeout")
				}
			}
		}()
		target, _ := url.Parse("http://" + address)
		waitDomesticReleaseReady(t, process, target.String(), inputs.InstalledSHA)
		server.Config.Handler = httputil.NewSingleHostReverseProxy(target)
		t.Logf("referral_poster_installed: exact binary selected source_sha=%s installed_sha=%s", inputs.SourceSHA, inputs.InstalledSHA)
	}
	server.StartTLS()
	fixture, _ := json.Marshal(actors)
	posterBytes := referralBrowserPosterFixture(t)
	if sourcePath := os.Getenv("AICRM_REFERRAL_POSTER_SOURCE"); sourcePath != "" {
		actual, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		posterBytes = actual
	}
	if len(posterBytes) > 1048576 {
		t.Fatal("poster browser upload fixture exceeds 1MiB; do not bypass manual-upload policy")
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(posterBytes)); err != nil || format != "png" {
		t.Fatal("poster browser upload fixture must be a valid PNG")
	}
	posterPath := filepath.Join(t.TempDir(), "poster.png")
	if err = os.WriteFile(posterPath, posterBytes, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repository, "cmd/aicrm/referral_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_REFERRAL_BROWSER_URL="+server.URL, "AICRM_REFERRAL_BROWSER_ACTORS="+string(fixture), "AICRM_REFERRAL_POSTER_PATH="+posterPath)
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

func referralPosterInstalledEnvironment(databaseURL, sha, address, origin string) []string {
	environment := paidReferralInstalledEnvironment(databaseURL, sha, address)
	for i, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "AICRM_PUBLIC_ORIGIN", "AICRM_H5_PUBLIC_ORIGIN":
			environment[i] = key + "=" + origin
		case "AICRM_WECHAT_PAY_APP_ID":
			environment[i] = key + "=wx-referral-fixture"
		case "AICRM_WECHAT_PAY_APP_SCOPE":
			environment[i] = key + "=wechat-app:referral-fixture"
		}
	}
	return environment
}
func TestReferralPosterInstalledEnvironment(t *testing.T) {
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "AICRM_") {
			t.Setenv(key, "")
		}
	}
	for _, value := range referralPosterInstalledEnvironment("postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_poster", strings.Repeat("a", 40), "127.0.0.1:18081", "https://127.0.0.1:18443") {
		key, value, _ := strings.Cut(value, "=")
		t.Setenv(key, value)
	}
	cfg, err := platformconfig.Load()
	if err != nil || cfg.PublicOrigin != "https://127.0.0.1:18443" || cfg.WeChatPay.AppScope != "wechat-app:referral-fixture" || cfg.WeChatPay.Enabled || cfg.Alipay.Enabled || cfg.Effects.ProviderEnabled {
		t.Fatalf("installed poster environment rejected or enables Providers: %v", err)
	}
}

// Deterministic, correlated gray texture keeps PNG below the manual upload
// policy while retaining enough detail to exercise JPEG95 reduction.
func referralBrowserPosterFixture(t *testing.T) []byte {
	t.Helper()
	poster := image.NewGray(image.Rect(0, 0, 1250, 2426))
	for y := 0; y < 2426; y++ {
		for x := 0; x < 1250; x++ {
			poster.Pix[y*poster.Stride+x] = uint8(96 + (x/32+y/48)%64)
		}
	}
	const side = 640
	noise := make([]byte, side*side)
	var state uint32 = 42
	for i := range noise {
		state = 1664525*state + 1013904223
		noise[i] = uint8(state >> 24)
	}
	for y := 1; y < side-1; y++ {
		for x := 1; x < side-1; x++ {
			sum := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					sum += int(noise[(y+dy)*side+x+dx])
				}
			}
			poster.Pix[(y+120)*poster.Stride+x+80] = uint8(sum / 9)
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, poster); err != nil {
		t.Fatal(err)
	}
	return pngBytes.Bytes()
}
func TestReferralPosterFixtureFitsManualUploadAndJPEG95(t *testing.T) {
	raw := referralBrowserPosterFixture(t)
	if len(raw) == 0 || len(raw) > 1048576 {
		t.Fatalf("fixture PNG bytes=%d", len(raw))
	}
	decoded, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil || format != "png" || decoded.Bounds().Dx() != 1250 || decoded.Bounds().Dy() != 2426 {
		t.Fatalf("invalid PNG fixture: %v", err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: 95}); err != nil || encoded.Len() >= len(raw) {
		t.Fatalf("JPEG95 fixture must reduce PNG: PNG=%d JPEG=%d err=%v", len(raw), encoded.Len(), err)
	}
	t.Logf("manual upload fixture PNG=%d bytes JPEG95=%d bytes", len(raw), encoded.Len())
}
