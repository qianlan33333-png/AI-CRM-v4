package app

import (
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

// matchesVerifiedRefundPayment binds a callback or query result to the exact
// merchant order and Provider transaction already frozen on the paid Payment.
func matchesVerifiedRefundPayment(payment domain.Payment, merchantOrderNo, transactionReference, transactionDigest string) bool {
	if merchantOrderNo == "" || merchantOrderNo != payment.MerchantOrderNo ||
		!validProviderTransactionReference(transactionReference) || transactionReference != payment.ProviderTransactionReference {
		return false
	}

	transactionNamespace := ""
	switch payment.Provider {
	case domain.ProviderWeChatPay:
		transactionNamespace = "wechatpay.transaction"
	case domain.ProviderAlipay:
		transactionNamespace = "alipay.transaction"
	default:
		return false
	}
	wantDigest := string(effectport.Hash(transactionNamespace, transactionReference))
	return transactionDigest == wantDigest && payment.ProviderTransactionDigest == wantDigest
}
