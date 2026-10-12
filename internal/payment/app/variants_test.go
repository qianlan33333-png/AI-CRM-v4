package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestVariantCheckoutPriceSelectionAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name        string
		id, version int64
		enabled     bool
		empty       bool
		ok          bool
	}{{"valid", 21, 1, true, false, true}, {"wrong-parent", 22, 1, true, false, false}, {"missing", 0, 0, true, false, false}, {"outdated", 21, 2, true, false, false}, {"removed", 21, 1, true, true, false}, {"mode-disabled", 21, 1, false, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &variantReceiptStore{storeStub: &storeStub{}}
			orders := &checkoutOrderStub{}
			effects := &effectStub{}
			products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "课程", PriceMinor: 1, Currency: "CNY", Version: 9, VariantsEnabled: tc.enabled, Variants: []productport.Variant{{ID: 21, Version: 1, Name: "三人成行版", PriceMinor: 99900}}}}
			if tc.empty {
				products.product.Variants = nil
			}
			sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}
			service := NewService(uowStub{}, store, orders, sessions, effects)
			service.SetCheckoutProductReader(products)
			token := "pays_session_token_variant_005"
			command := paymentport.CreateCommand{ProductID: 5, ProductType: "standard", VariantID: tc.id, VariantVersion: tc.version, BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf, SessionToken: token, CheckoutSessionBinding: paymentport.CheckoutSessionBinding(token), ActorScope: "public-checkout", IdempotencyKey: "variant-checkout-key-0005"}
			first, err := service.Create(context.Background(), command)
			if !tc.ok {
				if !errors.Is(err, productport.ErrVariantSelection) || orders.command.ProductID != 0 || store.payment.ID != 0 {
					t.Fatalf("invalid selection created effects: %+v %v", orders.command, err)
				}
				return
			}
			if err != nil || first.AmountMinor != 99900 || orders.command.Variant == nil || orders.command.Variant.Name != "三人成行版" || orders.command.ProductID != 5 {
				t.Fatal(first, orders.command, err)
			}
			store.payment.MerchantOrderNo = orders.command.MerchantOrderNo
			products.product.Variants = nil
			products.product.VariantsEnabled = false
			replay, err := service.Create(context.Background(), command)
			if err != nil || replay.ID != first.ID || products.calls != 1 {
				t.Fatal("revalidated replay", replay, err)
			}
			command.VariantID = 99
			if _, err = service.Create(context.Background(), command); !errors.Is(err, paymentport.ErrConflict) {
				t.Fatalf("same key changed variant=%v", err)
			}
		})
	}
	raw, _ := json.Marshal(paymentport.CreateCommand{ProductID: 5})
	if strings.Contains(string(raw), "Variant") {
		t.Fatal("changed legacy digest format", string(raw))
	}
}

type variantReceiptStore struct {
	*storeStub
	digest [32]byte
}

func (s *variantReceiptStore) CreatePayment(ctx context.Context, p domain.Payment, k, d [32]byte, scope string) (domain.Payment, bool, error) {
	s.digest = d
	return s.storeStub.CreatePayment(ctx, p, k, d, scope)
}
func (s *variantReceiptStore) ReplayPayment(ctx context.Context, k, d [32]byte, scope string) (domain.Payment, bool, error) {
	if s.payment.ID > 0 && d != s.digest {
		return domain.Payment{}, false, paymentport.ErrConflict
	}
	return s.storeStub.ReplayPayment(ctx, k, d, scope)
}
