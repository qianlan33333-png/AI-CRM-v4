package outbound

import (
	"context"
	port "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"
	"time"
)

type controlPages struct {
	pages []port.PrivateMessageDeliveryPage
	calls int
}

func (p *controlPages) GetPrivateMessageSendResult(context.Context, string, string, string) (port.PrivateMessageDeliveryPage, error) {
	p.calls++
	return p.pages[p.calls-1], nil
}
func TestPrivateMessageControlRejectsSharedOrMismatchedTask(t *testing.T) {
	status := 0
	original := port.PrivateMessageDelivery{MessageID: "original", SenderUserID: "sender", ExternalUserID: "target", Status: &status}
	other := original
	other.ExternalUserID = "other"
	for _, items := range [][]port.PrivateMessageDelivery{{other}, {original, other}, {original, original}} {
		p := &controlPages{pages: []port.PrivateMessageDeliveryPage{{Items: items}}}
		if _, err := readSingleControlTarget(context.Background(), p, original); err == nil {
			t.Fatal("shared or mismatched task was allowed")
		}
	}
}
func TestPrivateMessageControlReadsAllPagesAndPreservesSentTime(t *testing.T) {
	status := 1
	sent := time.Now().Add(-time.Hour).UTC()
	original := port.PrivateMessageDelivery{MessageID: "original", SenderUserID: "sender", ExternalUserID: "target", Status: &status, SentAt: &sent}
	p := &controlPages{pages: []port.PrivateMessageDeliveryPage{{NextCursor: "next"}, {Items: []port.PrivateMessageDelivery{original}}}}
	observed, err := readSingleControlTarget(context.Background(), p, original)
	if err != nil || p.calls != 2 || observed.SentAt == nil || !observed.SentAt.Equal(sent) {
		t.Fatalf("calls=%d observed=%+v err=%v", p.calls, observed, err)
	}
	pending := 0
	regressed := original
	regressed.Status = &pending
	regressed.SentAt = nil
	p = &controlPages{pages: []port.PrivateMessageDeliveryPage{{Items: []port.PrivateMessageDelivery{regressed}}}}
	if _, err = readSingleControlTarget(context.Background(), p, original); err == nil {
		t.Fatal("known delivered fact regressed")
	}
}
