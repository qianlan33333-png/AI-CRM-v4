package store

import (
	"context"
	"testing"
	"time"

	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestCreatePlanRequiresTransaction(t *testing.T) {
	now := time.Now().UTC()
	aggregate, err := aiassistantdomain.NewPlan("review", "automation", effectport.Hash("source"), 1, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &Repository{}
	_, _, err = repository.CreatePlan(context.Background(), aggregate, []aiassistantport.RecipientCandidate{{CustomerID: customerdomain.CustomerID(1), StaffID: 2, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}}}, 7, now)
	if err != platformpostgres.ErrTransactionNeeded {
		t.Fatalf("err=%v want transaction required", err)
	}
}
