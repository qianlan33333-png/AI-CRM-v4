package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	alipaysdk "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

type alipayCallbackRegressionApplication struct {
	paymenthttp.Application
	applied []paymentprovider.CallbackResult
}

func (app *alipayCallbackRegressionApplication) ApplyVerifiedCallback(_ context.Context, result paymentprovider.CallbackResult) error {
	app.applied = append(app.applied, result)
	return nil
}

type alipayCallbackRegressionRequestSecurity struct{ authenticateCalls, csrfCalls int }

func (security *alipayCallbackRegressionRequestSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.authenticateCalls++
	return accessdomain.Principal{}, errors.New("unexpected session authentication")
}

func (security *alipayCallbackRegressionRequestSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.csrfCalls++
	return accessdomain.Principal{}, errors.New("unexpected CSRF authorization")
}

type alipayCallbackRegressionAccessAuthentication struct{ authenticateCalls, csrfCalls, loginCalls int }

func (auth *alipayCallbackRegressionAccessAuthentication) Authenticate(context.Context, string) (accessdomain.Principal, error) {
	auth.authenticateCalls++
	return accessdomain.Principal{}, errors.New("unexpected admin session authentication")
}

func (auth *alipayCallbackRegressionAccessAuthentication) AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error) {
	auth.csrfCalls++
	return accessdomain.Principal{}, errors.New("unexpected admin CSRF authorization")
}

func (auth *alipayCallbackRegressionAccessAuthentication) LoginWithWeComUserID(context.Context, accessapp.WeComLoginCommand) (accessapp.IssuedSession, error) {
	auth.loginCalls++
	return accessapp.IssuedSession{}, errors.New("unexpected login")
}

