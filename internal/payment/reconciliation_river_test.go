package payment

import (
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestReconciliationMaxAttempts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target paymentport.ReconciliationTarget
		want   int
	}{
		{
			name:   "wechat payment",
			target: paymentport.ReconciliationTarget{Provider: domain.ProviderWeChatPay, PaymentID: 1},
			want:   weChatPayPaymentReconciliationMaxAttempts,
		},
		{
			name:   "wechat refund",
			target: paymentport.ReconciliationTarget{Provider: domain.ProviderWeChatPay, RefundID: 1},
			want:   defaultReconciliationMaxAttempts,
		},
		{
			name:   "wechat shop refund",
			target: paymentport.ReconciliationTarget{Provider: domain.ProviderWeChatShop, RefundID: 1},
			want:   defaultReconciliationMaxAttempts,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := reconciliationMaxAttempts(test.target); got != test.want {
				t.Fatalf("reconciliationMaxAttempts() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestWeChatPayPaymentReconciliationRetryWindowCoversOneDay(t *testing.T) {
	t.Parallel()

	// River v0.24's default retry policy delays a retry after failed attempt n
	// by n^4 seconds with a +/-10% jitter. InsertOpts.MaxAttempts includes the
	// first execution, so the last retry before attempt 16 follows attempt 15.
	var unjitteredRetrySeconds int64
	for attempt := int64(1); attempt < weChatPayPaymentReconciliationMaxAttempts; attempt++ {
		unjitteredRetrySeconds += attempt * attempt * attempt * attempt
	}
	minimumRetryWindow := time.Duration(9*unjitteredRetrySeconds/10) * time.Second
	if minimumRetryWindow < 24*time.Hour {
		t.Fatalf("minimum retry window = %s, want at least 24h", minimumRetryWindow)
	}
	t.Logf("River v0.24 minimum retry window for %d max attempts: %s", weChatPayPaymentReconciliationMaxAttempts, minimumRetryWindow)
}
