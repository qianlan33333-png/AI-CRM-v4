package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type callbackBoundaryUOW struct{ active bool }

func (unit *callbackBoundaryUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	if unit.active {
		return errors.New("nested callback unit of work")
	}
	unit.active = true
	defer func() { unit.active = false }()
	return callback(ctx)
}

var _ platformport.UnitOfWork = (*callbackBoundaryUOW)(nil)

type callbackContactReader struct {
	unit      *callbackBoundaryUOW
	contact   wecomport.ExternalContact
	requested string
	calls     int
}

func (reader *callbackContactReader) ReadExternalContact(_ context.Context, externalUserID string) (wecomport.ExternalContact, error) {
	if reader.unit.active {
		return wecomport.ExternalContact{}, errors.New("provider detail read inside transaction")
	}
	reader.calls++
	reader.requested = externalUserID
	return reader.contact, nil
}

var _ wecomport.ExternalContactReader = (*callbackContactReader)(nil)

func TestInboxProcessorLinksCallbackUnionIDToExistingPayerRoot(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	const (
		buyerCustomerID customerdomain.CustomerID = 26
		buyerIdentityID                           = 901
		externalID                                = "external-new"
		unionID                                   = "union-buyer"
		openPlatformID                            = "open-platform-1"
	)
	identity.byKey[memoryIdentityKey("wechat-open-platform:"+openPlatformID, unionID)] = identityport.ProvisionResult{CustomerID: buyerCustomerID, IdentityID: buyerIdentityID}

	unit := &callbackBoundaryUOW{}
	reader := &callbackContactReader{unit: unit, contact: wecomport.ExternalContact{ExternalUserID: externalID, UnionID: unionID}}
	processor, relationships, entrantReceipts := callbackProcessorForIdentityTest(t, identity, unit, reader, openPlatformID, externalID)

	if count, err := processor.ProcessOnce(context.Background(), "callback-test", 1); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if reader.calls != 1 || reader.requested != externalID {
		t.Fatalf("provider detail reads=%d requested=%q", reader.calls, reader.requested)
	}
	if len(relationships.values) != 0 || len(entrantReceipts.values) != 1 || entrantReceipts.values[0].CustomerID != buyerCustomerID {
		t.Fatalf("payer root was not used: relationships=%+v entrant=%+v", relationships.values, entrantReceipts.values)
	}
	if _, found := identity.byKey[memoryIdentityKey("wecom-corp:corp-1", externalID)]; !found {
		t.Fatal("verified external contact identity was not attached to payer root")
	} else if got := identity.byKey[memoryIdentityKey("wecom-corp:corp-1", externalID)].CustomerID; got != buyerCustomerID {
		t.Fatalf("external identity root=%d want payer root=%d", got, buyerCustomerID)
	}
}

func TestInboxProcessorCrossRootUnionIDCandidateDoesNotActivateRelationship(t *testing.T) {
	identity := newMemoryLifecycleIdentity()
	const (
		wecomCustomerID customerdomain.CustomerID = 6
		payerCustomerID customerdomain.CustomerID = 26
		externalID                                = "external-existing"
		unionID                                   = "union-already-owned"
		openPlatformID                            = "open-platform-1"
	)
	identity.byKey[memoryIdentityKey("wecom-corp:corp-1", externalID)] = identityport.ProvisionResult{CustomerID: wecomCustomerID, IdentityID: 61}
	identity.byKey[memoryIdentityKey("wechat-open-platform:"+openPlatformID, unionID)] = identityport.ProvisionResult{CustomerID: payerCustomerID, IdentityID: 261}

	unit := &callbackBoundaryUOW{}
	reader := &callbackContactReader{unit: unit, contact: wecomport.ExternalContact{ExternalUserID: externalID, UnionID: unionID}}
	processor, relationships, entrantReceipts := callbackProcessorForIdentityTest(t, identity, unit, reader, openPlatformID, externalID)
	if count, err := processor.ProcessOnce(context.Background(), "callback-test", 1); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if relationships.active("corp-1", "employee-1", wecomCustomerID) || relationships.active("corp-1", "employee-1", payerCustomerID) {
		t.Fatalf("cross-root candidate activated a relationship: %+v", relationships.values)
	}
	if len(entrantReceipts.values) != 1 || entrantReceipts.values[0].Status != "identity_conflict" {
		t.Fatalf("entrant conflict receipt=%+v", entrantReceipts.values)
	}
	if identity.byKey[memoryIdentityKey("wecom-corp:corp-1", externalID)].CustomerID != wecomCustomerID ||
		identity.byKey[memoryIdentityKey("wechat-open-platform:"+openPlatformID, unionID)].CustomerID != payerCustomerID {
		t.Fatal("cross-root identities were moved without explicit merge confirmation")
	}
}

func callbackProcessorForIdentityTest(t *testing.T, identity *memoryLifecycleIdentity, unit *callbackBoundaryUOW, reader *callbackContactReader, openPlatformID, externalID string) (InboxProcessor, *lifecycleRelationships, *lifecycleReceipts) {
	t.Helper()
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(CallbackEvent{
		CorpID: "corp-1", ToUserName: "corp-1", MsgType: "event", Event: "change_external_contact",
		ChangeType: ChangeAddExternalContact, ExternalUserID: externalID, UserID: "employee-1",
		CreateTime: 1788336000,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := idempotency.Parse("wecom:identity-callback:test-0001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.Ingest(context.Background(), webhook.Ingest{Provider: callbackProvider, IdempotencyKey: key, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	relationships := &lifecycleRelationships{}
	entrantReceipts := &lifecycleReceipts{}
	auditService, err := audit.NewService(&memoryAuditStore{})
	if err != nil {
		t.Fatal(err)
	}
	return InboxProcessor{
		Enabled: true, CorpID: "corp-1", Inbox: inbox, UOW: unit, Directory: reader,
		UnionIDOpenPlatformID: openPlatformID,
		Lifecycle:             lifecycleFor(identity, relationships, &lifecycleStates{}, entrantReceipts),
		Receipts:              &memoryCallbackReceipts{}, Audit: auditService,
	}, relationships, entrantReceipts
}

var _ identityport.VerifiedIdentityLinker = (*memoryLifecycleIdentity)(nil)
