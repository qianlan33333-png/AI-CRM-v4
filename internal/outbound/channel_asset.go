package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ChannelAssetProvider struct {
	reader channelport.PublishedConfigReader
	writer wecomport.AcquisitionAssetWriter
}

func NewChannelAssetProvider(reader channelport.PublishedConfigReader, writer wecomport.AcquisitionAssetWriter) *ChannelAssetProvider {
	return &ChannelAssetProvider{reader: reader, writer: writer}
}
func (provider *ChannelAssetProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.writer == nil || envelope.Kind != effectport.KindChannelAsset || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.asset.invalid")}, nil
	}
	config, err := provider.reader.ReadPublishedConfig(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.asset.config-unavailable", string(envelope.Fingerprint()))}, nil
	}
	state := config.StateValue
	if state == "" {
		state = "ca-" + strings.TrimPrefix(string(envelope.SourceRefDigest), "sha256:")[:48]
	}
	request := wecomport.AcquisitionAssetRequest{Name: config.ChannelName, State: state, SkipVerify: config.SkipVerify, StaffUserIDs: config.StaffProviderRefs}
	var result wecomport.AcquisitionAssetResult
	if config.Kind == "contact_way_qrcode" {
		switch config.Operation {
		case "", "create":
			result, err = provider.writer.CreateContactWay(ctx, request)
		case "update":
			result, err = provider.writer.UpdateContactWay(ctx, config.TargetProviderAssetRef, request)
		case "delete":
			err = provider.writer.DeleteContactWay(ctx, config.TargetProviderAssetRef)
		default:
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.asset.operation-unsupported", config.Operation)}, nil
		}
	} else if config.Kind == "customer_acquisition_link" {
		result, err = provider.writer.CreateCustomerAcquisitionLink(ctx, request)
	} else {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.asset.unsupported", config.Kind)}, nil
	}
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("channel.asset.provider-error", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if config.Operation == "delete" {
		payload, _ := json.Marshal(map[string]string{"provider_asset_ref": config.TargetProviderAssetRef})
		artifact := effectport.ResultArtifact{Kind: "channel.acquisition_asset.deleted.v1", Payload: payload}
		artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
		return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.asset.deleted", string(envelope.Fingerprint()), string(artifact.Digest)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
	}
	payload, err := json.Marshal(struct {
		ProviderAssetRef string `json:"provider_asset_ref"`
		URL              string `json:"url"`
	}{result.ProviderAssetRef, result.URL})
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("channel.asset.artifact-error", string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, err
	}
	artifact := effectport.ResultArtifact{Kind: "channel.acquisition_asset.v1", Payload: payload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.asset.executed", string(envelope.Fingerprint()), string(artifact.Digest)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

type ChannelAssetCompletionSink struct {
	writer channelport.AssetCompletionWriter
}

func NewChannelAssetCompletionSink(writer channelport.AssetCompletionWriter) (*ChannelAssetCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("channel asset completion writer is required")
	}
	return &ChannelAssetCompletionSink{writer: writer}, nil
}
func (sink *ChannelAssetCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || envelope.Kind != effectport.KindChannelAsset {
		return errors.New("invalid channel asset completion")
	}
	if result.Completion == effectport.StateRetryable {
		return nil
	}
	completion := channelport.AssetCompletion{EffectRef: effectRef, State: string(result.Completion), Attempt: attempt.Number, CompletedAt: time.Now().UTC(), ResultDigest: string(result.ReceiptDigest)}
	if result.Completion == effectport.StateExecuted {
		if !result.Artifact.Valid() {
			return errors.New("channel asset artifact missing")
		}
		if result.Artifact.Kind == "channel.acquisition_asset.deleted.v1" {
			completion.ResultDigest = string(result.Artifact.Digest)
			return sink.writer.CompletePublishedAsset(ctx, completion)
		}
		if result.Artifact.Kind != "channel.acquisition_asset.v1" {
			return errors.New("channel asset artifact invalid")
		}
		var artifact struct {
			ProviderAssetRef string `json:"provider_asset_ref"`
			URL              string `json:"url"`
		}
		if json.Unmarshal(result.Artifact.Payload, &artifact) != nil || artifact.ProviderAssetRef == "" || artifact.URL == "" {
			return errors.New("channel asset artifact invalid")
		}
		completion.ProviderAssetRef = artifact.ProviderAssetRef
		completion.ResultURL = artifact.URL
		completion.ResultDigest = string(result.Artifact.Digest)
	}
	return sink.writer.CompletePublishedAsset(ctx, completion)
}

var _ effectport.ProviderAdapter = (*ChannelAssetProvider)(nil)
var _ effectport.CompletionSink = (*ChannelAssetCompletionSink)(nil)
