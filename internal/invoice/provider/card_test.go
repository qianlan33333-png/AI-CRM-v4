package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type cardMaterial struct {
	material
	card p.CardConfig
}

func (m cardMaterial) PrepareCardEffect(context.Context, string) (p.CardConfig, error) {
	return m.card, nil
}
func TestInvoiceCardEffectSignedOutcomeAndNoBlindRetry(t *testing.T) {
	_, key, _ := invoiceProviderFixture(t, func(*http.Request) string { return `{}` })
	trust := Trust{Mode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKey: &key.PublicKey}
	for _, mode := range []string{"issued", "rejected", "unsigned", "lost", "wrong_app"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client, verifier, _, err := NewTrustedClient(context.Background(), "service", "serial", key, trust, option.WithHTTPClient(&http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "lost" {
					return nil, errors.New("network lost")
				}
				var template CardTemplate
				if json.NewDecoder(r.Body).Decode(&template) != nil || template.SubMerchant != "sub" || template.AppID != "wx-card" || template.Information.PayeeName != "销售方" || template.Information.CustomCell.JumpURL != "https://crm.test/my/orders" {
					t.Fatal("template not frozen")
				}
				body := `{"card_id":"card-fixture","card_appid":"wx-card"}`
				status := 200
				if mode == "rejected" {
					status = 400
					body = `{"code":"PARAM_ERROR","message":"private failure"}`
				}
				if mode == "wrong_app" {
					body = `{"card_id":"card-fixture","card_appid":"other"}`
				}
				ts := fmt.Sprint(time.Now().Unix())
				sig := invoiceSignature(t, key, body, ts, "nonce")
				if mode == "unsigned" {
					sig = "invalid"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Wechatpay-Serial": {trust.PublicKeyID}, "Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {"nonce"}, "Wechatpay-Signature": {sig}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			card := p.CardConfig{MerchantID: "sub", AppID: "wx-card", PayeeName: "销售方", LogoURL: "https://mmbiz.qpic.cn/mmbiz_png/fixture/0", JumpURL: "https://crm.test/my/orders", Confirmed: true}
			raw, _ := json.Marshal(card)
			w := &WeChat{Client: client, Verifier: verifier, Material: cardMaterial{card: card}}
			env := effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceCardConfigure, PayloadDigest: effects.Hash("invoice.card.payload", string(raw))}
			result, e := w.Execute(context.Background(), env, effects.Attempt{EffectID: "fixture-card"})
			if e != nil {
				t.Fatal(e)
			}
			want := effects.StateUnknown
			if mode == "issued" {
				want = effects.StateExecuted
			}
			if mode == "rejected" {
				want = effects.StateFinalFailed
			}
			if result.Completion != want || calls != 1 || result.RealExternalCallExecuted != (mode == "issued") {
				t.Fatalf("outcome=%s calls=%d", result.Completion, calls)
			}
			env.PayloadDigest = effects.Hash("tamper", "fixture")
			if _, e = w.Execute(context.Background(), env, effects.Attempt{}); e == nil || calls != 1 {
				t.Fatal("tampered material reached provider")
			}
		})
	}
}
