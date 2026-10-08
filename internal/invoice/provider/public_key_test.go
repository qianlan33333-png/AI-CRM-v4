package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

func TestInvoicePublicKeyTitleEncryptionAndErrorTrust(t *testing.T) {
	_, merchantKey, a := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	platformKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	trust := Trust{Mode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKey: &platformKey.PublicKey}
	w, e := NewWithTrust(context.Background(), "service-merchant", "merchant-serial", strings.Repeat("k", 32), merchantKey, trust, &OfficialAccount{AppID: "wx-app"}, material{a}, paymentProof{})
	if e != nil {
		t.Fatal(e)
	}
	phone, email := "13800000000", "fixture@example.test"
	phoneCipher, _ := utils.EncryptOAEPWithPublicKey(phone, &merchantKey.PublicKey)
	emailCipher, _ := utils.EncryptOAEPWithPublicKey(email, &merchantKey.PublicKey)
	status := 200
	signatureOK := true
	requestCount := 0
	sdk, verifier, _, e := NewTrustedClient(context.Background(), "service-merchant", "merchant-serial", merchantKey, trust, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
		requestCount++
		if !strings.Contains(r.Header.Get("Authorization"), `mchid="service-merchant"`) || r.Header.Get("Wechatpay-Serial") != trust.PublicKeyID {
			t.Fatal("incorrect signing or trust source")
		}
		body := `{}`
		switch {
		case strings.HasSuffix(r.URL.Path, "user-title"):
			body = fmt.Sprintf(`{"type":"ORGANIZATION","name":"单位抬头","taxpayer_id":"fixture-tax","address":"fixture address","telephone":"02012345678","bank_name":"fixture bank","bank_account":"123","phone":"%s","email":"%s","user_apply_message":"fixture remark"}`, phoneCipher, emailCipher)
		case strings.HasSuffix(r.URL.Path, "issue-general"):
			var issued struct {
				Buyer p.Buyer `json:"buyer_information"`
			}
			if json.NewDecoder(r.Body).Decode(&issued) != nil {
				t.Fatal("invalid issue request")
			}
			decodedPhone, err := utils.DecryptOAEP(issued.Buyer.Phone, platformKey)
			if err != nil || decodedPhone != phone {
				t.Fatal("phone must use payment public key")
			}
			decodedEmail, err := utils.DecryptOAEP(issued.Buyer.Email, platformKey)
			if err != nil || decodedEmail != email {
				t.Fatal("email must use payment public key")
			}
			if issued.Buyer.Address != "fixture address" || issued.Buyer.BankName != "fixture bank" || issued.Buyer.BankAccount != "123" || issued.Buyer.UserApplyMessage != "fixture remark" {
				t.Fatal("official title fields lost")
			}
		default:
			body = `{"code":"NOT_FOUND","message":"fixture"}`
		}
		ts := fmt.Sprint(time.Now().Unix())
		sig := invoiceSignature(t, platformKey, body, ts, "nonce")
		if !signatureOK {
			sig = "WECHATPAY/SIGNTEST/invalid"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Wechatpay-Serial": {trust.PublicKeyID}, "Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {"nonce"}, "Wechatpay-Signature": {sig}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	if e != nil {
		t.Fatal(e)
	}
	w.Client = sdk
	w.Verifier = verifier
	buyer, e := w.ReadTitle(context.Background(), a)
	if e != nil || buyer.Phone != phone || buyer.Email != email {
		t.Fatal("title decrypt failed", e)
	}
	a.Buyer = buyer
	if e = w.Write(context.Background(), a, false); e != nil {
		t.Fatal("issue failed", e)
	}
	if a.Buyer.Phone != phone || a.Buyer.Email != email {
		t.Fatal("frozen buyer mutated by encryption")
	}
	// A forged 404 is unavailable, never an authenticated absence of a ticket.
	status = 404
	signatureOK = false
	if _, e = w.Query(context.Background(), a); !errors.Is(e, p.ErrUnavailable) {
		t.Fatal("unsigned absence accepted", e)
	}
	signatureOK = true
	if _, e = w.Query(context.Background(), a); !errors.Is(e, p.ErrPending) {
		t.Fatal("verified absence rejected", e)
	}
	// Invalid local cipher material fails before any provider call.
	prior := requestCount
	a.Buyer.Phone = strings.Repeat("x", 300)
	w.Material = material{a}
	result, e := w.Execute(context.Background(), effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceIssue}, effects.Attempt{EffectID: "eer_cipher"})
	if e != nil || result.CallAttempted || result.Completion != effects.StateFinalFailed || requestCount != prior {
		t.Fatal("local encryption failure counted as provider attempt", result, e)
	}
}

func TestInvoicePublicKeyNotificationsAndWrongTrust(t *testing.T) {
	_, merchantKey, a := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	platformKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	trust := Trust{Mode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKey: &platformKey.PublicKey}
	apiKey := strings.Repeat("k", 32)
	w, e := NewWithTrust(context.Background(), "service-merchant", "merchant-serial", apiKey, merchantKey, trust, &OfficialAccount{AppID: "wx-app"}, material{a}, paymentProof{})
	if e != nil {
		t.Fatal(e)
	}
	block, _ := aes.NewCipher([]byte(apiKey))
	gcm, _ := cipher.NewGCM(block)
	raw, _ := json.Marshal(map[string]any{"mchid": "service-merchant", "sub_mchid": "sub", "fapiao_apply_id": a.ID})
	encrypted := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte("123456789012"), raw, []byte("fixture")))
	body, _ := json.Marshal(map[string]any{"id": "notify-1", "event_type": "FAPIAO.ISSUED", "resource_type": "encrypt-resource", "resource": map[string]any{"algorithm": "AEAD_AES_256_GCM", "nonce": "123456789012", "associated_data": "fixture", "ciphertext": encrypted}})
	makeRequest := func(serial string, signer *rsa.PrivateKey) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "https://crm.test/api/callbacks/wechat-invoice", strings.NewReader(string(body)))
		ts := fmt.Sprint(time.Now().Unix())
		r.Header.Set("Wechatpay-Timestamp", ts)
		r.Header.Set("Wechatpay-Nonce", "nonce")
		r.Header.Set("Wechatpay-Serial", serial)
		r.Header.Set("Wechatpay-Signature", invoiceSignature(t, signer, string(body), ts, "nonce"))
		return r
	}
	callback, e := NewNotificationVerifier("service-merchant", apiKey, trust)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := any(callback).(effects.ProviderAdapter); ok {
		t.Fatal("notification verifier grants external write capability")
	}
	for _, verifier := range []p.NotificationVerifier{w, callback} {
		if n, e := verifier.Verify(context.Background(), makeRequest(trust.PublicKeyID, platformKey)); e != nil || n.ApplicationID != a.ID {
			t.Fatal("public-key notification not verified", e)
		}
		for _, r := range []*http.Request{makeRequest("PUB_KEY_ID_foreign", platformKey), makeRequest(trust.PublicKeyID, merchantKey)} {
			if _, e = verifier.Verify(context.Background(), r); !errors.Is(e, p.ErrInvalid) {
				t.Fatal("wrong key or key ID accepted", e)
			}
		}
		stale := makeRequest(trust.PublicKeyID, platformKey)
		ts := fmt.Sprint(time.Now().Add(-10 * time.Minute).Unix())
		stale.Header.Set("Wechatpay-Timestamp", ts)
		stale.Header.Set("Wechatpay-Signature", invoiceSignature(t, platformKey, string(body), ts, "nonce"))
		if _, e = verifier.Verify(context.Background(), stale); !errors.Is(e, p.ErrInvalid) {
			t.Fatal("stale signed notification accepted", e)
		}
	}
	wrongKey, _ := NewNotificationVerifier("service-merchant", strings.Repeat("x", 32), trust)
	wrongScope, _ := NewNotificationVerifier("foreign-service", apiKey, trust)
	for _, verifier := range []*NotificationVerifier{wrongKey, wrongScope} {
		if _, e = verifier.Verify(context.Background(), makeRequest(trust.PublicKeyID, platformKey)); !errors.Is(e, p.ErrInvalid) {
			t.Fatal("wrong decryption key or service scope accepted", e)
		}
	}
	for _, wrong := range []Trust{{Mode: "public_key", PublicKeyID: "serial", PublicKey: &platformKey.PublicKey}, {Mode: "certificate", PublicKeyID: trust.PublicKeyID, PublicKey: &platformKey.PublicKey}, {Mode: "", PublicKeyID: trust.PublicKeyID, PublicKey: &platformKey.PublicKey}} {
		if _, _, _, e := NewTrustedClient(context.Background(), "service-merchant", "serial", merchantKey, wrong); e == nil {
			t.Fatal("invalid trust accepted")
		}
	}
}
