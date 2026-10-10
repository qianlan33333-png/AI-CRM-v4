package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

type paymentRefundApplication interface {
	GetPayment(context.Context, int64) (paymentdomain.Payment, error)
	RequestRefund(context.Context, paymentport.RefundCommand) (paymentdomain.Refund, error)
	ReconcileWeChatPayRefund(context.Context, int64) (paymentdomain.Refund, error)
}
type paymentRefundRoleReceipt struct {
	Refund            paymentdomain.Refund
	ProviderConfirmed bool
}

// This is an OS-authenticated one-shot operator command, not an HTTP route or
// a new Provider writer. Explicit user authorization is retained in the
// original Payment operation receipt/audit via its immutable actor scope.
func executePaymentRefund(ctx context.Context, service paymentRefundApplication, cfg platformconfig.Runtime, effectiveUID int) (paymentRefundRoleReceipt, error) {
	if effectiveUID != 0 || cfg.Role != platformconfig.RolePaymentRefund || service == nil || cfg.PaymentRefundID < 1 || cfg.PaymentRefundOrderID < 1 || cfg.PaymentRefundAmountMinor < 1 || cfg.PaymentRefundAuthorization == "" || cfg.PaymentRefundKey == "" {
		return paymentRefundRoleReceipt{}, errors.New("authorized root refund parameters required")
	}
	payment, err := service.GetPayment(ctx, cfg.PaymentRefundID)
	if err != nil || payment.ID != cfg.PaymentRefundID || payment.OrderID != cfg.PaymentRefundOrderID || payment.Provider != paymentdomain.ProviderWeChatPay || payment.Historical || payment.Status != paymentdomain.StatusPaid || cfg.PaymentRefundAmountMinor > payment.AmountMinor {
		return paymentRefundRoleReceipt{}, errors.New("refund target does not match native paid order")
	}
	actor := "ops:root-refund:" + cfg.PaymentRefundAuthorization
	digest := sha256.Sum256([]byte(actor + "|" + cfg.PaymentRefundKey + "|" + strconv.FormatInt(payment.ID, 10) + "|" + strconv.FormatInt(cfg.PaymentRefundAmountMinor, 10)))
	refund, err := service.RequestRefund(ctx, paymentport.RefundCommand{PaymentID: payment.ID, AmountMinor: cfg.PaymentRefundAmountMinor, RefundNo: fmt.Sprintf("OPRF-%x", digest[:12]), Reason: "authorized_order_refund", ActorScope: actor, IdempotencyKey: cfg.PaymentRefundKey})
	if err != nil {
		return paymentRefundRoleReceipt{}, errors.New("refund not accepted; preserve original receipt and reconcile funding")
	}
	result := paymentRefundRoleReceipt{Refund: refund}
	// The normal worker owns the Provider write. This command only requests the
	// original refund and reads its verified result, including on a safe replay.
	if refund.Status == paymentdomain.RefundEffectAccepted || refund.Status == paymentdomain.RefundOutcomeUnknown || refund.Status == paymentdomain.RefundCompleted {
		verified, readErr := service.ReconcileWeChatPayRefund(ctx, refund.ID)
		if readErr == nil {
			result.Refund = verified
			result.ProviderConfirmed = verified.Status == paymentdomain.RefundCompleted
		}
	}
	return result, nil
}
