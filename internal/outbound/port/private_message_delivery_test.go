package port

import (
	"context"
	"testing"
)

type deliveryPages struct{ calls int }

func (p *deliveryPages) GetPrivateMessageSendResult(context.Context, string, string, string) (PrivateMessageDeliveryPage, error) {
	p.calls++
	if p.calls == 1 {
		return PrivateMessageDeliveryPage{Items: []PrivateMessageDelivery{{MessageID: "other", SenderUserID: "sender", ExternalUserID: "customer"}}, NextCursor: "next"}, nil
	}
	status := 1
	return PrivateMessageDeliveryPage{Items: []PrivateMessageDelivery{{MessageID: "msg", SenderUserID: "sender", ExternalUserID: "customer", Status: &status}}}, nil
}

func TestReconcilePrivateMessageDeliveryReadsEveryPageAndMatchesRecipient(t *testing.T) {
	provider := &deliveryPages{}
	result, err := ReconcilePrivateMessageDelivery(context.Background(), provider, PrivateMessageDelivery{MessageID: "msg", SenderUserID: "sender", ExternalUserID: "customer"})
	if err != nil || result.Status == nil || *result.Status != 1 || provider.calls != 2 {
		t.Fatalf("result=%+v calls=%d err=%v", result, provider.calls, err)
	}
}
