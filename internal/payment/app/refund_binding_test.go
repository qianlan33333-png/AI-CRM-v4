package app

import (
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	"testing"
)

func TestMatchesVerifiedRefundPaymentRequiresExactOriginalTransaction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provider  domain.Provider
		namespace string
	}{
		{name: "wechat_pay", provider: domain.ProviderWeChatPay, namespace: "wechatpay.transaction"},
		{name: "alipay", provider: domain.ProviderAlipay, namespace: "alipay.transaction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merchantOrderNo, transactionReference := "M-refund-binding", "transaction-refund-binding"
			digest := string(effectport.Hash(tc.namespace, transactionReference))
			payment := domain.Payment{
				Provider: tc.provider, MerchantOrderNo: merchantOrderNo,
				ProviderTransactionReference: transactionReference, ProviderTransactionDigest: digest,
			}
			if !matchesVerifiedRefundPayment(payment, merchantOrderNo, transactionReference, digest) {
				t.Fatal("matching Provider fact was rejected")
			}
			for _, mismatch := range []struct {
				name, orderNo, reference, digest string
			}{
				{name: "merchant_order", orderNo: "M-other", reference: transactionReference, digest: digest},
				{name: "transaction_reference", orderNo: merchantOrderNo, reference: "transaction-other", digest: string(effectport.Hash(tc.namespace, "transaction-other"))},
				{name: "transaction_digest", orderNo: merchantOrderNo, reference: transactionReference, digest: string(effectport.Hash("other.transaction", transactionReference))},
				{name: "missing_transaction", orderNo: merchantOrderNo},
			} {
				t.Run(mismatch.name, func(t *testing.T) {
					if matchesVerifiedRefundPayment(payment, mismatch.orderNo, mismatch.reference, mismatch.digest) {
						t.Fatal("mismatched Provider fact was accepted")
					}
				})
			}
		})
	}
}
