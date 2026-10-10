package provider

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
)

type absenceTransport func(*http.Request) (*http.Response, error)

func (fn absenceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestOfficialProfitSharingAbsenceRequiresSigned404(t *testing.T) {
	merchant, platform := newProfitSharingAuthKey(t), newProfitSharingAuthKey(t)
	template := &x509.Certificate{SerialNumber: big.NewInt(123456), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &platform.PublicKey, platform)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	material := ProfitSharingMaterial{PayloadDigest: effectport.Hash("signed-absence-test"), AppID: "wx-test", ProviderOrderNo: "v3ps_ABCDEFGHIJKLMNOPQRST", TransactionReference: "trusted-transaction", ReceiverAccount: "trusted-openid", AmountMinor: 204}
	for _, row := range []struct {
		name              string
		status            int
		code              string
		tampered, missing bool
		want              bool
		certificate       bool
	}{
		{name: "signed absence", status: 404, code: "RESOURCE_NOT_EXISTS", want: true},
		{name: "unsigned absence", status: 404, code: "RESOURCE_NOT_EXISTS", missing: true},
		{name: "tampered absence", status: 404, code: "RESOURCE_NOT_EXISTS", tampered: true},
		{name: "wrong status", status: 500, code: "RESOURCE_NOT_EXISTS"},
		{name: "wrong code", status: 404, code: "INVALID_REQUEST"},
		{name: "certificate signed absence", status: 404, code: "RESOURCE_NOT_EXISTS", want: true, certificate: true},
		{name: "certificate unsigned absence", status: 404, code: "RESOURCE_NOT_EXISTS", missing: true, certificate: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			sdk, err := NewOfficialProfitSharingSDKWithPublicKey(context.Background(), "merchant", "merchant-serial", "PUB_KEY_ID_test", merchant, &platform.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			serial := "PUB_KEY_ID_test"
			options := []core.ClientOption{option.WithWechatPayPublicKeyAuthCipher("merchant", "merchant-serial", merchant, serial, &platform.PublicKey)}
			if row.certificate {
				sdk, err = NewOfficialProfitSharingSDKWithCertificate(context.Background(), "merchant", "merchant-serial", merchant, certificate)
				if err != nil {
					t.Fatal(err)
				}
				serial = fmt.Sprintf("%X", certificate.SerialNumber.Bytes())
				options = []core.ClientOption{option.WithMerchantCredential("merchant", "merchant-serial", merchant), option.WithVerifier(verifiers.NewSHA256WithRSAVerifier(core.NewCertificateMapWithList([]*x509.Certificate{certificate})))}
			}
			calls := 0
			options = append(options, option.WithHTTPClient(&http.Client{Transport: absenceTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v3/profitsharing/orders/") || r.URL.Query().Get("transaction_id") != material.TransactionReference {
					t.Fatal("unexpected original-order request")
				}
				body := fmt.Sprintf(`{"code":%q,"message":"private account must not escape"}`, row.code)
				timestamp := fmt.Sprint(time.Now().Unix())
				nonce := "fixture-nonce"
				signature := signTest(t, platform, timestamp+"\n"+nonce+"\n"+body+"\n")
				if row.tampered {
					body += " "
				}
				header := http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {serial}, "Wechatpay-Signature": {signature}}
				if row.missing {
					header = http.Header{}
				}
				return &http.Response{StatusCode: row.status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}))
			client, err := core.NewClient(context.Background(), options...)
			if err != nil {
				t.Fatal(err)
			}
			sdk.orders.Client = client
			query, err := sdk.QueryOrder(context.Background(), material)
			if query.OrderNotFound != row.want || (row.want && (err != nil || query.OccurredAt.IsZero())) || (!row.want && err == nil) || calls != 1 {
				t.Fatalf("query=%+v err=%v calls=%d", query, err, calls)
			}
			if err != nil && strings.Contains(err.Error(), "private account") {
				t.Fatal("raw response escaped")
			}
		})
	}
}
