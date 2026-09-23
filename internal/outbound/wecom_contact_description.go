package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// ContactDescriptionDispatch is the immutable, PII-free provider dispatch
// projection. The future Outbound-owned receipt table binds SourceRefDigest to
// EffectRef; raw external_userid and human description never enter that table
// or an EER envelope.
type ContactDescriptionDispatch struct {
	EffectRef                 string
	CustomerID                customerdomain.CustomerID
	EmployeeUserID            string
	Operation                 string
	TargetDigest              effectport.Digest
	PayloadDigest             effectport.Digest
	ObservedDescriptionDigest effectport.Digest
}

type ContactDescriptionDispatchReader interface {
	ReadContactDescriptionDispatch(context.Context, string) (ContactDescriptionDispatch, error)
}

// ContactDescriptionProvider performs the only provider write for this
// contract. It obtains the target only after EER acceptance, reads the live
// follow relationship, and treats a post-write readback failure independently
// from a successful write. The completion artifact contains only fixed state
// and digests, never IDs or description text.
type ContactDescriptionProvider struct {
	enabled  bool
	dispatch ContactDescriptionDispatchReader
	contacts ContactDescriptionTargetResolver
	reader   wecomport.ExternalContactDescriptionTargetReader
	writer   wecomport.ExternalContactDescriptionWriter
}

type ContactDescriptionTargetResolver interface {
	ResolveContactDescriptionTarget(context.Context, customerdomain.CustomerID, string) (wecomport.CurrentExternalContact, error)
}

func NewContactDescriptionProvider(enabled bool, dispatch ContactDescriptionDispatchReader, contacts ContactDescriptionTargetResolver, reader wecomport.ExternalContactDescriptionTargetReader, writer wecomport.ExternalContactDescriptionWriter) (*ContactDescriptionProvider, error) {
	if dispatch == nil || contacts == nil || reader == nil || writer == nil {
		return nil, errors.New("contact description provider dependencies are required")
	}
	return &ContactDescriptionProvider{enabled: enabled, dispatch: dispatch, contacts: contacts, reader: reader, writer: writer}, nil
}

