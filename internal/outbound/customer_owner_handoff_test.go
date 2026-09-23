package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ownerHandoffReaderStub struct {
	value customerport.OwnerHandoffExecution
	err   error
}

func (s ownerHandoffReaderStub) ReadOwnerHandoffExecution(context.Context, string) (customerport.OwnerHandoffExecution, error) {
	return s.value, s.err
}

type ownerHandoffWriterStub struct {
	result wecomport.CustomerTransferResult
	err    error
	calls  int
}

func (s *ownerHandoffWriterStub) TransferCustomer(context.Context, string, string, []string, string) (wecomport.CustomerTransferResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *ownerHandoffWriterStub) TransferResult(context.Context, string, string, string) (wecomport.CustomerTransferResult, error) {
	return wecomport.CustomerTransferResult{}, nil
}

func ownerHandoffEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerOwnerHandoff, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy")}
}

func ownerHandoffExecution() customerport.OwnerHandoffExecution {
	envelope := ownerHandoffEnvelope()
	return customerport.OwnerHandoffExecution{EffectID: "eer_9", SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadRefDigest: string(envelope.PayloadDigest), PolicyRefDigest: string(envelope.PolicyVersionHash), SourceUserID: "source", TargetUserID: "target", SourceDigest: "sha256:source-snapshot", TargetDigest: "sha256:target-snapshot", PayloadDigest: string(envelope.PayloadDigest), PolicyDigest: string(envelope.PolicyVersionHash), Lines: []customerport.OwnerHandoffExecutionLine{{Line: 1, CustomerID: 1, ExternalUserID: "external"}}}
}

func TestCustomerOwnerHandoffProviderDoesNotRetryAttemptedFailure(t *testing.T) {
	writer := &ownerHandoffWriterStub{err: wecomport.WrapProviderWriteError(errors.New("lost response"), true)}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err == nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestCustomerOwnerHandoffProviderAcceptsOnlyExactFrozenTarget(t *testing.T) {
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external"}}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateExecuted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	writer.result.AcceptedExternalUserIDs = []string{"wrong-customer"}
	result, err = provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted {
		t.Fatalf("cross-target result=%+v err=%v", result, err)
	}
}

func TestCustomerOwnerHandoffProviderTreatsDefinitiveProviderRejectionAsFinal(t *testing.T) {
	writer := &ownerHandoffWriterStub{err: wecomport.WrapProviderWriteOutcome(errors.New("provider rejected request"), true, false)}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, callErr := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if callErr == nil || result.Completion != effectport.StateFinalFailed || !result.CallAttempted || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, callErr)
	}
}

func TestCustomerOwnerHandoffProviderReturnsPerLineArtifactForPartialAndRefusedBatch(t *testing.T) {
	execution := ownerHandoffExecution()
	execution.Lines = []customerport.OwnerHandoffExecutionLine{
		{Line: 1, CustomerID: 11, ExternalUserID: "external-1"},
		{Line: 2, CustomerID: 12, ExternalUserID: "external-2"},
		{Line: 3, CustomerID: 13, ExternalUserID: "external-3"},
	}
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external-1"}, RejectedExternalUserIDs: []string{"external-2", "external-3"}, FailedCount: 2}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: execution}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateExecuted || !result.Artifact.Valid() || result.Artifact.Kind != customerOwnerHandoffArtifactKind || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
	var artifact customerOwnerHandoffArtifact
	if err = json.Unmarshal(result.Artifact.Payload, &artifact); err != nil || len(artifact.Lines) != 3 || artifact.Lines[0].Line != 1 || artifact.Lines[0].State != "provider_accepted" || artifact.Lines[1].State != "final_failed" || artifact.Lines[2].State != "final_failed" || artifact.Lines[0].EvidenceDigest == "" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
}

func TestCustomerOwnerHandoffProviderTreatsMissingOrAmbiguousBatchRowsAsUnknown(t *testing.T) {
	execution := ownerHandoffExecution()
	execution.Lines = []customerport.OwnerHandoffExecutionLine{
		{Line: 1, CustomerID: 11, ExternalUserID: "external-1"},
		{Line: 2, CustomerID: 12, ExternalUserID: "external-2"},
	}
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external-1"}}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: execution}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.Artifact.Valid() || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
	var artifact customerOwnerHandoffArtifact
	if err = json.Unmarshal(result.Artifact.Payload, &artifact); err != nil || len(artifact.Lines) != 2 || artifact.Lines[0].State != "provider_accepted" || artifact.Lines[1].State != "outcome_unknown" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
}
