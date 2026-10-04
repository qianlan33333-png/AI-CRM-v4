package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
)

// Principal identity is injected only at the trusted administrator seam; the
// real Payment HTTP/application/PG and EER acceptance run, with no effect worker.
type refundActorScopeSecurity struct{ id int64 }

func (s refundActorScopeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: s.id, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s refundActorScopeSecurity) AuthorizeCSRF(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	return s.Authenticate(ctx, r)
}

func TestPostgreSQLRefundHTTPActorScopedKeysAndLegacyReplay(t *testing.T) {
	f := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{deferEffectsWorker: true})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"fixture": &key.PublicKey}, []byte("0123456789abcdef0123456789abcdef"), "app", "mch")
	if err != nil {
		t.Fatal(err)
	}
	newHandler := func(actor int64) *paymenthttp.Handler {
		h, e := paymenthttp.NewHandler(f.application.paymentReconciliation, verifier, refundActorScopeSecurity{id: actor}, true)
		if e != nil {
			t.Fatal(e)
		}
		return h
	}
	seed := func(suffix string) (int64, string, string) {
		merchant := "v3pay_refund_scope_" + suffix
		txref := "tx-scope-" + suffix
		now := time.Now().UTC()
		var orderID, paymentID int64
		if err := f.application.pool.Native().QueryRow(f.ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','actor-refund-fixture',$1,$1,11,11,1000,'CNY','paid','native',true,$2,$2) RETURNING id`, merchant, now).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		if err := f.application.pool.Native().QueryRow(f.ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,provider_transaction_reference,provider_transaction_digest,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,4,11,11,1000,'CNY','paid',$3,$4,1,$5,$5) RETURNING id`, orderID, merchant, txref, effectport.Hash("wechatpay.transaction", txref), now).Scan(&paymentID); err != nil {
			t.Fatal(err)
		}
		return paymentID, merchant, txref
	}
	post := func(h http.Handler, paymentID int64, merchant, txref, key, refundNo string, amount int64) *httptest.ResponseRecorder {
		path := "/api/admin/wechat-pay/orders/" + merchant + "/refunds"
		body := map[string]any{"refund_amount_total": amount, "reason": "fixture refund", "transaction_id_confirmation": txref, "checked": true}
		if refundNo != "" {
			path = fmt.Sprintf("/api/admin/payments/%d/refunds", paymentID)
			body = map[string]any{"amount_minor": amount, "refund_no": refundNo, "reason": "fixture refund", "transaction_id_confirmation": txref}
		}
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	for _, generic := range []bool{false, true} {
		t.Run(fmt.Sprintf("generic_%t", generic), func(t *testing.T) {
			p1, m1, tx1 := seed(fmt.Sprintf("%t_a", generic))
			p2, m2, tx2 := seed(fmt.Sprintf("%t_b", generic))
			k := fmt.Sprintf("actor-scope-shared-key-%t-0001", generic)
			n1, n2 := "", ""
			if generic {
				n1, n2 = "RF-explicit-a", "RF-explicit-b"
			}
			first := post(newHandler(101), p1, m1, tx1, k, n1, 100)
			if first.Code != 202 {
				t.Fatalf("first=%d %s", first.Code, first.Body.String())
			}
			second := post(newHandler(102), p2, m2, tx2, k, n2, 100)
			if second.Code != 202 {
				t.Fatalf("other actor same key=%d %s", second.Code, second.Body.String())
			}
			for _, c := range []struct {
				actor, id int64
				m, tx, no string
			}{{101, p1, m1, tx1, n1}, {102, p2, m2, tx2, n2}} {
				replay := post(newHandler(c.actor), c.id, c.m, c.tx, k, c.no, 100)
				if replay.Code != 202 {
					t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
				}
				changed := post(newHandler(c.actor), c.id, c.m, c.tx, k, c.no, 101)
				if changed.Code != 409 {
					t.Fatalf("changed payload=%d %s", changed.Code, changed.Body.String())
				}
			}
			var rows, refs, effects int
			if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*),count(DISTINCT refund_no),count(DISTINCT external_effect_id) FROM payment_refunds WHERE payment_id=ANY($1)`, []int64{p1, p2}).Scan(&rows, &refs, &effects); err != nil || rows != 2 || refs != 2 || effects != 2 {
				t.Fatalf("refunds=%d refs=%d effects=%d err=%v", rows, refs, effects, err)
			}
		})
	}
	t.Run("existing_legacy_receipt", func(t *testing.T) {
		p, m, tx := seed("legacy")
		k := "old-compat-refund-key-0001"
		digest := sha256.Sum256([]byte(k))
		oldNo := fmt.Sprintf("RF-%x", digest[:12])
		old, err := f.application.paymentReconciliation.RequestRefund(f.ctx, paymentport.RefundCommand{PaymentID: p, AmountMinor: 100, RefundNo: oldNo, Reason: "fixture refund", ActorScope: "admin:103", IdempotencyKey: k})
		if err != nil {
			t.Fatal(err)
		}
		replay := post(newHandler(103), p, m, tx, k, "", 100)
		if replay.Code != 202 {
			t.Fatalf("legacy replay=%d %s", replay.Code, replay.Body.String())
		}
		var decoded map[string]any
		if err := json.Unmarshal(replay.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["out_refund_no"] != oldNo {
			t.Fatalf("legacy number changed: %s", replay.Body.String())
		}
		changed := post(newHandler(103), p, m, tx, k, "", 101)
		if changed.Code != 409 {
			t.Fatalf("legacy payload accepted=%d", changed.Code)
		}
		var count int
		if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*) FROM payment_refunds WHERE payment_id=$1 AND external_effect_id=$2`, p, strings.TrimPrefix(old.EffectID, "eer_")).Scan(&count); err != nil || count != 1 {
			t.Fatalf("legacy duplicate count=%d err=%v", count, err)
		}
	})
}
