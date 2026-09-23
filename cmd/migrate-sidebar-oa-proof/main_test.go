package main

import (
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	proof "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration/oaproof"
	pay "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"testing"
)

func TestProviderProofRequiresExactScopedPayerAndMoney(t *testing.T) {
	r := proof.Row{MerchantOrderNo: "order", AppID: "app", OpenID: "openid", AmountMinor: 100}
	q := pay.WeChatPayPaymentQuery{MerchantOrderNo: "order", AppID: "app", PayerOpenID: "openid", AmountMinor: 100, Currency: "CNY", Status: "SUCCESS", EvidenceDigest: effect.Hash("test", "receipt")}
	if !matches(r, q) {
		t.Fatal("matching rejected")
	}
	for _, change := range []func(*pay.WeChatPayPaymentQuery){func(q *pay.WeChatPayPaymentQuery) { q.AppID = "other" }, func(q *pay.WeChatPayPaymentQuery) { q.PayerOpenID = "other" }, func(q *pay.WeChatPayPaymentQuery) { q.AmountMinor++ }, func(q *pay.WeChatPayPaymentQuery) { q.Status = "CLOSED" }, func(q *pay.WeChatPayPaymentQuery) { q.EvidenceDigest = "" }} {
		bad := q
		change(&bad)
		if matches(r, bad) {
			t.Fatal("mismatched signed evidence accepted")
		}
	}
}
