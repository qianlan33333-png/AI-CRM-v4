package main

import (
	"context"
	"errors"
	"testing"

	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

type refundRoleStub struct {
	payment                  paymentdomain.Payment
	refund                   paymentdomain.Refund
	queryErr                 error
	reads, requests, queries int
	command                  paymentport.RefundCommand
}

func (s *refundRoleStub) GetPayment(context.Context, int64) (paymentdomain.Payment, error) {
	s.reads++
	return s.payment, nil
}
func (s *refundRoleStub) RequestRefund(_ context.Context, c paymentport.RefundCommand) (paymentdomain.Refund, error) {
	s.requests++
	s.command = c
	return s.refund, nil
}
func (s *refundRoleStub) ReconcileWeChatPayRefund(context.Context, int64) (paymentdomain.Refund, error) {
	s.queries++
	return s.refund, s.queryErr
}

func TestPaymentRefundRoleRequiresRootAndExactNativeTarget(t *testing.T) {
	cfg := platformconfig.Runtime{Role: platformconfig.RolePaymentRefund, PaymentRefundID: 1244, PaymentRefundOrderID: 5626, PaymentRefundAmountMinor: 680, PaymentRefundKey: "order-5626-refund-680-v1", PaymentRefundAuthorization: "user-task-fixture"}
	for _, name := range []string{"non-root", "wrong-order", "wrong-payment", "historical", "wrong-provider", "amount-mismatch", "pending-payment", "accepted-not-confirmed", "verified-completed", "query-failed"} {
		t.Run(name, func(t *testing.T) {
			uid := 0
			stub := &refundRoleStub{payment: paymentdomain.Payment{ID: 1244, OrderID: 5626, Provider: paymentdomain.ProviderWeChatPay, Status: paymentdomain.StatusPaid, AmountMinor: 680}, refund: paymentdomain.Refund{ID: 33, AmountMinor: 680, Status: paymentdomain.RefundEffectAccepted}}
			switch name {
			case "non-root":
				uid = 501
			case "wrong-order":
				stub.payment.OrderID++
			case "wrong-payment":
				stub.payment.ID++
			case "historical":
				stub.payment.Historical = true
			case "wrong-provider":
				stub.payment.Provider = paymentdomain.ProviderAlipay
			case "amount-mismatch":
				stub.payment.AmountMinor = 679
			case "pending-payment":
				stub.payment.Status = paymentdomain.StatusAwaitingPayment
			case "verified-completed":
				stub.refund.Status = paymentdomain.RefundCompleted
			case "query-failed":
				stub.refund.Status = paymentdomain.RefundCompleted
				stub.queryErr = errors.New("provider unavailable")
			}
			receipt, err := executePaymentRefund(context.Background(), stub, cfg, uid)
			accepted := name == "accepted-not-confirmed" || name == "verified-completed" || name == "query-failed"
			if (err == nil) != accepted || stub.requests != map[bool]int{true: 1, false: 0}[accepted] {
				t.Fatalf("receipt=%+v err=%v requests=%d", receipt, err, stub.requests)
			}
			if name == "non-root" && stub.reads != 0 {
				t.Fatal("unauthorized root command reached service")
			}
			if receipt.ProviderConfirmed != (name == "verified-completed") {
				t.Fatal("accepted or failed query was presented as confirmed refund")
			}
			if accepted && (stub.command.PaymentID != 1244 || stub.command.AmountMinor != 680 || stub.command.IdempotencyKey != cfg.PaymentRefundKey || stub.command.ActorScope != "ops:root-refund:user-task-fixture") {
				t.Fatal("immutable refund authorization binding omitted")
			}
		})
	}
}
