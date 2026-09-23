package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestFakeAlipayAdapterEmitsFixedScopedVerifiedFact(t *testing.T) {
	adapter, err := NewFakeAlipayAdapter("2021001234567890", "2088123412345678")
	if err != nil {
		t.Fatal(err)
	}

	fact, err := adapter.Verify(context.Background(), IdentityRequest{Value: "2088123412345678"})
	if err != nil {
		t.Fatal(err)
	}
	if !fact.Valid() {
		t.Fatal("fake adapter returned an invalid verified fact")
	}
	ref := fact.Reference()
	if ref.Kind != identitydomain.KindAlipayUserID {
		t.Fatalf("kind=%q, want %q", ref.Kind, identitydomain.KindAlipayUserID)
	}
	if ref.Scope != "alipay-app:2021001234567890" {
		t.Fatalf("scope=%q", ref.Scope)
	}
	if ref.NormalizedValue != "2088123412345678" {
		t.Fatalf("value=%q", ref.NormalizedValue)
	}
	if ref.Assurance != identitydomain.AssuranceVerified || ref.Source != SourceAlipay {
		t.Fatalf("assurance/source=%q/%q", ref.Assurance, ref.Source)
	}
}

func TestFakeAlipayAdapterRejectsUnverifiedIDsWithoutProducingFact(t *testing.T) {
	adapter, err := NewFakeAlipayAdapter("2021001234567890", "2088123412345678")
	if err != nil {
		t.Fatal(err)
	}

	fact, err := adapter.Verify(context.Background(), IdentityRequest{Value: "2088999999999999"})
	if !errors.Is(err, ErrIdentityNotVerified) {
		t.Fatalf("error=%v, want ErrIdentityNotVerified", err)
	}
	if fact.Valid() {
		t.Fatal("unverified identity must not produce a usable fact")
	}
}

func TestIdentityRequestCannotSelectVerificationMetadata(t *testing.T) {
	typeOfRequest := reflect.TypeOf(IdentityRequest{})
	for _, field := range []string{"Kind", "Scope", "Assurance", "Source"} {
		if _, found := typeOfRequest.FieldByName(field); found {
			t.Fatalf("IdentityRequest must not expose caller-selected %s", field)
		}
	}
}

func TestFakeAlipayAdapterValidatesAppIDAndProviderValues(t *testing.T) {
	for _, appID := range []string{"", " app", "app id", "alipay-app:other", "alipay/app"} {
		if _, err := NewFakeAlipayAdapter(appID, "user-1"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("appID %q error=%v, want ErrInvalidRequest", appID, err)
		}
	}
	for _, userID := range []string{"", " user", "user id", "user\n"} {
		if _, err := NewFakeAlipayAdapter("2021001234567890", userID); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("userID %q error=%v, want ErrInvalidRequest", userID, err)
		}
	}
}

func TestFakeAlipayAdapterHonorsCancellationBeforeVerification(t *testing.T) {
	adapter, err := NewFakeAlipayAdapter("2021001234567890", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fact, err := adapter.Verify(ctx, IdentityRequest{Value: "user-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
	if fact.Valid() {
		t.Fatal("canceled verification must not produce a fact")
	}
}
