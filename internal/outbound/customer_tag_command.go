package outbound

import (
	"context"
	"errors"
	"strconv"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// CustomerTagProvider owns the actual WeCom mark_tag leaf. It re-resolves both
// the following relationship and local-tag binding immediately before the call,
// and refuses changed facts instead of retargeting a pending command.
type CustomerTagProvider struct {
	genericEnabled         bool
	channelEntryTagEnabled bool
	reader                 customerport.TagCommandDispatchReader
	contacts               wecomport.CurrentExternalContactReader
	tags                   tagport.ProviderTagBindingReader
	writer                 wecomport.CustomerTagWriter
	observer               wecomport.CustomerTagObservationRefresher
}

// CustomerTagProviderConfig separates a normal Customer-originated command
// from a channel_entry_tag command. The latter keeps the existing Channel Tag
// capability/callback gate when its persisted intent is handled by Customer.
type CustomerTagProviderConfig struct {
	GenericEnabled         bool
	ChannelEntryTagEnabled bool
}

func NewCustomerTagProvider(config CustomerTagProviderConfig, reader customerport.TagCommandDispatchReader, contacts wecomport.CurrentExternalContactReader, tags tagport.ProviderTagBindingReader, writer wecomport.CustomerTagWriter, observers ...wecomport.CustomerTagObservationRefresher) (*CustomerTagProvider, error) {
	if reader == nil || contacts == nil || tags == nil || writer == nil {
		return nil, errors.New("customer tag provider dependencies are required")
	}
	provider := &CustomerTagProvider{genericEnabled: config.GenericEnabled, channelEntryTagEnabled: config.ChannelEntryTagEnabled, reader: reader, contacts: contacts, tags: tags, writer: writer}
	if len(observers) > 0 {
		provider.observer = observers[0]
	}
	return provider, nil
}
func (p *CustomerTagProvider) Execute(ctx context.Context, e effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || !e.Valid() || e.Kind != effectport.KindCustomerTagCommand || e.PolicyVersionHash != effectport.Hash("customer.tag.command.policy.v1") {
		return customerTagFinal("invalid_command", effectport.Hash("customer.tag.invalid")), nil
	}
	d, err := p.reader.ReadTagCommandDispatch(ctx, string(e.SourceRefDigest))
	if err != nil {
		return customerTagFinal("command_unavailable", effectport.Hash("customer.tag.command-unavailable", string(e.Fingerprint()))), nil
	}
	if !p.sourceEnabled(d.Source) {
		return customerTagFinal("provider_disabled", effectport.Hash("customer.tag.disabled", d.Source, string(e.Fingerprint()))), nil
	}
	if attempt.EffectID == "" || d.EffectRef != attempt.EffectID {
		return customerTagFinal("effect_mismatch", effectport.Hash("customer.tag.effect-mismatch", d.EffectRef)), nil
	}
	contact, err := p.contacts.CurrentExternalContact(ctx, d.CustomerID, d.StaffID)
	if err == nil && effectport.Hash("customer.tag.command.target.v1", contact.EmployeeUserID, contact.ExternalUserID) != effectport.Digest(d.TargetDigest) {
		return customerTagFinal("target_changed", effectport.Hash("customer.tag.target-changed", d.EffectRef)), nil
	}
	if err != nil {
		return customerTagFinal("target_changed", effectport.Hash("customer.tag.target-changed", d.EffectRef)), nil
	}
	add, ok := p.providerTags(ctx, d.AddTagIDs)
	if !ok {
		return customerTagFinal("binding_changed", effectport.Hash("customer.tag.binding-changed", d.EffectRef)), nil
	}
	remove, ok := p.providerTags(ctx, d.RemoveTagIDs)
	if !ok {
		return customerTagFinal("binding_changed", effectport.Hash("customer.tag.binding-changed", d.EffectRef)), nil
	}
	if e.TargetRefDigest != effectport.Digest(d.TargetDigest) || e.PayloadDigest != effectport.Hash("customer.tag.command.payload.v1", joinIDs64(d.AddTagIDs), joinIDs64(d.RemoveTagIDs), d.BindingDigest) || effectport.Hash("customer.tag.command.binding.v1", joinProviderIDs(add), joinProviderIDs(remove)) != effectport.Digest(d.BindingDigest) {
		return customerTagFinal("binding_changed", effectport.Hash("customer.tag.binding-changed", d.EffectRef)), nil
	}
	err = p.writer.MarkContactTags(ctx, contact.EmployeeUserID, contact.ExternalUserID, add, remove)
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if !wecomport.ProviderWriteClassified(err) || wecomport.ProviderOutcomeUnknown(err) {
			state = effectport.StateUnknown
		} else if attempted && !wecomport.ProviderRetryable(err) {
			state = effectport.StateFinalFailed
		}
		result := effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("customer.tag.provider-error", d.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}
		if state == effectport.StateFinalFailed {
			result = customerTagFinal("provider_rejected", result.ReceiptDigest)
			result.CallAttempted, result.RealExternalCallExecuted = attempted, attempted
		}
		return result, err
	}
	// mark_tag has an explicit Provider success. Observation is a subsequent
	// read-only WeCom fact: its failure never rewrites this executed result and
	// never triggers a second mark_tag call.
	if p.observer != nil {
		_ = p.observer.RefreshCustomerTagObservation(ctx, d.EffectRef, d.CustomerID, contact.EmployeeUserID, contact.ExternalUserID)
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("customer.tag.executed", d.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (p *CustomerTagProvider) sourceEnabled(source string) bool {
	if p == nil {
		return false
	}
	if source == "channel_entry_tag" {
		return p.channelEntryTagEnabled
	}
	return p.genericEnabled
}
func (p *CustomerTagProvider) providerTags(ctx context.Context, ids []int64) ([]string, bool) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		v, ok, err := p.tags.ProviderTagID(ctx, id)
		if err != nil || !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}
func joinIDs64(v []int64) string {
	out := ""
	for i, id := range v {
		if i > 0 {
			out += ","
		}
		out += strconv.FormatInt(id, 10)
	}
	return out
}

// customerTagFinal records a short, fixed reason that the Customer owner can
// display safely. The opaque artifact is carried only through the EER completion
// transaction; it never stores a Provider body, code, or target identifier.
func customerTagFinal(reason string, receipt effectport.Digest) effectport.AdapterResult {
	payload := []byte(reason)
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: receipt, Artifact: effectport.ResultArtifact{Kind: "customer.tag.result_reason.v1", Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", "customer.tag.result_reason.v1", string(payload))}}
}

func joinProviderIDs(v []string) string {
	result := ""
	for i, item := range v {
		if i > 0 {
			result += ","
		}
		result += item
	}
	return result
}

type CustomerTagCompletionSink struct {
	writer  customerport.TagCommandCompletionWriter
	reader  customerport.TagCommandDispatchReader
	channel channelport.EntrantActionCompletionWriter
}

func NewCustomerTagCompletionSink(writer customerport.TagCommandCompletionWriter, reader customerport.TagCommandDispatchReader, channel ...channelport.EntrantActionCompletionWriter) (*CustomerTagCompletionSink, error) {
	if writer == nil || reader == nil {
		return nil, errors.New("customer tag completion writer is required")
	}
	sink := &CustomerTagCompletionSink{writer: writer, reader: reader}
	if len(channel) > 0 {
		sink.channel = channel[0]
	}
	return sink, nil
}
func (s *CustomerTagCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.writer == nil || envelope.Kind != effectport.KindCustomerTagCommand || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid customer tag completion")
	}
	completedAt := time.Now().UTC()
	reason, err := customerTagResultReason(result)
	if err != nil {
		return err
	}
	if err := s.writer.CompleteTagCommand(ctx, customerport.TagCommandCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), ResultReason: reason, Attempt: attempt.Number, Generation: attempt.Generation, Fence: attempt.Fence, CompletedAt: completedAt}); err != nil {
		return err
	}
	dispatch, readErr := s.reader.ReadTagCommandDispatch(ctx, string(envelope.SourceRefDigest))
	if readErr != nil {
		return readErr
	}
	if s.channel != nil && dispatch.Source == "channel_entry_tag" {
		if err := s.channel.CompleteEntrantAction(ctx, channelport.EntrantActionCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, CompletedAt: completedAt}); err != nil {
			return err
		}
	}
	return nil
}

func customerTagResultReason(result effectport.AdapterResult) (string, error) {
	if result.Artifact.Kind == "" {
		return "", nil
	}
	if result.Artifact.Kind != "customer.tag.result_reason.v1" || !result.Artifact.Valid() {
		return "", errors.New("invalid customer tag completion artifact")
	}
	reason := string(result.Artifact.Payload)
	if len(reason) == 0 || len(reason) > 64 {
		return "", errors.New("invalid customer tag result reason")
	}
	for index, value := range reason {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '_' || (index == 0 && (value < 'a' || value > 'z')) {
			return "", errors.New("invalid customer tag result reason")
		}
	}
	return reason, nil
}

var _ effectport.ProviderAdapter = (*CustomerTagProvider)(nil)
var _ effectport.CompletionSink = (*CustomerTagCompletionSink)(nil)
