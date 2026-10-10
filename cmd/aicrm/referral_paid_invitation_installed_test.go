package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func paidReferralInstalledEnvironment(databaseURL, releaseSHA, address string) []string {
	dataKey := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	return []string{"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_RELEASE_SHA=" + releaseSHA, "AICRM_LISTEN_ADDR=" + address, "AICRM_PUBLIC_ORIGIN=https://crm.example.test", "AICRM_H5_PUBLIC_ORIGIN=https://crm.example.test", "AICRM_REFERRAL_TOKEN_DATA_KEY=" + dataKey, "AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey, "AICRM_GROUP_OPS_WEBHOOK_SECRET=synthetic-referral-installed-secret-test", "AICRM_WECHAT_PAY_APP_ID=wx-promotion-checkout", "AICRM_WECHAT_PAY_APP_SCOPE=wechat-app:promotion-checkout", "AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false"}
}

func TestPaidReferralInstalledEnvironmentMeetsRuntimeConfig(t *testing.T) {
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "AICRM_") {
			t.Setenv(key, "")
		}
	}
	for _, value := range paidReferralInstalledEnvironment("postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_referral_input", strings.Repeat("a", 40), "127.0.0.1:18081") {
		key, value, _ := strings.Cut(value, "=")
		t.Setenv(key, value)
	}
	cfg, err := platformconfig.Load()
	if err != nil {
		t.Fatalf("installed referral synthetic runtime input rejected: %v", err)
	}
	if cfg.Role != platformconfig.RoleAPI || cfg.WeChatPay.Enabled || cfg.Alipay.Enabled || cfg.Effects.ProviderEnabled {
		t.Fatal("installed referral input must run API with Providers disabled")
	}
}

