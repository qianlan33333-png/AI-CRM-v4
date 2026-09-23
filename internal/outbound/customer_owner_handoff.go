package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const customerOwnerHandoffArtifactKind = "customer.owner_handoff.transfer_result.v1"

type customerOwnerHandoffArtifact struct {
	Version int                                `json:"version"`
	Lines   []customerOwnerHandoffArtifactLine `json:"lines"`
}

type customerOwnerHandoffArtifactLine struct {
	Line           int64  `json:"line"`
	State          string `json:"state"`
	EvidenceDigest string `json:"evidence_digest"`
}

type CustomerOwnerHandoffProvider struct {
	reader customerport.OwnerHandoffExecutionReader
	writer wecomport.CustomerTransferWriter
}

func NewCustomerOwnerHandoffProvider(reader customerport.OwnerHandoffExecutionReader, writer wecomport.CustomerTransferWriter) (*CustomerOwnerHandoffProvider, error) {
	if reader == nil || writer == nil {
		return nil, errors.New("owner handoff provider dependencies are required")
	}
	return &CustomerOwnerHandoffProvider{reader: reader, writer: writer}, nil
}

func (provider *CustomerOwnerHandoffProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.writer == nil || envelope.Kind != effectport.KindCustomerOwnerHandoff || attempt.EffectID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.invalid")}, nil
	}
	execution, err := provider.reader.ReadOwnerHandoffExecution(ctx, attempt.EffectID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("owner-handoff.intent-unavailable", attempt.EffectID)}, nil
	}
	if execution.EffectID != attempt.EffectID || execution.SourceRefDigest != string(envelope.SourceRefDigest) || execution.TargetRefDigest != string(envelope.TargetRefDigest) || execution.PayloadRefDigest != string(envelope.PayloadDigest) || execution.PolicyRefDigest != string(envelope.PolicyVersionHash) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.snapshot-mismatch", attempt.EffectID)}, nil
	}
	if len(execution.Lines) == 0 || len(execution.Lines) > 100 || execution.SourceUserID == "" || execution.TargetUserID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.invalid-subbatch", attempt.EffectID)}, nil
	}
	expected := make(map[string]customerport.OwnerHandoffExecutionLine, len(execution.Lines))
	externalIDs := make([]string, 0, len(execution.Lines))
	for _, line := range execution.Lines {
		if line.Line < 1 || line.ExternalUserID == "" {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.invalid-subbatch-line", attempt.EffectID)}, nil
		}
		if _, duplicate := expected[line.ExternalUserID]; duplicate {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.duplicate-subbatch-line", attempt.EffectID)}, nil
		}
		expected[line.ExternalUserID] = line
		externalIDs = append(externalIDs, line.ExternalUserID)
	}
	result, err := provider.writer.TransferCustomer(ctx, execution.SourceUserID, execution.TargetUserID, externalIDs, execution.WelcomeMessage)
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateFinalFailed
		// An unclassified attempted write is unsafe to retry just as a
		// classified transport disconnect is. Only a parsed Provider rejection
		// is final_failed after the write boundary.
		if attempted && (!wecomport.ProviderWriteClassified(err) || wecomport.ProviderOutcomeUnknown(err)) {
			state = effectport.StateUnknown
		}
		if !attempted && wecomport.ProviderRetryable(err) {
			state = effectport.StateRetryable
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("owner-handoff.provider-error", attempt.EffectID), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	accepted := make(map[string]struct{}, len(result.AcceptedExternalUserIDs))
	for _, externalID := range result.AcceptedExternalUserIDs {
		if _, expectedLine := expected[externalID]; !expectedLine {
			return ownerHandoffUnknownResult(attempt.EffectID)
		}
		if _, duplicate := accepted[externalID]; duplicate {
			return ownerHandoffUnknownResult(attempt.EffectID)
		}
		accepted[externalID] = struct{}{}
	}
	rejected := make(map[string]struct{}, len(result.RejectedExternalUserIDs))
	for _, externalID := range result.RejectedExternalUserIDs {
		if _, expectedLine := expected[externalID]; !expectedLine {
			return ownerHandoffUnknownResult(attempt.EffectID)
		}
		if _, duplicate := rejected[externalID]; duplicate {
			return ownerHandoffUnknownResult(attempt.EffectID)
		}
		if _, acceptedAlready := accepted[externalID]; acceptedAlready {
			return ownerHandoffUnknownResult(attempt.EffectID)
		}
		rejected[externalID] = struct{}{}
	}
	if result.FailedCount != len(rejected) {
		return ownerHandoffUnknownResult(attempt.EffectID)
	}
	artifact := customerOwnerHandoffArtifact{Version: 1, Lines: make([]customerOwnerHandoffArtifactLine, 0, len(execution.Lines))}
	complete := true
	for _, line := range execution.Lines {
		state := "outcome_unknown"
		if _, acceptedByProvider := accepted[line.ExternalUserID]; acceptedByProvider {
			state = "provider_accepted"
		} else if _, rejectedByProvider := rejected[line.ExternalUserID]; rejectedByProvider {
			state = "final_failed"
		} else {
			complete = false
		}
		artifact.Lines = append(artifact.Lines, customerOwnerHandoffArtifactLine{Line: line.Line, State: state, EvidenceDigest: string(effectport.Hash("customer-owner-handoff.transfer-result.v1", attempt.EffectID, strconv.FormatInt(line.Line, 10), state))})
	}
	payload, marshalErr := json.Marshal(artifact)
	if marshalErr != nil {
		return ownerHandoffUnknownResult(attempt.EffectID)
	}
	resultArtifact := effectport.ResultArtifact{Kind: customerOwnerHandoffArtifactKind, Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", customerOwnerHandoffArtifactKind, string(payload))}
	completion := effectport.StateExecuted
	receiptLabel := "owner-handoff.provider-accepted"
	if !complete {
		completion = effectport.StateUnknown
		receiptLabel = "owner-handoff.provider-partial-result"
	}
	return effectport.AdapterResult{Completion: completion, ReceiptDigest: effectport.Hash(receiptLabel, attempt.EffectID, string(resultArtifact.Digest)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: resultArtifact}, nil
}

func ownerHandoffUnknownResult(effectID string) (effectport.AdapterResult, error) {
	return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("owner-handoff.provider-result-unknown", effectID), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

type CustomerOwnerHandoffCompletionSink struct {
	writer customerport.OwnerHandoffCompletionWriter
}

func NewCustomerOwnerHandoffCompletionSink(writer customerport.OwnerHandoffCompletionWriter) (*CustomerOwnerHandoffCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("owner handoff completion writer is required")
	}
	return &CustomerOwnerHandoffCompletionSink{writer: writer}, nil
}

func (sink *CustomerOwnerHandoffCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || envelope.Kind != effectport.KindCustomerOwnerHandoff || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid owner handoff completion")
	}
	completion := customerport.OwnerHandoffCompletion{EffectID: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, Generation: attempt.Generation, Fence: attempt.Fence}
	artifactRequired := result.Completion == effectport.StateExecuted
	artifactPresent := result.Artifact.Valid() && result.Artifact.Kind == customerOwnerHandoffArtifactKind
	if artifactRequired && !artifactPresent {
		return errors.New("owner handoff completion artifact unavailable")
	}
	if artifactPresent {
		var artifact customerOwnerHandoffArtifact
		if err := json.Unmarshal(result.Artifact.Payload, &artifact); err != nil || artifact.Version != 1 || len(artifact.Lines) == 0 || len(artifact.Lines) > 100 {
			return errors.New("owner handoff completion artifact invalid")
		}
		seen := make(map[int64]struct{}, len(artifact.Lines))
		for _, line := range artifact.Lines {
			allowed := line.State == "provider_accepted" || line.State == "final_failed" || (result.Completion == effectport.StateUnknown && line.State == "outcome_unknown")
			if line.Line < 1 || !allowed || !effectport.ValidDigest(effectport.Digest(line.EvidenceDigest)) {
				return errors.New("owner handoff completion artifact line invalid")
			}
			if _, duplicate := seen[line.Line]; duplicate {
				return errors.New("owner handoff completion artifact duplicate line")
			}
			seen[line.Line] = struct{}{}
			completion.Lines = append(completion.Lines, customerport.OwnerHandoffLineCompletion{Line: line.Line, State: line.State, EvidenceDigest: line.EvidenceDigest})
		}
	}
	return sink.writer.CompleteOwnerHandoffEffect(ctx, completion)
}

var _ effectport.ProviderAdapter = (*CustomerOwnerHandoffProvider)(nil)
var _ effectport.CompletionSink = (*CustomerOwnerHandoffCompletionSink)(nil)
