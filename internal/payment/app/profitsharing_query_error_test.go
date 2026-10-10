package app

import (
	"errors"
	"fmt"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"strings"
	"testing"
)

func TestProfitSharingQueryFailurePreservesOnlySafeCurrentQueryCode(t *testing.T) {
	err := profitSharingReconciliationError(fmt.Errorf("unsafe wrapper secret-account: %w", paymentport.NewProfitSharingQueryError("RESOURCE_NOT_EXISTS")))
	if paymentport.ProfitSharingQueryCode(err) != "RESOURCE_NOT_EXISTS" || strings.Contains(err.Error(), "secret-account") || !errors.Is(err, paymentport.ErrUnavailable) {
		t.Fatal("safe query diagnostic was lost or raw wrapper retained")
	}
	unknown := profitSharingReconciliationError(errors.New("raw provider secret-token"))
	if unknown != paymentport.ErrUnavailable {
		t.Fatal("raw provider error escaped")
	}
	if code := paymentport.NewProfitSharingQueryError("unknown-code-secret-account").ProviderCode(); code != "UNKNOWN" {
		t.Fatalf("unapproved code=%q", code)
	}
}
