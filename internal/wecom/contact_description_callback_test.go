package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type descriptionCallbackInboxStore struct{ delivery webhook.Delivery }

func (store descriptionCallbackInboxStore) PutIfAbsent(context.Context, webhook.Delivery) (webhook.Delivery, bool, error) {
	return webhook.Delivery{}, false, errors.New("unexpected inbox write")
}

func (store descriptionCallbackInboxStore) Claim(context.Context, webhook.Claim) ([]webhook.Delivery, error) {
	return nil, errors.New("unexpected inbox claim")
}

func (store descriptionCallbackInboxStore) Complete(context.Context, webhook.Completion) (webhook.Delivery, error) {
	return webhook.Delivery{}, errors.New("unexpected inbox completion")
}

func (store descriptionCallbackInboxStore) Get(_ context.Context, id int64) (webhook.Delivery, error) {
	if id != store.delivery.ID {
		return webhook.Delivery{}, webhook.ErrInvalidDelivery
	}
	return store.delivery, nil
}

type descriptionCallbackIdentity struct {
	result identityport.ResolveResult
	ref    identitydomain.Reference
}

func (identity *descriptionCallbackIdentity) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	identity.ref = reference
	return identity.result, nil
}

type descriptionCallbackReader struct {
	target         wecomport.ExternalContactDescriptionTarget
	err            error
	calls          int
	externalUserID string
	employeeUserID string
}

func (reader *descriptionCallbackReader) ReadExternalContactDescriptionTarget(_ context.Context, externalUserID, employeeUserID string) (wecomport.ExternalContactDescriptionTarget, error) {
	reader.calls++
	reader.externalUserID = externalUserID
	reader.employeeUserID = employeeUserID
	return reader.target, reader.err
}

type descriptionCallbackIntentWriter struct {
	commands []outboundport.ContactDescriptionIntentCommand
	err      error
}

type descriptionCallbackJobSpy struct {
	calls   int
	inboxID int64
}

func (spy *descriptionCallbackJobSpy) EnqueueContactDescriptionObservation(_ context.Context, inboxID int64) error {
	spy.calls++
	spy.inboxID = inboxID
	return nil
}

func (writer *descriptionCallbackIntentWriter) WriteContactDescriptionIntentWithin(_ context.Context, command outboundport.ContactDescriptionIntentCommand) (outboundport.ContactDescriptionIntentResult, error) {
	writer.commands = append(writer.commands, command)
	return outboundport.ContactDescriptionIntentResult{IntentID: 1, EffectID: "eer_1"}, writer.err
}

func TestContactDescriptionCallbackWorkerReadsOnlyOneProcessedFullContact(t *testing.T) {
	description := "existing manual text"
	event := CallbackEvent{CorpID: "corp-1", MsgType: "event", Event: "change_external_contact", ChangeType: ChangeAddExternalContact, ExternalUserID: "external-1", UserID: "employee-1"}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	key, err := idempotency.Parse("wecom-description-callback-test-0001")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := webhook.NewService(descriptionCallbackInboxStore{delivery: webhook.Delivery{ID: 44, Provider: callbackProvider, IdempotencyKey: key, Payload: payload, Status: webhook.StatusProcessed}})
	if err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(19)
	relationships := &memoryRelationships{active: map[string]bool{relationshipKey("corp-1", "employee-1", customerID): true}}
	identity := &descriptionCallbackIdentity{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerID, IdentityID: 5}}
	reader := &descriptionCallbackReader{target: wecomport.ExternalContactDescriptionTarget{Description: description, Projected: true}}
	intents := &descriptionCallbackIntentWriter{}
	service := ContactDescriptionCallbackService{Enabled: true, CorpID: "corp-1", Inbox: inbox, Provider: reader, Identity: identity, Relationships: relationships, Intents: intents, UOW: directUOW{}}
	worker := NewContactDescriptionCallbackWorker()
	if err = worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(context.Background(), &river.Job[ContactDescriptionCallbackJobArgs]{JobRow: &rivertype.JobRow{}, Args: ContactDescriptionCallbackJobArgs{InboxID: 44}}); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || reader.externalUserID != "external-1" || reader.employeeUserID != "employee-1" || len(intents.commands) != 1 {
		t.Fatalf("reader=%+v intents=%+v", reader, intents.commands)
	}
	command := intents.commands[0]
	if command.Operation != outboundport.ContactDescriptionOperationWrite || command.Replan || command.SourceRunID != 0 || command.CustomerID != customerID || identity.ref.Kind != identitydomain.KindWeComExternalUserID || identity.ref.Scope != "wecom-corp:corp-1" || identity.ref.Assurance != identitydomain.AssuranceVerified {
		t.Fatalf("command=%+v identity_ref=%+v", command, identity.ref)
	}
	if command.ObservedDescriptionDigest != outboundport.ContactDescriptionObservedDigest(description) {
		t.Fatalf("observed=%s", command.ObservedDescriptionDigest)
	}
}

