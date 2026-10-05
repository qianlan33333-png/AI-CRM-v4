package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/module"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	radarstore "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/store"
)

// This local composition journey keeps the real Radar and Media PostgreSQL
// stores and the real OneID implementation. Only the trusted OAuth boundary is
// replaced with a loopback httptest server; the outer composition's separate
// disabled WeChat adapter is never called.
func TestPostgreSQLRadarPublicTrustedOAuthOneIDContentVisitorJourney(t *testing.T) {
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{deferEffectsWorker: true})
	ctx := fixture.ctx
	app := fixture.application
	origin := fixture.server.URL
	adminSession, adminCSRF := adminAccessLogin(t, app.handler, "product-browser-owner", "product-browser-owner-password")

	adminMutation := func(method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", idempotencyKey)
		request.Header.Set("X-CSRF-Token", adminCSRF)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: adminSession})
		request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: adminCSRF})
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, request)
		return response
	}
	created := adminMutation(http.MethodPost, "/api/admin/radar-links", `{"name":"public-core-oauth","title":"Public core OAuth","description":"local test","cover_image_id":`+strconv.FormatInt(fixture.materialFirstID, 10)+`,"auth_policy":"unionid_required"}`, "radar-public-core-create-0001")
	if created.Code != http.StatusCreated {
		t.Fatalf("create Radar link status=%d body=%s", created.Code, created.Body.String())
	}
	var createdPayload struct {
		Link struct {
			ID         int64  `json:"link_id"`
			PublicCode string `json:"public_code"`
			Version    int64  `json:"version"`
		} `json:"link"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil || createdPayload.Link.ID < 1 || createdPayload.Link.PublicCode == "" || createdPayload.Link.Version != 1 {
		t.Fatalf("decode created Radar link=%+v err=%v", createdPayload.Link, err)
	}
	radarID, publicCode := createdPayload.Link.ID, createdPayload.Link.PublicCode
	enabled := adminMutation(http.MethodPost, "/api/admin/radar-links/"+strconv.FormatInt(radarID, 10)+"/enable", `{"expected_version":1}`, "radar-public-core-enable-0001")
	if enabled.Code != http.StatusOK || !strings.Contains(enabled.Body.String(), `"status":"enabled"`) {
		t.Fatalf("enable Radar link status=%d body=%s", enabled.Code, enabled.Body.String())
	}

	callbackURL := origin + "/api/public/radar/oauth/callback"
	oauth := newRadarPublicCoreOAuthFake(t, callbackURL)
	uow, err := platformpostgres.NewUnitOfWork(app.pool)
	if err != nil {
		t.Fatal(err)
	}
	phoneVault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(fixture.dataKey))
	if err != nil {
		t.Fatal(err)
	}
	identityRepository := identitystore.NewPostgresStore(phoneVault)
	customerDirectory := customerstore.NewPostgreSQL()
	oneID := identityapp.OneIDService{Store: identityRepository, ProvisionedCustomer: customerProvisionedDirectoryAdapter{writer: customerDirectory}}
	identityQueries := identityquery.NewPostgreSQL(phoneVault)
	radarRepository := radarstore.NewPostgres()
	manager, err := radarapp.NewService(uow, radarRepository, radarRepository)
	if err != nil {
		t.Fatal(err)
	}
	mediaRepository, err := mediastore.NewPostgreSQL(app.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.BindMediaValidator(radarMediaReferenceAdapter{media: mediaRepository}); err != nil {
		t.Fatal(err)
	}
	query, err := radarapp.NewQueryService(uow, radarRepository)
	if err != nil {
		t.Fatal(err)
	}
	if err = query.BindAdminVisitorPresentation(radarVisitorPresentationAdapter{
		numbers: identityQueries, uow: uow, directory: customerDirectory, identities: identityQueries,
		corpScope: "wecom-corp:admin-layout-fixture-corp",
	}); err != nil {
		t.Fatal(err)
	}
	mediaService, err := mediaapp.NewHTTPFacade(mediaRepository)
	if err != nil {
		t.Fatal(err)
	}
	public, err := radarapp.NewPublicAccessService(uow, radarRepository, radarRepository, query, oauth, oneID, radarContentAdapter{media: mediaService})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := radarmodule.NewModuleRegistration().Bind(manager, query, public, requestAccessSecurity{authentication: app.authentication}, origin)
	if err != nil {
		t.Fatal(err)
	}
	publicHandler := bindings.Radar
	servePublic := func(method, target string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, target, body)
		response := httptest.NewRecorder()
		publicHandler.ServeHTTP(response, request)
		return response
	}

	// The public code first opens through /r/{code}, receives a local fake
	// provider URL, and the provider redirects only to this handler's callback.
	openForOAuth := servePublic(http.MethodGet, origin+"/r/"+publicCode, nil)
	if openForOAuth.Code != http.StatusFound {
		t.Fatalf("public OAuth entry status=%d body=%s", openForOAuth.Code, openForOAuth.Body.String())
	}
	authorizeURL, err := url.Parse(openForOAuth.Header().Get("Location"))
	if err != nil || authorizeURL.Host != strings.TrimPrefix(strings.TrimPrefix(oauth.server.URL, "http://"), "https://") || authorizeURL.Path != "/authorize" {
		t.Fatalf("public entry did not redirect to local OAuth fake: location=%q err=%v", openForOAuth.Header().Get("Location"), err)
	}
	state := authorizeURL.Query().Get("state")
	if len(state) != 43 {
		t.Fatalf("OAuth state length=%d", len(state))
	}
	var oauthReturn *http.Cookie
	for _, cookie := range openForOAuth.Result().Cookies() {
		if cookie.Name == "radar_oauth_return" {
			oauthReturn = cookie
		}
	}
	if oauthReturn == nil || oauthReturn.Path != "/api/public/radar/oauth/callback" || !oauthReturn.HttpOnly || !oauthReturn.Secure {
		t.Fatalf("OAuth return cookie missing or unsafe: %+v", oauthReturn)
	}
	localClient := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	authorizeRequest, err := http.NewRequest(http.MethodGet, authorizeURL.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	authorizeResponse, err := localClient.Do(authorizeRequest)
	if err != nil {
		t.Fatalf("call local OAuth authorize fake: %v", err)
	}
	_ = authorizeResponse.Body.Close()
	if authorizeResponse.StatusCode != http.StatusFound {
		t.Fatalf("local OAuth authorize status=%d", authorizeResponse.StatusCode)
	}
	providerCallback, err := url.Parse(authorizeResponse.Header.Get("Location"))
	if err != nil || providerCallback.Host != strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://") || providerCallback.Path != "/api/public/radar/oauth/callback" || providerCallback.Query().Get("state") != state || providerCallback.Query().Get("code") != "radar-public-core-code" {
		t.Fatalf("local fake callback target=%q err=%v", authorizeResponse.Header.Get("Location"), err)
	}
	callbackRequest := httptest.NewRequest(http.MethodGet, providerCallback.String(), nil)
	callbackRequest.AddCookie(oauthReturn)
	callbackResponse := httptest.NewRecorder()
	publicHandler.ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound || callbackResponse.Header().Get("Location") != "/r/"+publicCode || oauth.exchangeCount.Load() != 1 {
		t.Fatalf("OAuth callback status=%d location=%q local exchanges=%d body=%s", callbackResponse.Code, callbackResponse.Header().Get("Location"), oauth.exchangeCount.Load(), callbackResponse.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == "__Host-aicrm-radar" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || sessionCookie.Path != "/" || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.MaxAge != 1800 {
		t.Fatalf("verified session cookie missing or unsafe: %+v", sessionCookie)
	}

	viewer := httptest.NewRecorder()
	viewerRequest := httptest.NewRequest(http.MethodGet, origin+"/r/"+publicCode, nil)
	viewerRequest.AddCookie(sessionCookie)
	publicHandler.ServeHTTP(viewer, viewerRequest)
	if viewer.Code != http.StatusOK || !strings.Contains(viewer.Body.String(), "kind='image'") || !strings.Contains(viewer.Body.String(), "src='/api/public/radar/'+code+'/content'") {
		t.Fatalf("verified public viewer status=%d body=%s", viewer.Code, viewer.Body.String())
	}
	contentRequest := httptest.NewRequest(http.MethodGet, origin+"/api/public/radar/"+publicCode+"/content", nil)
	contentRequest.AddCookie(sessionCookie)
	contentResponse := httptest.NewRecorder()
	publicHandler.ServeHTTP(contentResponse, contentRequest)
	if contentResponse.Code != http.StatusOK || !strings.HasPrefix(contentResponse.Header().Get("Content-Type"), "image/") || !bytes.HasPrefix(contentResponse.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("Media-backed public content status=%d content-type=%q bytes=%d", contentResponse.Code, contentResponse.Header().Get("Content-Type"), contentResponse.Body.Len())
	}

	// A modified session secret must not authorize the same Media content.
	tampered := *sessionCookie
	tampered.Value = "A" + sessionCookie.Value[1:]
	if tampered.Value == sessionCookie.Value {
		tampered.Value = "B" + sessionCookie.Value[1:]
	}
	tamperedRequest := httptest.NewRequest(http.MethodGet, origin+"/api/public/radar/"+publicCode+"/content", nil)
	tamperedRequest.AddCookie(&tampered)
	tamperedResponse := httptest.NewRecorder()
	publicHandler.ServeHTTP(tamperedResponse, tamperedRequest)
	if tamperedResponse.Code != http.StatusNotFound {
		t.Fatalf("tampered session content status=%d body=%s", tamperedResponse.Code, tamperedResponse.Body.String())
	}

	proofMatch := regexp.MustCompile(`proof='([^']+)'`).FindStringSubmatch(viewer.Body.String())
	if len(proofMatch) != 2 {
		t.Fatal("viewer did not emit its same-origin event proof")
	}
	eventBody, err := json.Marshal(map[string]string{"stage": "image_loaded", "event_token": proofMatch[1]})
	if err != nil {
		t.Fatal(err)
	}
	eventRequest := httptest.NewRequest(http.MethodPost, origin+"/api/public/radar/"+publicCode+"/events", bytes.NewReader(eventBody))
	eventRequest.Header.Set("Content-Type", "application/json")
	eventRequest.Header.Set("Origin", origin)
	eventRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	eventRequest.AddCookie(sessionCookie)
	eventResponse := httptest.NewRecorder()
	publicHandler.ServeHTTP(eventResponse, eventRequest)
	var eventPayload struct {
		ReceiptID          string `json:"receipt_id"`
		IdentityAttributed bool   `json:"identity_attributed"`
	}
	if err = json.Unmarshal(eventResponse.Body.Bytes(), &eventPayload); err != nil || eventResponse.Code != http.StatusOK || eventPayload.ReceiptID == "" || !eventPayload.IdentityAttributed {
		t.Fatalf("public image event status=%d receipt=%q attributed=%t decode=%v body=%s", eventResponse.Code, eventPayload.ReceiptID, eventPayload.IdentityAttributed, err, eventResponse.Body.String())
	}

	sessionDigest := sha256.Sum256([]byte(sessionCookie.Value))
	var sessionID, identityID, customerID int64
	var attribution string
	var storedEvidence []byte
	if err = app.pool.Native().QueryRow(ctx, `SELECT id,identity_id,customer_id,attribution_status,evidence_digest FROM radar_view_sessions WHERE radar_id=$1 AND session_digest=$2`, radarID, sessionDigest[:]).Scan(&sessionID, &identityID, &customerID, &attribution, &storedEvidence); err != nil {
		t.Fatal(err)
	}
	if sessionID < 1 || identityID < 1 || customerID < 1 || attribution != "resolved" || len(storedEvidence) != sha256.Size {
		t.Fatalf("persisted resolved view session id=%d identity=%d customer=%d attribution=%q evidence-bytes=%d", sessionID, identityID, customerID, attribution, len(storedEvidence))
	}
	var eventCount int
	if err = app.pool.Native().QueryRow(ctx, `SELECT count(*) FROM radar_events WHERE session_id=$1 AND radar_id=$2 AND attribution_status='resolved' AND identity_id=$3 AND customer_id=$4 AND stage IN ('landing','oauth_verified','identity_resolved','content_opened','image_loaded')`, sessionID, radarID, identityID, customerID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	var allEventCount int
	if err = app.pool.Native().QueryRow(ctx, `SELECT count(*) FROM radar_events WHERE session_id=$1`, sessionID).Scan(&allEventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 5 || allEventCount != 5 {
		t.Fatalf("resolved session event count expected five lifecycle stages; selected=%d all=%d", eventCount, allEventCount)
	}

	var canonicalOneID string
	if err = app.pool.Native().QueryRow(ctx, `SELECT public_number::text FROM customers WHERE id=$1`, customerID).Scan(&canonicalOneID); err != nil {
		t.Fatal(err)
	}
	visitorRequest := httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/"+strconv.FormatInt(radarID, 10)+"/visitors?limit=20&offset=0", nil)
	visitorRequest.Header.Set("X-CSRF-Token", adminCSRF)
	visitorRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: adminSession})
	visitorRequest.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: adminCSRF})
	visitorResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(visitorResponse, visitorRequest)
	var visitorPage struct {
		Items []struct {
			OneID             *string `json:"oneid"`
			AttributionStatus string  `json:"attribution_status"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err = json.Unmarshal(visitorResponse.Body.Bytes(), &visitorPage); err != nil || visitorResponse.Code != http.StatusOK || visitorResponse.Header().Get("Cache-Control") != "no-store" || visitorPage.Total != 1 || len(visitorPage.Items) != 1 || visitorPage.Items[0].OneID == nil || *visitorPage.Items[0].OneID != canonicalOneID || visitorPage.Items[0].AttributionStatus != "resolved" {
		t.Fatalf("admin visitor read status=%d cache=%q total=%d items=%d canonical=%q decode=%v body=%s", visitorResponse.Code, visitorResponse.Header().Get("Cache-Control"), visitorPage.Total, len(visitorPage.Items), canonicalOneID, err, visitorResponse.Body.String())
	}

	var outboundIntents int
	if err = app.pool.Native().QueryRow(ctx, `SELECT count(*) FROM outbound_private_message_intents`).Scan(&outboundIntents); err != nil {
		t.Fatal(err)
	}
	if outboundIntents != 0 {
		t.Fatalf("public Radar journey unexpectedly created customer-message intents: %d", outboundIntents)
	}
	t.Logf("Radar public trusted-OAuth local journey PASS radar_id=%d session_id=%d event_count=%d; provider exchanges=%d", radarID, sessionID, allEventCount, oauth.exchangeCount.Load())
}

type radarPublicCoreOAuthFake struct {
	server        *httptest.Server
	callbackURL   string
	exchangeCount atomic.Int32
}

func newRadarPublicCoreOAuthFake(t *testing.T, callbackURL string) *radarPublicCoreOAuthFake {
	t.Helper()
	fake := &radarPublicCoreOAuthFake{callbackURL: callbackURL}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			state := r.URL.Query().Get("state")
			redirectURI := r.URL.Query().Get("redirect_uri")
			if len(state) != 43 || redirectURI != fake.callbackURL {
				http.Error(w, "invalid synthetic authorization request", http.StatusBadRequest)
				return
			}
			callback, err := url.Parse(redirectURI)
			if err != nil {
				http.Error(w, "invalid callback", http.StatusBadRequest)
				return
			}
			query := callback.Query()
			query.Set("state", state)
			query.Set("code", "radar-public-core-code")
			callback.RawQuery = query.Encode()
			http.Redirect(w, r, callback.String(), http.StatusFound)
		case "/token":
			if r.URL.Query().Get("code") != "radar-public-core-code" {
				http.Error(w, "invalid synthetic provider code", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unionid":"radar-public-core-unionid","scope":"snsapi_userinfo"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *radarPublicCoreOAuthFake) Enabled() bool { return fake != nil && fake.server != nil }

func (fake *radarPublicCoreOAuthFake) AuthorizationURL(state string) string {
	query := url.Values{"state": {state}, "redirect_uri": {fake.callbackURL}}
	return fake.server.URL + "/authorize?" + query.Encode()
}

func (fake *radarPublicCoreOAuthFake) Exchange(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	fake.exchangeCount.Add(1)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.server.URL+"/token?code="+url.QueryEscape(code), nil)
	if err != nil {
		return identitydomain.VerifiedFact{}, err
	}
	response, err := fake.server.Client().Do(request)
	if err != nil {
		return identitydomain.VerifiedFact{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identitydomain.VerifiedFact{}, io.ErrUnexpectedEOF
	}
	var payload struct {
		UnionID string `json:"unionid"`
		Scope   string `json:"scope"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload); err != nil || payload.UnionID != "radar-public-core-unionid" || payload.Scope != "snsapi_userinfo" {
		return identitydomain.VerifiedFact{}, io.ErrUnexpectedEOF
	}
	return identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:radar-public-core-test", Value: payload.UnionID, Source: "wechat.radar.oauth",
	})
}

var _ radarport.OAuthProvider = (*radarPublicCoreOAuthFake)(nil)