func (p *ContactDescriptionProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	base := effectport.Hash("wecom.contact.description", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
	if p == nil || !p.enabled || !envelope.Valid() || envelope.Kind != effectport.KindWeComContactDescription || envelope.PolicyVersionHash != effectport.Hash("wecom.contact.description.policy.v1") {
		return contactDescriptionFinal("provider_disabled", 0, effectport.Hash(string(base), "disabled")), nil
	}
	dispatch, err := p.dispatch.ReadContactDescriptionDispatch(ctx, string(envelope.SourceRefDigest))
	if err != nil || dispatch.EffectRef != attempt.EffectID || dispatch.CustomerID < 1 || dispatch.EmployeeUserID == "" || (dispatch.Operation != outboundport.ContactDescriptionOperationWrite && dispatch.Operation != outboundport.ContactDescriptionOperationReadback) || !effectport.ValidDigest(dispatch.TargetDigest) || !effectport.ValidDigest(dispatch.PayloadDigest) || !effectport.ValidDigest(dispatch.ObservedDescriptionDigest) || envelope.TargetRefDigest != dispatch.TargetDigest || envelope.PayloadDigest != dispatch.PayloadDigest {
		return contactDescriptionFinal("dispatch_changed", 0, effectport.Hash(string(base), "dispatch_changed")), nil
	}
	target, err := p.contacts.ResolveContactDescriptionTarget(ctx, dispatch.CustomerID, dispatch.EmployeeUserID)
	if err != nil || target.EmployeeUserID == "" || target.ExternalUserID == "" || dispatch.TargetDigest != effectport.Hash("wecom.contact.description.target.v1", target.EmployeeUserID, target.ExternalUserID) {
		return contactDescriptionFinal("target_changed", 0, effectport.Hash(string(base), "target_changed")), nil
	}
	live, err := p.reader.ReadExternalContactDescriptionTarget(ctx, target.ExternalUserID, target.EmployeeUserID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "read_before_write_failed")}, err
	}
	if !live.Projected {
		return contactDescriptionResult("description_unavailable", "not_requested", effectport.Hash(string(base), "description_unavailable")), nil
	}
	current := live.Description
	if dispatch.Operation == outboundport.ContactDescriptionOperationReadback {
		if strings.Contains(current, target.ExternalUserID) {
			return contactDescriptionResult("readback_checked", "confirmed", effectport.Hash(string(base), "readback_confirmed")), nil
		}
		return contactDescriptionResult("readback_checked", "failed", effectport.Hash(string(base), "readback_failed")), nil
	}
	if dispatch.ObservedDescriptionDigest != outboundport.ContactDescriptionObservedDigest(current) {
		return contactDescriptionResult("description_changed", "not_requested", effectport.Hash(string(base), "description_changed")), nil
	}
	if strings.Contains(current, target.ExternalUserID) {
		return contactDescriptionResult("already_present", "not_requested", effectport.Hash(string(base), "already_present")), nil
	}
	desired := target.ExternalUserID
	if current != "" {
		desired = current + "\n" + target.ExternalUserID
	}
	if len([]rune(desired)) > 150 {
		return contactDescriptionResult("too_long", "not_requested", effectport.Hash(string(base), "too_long")), nil
	}
	if err = p.writer.UpdateExternalContactDescription(ctx, wecomport.ExternalContactDescriptionUpdate{EmployeeID: target.EmployeeUserID, ExternalUserID: target.ExternalUserID, Description: desired}); err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if !wecomport.ProviderWriteClassified(err) || wecomport.ProviderOutcomeUnknown(err) {
			state = effectport.StateUnknown
		} else if attempted && !wecomport.ProviderRetryable(err) {
			code, _ := wecomport.ProviderErrorCode(err)
			return contactDescriptionFinal("provider_rejected", code, effectport.Hash(string(base), "provider_rejected", strconv.FormatInt(code, 10))), nil
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider_error"), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	readback := "failed"
	if observed, readErr := p.reader.ReadExternalContactDescriptionTarget(ctx, target.ExternalUserID, target.EmployeeUserID); readErr == nil {
		if observed.Projected && observed.Description == desired {
			readback = "confirmed"
		}
	}
	return contactDescriptionResult("written", readback, effectport.Hash(string(base), "written", readback)), nil
}

func contactDescriptionResult(status, readback string, receipt effectport.Digest) effectport.AdapterResult {
	payload, _ := json.Marshal(struct {
		Status   string `json:"status"`
		Readback string `json:"readback"`
	}{status, readback})
	kind := "wecom.contact.description.result.v1"
	artifact := effectport.ResultArtifact{Kind: kind, Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", kind, string(payload))}
	// A comparison-only outcome did perform the trusted Provider read, but it
	// did not execute the represented description write. The EER kernel has an
	// explicit, kind-scoped no-op completion path for this immutable contract;
	// never pretend that a write occurred merely to reach a terminal state.
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: receipt, FailureCode: status, CallAttempted: true, RealExternalCallExecuted: status == "written", Artifact: artifact}
}

func contactDescriptionFinal(reason string, providerErrorCode int64, receipt effectport.Digest) effectport.AdapterResult {
	payload, _ := json.Marshal(struct {
		Reason            string `json:"reason"`
		ProviderErrorCode int64  `json:"provider_error_code,omitempty"`
	}{Reason: reason, ProviderErrorCode: providerErrorCode})
	kind := "wecom.contact.description.reason.v1"
	artifact := effectport.ResultArtifact{Kind: kind, Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", kind, string(payload))}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: receipt, Artifact: artifact}
}

var _ effectport.ProviderAdapter = (*ContactDescriptionProvider)(nil)
