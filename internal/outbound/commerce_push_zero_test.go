package outbound

import (
	"context"
	"encoding/json"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"testing"
	"time"
)

func TestCommerceFreePayloadKeepsShapeWithoutFabricatingPayment(t *testing.T) {
	customer, product := int64(1), int64(2)
	service := &CommercePushService{identities: commercePushIdentityStub{identitydomain.KindPhone: "13900000000"}}
	event := orderport.PaidEvent{ID: 1, OrderID: 2, DomainEventOutboxID: 3, OccurredAt: time.Now(), Order: orderdomain.Snapshot{Provider: orderdomain.ProviderNoPayment, MerchantOrderNo: "free-local", PayerCustomerID: &customer, BeneficiaryCustomerID: &customer, Amount: orderdomain.Money{Currency: "CNY"}}}
	item := orderdomain.ItemSnapshot{ProductID: &product, ProductCode: "free", ProductName: "免费商品"}
	target := CommercePushTarget{BuyerPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}, BeneficiaryPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}}
	raw, missing, err := service.paidPayload(context.Background(), event, item, target, "free-delivery")
	if err != nil || missing {
		t.Fatal(missing, err)
	}
	var body struct {
		Event string `json:"event"`
		Order struct {
			Channel string `json:"pay_channel"`
			Amount  int64  `json:"paid_amount"`
			PaidAt  string `json:"paid_at"`
		} `json:"order"`
		Transaction struct {
			ID    string `json:"transaction_id"`
			State string `json:"trade_state"`
			At    string `json:"success_time"`
		} `json:"transaction"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Event != "transaction.paid" || body.Order.Channel != "" || body.Order.Amount != 0 || body.Order.PaidAt != "" || body.Transaction.ID != "" || body.Transaction.State != "" || body.Transaction.At != "" {
		t.Fatalf("fabricated payment facts: %s", raw)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"order", "product", "buyer", "transaction", "delivery_id", "domain_event_outbox_id"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("existing field %s missing", key)
		}
	}
}
