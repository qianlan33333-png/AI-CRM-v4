package main

import (
	"context"
	"strconv"

	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type channelLinkMutationSource interface {
	ReadPublishedLinkMutation(context.Context, string) (channelport.PublishedLinkMutation, error)
}
type channelLinkMutationReaderAdapter struct {
	uow    platformport.UnitOfWork
	source channelLinkMutationSource
}

func (adapter channelLinkMutationReaderAdapter) ReadPublishedLinkMutation(ctx context.Context, source string) (channelport.PublishedLinkMutation, error) {
	var result channelport.PublishedLinkMutation
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.source.ReadPublishedLinkMutation(tx, source)
		return readErr
	})
	return result, err
}

var _ channelport.PublishedLinkMutationReader = channelLinkMutationReaderAdapter{}

type channelLinkReconciliationStore interface {
	ReceiptForReconcile(context.Context, int64, string) (channelstore.AcquisitionLinkReceipt, error)
	ReplayLinkReconciliation(context.Context, channelstore.AcquisitionLinkReconcileCommand) (channelstore.AcquisitionLinkReceipt, bool, error)
	ReconcileLinkMutation(context.Context, channelstore.AcquisitionLinkReconcileCommand, *wecomport.CustomerAcquisitionLink) (channelstore.AcquisitionLinkReceipt, error)
}

type channelLinkReconciler struct {
	uow      platformport.UnitOfWork
	store    channelLinkReconciliationStore
	effects  *externaleffects.Repository
	provider wecomport.CustomerAcquisitionLinkProvider
}

func (adapter channelLinkReconciler) ReconcileAcquisitionLink(ctx context.Context, command channelstore.AcquisitionLinkReconcileCommand) (channelstore.AcquisitionLinkReceipt, error) {
	if adapter.uow == nil || adapter.store == nil || adapter.effects == nil {
		return channelstore.AcquisitionLinkReceipt{}, channelstore.ErrAcquisitionLinkUnavailable
	}
	var replay channelstore.AcquisitionLinkReceipt
	var replayed bool
	if err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		replay, replayed, readErr = adapter.store.ReplayLinkReconciliation(tx, command)
		return readErr
	}); err != nil {
		return channelstore.AcquisitionLinkReceipt{}, err
	}
	if replayed {
		return replay, nil
	}
	var preflight channelstore.AcquisitionLinkReceipt
	if err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		preflight, readErr = adapter.store.ReceiptForReconcile(tx, command.ReceiptID, command.LinkID)
		return readErr
	}); err != nil {
		return channelstore.AcquisitionLinkReceipt{}, err
	}
	var providerLink *wecomport.CustomerAcquisitionLink
	if command.Resolution == "provider_applied" && preflight.Operation != "delete" {
		if adapter.provider == nil {
			return channelstore.AcquisitionLinkReceipt{}, channelstore.ErrAcquisitionLinkUnavailable
		}
		link, err := adapter.provider.GetManagedAcquisitionLink(ctx, command.LinkID)
		if err != nil {
			return channelstore.AcquisitionLinkReceipt{}, err
		}
		providerLink = &link
	}
	var result channelstore.AcquisitionLinkReceipt
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		current, err := adapter.store.ReceiptForReconcile(tx, command.ReceiptID, command.LinkID)
		if err != nil {
			return err
		}
		if current.State != "outcome_unknown" {
			var replayErr error
			result, replayErr = adapter.store.ReconcileLinkMutation(tx, command, providerLink)
			return replayErr
		}
		if err = adapter.effects.ReconcileWithin(tx, externaleffects.ControlCommand{EffectID: current.EffectRef, ReceiptKey: externaleffects.Hash("channel.link.reconcile.v1", strconv.FormatInt(command.ActorID, 10), command.IdempotencyKey), EvidenceDigest: externaleffects.Digest(command.EvidenceDigest), ActorAdminUserID: command.ActorID}); err != nil {
			return err
		}
		result, err = adapter.store.ReconcileLinkMutation(tx, command, providerLink)
		return err
	})
	return result, err
}

var _ channelstore.AcquisitionLinkReconciler = channelLinkReconciler{}
