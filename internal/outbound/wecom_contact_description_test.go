package outbound

import (
	"context"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type descriptionDispatchStub struct{ value ContactDescriptionDispatch }

func (s descriptionDispatchStub) ReadContactDescriptionDispatch(context.Context, string) (ContactDescriptionDispatch, error) {
	return s.value, nil
}

type descriptionContactsStub struct {
	value wecomport.CurrentExternalContact
}

func (s descriptionContactsStub) ResolveContactDescriptionTarget(context.Context, customerdomain.CustomerID, string) (wecomport.CurrentExternalContact, error) {
	return s.value, nil
}

type descriptionReaderStub struct {
	values          []wecomport.ExternalContactDescriptionTarget
	err             error
	calls           int
	externalUserIDs []string
	employeeUserIDs []string
}

func (s *descriptionReaderStub) ReadExternalContactDescriptionTarget(_ context.Context, externalUserID, employeeUserID string) (wecomport.ExternalContactDescriptionTarget, error) {
	s.externalUserIDs = append(s.externalUserIDs, externalUserID)
	s.employeeUserIDs = append(s.employeeUserIDs, employeeUserID)
	if s.err != nil {
		return wecomport.ExternalContactDescriptionTarget{}, s.err
	}
	if s.calls >= len(s.values) {
		return wecomport.ExternalContactDescriptionTarget{}, errors.New("unexpected read")
	}
	v := s.values[s.calls]
	s.calls++
	return v, nil
}

type descriptionWriterStub struct {
	update wecomport.ExternalContactDescriptionUpdate
	err    error
	calls  int
}

func (s *descriptionWriterStub) UpdateExternalContactDescription(_ context.Context, update wecomport.ExternalContactDescriptionUpdate) error {
	s.calls++
	s.update = update
	return s.err
}

func descriptionDispatch(target effectport.Digest, current string) ContactDescriptionDispatch {
	observed := effectport.Hash("wecom.contact.description.observed.v1", current)
	return ContactDescriptionDispatch{EffectRef: "eer_1", CustomerID: 1, EmployeeUserID: "staff-1", Operation: outboundport.ContactDescriptionOperationWrite, TargetDigest: target, PayloadDigest: effectport.Hash("wecom.contact.description.payload.v1", string(observed)), ObservedDescriptionDigest: observed}
}
func descriptionEnvelope(target effectport.Digest, current string) effectport.Envelope {
	dispatch := descriptionDispatch(target, current)
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComContactDescription, SourceRefDigest: effectport.Hash("description-source"), TargetRefDigest: target, PayloadDigest: dispatch.PayloadDigest, PolicyVersionHash: effectport.Hash("wecom.contact.description.policy.v1")}
}
func descriptionTarget(value string) wecomport.ExternalContactDescriptionTarget {
	return wecomport.ExternalContactDescriptionTarget{Description: value, Projected: true}
}

func TestContactDescriptionProviderAppendsAndSeparatesReadbackState(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{descriptionTarget("manual"), descriptionTarget("manual\nexternal-1")}}
	writer := &descriptionWriterStub{}
	p, err := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "manual")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, "manual"), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || writer.calls != 1 || writer.update.Description != "manual\nexternal-1" || !result.Artifact.Valid() || string(result.Artifact.Payload) != `{"status":"written","readback":"confirmed"}` || len(reader.employeeUserIDs) != 2 || reader.employeeUserIDs[0] != "staff-1" || reader.externalUserIDs[0] != "external-1" {
		t.Fatalf("result=%+v write=%+v calls=%d err=%v", result, writer.update, writer.calls, err)
	}
}

func TestContactDescriptionProviderSkipsExistingAndNeverWrites(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{descriptionTarget("manual\nexternal-1")}}
	writer := &descriptionWriterStub{}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "manual\nexternal-1")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, "manual\nexternal-1"), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || writer.calls != 0 || string(result.Artifact.Payload) != `{"status":"already_present","readback":"not_requested"}` {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestContactDescriptionProviderWritesWithoutReadbackConfirmation(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{descriptionTarget("")}}
	writer := &descriptionWriterStub{}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, ""), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || writer.calls != 1 || string(result.Artifact.Payload) != `{"status":"written","readback":"failed"}` {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestContactDescriptionProviderSkipsWhenDescriptionWasNotProjected(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{{}}}
	writer := &descriptionWriterStub{}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, ""), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || result.FailureCode != "description_unavailable" || writer.calls != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestContactDescriptionProviderDoesNotAttemptWriteWhenTargetReadFails(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{err: errors.New("target detail invalid")}
	writer := &descriptionWriterStub{}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "manual")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, "manual"), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err == nil || result.Completion != effectport.StateRetryable || result.CallAttempted || result.RealExternalCallExecuted || writer.calls != 0 {
		t.Fatalf("result=%+v writer_calls=%d err=%v", result, writer.calls, err)
	}
}

func TestContactDescriptionProviderSkipsSnapshotChange(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{descriptionTarget("edited after scheduling")}}
	writer := &descriptionWriterStub{}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "manual")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, "manual"), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || result.FailureCode != "description_changed" || writer.calls != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestContactDescriptionProviderRetainsOnlyNumericRejectedProviderCode(t *testing.T) {
	target := effectport.Hash("wecom.contact.description.target.v1", "staff-1", "external-1")
	reader := &descriptionReaderStub{values: []wecomport.ExternalContactDescriptionTarget{descriptionTarget("")}}
	writer := &descriptionWriterStub{err: wecomport.WrapProviderWriteDispositionWithCode(errors.New("provider message that must not be persisted"), true, false, false, 40003)}
	p, _ := NewContactDescriptionProvider(true, descriptionDispatchStub{descriptionDispatch(target, "")}, descriptionContactsStub{wecomport.CurrentExternalContact{EmployeeUserID: "staff-1", ExternalUserID: "external-1"}}, reader, writer)
	result, err := p.Execute(context.Background(), descriptionEnvelope(target, ""), effectport.Attempt{EffectID: "eer_1", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || string(result.Artifact.Payload) != `{"reason":"provider_rejected","provider_error_code":40003}` || result.FailureCode != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
