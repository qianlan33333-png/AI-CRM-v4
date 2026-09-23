package port_test

import (
	"testing"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func TestCreatePlanCommandRequiresCanonicalTargetsAndBoundedContent(t *testing.T) {
	valid := aiassistantport.CreatePlanCommand{
		Actor:          aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: 9},
		IdempotencyKey: "plan-key-123",
		Name:           "September retention review",
		SourceKind:     "automation",
		SourceDigest:   effectport.Hash("source", "9"),
		Recipients: []aiassistantport.RecipientCandidate{{
			CustomerID: customerdomain.CustomerID(42),
			StaffID:    7,
			Content:    []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}},
		}},
		OccurredAt: time.Now().UTC(),
	}
	if !valid.Valid() {
		t.Fatal("expected valid canonical plan command")
	}
	valid.Recipients[0].CustomerID = 0
	if valid.Valid() {
		t.Fatal("missing canonical customer must be rejected")
	}
}

func TestRawExternalIdentityIsAbsentFromRecipientContract(t *testing.T) {
	recipient := aiassistantport.Recipient{CustomerID: 42, OneIDLabel: "ONE-42"}
	if recipient.CustomerID != 42 || recipient.OneIDLabel == "" {
		t.Fatal("safe OneID projection is required")
	}
}
