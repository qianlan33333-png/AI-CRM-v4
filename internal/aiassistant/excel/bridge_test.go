package excel

import (
	"context"
	"errors"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"
	"time"
)

type pages struct {
	values map[string]outbound.PrivateMessageDeliveryPage
	errAt  string
}

func (p pages) GetPrivateMessageSendResult(_ context.Context, msg, sender, cursor string) (outbound.PrivateMessageDeliveryPage, error) {
	if cursor == p.errAt {
		return outbound.PrivateMessageDeliveryPage{}, errors.New("source failure")
	}
	return p.values[cursor], nil
}
func TestReceiptFullPaginationAndExactTarget(t *testing.T) {
	status := 1
	now := time.Now()
	target := outbound.PrivateMessageDelivery{MessageID: "m", SenderUserID: "s", ExternalUserID: "e", Status: &status, SentAt: &now}
	other := target
	other.ExternalUserID = "other"
	b := Bridge{Provider: pages{errAt: "never", values: map[string]outbound.PrivateMessageDeliveryPage{"": {Items: []outbound.PrivateMessageDelivery{other}, NextCursor: "2"}, "2": {Items: []outbound.PrivateMessageDelivery{target}}}}}
	got, err := b.pollReceipt(context.Background(), target)
	if err != nil || got.ExternalUserID != "e" {
		t.Fatalf("full result: %v", err)
	}
	for name, p := range map[string]pages{
		"later page fails": {errAt: "2", values: map[string]outbound.PrivateMessageDeliveryPage{"": {Items: []outbound.PrivateMessageDelivery{target}, NextCursor: "2"}}},
		"missing":          {errAt: "never", values: map[string]outbound.PrivateMessageDeliveryPage{"": {Items: []outbound.PrivateMessageDelivery{other}}}},
		"duplicate":        {errAt: "never", values: map[string]outbound.PrivateMessageDeliveryPage{"": {Items: []outbound.PrivateMessageDelivery{target, target}}}},
		"cursor cycle":     {errAt: "never", values: map[string]outbound.PrivateMessageDeliveryPage{"": {NextCursor: "x"}, "x": {NextCursor: "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			b.Provider = p
			if _, err := b.pollReceipt(context.Background(), target); err == nil {
				t.Fatal("partial/ambiguous read became delivery proof")
			}
		})
	}
}