func TestAlipayCallbackComposedHTTPContract(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	provider, err := paymentprovider.NewAlipay(paymentprovider.AlipayConfig{
		Enabled: true, Production: true, AppID: "synthetic-composed-app", PrivateKey: privatePEM,
		AlipayPublicKey: publicPEM, NotifyURL: "https://example.test/api/public/alipay/callback",
		ReturnURL: "https://example.test/pay/alipay/return",
	})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := alipaysdk.New("synthetic-composed-app", privatePEM, true)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(values url.Values) {
		t.Helper()
		signature, signErr := signer.SignValues(values, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
		if signErr != nil {
			t.Fatal(signErr)
		}
		values.Set("sign", base64.StdEncoding.EncodeToString(signature))
	}
	copyValues := func(values url.Values) url.Values {
		copy := url.Values{}
		for name, entries := range values {
			copy[name] = append([]string(nil), entries...)
		}
		return copy
	}
	values := url.Values{
		"app_id": {"synthetic-composed-app"}, "notify_id": {"composed-notify-1"},
		"out_trade_no": {"composed-merchant-order-1"}, "trade_no": {"composed-provider-trade-1"},
		"trade_status": {"TRADE_SUCCESS"}, "total_amount": {"19.90"},
		"notify_time": {"2026-09-30 12:00:00"}, "sign_type": {"RSA2"},
	}
	sign(values)
	// This synthetic refund-shaped form exercises the existing adapter's
	// compatibility branch only; it does not assert an official async Alipay
	// refund notification contract.
	refundValues := url.Values{
		"app_id": {"synthetic-composed-app"}, "notify_id": {"composed-refund-notify-1"},
		"out_trade_no": {"composed-merchant-order-1"}, "trade_no": {"composed-provider-trade-1"},
		"out_request_no": {"composed-refund-request-1"}, "refund_amount": {"1.20"},
		"notify_time": {"2026-09-30 12:01:00"}, "sign_type": {"RSA2"},
	}
	sign(refundValues)

	app := &alipayCallbackRegressionApplication{}
	requestSecurity := &alipayCallbackRegressionRequestSecurity{}
	paymentHandler, err := paymenthttp.NewHandler(app, nil, requestSecurity, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = paymentHandler.SetAlipayCallbackVerifier(provider); err != nil {
		t.Fatal(err)
	}
	adminAPIs := http.NewServeMux()
	adminAPIs.Handle("/api/public/alipay/", paymentHandler)
	notFound := http.NotFoundHandler()
	auth := &alipayCallbackRegressionAccessAuthentication{}
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		notFound, notFound, adminAPIs, notFound, notFound, notFound, notFound, notFound,
		notFound, notFound, notFound, notFound, notFound, notFound, notFound, notFound,
		notFound, notFound, notFound, notFound, notFound, notFound, notFound, notFound,
		auth, "https://crm.example.test", "https://h5.example.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	postBody := func(requestBody, origin, fetchSite string) (int, string) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPost, server.URL+"/api/public/alipay/callback", strings.NewReader(requestBody))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		responseBody, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, string(responseBody)
	}
	post := func(form url.Values, origin string) (int, string) {
		t.Helper()
		return postBody(form.Encode(), origin, "")
	}

	status, body := post(values, "")
	if status != http.StatusOK || body != "success" || len(app.applied) != 1 {
		t.Fatalf("composed signed POST status=%d body=%q applied=%d", status, body, len(app.applied))
	}
	if got := app.applied[0]; got.Provider != paymentdomain.ProviderAlipay || got.Kind != "payment" || got.MerchantOrderNo != "composed-merchant-order-1" || got.AmountMinor != 1990 {
		t.Fatalf("composed callback was not normalized: %+v", got)
	}
	status, body = post(refundValues, "")
	if status != http.StatusOK || body != "success" || len(app.applied) != 2 {
		t.Fatalf("synthetic refund-shaped POST status=%d body=%q applied=%d", status, body, len(app.applied))
	}
	if got := app.applied[1]; got.Provider != paymentdomain.ProviderAlipay || got.Kind != "refund" || got.MerchantOrderNo != "composed-merchant-order-1" || got.RefundNo != "composed-refund-request-1" || got.AmountMinor != 120 {
		t.Fatalf("synthetic refund-shaped callback was not normalized: %+v", got)
	}
	if requestSecurity.authenticateCalls != 0 || requestSecurity.csrfCalls != 0 || auth.authenticateCalls != 0 || auth.csrfCalls != 0 || auth.loginCalls != 0 {
		t.Fatalf("callback unexpectedly reached access security: request=%+v admin=%+v", requestSecurity, auth)
	}

	wrongApp := copyValues(values)
	wrongApp.Set("app_id", "other-synthetic-app")
	wrongApp.Set("sign", "")
	sign(wrongApp)
	status, _ = post(wrongApp, "")
	if status != http.StatusUnauthorized || len(app.applied) != 2 {
		t.Fatalf("wrong-app composed POST status=%d applied=%d; want 401 and no added application call", status, len(app.applied))
	}

	tampered := copyValues(values)
	tampered.Set("total_amount", "199.00")
	status, _ = post(tampered, "")
	if status != http.StatusUnauthorized || len(app.applied) != 2 {
		t.Fatalf("tampered composed POST status=%d applied=%d; want 401 and no added application call", status, len(app.applied))
	}

	status, _ = post(values, "https://attacker.example")
	if status != http.StatusForbidden || len(app.applied) != 2 {
		t.Fatalf("foreign-origin POST status=%d applied=%d; want 403 and no added application call", status, len(app.applied))
	}
	status, _ = postBody(values.Encode(), "", "cross-site")
	if status != http.StatusForbidden || len(app.applied) != 2 {
		t.Fatalf("cross-site POST without Origin status=%d applied=%d; want 403 and no added application call", status, len(app.applied))
	}
	status, _ = postBody("broken=%zz", "", "")
	if status != http.StatusBadRequest || len(app.applied) != 2 {
		t.Fatalf("malformed form POST status=%d applied=%d; want 400 and no added application call", status, len(app.applied))
	}
	oversizedBody := "field=" + strings.Repeat("a", 64<<10)
	status, _ = postBody(oversizedBody, "", "")
	if status != http.StatusBadRequest || len(app.applied) != 2 {
		t.Fatalf("oversized form POST status=%d body_bytes=%d applied=%d; want 400 and no added application call", status, len(oversizedBody), len(app.applied))
	}
	status, body = postBody(values.Encode(), "https://crm.example.test", "cross-site")
	if status != http.StatusOK || body != "success" || len(app.applied) != 3 {
		t.Fatalf("matching-origin cross-site-metadata POST status=%d body=%q applied=%d; want 200 and one applied callback", status, body, len(app.applied))
	}
	getResponse, err := http.Get(server.URL + "/api/public/alipay/callback")
	if err != nil {
		t.Fatal(err)
	}
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusNotFound || len(app.applied) != 3 {
		t.Fatalf("composed GET status=%d applied=%d; want 404 and no added application call", getResponse.StatusCode, len(app.applied))
	}
}
