package provider

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/emmansun/gmsm/sm3"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type invoiceTransport func(*http.Request) (*http.Response, error)

func (f invoiceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type material struct{ a p.Application }

func (m material) PrepareInvoiceEffect(context.Context, string, bool) (p.Application, error) {
	return m.a, nil
}

type paymentProof struct{}

func (paymentProof) VerifyInvoicePayment(context.Context, p.Snapshot) error { return nil }
func invoiceSignature(t *testing.T, key *rsa.PrivateKey, body, ts, nonce string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(ts + "\n" + nonce + "\n" + body + "\n"))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(sig)
}
func invoiceProviderFixture(t *testing.T, respond func(*http.Request) string) (*WeChat, *rsa.PrivateKey, p.Application) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(12345), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, _ := x509.ParseCertificate(der)
	a := p.Application{ID: "inv_" + strings.Repeat("a", 28), InvoiceID: "inv_" + strings.Repeat("b", 28), BlueNumber: "BLUE1", Snapshot: p.Snapshot{MerchantID: "sub", AppID: "wx-app", AppScope: "wechat-app:wx-app", OrderNo: "order-1", TransactionID: "transaction-1", Amount: 9900, SellerTaxID: "tax-1", BillingPersonID: "finance", Lines: []p.Line{{TaxCode: "3010101020203000000", GoodsName: "*培训服务*课程", TotalAmount: 9900, Quantity: 100000000, TaxRate: 600}}}, Buyer: p.Buyer{Type: "INDIVIDUAL", Name: "本人"}}
	a.ID = a.Snapshot.TransactionID
	w, e := New(context.Background(), "service-merchant", "merchant-serial", strings.Repeat("k", 32), key, cert, &OfficialAccount{AppID: "wx-app"}, material{a}, paymentProof{})
	if e != nil {
		t.Fatal(e)
	}
	client, _, _, e := NewTrustedClient(context.Background(), "service-merchant", "merchant-serial", key, Trust{Mode: "certificate", Certificate: cert}, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
		body := respond(r)
		ts := fmt.Sprint(time.Now().Unix())
		nonce := "fixture-nonce"
		return &http.Response{StatusCode: 200, Header: http.Header{"Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {w.PlatformSerial}, "Wechatpay-Signature": {invoiceSignature(t, key, body, ts, nonce)}, "Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	if e != nil {
		t.Fatal(e)
	}
	w.Client = client
	return w, key, a
}
func TestInvoiceOfficialSDKRequestsFreezeAndQueryBinding(t *testing.T) {
	var issue map[string]json.RawMessage
	w, _, a := invoiceProviderFixture(t, func(r *http.Request) string {
		if !strings.Contains(r.Header.Get("Authorization"), "service-merchant") {
			t.Fatal("unsigned official API")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "title-url"):
			if r.URL.Query().Get("openid") != "trusted-openid" || r.URL.Query().Get("fapiao_apply_id") == "" {
				t.Fatal("title identity binding")
			}
			return `{"miniprogram_appid":"wx-official","miniprogram_path":"pages/title","miniprogram_user_name":"gh-official"}`
		case strings.HasSuffix(r.URL.Path, "user-title"):
			if r.URL.Query().Get("scene") != "WITH_WECHATPAY" || r.URL.Query().Get("fapiao_apply_id") != "transaction-1" {
				t.Fatal("wrong title scene")
			}
			return `{"type":"INDIVIDUAL","name":"本人","amount":9900}`
		case strings.HasSuffix(r.URL.Path, "issue-general"):
			if json.NewDecoder(r.Body).Decode(&issue) != nil {
				t.Fatal("issue body")
			}
			return `{}`
		case strings.HasSuffix(r.URL.Path, "reverse"):
			var b struct {
				Reason   string              `json:"reverse_reason"`
				Invoices []map[string]string `json:"fapiao_information"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			if !strings.HasSuffix(r.URL.Path, "/fapiao-applications/transaction-1/reverse") || b.Reason != "SALES_RETURN" || len(b.Invoices) != 1 || b.Invoices[0]["fapiao_number"] != "BLUE1" {
				t.Fatal("reversal shape")
			}
			return `{}`
		default:
			if !strings.HasSuffix(r.URL.Path, "/fapiao-applications/transaction-1") {
				t.Fatal("query did not retain original transaction")
			}
			return `{"fapiao_information":[{"fapiao_id":"foreign","status":"ISSUED"}]}`
		}
	})
	if _, e := w.Title(context.Background(), a, "trusted-openid"); e != nil {
		t.Fatal(e)
	}
	if _, e := w.ReadTitle(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	if e := w.Write(context.Background(), a, false); e != nil {
		t.Fatal(e)
	}
	var applyID string
	if json.Unmarshal(issue["fapiao_apply_id"], &applyID) != nil || applyID != a.Snapshot.TransactionID {
		t.Fatal("issue did not retain original transaction")
	}
	var detail struct {
		ID           string           `json:"fapiao_id"`
		Amount       int64            `json:"total_amount"`
		Items        []p.Line         `json:"items"`
		Transactions []map[string]any `json:"transaction_information"`
	}
	if json.Unmarshal(issue["fapiao_information"], &detail) != nil || detail.ID != a.InvoiceID || detail.Amount != 9900 || len(detail.Items) != 1 || len(detail.Transactions) != 1 {
		t.Fatal("immutable issue contract")
	}
	if e := w.Write(context.Background(), a, true); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Query(context.Background(), a); !errors.Is(e, p.ErrConflict) {
		t.Fatal("foreign invoice accepted", e)
	}
}
func TestInvoiceOfficialCallbackSignatureDecryptAndReplayDigest(t *testing.T) {
	w, key, a := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	block, _ := aes.NewCipher([]byte(strings.Repeat("k", 32)))
	gcm, _ := cipher.NewGCM(block)
	makeRequest := func(extra, merchant string) *http.Request {
		plain, _ := json.Marshal(map[string]string{"mchid": merchant, "sub_mchid": "sub", "fapiao_apply_id": a.ID, "extra": extra})
		nonce := "123456789012"
		ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), plain, []byte("invoice")))
		raw, _ := json.Marshal(map[string]any{"id": "notice-1", "event_type": "FAPIAO.ISSUED", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": nonce, "associated_data": "invoice"}})
		r := httptest.NewRequest("POST", "https://crm.fixture/api/callbacks/wechat-invoice", strings.NewReader(string(raw)))
		ts := fmt.Sprint(time.Now().Unix())
		r.Header.Set("Wechatpay-Timestamp", ts)
		r.Header.Set("Wechatpay-Nonce", "notify-nonce")
		r.Header.Set("Wechatpay-Serial", w.PlatformSerial)
		r.Header.Set("Wechatpay-Signature", invoiceSignature(t, key, string(raw), ts, "notify-nonce"))
		return r
	}
	first, e := w.Verify(context.Background(), makeRequest("one", "service-merchant"))
	if e != nil || first.ApplicationID != a.ID {
		t.Fatal("verified encrypted notice", e)
	}
	second, e := w.Verify(context.Background(), makeRequest("two", "service-merchant"))
	if e != nil || second.BodyDigest == first.BodyDigest {
		t.Fatal("body changes invisible to receipt", e)
	}
	bad := makeRequest("one", "service-merchant")
	bad.Header.Set("Wechatpay-Signature", "invalid")
	if _, e = w.Verify(context.Background(), bad); !errors.Is(e, p.ErrInvalid) {
		t.Fatal("bad signature", e)
	}
	if _, e = w.Verify(context.Background(), makeRequest("one", "foreign")); !errors.Is(e, p.ErrInvalid) {
		t.Fatal("wrong SP merchant", e)
	}
	bad = makeRequest("one", "service-merchant")
	bad.Header.Set("Wechatpay-Timestamp", fmt.Sprint(time.Now().Add(-10*time.Minute).Unix()))
	if _, e = w.Verify(context.Background(), bad); !errors.Is(e, p.ErrInvalid) {
		t.Fatal("expired callback", e)
	}
}
func TestInvoiceFileUnsignedDigestAndRedirectBoundary(t *testing.T) {
	badURL := false
	w, _, a := invoiceProviderFixture(t, func(r *http.Request) string {
		if !strings.Contains(r.URL.Path, "/fapiao-applications/transaction-1") {
			t.Fatal("file lookup did not retain original transaction")
		}
		if strings.HasSuffix(r.URL.Path, "fapiao-files") {
			url := "https://pay.wechatpay.cn/invoicing/fapiao/fapiao-file?token=fixture"
			if badURL {
				url = "https://evil.test/file?token=fixture"
			}
			return fmt.Sprintf(`{"fapiao_download_info_list":[{"fapiao_id":"%s","download_url":"%s"}]}`, "inv_"+strings.Repeat("b", 28), url)
		}
		return fmt.Sprintf(`{"fapiao_information":[{"fapiao_id":"%s","status":"ISSUED","total_amount":9900,"seller_information":{"taxpayer_id":"tax-1"},"blue_fapiao":{"fapiao_number":"BLUE1"},"card_information":{"card_openid":"trusted-openid"}}]}`, "inv_"+strings.Repeat("b", 28))
	})
	pdf := []byte("%PDF-1.5 fixture")
	validDigest := true
	w.FileClient = &http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("mchid") != "service-merchant" || r.URL.Query().Get("openid") != "trusted-openid" {
			t.Fatal("download credential contract")
		}
		digest := fmt.Sprintf("%X", sm3.Sum(pdf))
		if !validDigest {
			digest = strings.Repeat("0", 64)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Sm3-Digest": {digest}}, Body: io.NopCloser(strings.NewReader(string(pdf)))}, nil
	})}
	if _, e := w.File(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	validDigest = false
	if _, e := w.File(context.Background(), a); !errors.Is(e, p.ErrConflict) {
		t.Fatal("corrupt document", e)
	}
	badURL = true
	if _, e := w.File(context.Background(), a); !errors.Is(e, p.ErrConflict) {
		t.Fatal("foreign download URL", e)
	}
}

type invoiceRejectedPayment struct{ err error }

func (v invoiceRejectedPayment) VerifyInvoicePayment(context.Context, p.Snapshot) error {
	return v.err
}
func TestInvoiceEffectUnknownCannotRetryAndNoWriteBeforePaymentProof(t *testing.T) {
	w, key, _ := invoiceProviderFixture(t, func(*http.Request) string { t.Fatal("unverified payment reached invoice write"); return `{}` })
	w.Payments = invoiceRejectedPayment{err: p.ErrUnavailable}
	result, e := w.Execute(context.Background(), effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceIssue}, effects.Attempt{EffectID: "eer_1"})
	if e != nil || result.CallAttempted || result.Completion != effects.StateFinalFailed || result.FailureCode != "invoice_payment_check_unavailable" {
		t.Fatal("payment read misclassified as invoice write", result, e)
	}
	w.Payments = invoiceRejectedPayment{err: p.ErrConflict}
	result, e = w.Execute(context.Background(), effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceIssue}, effects.Attempt{EffectID: "eer_1"})
	if e != nil || result.CallAttempted || result.FailureCode != "invoice_payment_unverified" {
		t.Fatal("conflicting payment proof did not fail closed", result, e)
	}
	w, key, _ = invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	w.PlatformSerial = "PUB_KEY_ID_fixture"
	w.Client, _, _, _ = NewTrustedClient(context.Background(), "service-merchant", "merchant-serial", key, Trust{Mode: "public_key", PublicKeyID: w.PlatformSerial, PublicKey: &key.PublicKey}, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic transport loss") })}))
	result, e = w.Execute(context.Background(), effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceIssue}, effects.Attempt{EffectID: "eer_2"})
	if e != nil || !result.CallAttempted || result.Completion != effects.StateUnknown || result.SafeToRetryRejected {
		t.Fatal("ambiguous issue not kept unknown", result, e)
	}
}

func TestInvoiceUnsubmittedRequiresExactSignedOfficialProof(t *testing.T) {
	const message = "商户尚未同意开票，无法对当前发票申请单进行当前操作，请先调用【开具电子发票】接口进行开票"
	for _, kind := range []string{"signed", "unsigned", "other-limit", "issued"} {
		t.Run(kind, func(t *testing.T) {
			w, key, a := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
			trust := Trust{Mode: "public_key", PublicKey: &key.PublicKey, PublicKeyID: "PUB_KEY_ID_1234567890"}
			client, verifier, id, e := NewTrustedClient(context.Background(), w.MerchantID, "merchant-serial", key, trust, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Query().Get("fapiao_id") != "" || r.URL.Query().Get("sub_mchid") != a.Snapshot.MerchantID {
					t.Fatal("filtered or write request")
				}
				raw, _ := json.Marshal(map[string]string{"code": "RULE_LIMIT", "message": message})
				status := 403
				if kind == "other-limit" {
					raw = []byte(`{"code":"RULE_LIMIT","message":"other limit"}`)
				}
				if kind == "issued" {
					raw = []byte(`{"fapiao_information":[{"status":"ISSUED"}]}`)
					status = 200
				}
				ts := fmt.Sprint(time.Now().Unix())
				nonce := "fixture-nonce"
				sig := invoiceSignature(t, key, string(raw), ts, nonce)
				if kind == "unsigned" {
					sig = "invalid"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {trust.PublicKeyID}, "Wechatpay-Signature": {sig}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}))
			if e != nil {
				t.Fatal(e)
			}
			w.Client = client
			w.Verifier = verifier
			w.PlatformSerial = id
			e = w.VerifyUnsubmitted(context.Background(), a)
			if (kind == "signed") != (e == nil) {
				t.Fatal("absence proof boundary", kind, e)
			}
		})
	}
}

func TestInvoiceSignedRejectionBecomesPrivateOwnerReceipt(t *testing.T) {
	for _, signed := range []bool{true, false} {
		w, key, a := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
		trust := Trust{Mode: "public_key", PublicKey: &key.PublicKey, PublicKeyID: "PUB_KEY_ID_1234567890"}
		const privateMessage = "private invoice title mismatch"
		client, verifier, id, e := NewTrustedClient(context.Background(), w.MerchantID, "merchant-serial", key, trust, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" {
				t.Fatal("unexpected read")
			}
			raw := `{"code":"RULE_LIMIT","message":"` + privateMessage + `"}`
			ts, nonce := fmt.Sprint(time.Now().Unix()), "fixture-nonce"
			sig := invoiceSignature(t, key, raw, ts, nonce)
			if !signed {
				sig = "invalid"
			}
			return &http.Response{StatusCode: 403, Header: http.Header{"Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {trust.PublicKeyID}, "Wechatpay-Signature": {sig}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})}))
		if e != nil {
			t.Fatal(e)
		}
		w.Client, w.Verifier, w.PlatformSerial = client, verifier, id
		result, e := w.Execute(context.Background(), effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceIssue}, effects.Attempt{EffectID: "eer_1"})
		if e != nil || result.Completion != effects.StateUnknown || !result.CallAttempted || result.RealExternalCallExecuted || result.SafeToRetryRejected {
			t.Fatal("rejection changed unknown safety boundary", e)
		}
		if signed {
			var response p.OfficialResponse
			if !result.Artifact.Valid() || result.Artifact.Kind != p.OfficialResponseArtifact || json.Unmarshal(result.Artifact.Payload, &response) != nil || response.HTTPStatus != 403 || response.Code != "RULE_LIMIT" || response.Message != privateMessage {
				t.Fatal("signed owner receipt missing")
			}
			if strings.Contains(response.Error(), privateMessage) {
				t.Fatal("private response leaked to error logs")
			}
		} else if result.Artifact.Kind != "" {
			t.Fatal("unverified response stored as official receipt")
		}
		if a.InvoiceID == "" {
			t.Fatal("fixture lost invoice binding")
		}
	}
}