// The shared order fixture seeds core customers directly. Prepare the minimum
// presentation directory through its owner, as the invoice installed fixture
// does, before launching an actual API. Keep production readiness unchanged.
func preparePaidReferralInstalledDirectory(t *testing.T, f *promotionCheckoutFixture, customerIDs []int64) {
	t.Helper()
	ctx := context.Background()
	var directory customerport.ProjectionWriter = customerstore.NewPostgreSQL()
	if err := f.uow.Within(ctx, func(tx context.Context) error {
		now := time.Now().UTC()
		for _, id := range customerIDs {
			if err := directory.UpsertDirectoryProjection(tx, customerport.DirectoryProjection{
				CustomerID: customerdomain.CustomerID(id), CustomerStatus: customerdomain.StatusActive,
				DisplayName: "隔离活动测试用户", OneIDLabel: "CID-" + strconv.FormatInt(id, 10), ActivationState: "active",
				Source: "referral.paid.installed.fixture", SourceVersion: 1, LastSyncedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal("prepare paid referral fixture minimum directory", err)
	}
	if err := checkCurrentReleaseSchema(ctx, f.pool, platformconfig.Runtime{}); err != nil {
		t.Fatal("paid referral fixture fails installed schema readiness", err)
	}
	t.Log("paid referral fixture minimum directory and schema readiness: passed")
}

// Optional installed readback joins the real-order Journey only when the
// existing fixed staging helper supplies its protected, exact-binary inputs.
// It uses the Journey's isolated synthetic database; all Providers stay off.
func verifyInstalledPaidReferral(t *testing.T, f *promotionCheckoutFixture, actor referralport.TrustedSessionActor, campaignID int64) {
	verifyInstalledPaidReferralWithOptions(t, f, actor, campaignID, false, 1)
}

func verifyInstalledPaidReferralWithOptions(t *testing.T, f *promotionCheckoutFixture, actor referralport.TrustedSessionActor, campaignID int64, financial bool, expectedInvitations int64) {
	t.Helper()
	if *domesticReleaseSmokeInputFile == "" {
		return
	}
	inputs, err := readDomesticReleaseSmokeInputs(*domesticReleaseSmokeInputFile)
	if err != nil {
		t.Fatal("invalid installed referral smoke input")
	}
	if inputs.StageRole != "staging" || !validDomesticReleaseSHA(inputs.InstalledSHA) || !validDomesticReleaseSHA(inputs.SourceSHA) {
		t.Fatal("installed referral Journey requires staging identities")
	}
	binary := filepath.Clean(inputs.InstalledBinary)
	releaseRoot := filepath.Dir(filepath.Dir(binary))
	if binary != filepath.Join("/opt/aicrm/releases", inputs.InstalledSHA, "bin/aicrm") {
		t.Fatal("installed referral binary is outside exact release")
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatal("installed referral binary unavailable")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("cannot hash installed referral binary")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != inputs.InstalledBinarySHA256 {
		t.Fatal("installed referral binary digest differs")
	}
	sessions, err := distributionapp.NewBrowserSessionService(f.uow, f.distributions)
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.BindIdentitySubjects(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	session, _, err := sessions.Issue(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	environment := paidReferralInstalledEnvironment(f.pool.Config().ConnConfig.ConnString(), inputs.InstalledSHA, address)
	if financial {
		environment = paidReferralFinancialInstalledEnvironment(t, f.pool.Config().ConnConfig.ConnString(), inputs.InstalledSHA, address)
	}
	process := startDomesticSmokeProcess(t, binary, releaseRoot, environment)
	// Stop before this Journey's deferred database cleanup removes its clone.
	defer func() {
		if process.finished {
			return
		}
		_ = process.command.Process.Signal(os.Interrupt)
		select {
		case stopErr := <-process.done:
			process.finished = true
			if stopErr != nil {
				t.Error("installed referral API did not stop cleanly")
			}
		case <-time.After(8 * time.Second):
			_ = process.command.Process.Kill()
			<-process.done
			process.finished = true
			t.Error("installed referral API did not stop after interrupt")
		}
	}()
	base := "http://" + address
	waitDomesticReleaseReady(t, process, base, inputs.InstalledSHA)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path string) *http.Response {
		t.Helper()
		req, e := http.NewRequest(method, base+path, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: session})
		if method == http.MethodPost {
			req.Header.Set("Origin", "https://crm.example.test")
			req.Header.Set("Idempotency-Key", "installed-paid-referral-invite")
			req.Header.Set("X-Distribution-CSRF", "synthetic-referral-csrf")
			req.AddCookie(&http.Cookie{Name: "aicrm_distribution_csrf", Value: "synthetic-referral-csrf"})
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal("installed referral request failed")
		}
		return response
	}
	path := "/api/v1/referral/campaigns/" + strconv.FormatInt(campaignID, 10)
	response := request(http.MethodGet, path+"/me")
	var mine struct {
		Invitations int64 `json:"direct_invitation_count"`
		Rank        int64 `json:"personal_rank"`
		Available   bool  `json:"invitation_available"`
	}
	err = json.NewDecoder(response.Body).Decode(&mine)
	response.Body.Close()
	if response.StatusCode != 200 || err != nil || mine.Invitations != expectedInvitations || mine.Rank != 1 || !mine.Available {
		t.Fatal("installed paid participant readback failed")
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Now().In(shanghai).Format("2006-01-02")
	for _, period := range []string{"total", "all", "day", "week"} {
		query := "?kind=personal&period=" + period
		if period == "day" || period == "week" {
			query += "&date=" + date
		}
		response = request(http.MethodGet, path+"/leaderboard"+query)
		var board struct {
			Items []struct {
				Score int64 `json:"score"`
			} `json:"items"`
		}
		err = json.NewDecoder(response.Body).Decode(&board)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil || len(board.Items) != 1 || board.Items[0].Score != expectedInvitations {
			t.Fatalf("installed paid people leaderboard failed period=%s status=%d", period, response.StatusCode)
		}
	}
	response = request(http.MethodPost, path+"/invite")
	var link struct {
		URL string `json:"url"`
	}
	err = json.NewDecoder(response.Body).Decode(&link)
	response.Body.Close()
	parsed, parseErr := url.Parse(link.URL)
	if response.StatusCode != http.StatusOK || err != nil || parseErr != nil || parsed.Host != "crm.example.test" || !regexp.MustCompile(`^/referral/invite/rfi_[A-Za-z0-9_-]{43}$`).MatchString(parsed.Path) {
		t.Fatal("installed invitation still requires financial registration")
	}
	if financial {
		digest := sha256.Sum256([]byte(strings.TrimPrefix(parsed.Path, "/referral/invite/")))
		var identityID int64
		var appID, appScope, channel string
		if e := f.pool.QueryRow(context.Background(), `SELECT issuer_identity_id,issuer_app_id,issuer_app_scope,issuer_channel FROM referral_invitations WHERE token_digest=$1`, digest[:]).Scan(&identityID, &appID, &appScope, &channel); e != nil || identityID != actor.IdentityID || appID != actor.AppID || appScope != actor.AppScope || channel != actor.Channel {
			t.Fatal("installed financial invitation lost verified issuer snapshot")
		}
	}
	response = request(http.MethodGet, parsed.Path)
	cookies := response.Cookies()
	response.Body.Close()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "aicrm_referral_activity_context" && cookie.Value == strings.TrimPrefix(parsed.Path, "/referral/invite/") && cookie.HttpOnly && cookie.Secure {
			found = true
		}
	}
	if response.StatusCode != 303 || response.Header.Get("Location") != "/p/promotion-checkout-product" || !found {
		t.Fatal("installed paid invitation handoff lost opaque attribution")
	}
	response = request(http.MethodGet, "/referral?campaign="+strconv.FormatInt(campaignID, 10))
	html, e := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	response.Body.Close()
	if response.StatusCode != 200 || e != nil || !bytes.Contains(html, []byte("referral-root")) {
		t.Fatal("installed activity page unavailable")
	}
	matches := regexp.MustCompile(`<script[^>]+src="([^"]+)"`).FindSubmatch(html)
	if len(matches) != 2 {
		t.Fatal("installed activity asset missing")
	}
	documentURL, _ := url.Parse(base + "/referral?campaign=" + strconv.FormatInt(campaignID, 10))
	assetRef, e := url.Parse(string(matches[1]))
	if e != nil {
		t.Fatal(e)
	}
	assetURL := documentURL.ResolveReference(assetRef)
	if assetURL.Host != address {
		t.Fatal("installed activity asset crosses origin")
	}
	response = request(http.MethodGet, assetURL.RequestURI())
	js, e := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	response.Body.Close()
	if response.StatusCode != 200 || e != nil || !bytes.Contains(js, []byte("/invite")) || bytes.Contains(js, []byte("promotion-link")) {
		t.Fatal("installed activity asset still calls financial promotion")
	}
	t.Logf("referral_paid_sharing_rankings_installed: PASS source_sha=%s installed_sha=%s", inputs.SourceSHA, inputs.InstalledSHA)
}
