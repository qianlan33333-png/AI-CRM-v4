package main

import (
	"context"
	"encoding/json"
	"errors"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"strings"
	"testing"
	"time"
)

type receiptStoreFixture struct {
	value outbound.PrivateMessageDelivery
}

func (s receiptStoreFixture) PrivateMessageReceipt(context.Context, string) (outbound.PrivateMessageDelivery, bool, error) {
	return s.value, true, nil
}

type receiptProviderFixture struct {
	calls int
	pages []outbound.PrivateMessageDeliveryPage
	err   error
}

func (p *receiptProviderFixture) GetPrivateMessageSendResult(_ context.Context, msg, sender, cursor string) (outbound.PrivateMessageDeliveryPage, error) {
	if msg != "msg-original" || sender != "HuangYouCan" {
		panic("read did not use frozen original task")
	}
	page := p.pages[p.calls]
	p.calls++
	return page, p.err
}
func TestAIExecutionReceiptReadPreservesCreationEvidenceAndMatchesAllPages(t *testing.T) {
	now := time.Now().UTC()
	zero := int64(0)
	status := 1
	receipt := outbound.PrivateMessageDelivery{MessageID: "msg-original", SenderUserID: "HuangYouCan", ExternalUserID: "PRIVATE_TARGET", Reason: "PRIVATE_FAILURE_PAYLOAD", ObservedAt: now}
	provider := &receiptProviderFixture{pages: []outbound.PrivateMessageDeliveryPage{{HTTPStatusCode: 200, ProviderErrorCode: &zero, NextCursor: "next"}, {HTTPStatusCode: 200, ProviderErrorCode: &zero, Items: []outbound.PrivateMessageDelivery{{MessageID: receipt.MessageID, SenderUserID: receipt.SenderUserID, ExternalUserID: receipt.ExternalUserID, Status: &status, SentAt: &now, ObservedAt: now}}}}}
	adapter := aiExecutionReceiptAdapter{store: receiptStoreFixture{receipt}, provider: provider}
	local, err := adapter.ReadExecutionReceipt(context.Background(), "ref", false)
	if err != nil || provider.calls != 0 || local.CreationErrorCode != nil {
		t.Fatalf("local=%+v err=%v", local, err)
	}
	actual, err := adapter.ReadExecutionReceipt(context.Background(), "ref", true)
	if err != nil || provider.calls != 2 || actual.QueryState != "observed" || actual.ProviderStatus == nil || *actual.ProviderStatus != 1 || actual.CreationErrorCode != nil || actual.QueryErrorCode == nil || *actual.QueryErrorCode != 0 {
		t.Fatalf("actual=%+v err=%v", actual, err)
	}
	raw, _ := json.Marshal(actual)
	if strings.Contains(string(raw), "PRIVATE_TARGET") || strings.Contains(string(raw), "PRIVATE_FAILURE_PAYLOAD") {
		t.Fatal("Provider target identity leaked")
	}
	if receipt.Status != nil {
		t.Fatal("read modified local evidence")
	}
}
func TestAIExecutionReceiptReadRejectsDuplicateAndProviderFailure(t *testing.T) {
	zero, denied, status := int64(0), int64(48002), 0
	row := outbound.PrivateMessageDelivery{MessageID: "msg-original", SenderUserID: "HuangYouCan", ExternalUserID: "PRIVATE_TARGET", Status: &status}
	for _, tc := range []struct {
		pages   []outbound.PrivateMessageDeliveryPage
		err     error
		failure string
	}{{[]outbound.PrivateMessageDeliveryPage{{HTTPStatusCode: 200, ProviderErrorCode: &zero, Items: []outbound.PrivateMessageDelivery{row, row}}}, nil, "receipt_not_verified"}, {[]outbound.PrivateMessageDeliveryPage{{HTTPStatusCode: 200, ProviderErrorCode: &denied}}, errors.New("SECRET error must not be exposed"), "wecom_errcode_48002"}, {[]outbound.PrivateMessageDeliveryPage{{HTTPStatusCode: 200, ProviderErrorCode: &zero}}, nil, "receipt_not_verified"}} {
		adapter := aiExecutionReceiptAdapter{store: receiptStoreFixture{row}, provider: &receiptProviderFixture{pages: tc.pages, err: tc.err}}
		value, err := adapter.ReadExecutionReceipt(context.Background(), "ref", true)
		if err != nil || value.QueryState != "failed" || value.ProviderStatus != nil || value.QueryFailure != tc.failure {
			t.Fatalf("value=%+v err=%v", value, err)
		}
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatal("raw failure leaked")
		}
	}
}
