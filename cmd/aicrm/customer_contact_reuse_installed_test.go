package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

// Optional final-package part of TestPostgreSQLCustomerContactReuse. Source
// composition seeds only synthetic definitions and verified identity sessions.
// Every business submission, authorization and prefill assertion uses HTTP to
// the exact installed binary; no source handler is used after the proxy binds.
func customerContactReuseInstalledHTTP(t *testing.T) {
	input := testconfig.InstalledProduct()
	db, err := url.Parse(os.Getenv("AICRM_DATABASE_URL"))
	if input.Binary == "" || err != nil || (db.Hostname() != "127.0.0.1" && db.Hostname() != "localhost" && db.Hostname() != "::1") || !strings.HasPrefix(strings.TrimPrefix(db.Path, "/"), "aicrm_test_") || !strings.HasSuffix(db.Path, "_acceptance_test") {
		t.Fatal("installed contact HTTP requires exact package and loopback acceptance database")
	}
	f := newProductExternalPushChromiumFixtureWithOptions(t, 150*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true})
	p := f.application.pool.Native()
	owner := issuePublicCommerceTrustedH5Session(t, f)
	nextOwner := issuePublicCommerceTrustedH5SessionWithKey(t, f, "contact-installed-next-session")
	other := issuePeriodicContactSession(t, f, "contact-reuse-other")
	if _, err = p.Exec(f.ctx, `UPDATE products SET legacy_admin_projection=legacy_admin_projection || '{"require_mobile":true,"contact_collection_level":"shipping_address"}'::jsonb WHERE id=$1`, f.productID); err != nil {
		t.Fatal(err)
	}
	// A normal employee has the existing viewer role, without reveal privileges.
	if _, err = p.Exec(f.ctx, `WITH staff AS (INSERT INTO admin_users(username,password_hash,display_name,login_enabled,access_granted_at) SELECT 'contact-viewer',password_hash,'Contact viewer',true,now() FROM admin_users WHERE username='product-browser-owner' RETURNING id) INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM staff`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(f.application.pool)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(f.dataKey))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := surveystore.NewPostgreSQL(p, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	definitions := surveyapp.NewService(uow, repo)
	var actor int64
	if err = p.QueryRow(f.ctx, `SELECT id FROM admin_users WHERE username='product-browser-owner'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	createSurvey := func(slug string, mode surveyport.AnswerDisplayMode, shipping bool) surveyport.Questionnaire {
		t.Helper()
		q := publicSurveyPresentationDefinition(slug, mode, "联系资料", "")
		kind, target := surveyport.QuestionMobile, "contact_phone"
		if shipping {
			kind, target = surveyport.QuestionShippingAddress, "shipping_contact"
		}
		q.Questions = []surveyport.Question{{Type: kind, Title: "常用资料", SidebarProfileField: target, Required: true, Options: []surveyport.Option{}}, {Type: surveyport.QuestionMobile, Title: "推荐人", SortOrder: 1, Options: []surveyport.Option{}}}
		result, e := definitions.Create(f.ctx, surveyport.CreateCommand{Questionnaire: q, ActorID: actor, IdempotencyKey: slug + "-create"})
		if e != nil {
			t.Fatal(e)
		}
		result, e = definitions.Publish(f.ctx, result.ID, result.Version, actor, slug+"-publish")
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	phoneSurvey := createSurvey("contact-installed-phone", surveyport.DisplayAllInOne, false)
	shippingSurvey := createSurvey("contact-installed-shipping", surveyport.DisplayOneByOne, true)
	nextSurvey := createSurvey("contact-installed-next", surveyport.DisplayAllInOne, true)
	// Synthetic verified provider fact resolves the same existing OneID subject;
	// source OAuth only issues the fixture session, without provider networking.
	fact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:public-commerce-fixture-platform", "public-commerce-browser-unionid")
	oauth := surveyapp.NewOAuthService(uow, repo, surveyDetachOAuthProvider{fact: fact}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()})
	if err = oauth.BindIdentitySubjects(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	location, err := oauth.Start(f.ctx, phoneSurvey.Slug)
	if err != nil {
		t.Fatal(err)
	}
	locationURL, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	surveyToken, _, err := oauth.Complete(f.ctx, locationURL.Query().Get("state"), "synthetic-contact-code")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := oauth.ResolveSession(f.ctx, surveyToken)
	if err != nil || subject.CustomerID == nil || int64(*subject.CustomerID) != owner.payerCustomerID {
		t.Fatal("survey fixture did not resolve order customer")
	}
	// Registered before process cleanup: emit success only after its bounded
	// interrupt/wait assertion has completed, never on a still-running API.
	t.Cleanup(func() {
		if !t.Failed() {
			t.Logf("installed_customer_contact_reuse_http: PASS source_sha=%s binary_sha256=%s stopped_process=true provider_attempts=0 order_survey_prefill=true permissions_csrf_audit=true replay_rollback=true", input.SHA, input.BinarySHA256)
		}
	})
	useInstalledPeriodicContactFixtureWithEnvironment(t, f, []string{"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1"})
	client := f.server.Client()
	client.Timeout = 15 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Existing login assertions now cross the network into installed bytes too.
	networkHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, e := http.NewRequestWithContext(f.ctx, r.Method, f.server.URL+r.URL.RequestURI(), r.Body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header = r.Header.Clone()
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		for k, values := range res.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		if _, e = io.Copy(w, res.Body); e != nil {
			t.Fatal(e)
		}
	})
	admin, adminCSRF := adminAccessLogin(t, networkHandler, "product-browser-owner", "product-browser-owner-password")
	staff, staffCSRF := adminAccessLogin(t, networkHandler, "contact-viewer", "product-browser-owner-password")
	call := func(method, path string, payload any, key, payToken, surveySession, adminSession, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(payload)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", f.server.URL)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		for name, value := range map[string]string{paymentport.TrustedSessionCookieName: payToken, "__Host-aicrm_survey_identity": surveySession, accesshttp.SessionCookieName: adminSession, accesshttp.CSRFCookieName: csrf} {
			if value != "" {
				r.AddCookie(&http.Cookie{Name: name, Value: value})
			}
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		networkHandler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, want int) map[string]any {
		t.Helper()
		if w.Code != want {
			t.Fatalf("installed contact HTTP status=%d want=%d", w.Code, want)
		}
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("installed contact response is not JSON")
		}
		return result
	}
	checkoutPath := fmt.Sprintf("/api/v1/wechat-pay/checkout-session?product_id=%d&product_kind=standard", f.productID)
	binding := decode(call("GET", checkoutPath, nil, "", owner.token, "", "", ""), 200)["checkout_session_binding"]
	ship := customerport.ShippingContact{MobileE164: "+8613800000081", RecipientName: "合成收件人", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "合成街道 81 号"}
	order := map[string]any{"product_id": f.productID, "product_kind": "standard", "beneficiary_selection": "payer_self", "coupon_claim_id": 0, "contact_collection_level": "shipping_address", "checkout_session_binding": binding, "provider": "alipay", "channel": "alipay_page", "mobile": ship.MobileE164, "recipient_name": ship.RecipientName, "province_code": ship.ProvinceCode, "province_name": ship.ProvinceName, "city_code": ship.CityCode, "city_name": ship.CityName, "district_code": ship.DistrictCode, "district_name": ship.DistrictName, "detail_address": ship.DetailAddress}
	first := call("POST", "/api/v1/alipay/checkouts", order, "contact-installed-order-A", owner.token, "", "", "")
	firstOrder := decode(first, 202)
	checkPrefill := func(phone, detail string) {
		t.Helper()
		w := call("GET", checkoutPath, nil, "", nextOwner.token, "", "", "")
		body := decode(w, 200)
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("contact prefill cacheable")
		}
		raw, _ := json.Marshal(body["contact_prefill"])
		var profile customerport.ContactProfile
		if json.Unmarshal(raw, &profile) != nil || profile.MobileE164 != phone || profile.Shipping == nil || profile.Shipping.DetailAddress != detail {
			t.Fatal("installed persisted contact prefill mismatch")
		}
	}
	checkPrefill(ship.MobileE164, ship.DetailAddress)
	// A supplied customer ID never changes the session owner.
	for _, token := range []string{"", other} {
		w := call("GET", checkoutPath+fmt.Sprintf("&customer_id=%d", owner.payerCustomerID), nil, "", token, "", "", "")
		if token == "" && w.Code != 401 {
			t.Fatal("anonymous checkout prefill allowed")
		}
		if token != "" {
			decode(w, 200)
		}
		if bytes.Contains(w.Body.Bytes(), []byte(ship.MobileE164)) || bytes.Contains(w.Body.Bytes(), []byte(ship.DetailAddress)) {
			t.Fatal("cross-customer contact leak")
		}
	}
	surveySessionPath := func(q surveyport.Questionnaire) string { return "/api/h5/surveys/session?slug=" + q.Slug }
	assertSurveyPrefill := func(q surveyport.Questionnaire, phone, detail string) {
		t.Helper()
		w := call("GET", surveySessionPath(q), nil, "", "", surveyToken, "", "")
		body := decode(w, 200)
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("survey prefill cacheable")
		}
		raw, _ := json.Marshal(body["contact_prefill"])
		var answers map[surveyport.ID]surveyport.SubmissionAnswer
		if json.Unmarshal(raw, &answers) != nil || len(answers) != 1 {
			t.Fatal("survey mapped source mismatch")
		}
		a := answers[q.Questions[0].ID]
		if q.Questions[0].Type == surveyport.QuestionMobile {
			if a.TextValue != strings.TrimPrefix(phone, "+86") || a.ShippingAddress != nil {
				t.Fatal("phone-only survey leaked shipping or wrong phone")
			}
		} else if a.ShippingAddress == nil || a.ShippingAddress.MobileE164 != phone || a.ShippingAddress.DetailAddress != detail {
			t.Fatal("shipping survey prefill mismatch")
		}
	}
	assertSurveyPrefill(phoneSurvey, ship.MobileE164, ship.DetailAddress)
	assertSurveyPrefill(shippingSurvey, ship.MobileE164, ship.DetailAddress)
	w := call("GET", surveySessionPath(nextSurvey), nil, "", "", "", "", "")
	if w.Code != 401 || bytes.Contains(w.Body.Bytes(), []byte(ship.DetailAddress)) {
		t.Fatal("anonymous survey contact leak")
	}
	submit := func(q surveyport.Questionnaire, key string, a surveyport.SubmissionAnswer) *httptest.ResponseRecorder {
		return call("POST", "/api/public/questionnaires/"+q.Slug+"/submissions", map[string]any{"version": q.DefinitionVersion, "submission_key": key, "answers": []surveyport.SubmissionAnswer{a, {QuestionID: q.Questions[1].ID, TextValue: "13900000099"}}}, "", "", surveyToken, "", "")
	}
	phoneAnswer := surveyport.SubmissionAnswer{QuestionID: phoneSurvey.Questions[0].ID, TextValue: "13800000082"}
	phoneReceipt := submit(phoneSurvey, strings.Repeat("a", 43), phoneAnswer)
	decode(phoneReceipt, 201)
	checkPrefill("+8613800000082", ship.DetailAddress)
	assertSurveyPrefill(shippingSurvey, "+8613800000082", ship.DetailAddress)
	ship.MobileE164 = "+8613800000083"
	ship.DetailAddress = "合成街道 83 号"
	shippingReceipt := submit(shippingSurvey, strings.Repeat("b", 43), surveyport.SubmissionAnswer{QuestionID: shippingSurvey.Questions[0].ID, ShippingAddress: &ship})
	decode(shippingReceipt, 201)
	checkPrefill(ship.MobileE164, ship.DetailAddress)
	assertSurveyPrefill(nextSurvey, ship.MobileE164, ship.DetailAddress)
	replay := decode(call("POST", "/api/v1/alipay/checkouts", order, "contact-installed-order-A", owner.token, "", "", ""), 202)
	if replay["merchant_order_no"] != firstOrder["merchant_order_no"] {
		t.Fatal("installed order replay identity changed")
	}
	replaySurvey := submit(phoneSurvey, strings.Repeat("a", 43), phoneAnswer)
	decode(replaySurvey, 201)
	if !bytes.Equal(phoneReceipt.Body.Bytes(), replaySurvey.Body.Bytes()) {
		t.Fatal("installed survey receipt replay drift")
	}
	checkPrefill(ship.MobileE164, ship.DetailAddress)
	// Fail the profile write inside installed submission UoW; compare the entire
	// encrypted row and business/receipt counts rather than only its read model.
	snapshot := func() string {
		t.Helper()
		var result string
		e := p.QueryRow(f.ctx, `SELECT jsonb_build_array((SELECT to_jsonb(c) FROM customer_contact_profiles c WHERE customer_id=$1),(SELECT count(*) FROM orders),(SELECT count(*) FROM survey_submissions),(SELECT count(*) FROM idempotency_receipts))::text`, owner.payerCustomerID).Scan(&result)
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	before := snapshot()
	if _, err = p.Exec(f.ctx, `CREATE FUNCTION contact_acceptance_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic contact rollback'; END $$; CREATE TRIGGER contact_acceptance_fail AFTER INSERT OR UPDATE ON customer_contact_profiles FOR EACH ROW EXECUTE FUNCTION contact_acceptance_fail()`); err != nil {
		t.Fatal(err)
	}
	failed := submit(nextSurvey, strings.Repeat("c", 43), surveyport.SubmissionAnswer{QuestionID: nextSurvey.Questions[0].ID, ShippingAddress: &ship})
	if failed.Code < 500 {
		t.Fatalf("injected installed failure status=%d", failed.Code)
	}
	if _, err = p.Exec(f.ctx, `DROP TRIGGER contact_acceptance_fail ON customer_contact_profiles; DROP FUNCTION contact_acceptance_fail()`); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("installed rollback left partial contact/submission/receipt")
	}
	checkPrefill(ship.MobileE164, ship.DetailAddress)
	revealPath := fmt.Sprintf("/api/admin/customers/%d/contact-reveal", owner.payerCustomerID)
	for _, c := range []struct {
		session, csrf string
		want          int
	}{{"", "", 403}, {staff, staffCSRF, 403}, {admin, "", 403}} {
		w := call("POST", revealPath, nil, "", "", "", c.session, c.csrf)
		if w.Code != c.want || bytes.Contains(w.Body.Bytes(), []byte(ship.MobileE164)) || bytes.Contains(w.Body.Bytes(), []byte(ship.DetailAddress)) {
			t.Fatalf("installed reveal permission status=%d want=%d", w.Code, c.want)
		}
	}
	w = call("POST", revealPath, nil, "", "", "", admin, adminCSRF)
	body := decode(w, 200)
	revealedShipping, hasShipping := body["shipping_address"].(map[string]any)
	if body["mobile_e164"] != ship.MobileE164 || !hasShipping || revealedShipping["detail_address"] != ship.DetailAddress || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("installed admin reveal mismatch")
	}
	var auditCount int
	var audit string
	if err = p.QueryRow(f.ctx, `SELECT count(*),coalesce(string_agg(payload::text,''),'') FROM audit_events WHERE action='customer.contact_revealed' AND resource_id=$1`, fmt.Sprint(owner.payerCustomerID)).Scan(&auditCount, &audit); err != nil || auditCount != 1 || strings.Contains(audit, ship.MobileE164) || strings.Contains(audit, ship.DetailAddress) || !strings.Contains(audit, "contact_query") {
		t.Fatal("installed contact audit missing or unsafe")
	}
	// New forms only receive their configured collection unit. A none-mode
	// response cannot accidentally expose the customer's stored shipping facts.
	for _, level := range []string{"mobile", "none"} {
		if _, err = p.Exec(f.ctx, `UPDATE products SET legacy_admin_projection=legacy_admin_projection || jsonb_build_object('contact_collection_level',$2::text,'require_mobile',$3::boolean) WHERE id=$1`, f.productID, level, level == "mobile"); err != nil {
			t.Fatal(err)
		}
		body := decode(call("GET", checkoutPath, nil, "", nextOwner.token, "", "", ""), 200)
		raw, _ := json.Marshal(body["contact_prefill"])
		var profile customerport.ContactProfile
		if json.Unmarshal(raw, &profile) != nil || profile.Shipping != nil || (level == "mobile" && profile.MobileE164 != ship.MobileE164) || (level == "none" && profile.MobileE164 != "") {
			t.Fatal("installed collection mode disclosed wrong contact unit")
		}
	}
	var attempts int
	if err = p.QueryRow(f.ctx, `SELECT count(*) FROM external_effect_attempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("installed contact fixture attempted Provider write")
	}
	// Confirm one reusable record; source contracts above also verify that old
	// order snapshots retain their original phone and address.
	var count int
	if err = p.QueryRow(f.ctx, `SELECT count(*) FROM customer_contact_profiles WHERE customer_id=$1`, owner.payerCustomerID).Scan(&count); err != nil || count != 1 {
		t.Fatal("installed contact profile cardinality")
	}
}
