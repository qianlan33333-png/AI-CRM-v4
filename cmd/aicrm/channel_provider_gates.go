package main

import (
	"context"
	"errors"

	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type channelFollowUserGate struct {
	enabled bool
	source  interface {
		channelstore.FollowUserReader
		GetContactWay(context.Context, string) (wecomport.AcquisitionAssetResult, error)
	}
}

func (gate channelFollowUserGate) GetContactWay(ctx context.Context, configID string) (wecomport.AcquisitionAssetResult, error) {
	if !gate.enabled || gate.source == nil {
		return wecomport.AcquisitionAssetResult{}, errors.New("channel provider read disabled")
	}
	return gate.source.GetContactWay(ctx, configID)
}

func (gate channelFollowUserGate) ListContactStaff(ctx context.Context) ([]string, error) {
	if !gate.enabled || gate.source == nil {
		return nil, errors.New("channel provider read disabled")
	}
	return gate.source.ListContactStaff(ctx)
}

type channelEntrantProviderGate struct {
	welcome, tag bool
	source       effectport.ProviderAdapter
}

type channelLinkProviderGate struct {
	read, write bool
	source      wecomport.CustomerAcquisitionLinkProvider
}

func (gate channelLinkProviderGate) ListManagedAcquisitionLinks(ctx context.Context, cursor string, limit int) (wecomport.CustomerAcquisitionLinkPage, error) {
	if !gate.read || gate.source == nil {
		return wecomport.CustomerAcquisitionLinkPage{}, errors.New("channel provider read disabled")
	}
	return gate.source.ListManagedAcquisitionLinks(ctx, cursor, limit)
}
func (gate channelLinkProviderGate) GetManagedAcquisitionLink(ctx context.Context, id string) (wecomport.CustomerAcquisitionLink, error) {
	if !gate.read || gate.source == nil {
		return wecomport.CustomerAcquisitionLink{}, errors.New("channel provider read disabled")
	}
	return gate.source.GetManagedAcquisitionLink(ctx, id)
}
func (gate channelLinkProviderGate) CreateManagedAcquisitionLink(ctx context.Context, input wecomport.CustomerAcquisitionLinkInput) (wecomport.CustomerAcquisitionLink, error) {
	if !gate.write || gate.source == nil {
		return wecomport.CustomerAcquisitionLink{}, errors.New("channel link provider write disabled")
	}
	return gate.source.CreateManagedAcquisitionLink(ctx, input)
}
func (gate channelLinkProviderGate) UpdateManagedAcquisitionLink(ctx context.Context, id string, input wecomport.CustomerAcquisitionLinkInput) (wecomport.CustomerAcquisitionLink, error) {
	if !gate.write || gate.source == nil {
		return wecomport.CustomerAcquisitionLink{}, errors.New("channel link provider write disabled")
	}
	return gate.source.UpdateManagedAcquisitionLink(ctx, id, input)
}
func (gate channelLinkProviderGate) DeleteManagedAcquisitionLink(ctx context.Context, id string) error {
	if !gate.write || gate.source == nil {
		return errors.New("channel link provider write disabled")
	}
	return gate.source.DeleteManagedAcquisitionLink(ctx, id)
}

func (gate channelEntrantProviderGate) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	allowed := envelope.Kind == effectport.KindChannelWelcome && gate.welcome || envelope.Kind == effectport.KindChannelEntryTag && gate.tag
	if !allowed || gate.source == nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.provider.capability-disabled", string(envelope.Kind))}, nil
	}
	return gate.source.Execute(ctx, envelope, attempt)
}
