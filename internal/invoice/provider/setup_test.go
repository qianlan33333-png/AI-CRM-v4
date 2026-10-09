package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInvoiceSetupSignedReadbackAndSingleConfigurationAttempt(t *testing.T) {
	_, key, _ := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	trust := Trust{Mode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKey: &key.PublicKey}
	calls := 0
	status := 200
	validSignature := true
	lost := false
	cli, verifier, _, e := NewTrustedClient(context.Background(), "service-merchant", "serial", key, trust, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if lost {
			return nil, errors.New("fixture network loss")
		}
		if strings.HasSuffix(r.URL.Path, "tax-codes") && (r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("sub_mchid") != "sub") {
			t.Fatal("tax pagination contract")
		}
		if r.Method == http.MethodPatch {
			var c DevelopmentConfig
			if json.NewDecoder(r.Body).Decode(&c) != nil || c.CallbackURL != "https://crm.test/api/callbacks/wechat-invoice" || c.SubMerchant != "sub" || c.ShowInvoiceCell || c.SupportVAT {
				t.Fatal("development patch not bounded to CRM flow")
			}
		}
		body := `{"callback_url":"https://crm.test/api/callbacks/wechat-invoice","show_fapiao_cell":false,"support_vat_fapiao":false}`
		if status != 200 {
			body = `{"code":"PARAM_ERROR","message":"fixture private error"}`
		}
		ts := fmt.Sprint(time.Now().Unix())
		signature := invoiceSignature(t, key, body, ts, "nonce")
		if !validSignature {
			signature = "invalid"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Wechatpay-Serial": {trust.PublicKeyID}, "Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {"nonce"}, "Wechatpay-Signature": {signature}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	if e != nil {
		t.Fatal(e)
	}
	setup := SetupClient{Client: cli, Verifier: verifier}
	if r := setup.TaxCodes(context.Background(), "sub"); !r.SignatureVerified || r.Outcome != "confirmed" {
		t.Fatal("readback not verified")
	}
	patch := DevelopmentConfig{SubMerchant: "sub", CallbackURL: "https://crm.test/api/callbacks/wechat-invoice"}
	if r := setup.ConfigureDevelopment(context.Background(), patch); !r.SignatureVerified || r.Outcome != "confirmed" {
		t.Fatal("patch not verified")
	}
	status = 400
	if r := setup.Status(context.Background(), "sub"); !r.SignatureVerified || r.Outcome != "rejected" {
		t.Fatal("signed API error not captured")
	}
	status = 500
	if r := setup.ConfigureDevelopment(context.Background(), patch); !r.SignatureVerified || r.Outcome != "outcome_unknown" {
		t.Fatal("signed server failure falsely established write finality")
	}
	status = 400
	validSignature = false
	if r := setup.Status(context.Background(), "sub"); r.SignatureVerified || len(r.Body) != 0 {
		t.Fatal("unsigned error facts leaked into receipt")
	}
	lost = true
	before := calls
	if r := setup.ConfigureDevelopment(context.Background(), patch); r.Outcome != "outcome_unknown" || calls != before+1 {
		t.Fatal("ambiguous setup write retried or misclassified")
	}
}
