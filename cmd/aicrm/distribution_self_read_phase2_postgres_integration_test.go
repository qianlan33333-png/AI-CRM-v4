package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/http"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// Uses real Order -> Distribution commissions, browser sessions and the
// composition HTTP handler. No worker or external Provider is started.
func TestPostgreSQLDistributionSelfReadHTTPIsolation(t *testing.T) {
	f := newPromotionCheckoutFixture(t)
	defer f.close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	firstLink := f.issue(t, f.promoter, "self-read-promotion-first")
	otherLink := f.issue(t, f.otherPromoter, "self-read-promotion-other")
	for i, link := range []distributionport.PromotionLink{firstLink, otherLink, firstLink} {
		suffix := "self-read-order-" + strconv.Itoa(i)
		order := f.createOrder(t, f.buyer, f.buyer, promotionCheckoutToken(t, link), suffix, suffix)
		f.settle(t, order, suffix+"-paid")
	}
	sessions, err := distributionapp.NewBrowserSessionService(f.uow, f.distributions)
	if err != nil {
		t.Fatal(err)
	}
	firstToken, _, err := sessions.Issue(ctx, f.promoter)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := sessions.Issue(ctx, f.otherPromoter)
	if err != nil {
		t.Fatal(err)
	}
	var buyerIdentity int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'mp_openid','wechat-app:promotion-checkout','self-read-unregistered','verified','self-read-fixture',1,$2) RETURNING id`, f.buyer, time.Now().UTC()).Scan(&buyerIdentity); err != nil {
		t.Fatal(err)
	}
	buyerToken, _, err := sessions.Issue(ctx, distributionport.TrustedSessionActor{CustomerID: f.buyer, IdentityID: buyerIdentity, AppID: f.promoter.AppID, AppScope: f.promoter.AppScope, Channel: f.promoter.Channel, OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	key, cert := distributionFixturePaymentCredentials(t)
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: f.pool.Config().ConnString(), PublicOrigin: "https://distribution-read.example.test", ReleaseSHA: strings.Repeat("1", 40), WorkerOwner: "distribution-self-read", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "distribution-read-fixture"},
		Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey},
		Effects:   platformconfig.Effects{ProviderEnabled: true},
		WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-promotion-checkout", AppScope: "wechat-app:promotion-checkout", AppSecret: "fixture-secret", OrderContactDataKey: dataKey, MerchantID: "fixture-mch", MerchantSerial: "fixture-merchant", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: "0123456789abcdef0123456789abcdef", ProfitSharingEnabled: true, ProfitSharingAuthMode: "certificate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	get := func(path, cookieName, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		application.handler.ServeHTTP(w, r)
		return w
	}
	readPage := func(token, query string) struct {
		Items []struct {
			ID string `json:"commission_id"`
		} `json:"items"`
		Cursor string `json:"next_cursor"`
	} {
		w := get("/api/v1/distribution/commissions"+query, distributionhttp.DistributionSessionCookieName, token)
		if w.Code != http.StatusOK {
			t.Fatalf("commissions status=%d body=%s", w.Code, w.Body.String())
		}
		for _, private := range []string{"customer_id", "identity_id", "receiver_reference", "openid"} {
			if strings.Contains(w.Body.String(), "\""+private+"\"") {
				t.Fatalf("public response leaked %s", private)
			}
		}
		var page struct {
			Items []struct {
				ID string `json:"commission_id"`
			} `json:"items"`
			Cursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := readPage(firstToken, "?limit=1")
	second := readPage(firstToken, "?limit=1&cursor="+first.Cursor)
	other := readPage(otherToken, "")
	if len(first.Items) != 1 || len(second.Items) != 1 || len(other.Items) != 1 || first.Items[0].ID == second.Items[0].ID || first.Items[0].ID == other.Items[0].ID || second.Items[0].ID == other.Items[0].ID {
		t.Fatalf("actor/page isolation first=%+v second=%+v other=%+v", first, second, other)
	}
	if page := readPage(buyerToken, ""); len(page.Items) != 0 {
		t.Fatalf("unregistered customer saw commissions=%+v", page)
	}
	for token, expected := range map[string]int64{firstToken: 200, otherToken: 100, buyerToken: 0} {
		w := get("/api/v1/distribution/earnings", distributionhttp.DistributionSessionCookieName, token)
		var body struct {
			Initial   int64 `json:"initial_commission_minor"`
			Unsettled int64 `json:"unsettled_payable_minor"`
			Gross     int64 `json:"gross_paid_sales_minor"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Initial != expected || body.Unsettled != expected || body.Gross != expected*10 {
			t.Fatalf("own earnings status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// A valid cursor from another actor can move a page, but must never grant
	// that actor's rows. The server's customer predicate remains authoritative.
	for _, item := range readPage(firstToken, "?cursor="+other.Items[0].ID).Items {
		if item.ID == other.Items[0].ID {
			t.Fatal("foreign cursor disclosed foreign commission")
		}
	}
	for _, path := range []string{"/api/v1/distribution/earnings?customer_id=" + strconv.FormatInt(f.otherPromoter.CustomerID, 10), "/api/v1/distribution/commissions?customer_id=" + strconv.FormatInt(f.otherPromoter.CustomerID, 10), "/api/v1/distribution/commissions?limit=1&limit=2", "/api/v1/distribution/commissions?cursor=invalid"} {
		if w := get(path, distributionhttp.DistributionSessionCookieName, firstToken); w.Code < 400 || w.Code >= 500 {
			t.Fatalf("input override not rejected path=%s status=%d", path, w.Code)
		}
	}
	for _, credential := range []struct{ name, token string }{{distributionhttp.DistributionSessionCookieName, ""}, {distributionhttp.DistributionSessionCookieName, firstToken + "tampered"}, {paymenthttp.SessionCookieName, firstToken}, {"aicrm_admin_session", firstToken}} {
		if w := get("/api/v1/distribution/earnings", credential.name, credential.token); w.Code != http.StatusUnauthorized {
			t.Fatalf("untrusted session accepted name=%s status=%d", credential.name, w.Code)
		}
	}
	digest := sha256.Sum256([]byte(firstToken))
	if _, err := f.pool.Exec(ctx, "UPDATE distribution_browser_sessions SET created_at=$2::timestamptz - interval '1 hour', expires_at=$2::timestamptz WHERE token_digest=$1", digest[:], time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if w := get("/api/v1/distribution/earnings", distributionhttp.DistributionSessionCookieName, firstToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session accepted status=%d", w.Code)
	}
}
