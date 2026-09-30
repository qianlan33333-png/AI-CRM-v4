package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type unionLinkSpy struct {
	commands []identityport.VerifiedLinkCommand
	status   string
}

func (s *unionLinkSpy) LinkVerifiedIdentityToCustomer(_ context.Context, c identityport.VerifiedLinkCommand) (identityport.VerifiedLinkResult, error) {
	s.commands = append(s.commands, c)
	return identityport.VerifiedLinkResult{Status: s.status}, nil
}

func TestContactUnionIDLinkerVerifiesSourceAndPreservesMissing(t *testing.T) {
	spy := &unionLinkSpy{status: "attached"}
	linker := ContactUnionIDLinker{Scope: "wechat-open-platform:wx-test", Identity: spy}
	contact := wecomport.ExternalContact{ExternalUserID: "external-1"}
	status, err := linker.Link(context.Background(), 7, "external-1", contact, "wecom.directory_sync", 42)
	if err != nil || status != "missing" || len(spy.commands) != 0 {
		t.Fatalf("missing status=%q err=%v calls=%d", status, err, len(spy.commands))
	}
	contact.UnionID = "union-1"
	status, err = linker.Link(context.Background(), 7, "external-1", contact, "wecom.directory_sync", 42)
	if err != nil || status != "attached" || len(spy.commands) != 1 {
		t.Fatalf("attached status=%q err=%v calls=%d", status, err, len(spy.commands))
	}
	command := spy.commands[0]
	ref := command.Fact.Reference()
	if command.CustomerID != 7 || ref.Kind != identitydomain.KindUnionID || ref.Scope != linker.Scope || ref.NormalizedValue != "union-1" || ref.Source != "wecom.directory_sync" || command.Evidence.Strength != identitydomain.EvidenceStrong || command.Evidence.Digest == "" || command.Evidence.EventID != "42" {
		t.Fatalf("link command=%+v reference=%+v", command, ref)
	}
	contact.ExternalUserID = "other"
	if _, err = linker.Link(context.Background(), 7, "external-1", contact, "wecom.directory_sync", 42); err == nil || len(spy.commands) != 1 {
		t.Fatalf("mismatched provider contact accepted: %v", err)
	}
	spy.status = "merge_candidate"
	contact.ExternalUserID = "external-1"
	if status, err = linker.Link(context.Background(), 7, "external-1", contact, "wecom.directory_sync", 42); err != nil || status != "merge_candidate" {
		t.Fatalf("candidate status=%q err=%v", status, err)
	}
}

type unionContactReader struct {
	contact wecomport.ExternalContact
	err     error
	calls   int
}

func (reader *unionContactReader) ReadExternalContact(_ context.Context, _ string) (wecomport.ExternalContact, error) {
	reader.calls++
	return reader.contact, reader.err
}

func TestContactUnionIDCallbackReadsDetailAfterProcessedAdd(t *testing.T) {
	event := CallbackEvent{CorpID: "corp-1", MsgType: "event", Event: "change_external_contact", ChangeType: ChangeAddExternalContact, ExternalUserID: "external-1", UserID: "employee-1"}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	key, err := idempotency.Parse("wecom-unionid-callback-test-0001")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := webhook.NewService(descriptionCallbackInboxStore{delivery: webhook.Delivery{ID: 88, Provider: callbackProvider, IdempotencyKey: key, Payload: payload, Status: webhook.StatusProcessed}})
	if err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(19)
	resolver := &descriptionCallbackIdentity{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerID}}

	spy := &unionLinkSpy{status: "attached"}
	reader := &unionContactReader{contact: wecomport.ExternalContact{ExternalUserID: "external-1", UnionID: "union-1"}}
	service := ContactUnionIDCallbackService{Enabled: true, CorpID: "corp-1", Inbox: inbox, Provider: reader, Resolver: resolver,
		UnionIDs: ContactUnionIDLinker{Scope: "wechat-open-platform:wx-test", Identity: spy}, UOW: directUOW{}}
	if err = service.Process(context.Background(), 88); err != nil || reader.calls != 1 || len(spy.commands) != 1 || spy.commands[0].Fact.Reference().Source != "wecom.callback_detail" {
		t.Fatalf("processed err=%v reads=%d links=%d", err, reader.calls, len(spy.commands))
	}
	reader.contact.UnionID = ""
	if err = service.Process(context.Background(), 88); err != nil || len(spy.commands) != 1 {
		t.Fatalf("missing provider unionid err=%v links=%d", err, len(spy.commands))
	}
	reader.contact.UnionID = "union-1"
	spy.status = "already_linked"
	if err = service.Process(context.Background(), 88); err != nil || len(spy.commands) != 2 {
		t.Fatalf("replayed detail err=%v links=%d", err, len(spy.commands))
	}
	spy.status = "merge_candidate"
	if err = service.Process(context.Background(), 88); err != nil || len(spy.commands) != 3 {
		t.Fatalf("cross-root detail err=%v links=%d", err, len(spy.commands))
	}
	reader.err = errors.New("provider unavailable")
	if err = service.Process(context.Background(), 88); err == nil || len(spy.commands) != 3 {
		t.Fatalf("provider error err=%v links=%d", err, len(spy.commands))
	}
}
