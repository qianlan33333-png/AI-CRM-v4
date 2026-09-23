package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ChannelLinkProvider struct {
	reader   channelport.PublishedLinkMutationReader
	provider wecomport.CustomerAcquisitionLinkProvider
}

func NewChannelLinkProvider(reader channelport.PublishedLinkMutationReader, provider wecomport.CustomerAcquisitionLinkProvider) *ChannelLinkProvider {
	return &ChannelLinkProvider{reader: reader, provider: provider}
}
func (adapter *ChannelLinkProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if adapter == nil || adapter.reader == nil || adapter.provider == nil || envelope.Kind != effectport.KindChannelLink || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.link.invalid")}, nil
	}
	mutation, err := adapter.reader.ReadPublishedLinkMutation(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.link.read-unavailable", string(envelope.Fingerprint()))}, nil
	}
	input := wecomport.CustomerAcquisitionLinkInput{LinkName: mutation.LinkName, UserIDs: mutation.UserIDs, DepartmentIDs: mutation.DepartmentIDs, SkipVerify: mutation.SkipVerify}
	var link wecomport.CustomerAcquisitionLink
	switch mutation.Operation {
	case "create":
		link, err = adapter.provider.CreateManagedAcquisitionLink(ctx, input)
	case "update":
		link, err = adapter.provider.UpdateManagedAcquisitionLink(ctx, mutation.LinkID, input)
	case "delete":
		err = adapter.provider.DeleteManagedAcquisitionLink(ctx, mutation.LinkID)
	default:
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.link.unsupported")}, nil
	}
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("channel.link.provider-error", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	result := effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.link.executed", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}
	if mutation.Operation != "delete" {
		payload, marshalErr := json.Marshal(link)
		if marshalErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("channel.link.artifact-failed"), CallAttempted: true, RealExternalCallExecuted: true}, marshalErr
		}
		result.Artifact = effectport.ResultArtifact{Kind: "channel.acquisition_link.v1", Payload: payload}
		result.Artifact.Digest = effectport.Hash("external-effect.artifact.v1", result.Artifact.Kind, string(payload))
	} else {
		payload, _ := json.Marshal(map[string]string{"link_id": mutation.LinkID})
		result.Artifact = effectport.ResultArtifact{Kind: "channel.acquisition_link.deleted.v1", Payload: payload}
		result.Artifact.Digest = effectport.Hash("external-effect.artifact.v1", result.Artifact.Kind, string(payload))
	}
	return result, nil
}

type ChannelLinkCompletionSink struct {
	writer channelport.LinkMutationCompletionWriter
}

func NewChannelLinkCompletionSink(writer channelport.LinkMutationCompletionWriter) (*ChannelLinkCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("channel link completion writer required")
	}
	return &ChannelLinkCompletionSink{writer: writer}, nil
}
func (sink *ChannelLinkCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, _ effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || envelope.Kind != effectport.KindChannelLink || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid channel link completion")
	}
	if result.Completion == effectport.StateRetryable {
		return nil
	}
	completion := channelport.LinkMutationCompletion{EffectRef: effectRef, State: string(result.Completion), OutcomeDigest: string(result.ReceiptDigest), BusinessEndpointDispatched: result.CallAttempted, RealExternalCallExecuted: result.RealExternalCallExecuted, CompletedAt: time.Now().UTC()}
	if result.Completion == effectport.StateExecuted && !result.Artifact.Valid() {
		return errors.New("channel link artifact missing")
	}
	if result.Completion == effectport.StateExecuted && result.Artifact.Kind == "channel.acquisition_link.v1" {
		var link wecomport.CustomerAcquisitionLink
		if json.Unmarshal(result.Artifact.Payload, &link) != nil {
			return errors.New("invalid channel link artifact")
		}
		completion.LinkID = link.LinkID
		completion.URL = link.URL
	} else if result.Completion == effectport.StateExecuted && result.Artifact.Kind != "channel.acquisition_link.deleted.v1" {
		return errors.New("invalid channel link artifact")
	}
	return sink.writer.CompleteLinkMutation(ctx, completion)
}

var _ effectport.ProviderAdapter = (*ChannelLinkProvider)(nil)
var _ effectport.CompletionSink = (*ChannelLinkCompletionSink)(nil)