func TestContactDescriptionCallbackWorkerSkipsNonFullAndNeverReplansUnknown(t *testing.T) {
	tests := []struct {
		name       string
		changeType string
		intentErr  error
		wantReads  int
		wantWrites int
	}{
		{name: "half contact", changeType: ChangeAddHalfExternalContact},
		{name: "unknown previous write", changeType: ChangeAddExternalContact, intentErr: outboundport.ErrContactDescriptionOutcomeUnknown, wantReads: 1, wantWrites: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			description := "manual"
			event := CallbackEvent{CorpID: "corp-1", MsgType: "event", Event: "change_external_contact", ChangeType: testCase.changeType, ExternalUserID: "external-1", UserID: "employee-1"}
			payload, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			key, err := idempotency.Parse("wecom-description-callback-test-0002")
			if err != nil {
				t.Fatal(err)
			}
			inbox, err := webhook.NewService(descriptionCallbackInboxStore{delivery: webhook.Delivery{ID: 45, Provider: callbackProvider, IdempotencyKey: key, Payload: payload, Status: webhook.StatusProcessed}})
			if err != nil {
				t.Fatal(err)
			}
			customerID := customerdomain.CustomerID(19)
			relationships := &memoryRelationships{active: map[string]bool{relationshipKey("corp-1", "employee-1", customerID): true}}
			reader := &descriptionCallbackReader{target: wecomport.ExternalContactDescriptionTarget{Description: description, Projected: true}}
			intents := &descriptionCallbackIntentWriter{err: testCase.intentErr}
			service := ContactDescriptionCallbackService{Enabled: true, CorpID: "corp-1", Inbox: inbox, Provider: reader, Identity: &descriptionCallbackIdentity{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerID}}, Relationships: relationships, Intents: intents, UOW: directUOW{}}
			if err = service.Process(context.Background(), 45); err != nil {
				t.Fatal(err)
			}
			if reader.calls != testCase.wantReads || len(intents.commands) != testCase.wantWrites {
				t.Fatalf("reads=%d writes=%d", reader.calls, len(intents.commands))
			}
			if len(intents.commands) == 1 && intents.commands[0].Replan {
				t.Fatalf("callback tried to replan unknown command=%+v", intents.commands[0])
			}
		})
	}
}

func TestContactDescriptionCallbackWorkerRejectsInvalidTargetReaderDetail(t *testing.T) {
	event := CallbackEvent{CorpID: "corp-1", MsgType: "event", Event: "change_external_contact", ChangeType: ChangeAddExternalContact, ExternalUserID: "external-1", UserID: "employee-1"}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	key, err := idempotency.Parse("wecom-description-callback-test-0003")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := webhook.NewService(descriptionCallbackInboxStore{delivery: webhook.Delivery{ID: 46, Provider: callbackProvider, IdempotencyKey: key, Payload: payload, Status: webhook.StatusProcessed}})
	if err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(19)
	relationships := &memoryRelationships{active: map[string]bool{relationshipKey("corp-1", "employee-1", customerID): true}}
	reader := &descriptionCallbackReader{err: errors.New("provider target mismatch")}
	intents := &descriptionCallbackIntentWriter{}
	service := ContactDescriptionCallbackService{Enabled: true, CorpID: "corp-1", Inbox: inbox, Provider: reader, Identity: &descriptionCallbackIdentity{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerID}}, Relationships: relationships, Intents: intents, UOW: directUOW{}}
	if err = service.Process(context.Background(), 46); err == nil || len(intents.commands) != 0 {
		t.Fatalf("err=%v intents=%+v", err, intents.commands)
	}
}

func TestInboxProcessorQueuesDescriptionJobOnlyAfterFullLifecycle(t *testing.T) {
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"corp_id":"corp-1","to_user_name":"corp-1","msg_type":"event","event":"change_external_contact","change_type":"add_external_contact","external_userid":"external-1","userid":"employee-1","state_present":false,"create_time":1788336000,"msg_id_present":false,"welcome_code_present":false,"source_present":false,"fail_reason_present":false}`)
	key, err := idempotency.Parse("wecom-description-processor-test-0001")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: callbackProvider, IdempotencyKey: key, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	identity := newMemoryLifecycleIdentity()
	relationships := &lifecycleRelationships{}
	jobs := &descriptionCallbackJobSpy{}
	auditService, err := platformaudit.NewService(&memoryAuditStore{})
	if err != nil {
		t.Fatal(err)
	}
	processor := InboxProcessor{Enabled: true, CorpID: "corp-1", Inbox: inbox, UOW: directUOW{}, Lifecycle: lifecycleFor(identity, relationships, &lifecycleStates{}, &lifecycleReceipts{}), Receipts: &memoryCallbackReceipts{}, Audit: auditService, DescriptionJobs: jobs}
	if count, processErr := processor.ProcessOnce(context.Background(), "description-test", 1); processErr != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, processErr)
	}
	if jobs.calls != 1 || jobs.inboxID != accepted.Delivery.ID || identity.CustomerCount() != 1 || !relationships.active("corp-1", "employee-1", 1) {
		t.Fatalf("job=%+v customers=%d relationship=%t", jobs, identity.CustomerCount(), relationships.active("corp-1", "employee-1", 1))
	}
}
