package app

import (
	"strings"
	"testing"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
)

func TestPromotionTokenIssuerBindsCommandAndRejectsInvalidDataKey(t *testing.T) {
	issuer, err := newPromotionTokenIssuer("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY")
	if err != nil {
		t.Fatal(err)
	}
	first, err := issuer.issue("customer:17", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "dpc_") || len(first) != 47 {
		t.Fatalf("token=%q", first)
	}
	replay, err := issuer.issue("customer:17", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001")
	if err != nil || replay != first {
		t.Fatalf("replay=%q err=%v want=%q", replay, err, first)
	}
	rotated, err := newPromotionTokenIssuer("eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg")
	if err != nil {
		t.Fatal(err)
	}
	rotatedReplay, err := rotated.issue("customer:17", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001")
	if err != nil || rotatedReplay == first {
		t.Fatalf("rotated token=%q err=%v original=%q", rotatedReplay, err, first)
	}
	for _, command := range []struct {
		actorScope string
		productID  int64
		product    distributiondomain.ProductType
		key        string
	}{
		{"customer:18", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001"},
		{"customer:17", 9, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001"},
		{"customer:17", 8, distributiondomain.ProductTypeServicePeriod, "credential-idempotency-key-0001"},
		{"customer:17", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0002"},
	} {
		other, issueErr := issuer.issue(command.actorScope, command.productID, command.product, command.key)
		if issueErr != nil || other == first {
			t.Fatalf("input=%+v token=%q err=%v", command, other, issueErr)
		}
	}
	if _, err = newPromotionTokenIssuer("not-base64"); err == nil {
		t.Fatal("invalid data key was accepted")
	}
	if _, err = issuer.issue("", 8, distributiondomain.ProductTypeStandard, "credential-idempotency-key-0001"); err == nil {
		t.Fatal("invalid token input was accepted")
	}
}
