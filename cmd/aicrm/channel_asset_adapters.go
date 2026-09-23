package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type channelPublishedAssetStore interface {
	ReadPublishedAsset(context.Context, string) (channelstore.PublishedAssetSnapshot, error)
}

type channelAssetReconciliationStore interface {
	GetAsset(context.Context, int64, string) (channelstore.AcquisitionAsset, error)
	ReadPublishedAssetByEffect(context.Context, string) (channelstore.PublishedAssetSnapshot, error)
	ReconcileAsset(context.Context, channelstore.AssetReconciliation) (channelstore.AcquisitionAsset, error)
}
type channelAssetReconciler struct {
	uow      platformport.UnitOfWork
	assets   channelAssetReconciliationStore
	effects  *externaleffects.Repository
	bindings channelStateBindingWriter
	digester wecom.StateDigester
	corpID   string
}

func (adapter channelAssetReconciler) ReconcileChannelAsset(ctx context.Context, command channelstore.AssetReconcileCommand) (channelstore.AcquisitionAsset, error) {
	if adapter.uow == nil || adapter.assets == nil || adapter.effects == nil {
		return channelstore.AcquisitionAsset{}, channelstore.ErrAssetUnavailable
	}
	var result channelstore.AcquisitionAsset
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		current, readErr := adapter.assets.GetAsset(tx, command.ChannelID, command.EffectRef)
		if readErr != nil {
			return readErr
		}
		reconciliation := channelstore.AssetReconciliation{ChannelID: command.ChannelID, ActorID: command.ActorID, EffectRef: command.EffectRef, Resolution: command.Resolution, EvidenceDigest: command.EvidenceDigest, ProviderAssetRef: command.ProviderAssetRef, ResultURL: command.ResultURL, ResultDigest: command.EvidenceDigest, IdempotencyKey: command.IdempotencyKey, CompletedAt: time.Now().UTC()}
		if current.State != "outcome_unknown" {
			var replayErr error
			result, replayErr = adapter.assets.ReconcileAsset(tx, reconciliation)
			return replayErr
		}
		if command.Resolution == "provider_applied" && current.Operation != "delete" {
			parsed, parseErr := url.Parse(command.ResultURL)
			if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || strings.TrimSpace(command.ProviderAssetRef) != command.ProviderAssetRef || command.ProviderAssetRef == "" {
				return channelstore.ErrInvalidCatalogCommand
			}
		}
		if command.Resolution == "provider_applied" && current.Operation == "delete" && (command.ProviderAssetRef != "" || command.ResultURL != "") {
			return channelstore.ErrInvalidCatalogCommand
		}
		if reconcileErr := adapter.effects.ReconcileWithin(tx, externaleffects.ControlCommand{EffectID: command.EffectRef, ReceiptKey: externaleffects.Hash("channel.asset.reconcile.v1", strconv.FormatInt(command.ActorID, 10), command.IdempotencyKey), EvidenceDigest: externaleffects.Digest(command.EvidenceDigest), ActorAdminUserID: command.ActorID}); reconcileErr != nil {
			return reconcileErr
		}
		if command.Resolution == "provider_applied" {
			snapshot, snapshotErr := adapter.assets.ReadPublishedAssetByEffect(tx, command.EffectRef)
			if snapshotErr != nil {
				return snapshotErr
			}
			if bindErr := applyChannelAssetBinding(tx, current, snapshot.StateValue, command.EffectRef, time.Now().UTC(), adapter.bindings, adapter.digester, adapter.corpID); bindErr != nil {
				return bindErr
			}
		}
		result, readErr = adapter.assets.ReconcileAsset(tx, reconciliation)
		return readErr
	})
	return result, err
}

type channelPublishedStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}
type channelPublishedConfigAdapter struct {
	uow    platformport.UnitOfWork
	assets channelPublishedAssetStore
	users  channelPublishedStaffReader
}

func (adapter channelPublishedConfigAdapter) ReadPublishedConfig(ctx context.Context, source string) (channelport.PublishedConfig, error) {
	if adapter.uow == nil || adapter.assets == nil || adapter.users == nil {
		return channelport.PublishedConfig{}, errors.New("published channel config unavailable")
	}
	var snapshot channelstore.PublishedAssetSnapshot
	staff := []string{}
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		snapshot, readErr = adapter.assets.ReadPublishedAsset(tx, source)
		if readErr != nil {
			return readErr
		}
		for _, id := range snapshot.StaffIDs {
			user, userErr := adapter.users.UserByID(tx, id, false)
			if userErr != nil || !user.Active || user.WeComUserID == "" {
				return errors.New("published channel staff unavailable")
			}
			staff = append(staff, user.WeComUserID)
		}
		return nil
	})
	if err != nil {
		return channelport.PublishedConfig{}, err
	}
	return channelport.PublishedConfig{AssetID: snapshot.Asset.ID, ChannelID: snapshot.Asset.ChannelID, ConfigVersion: snapshot.Asset.ConfigVersion, AssetVersion: snapshot.Asset.AssetVersion, Kind: string(snapshot.Asset.Kind), Operation: snapshot.Asset.Operation, TargetProviderAssetRef: snapshot.Asset.TargetProviderAssetRef, ChannelCode: snapshot.ChannelCode, ChannelName: snapshot.ChannelName, StateValue: snapshot.StateValue, SkipVerify: snapshot.SkipVerify, StaffProviderRefs: staff}, nil
}

type channelAssetWriter interface {
	channelport.AssetCompletionWriter
	ReadPublishedAssetByEffect(context.Context, string) (channelstore.PublishedAssetSnapshot, error)
}
type channelStateBindingWriter interface {
	PutBinding(context.Context, channelstore.StateBinding) (channelstore.StateBinding, bool, error)
	EndBindingForAssetID(context.Context, int64, time.Time) error
}
type channelAssetCompletionAdapter struct {
	assets   channelAssetWriter
	bindings channelStateBindingWriter
	digester wecom.StateDigester
	corpID   string
}

func (adapter channelAssetCompletionAdapter) CompletePublishedAsset(ctx context.Context, completion channelport.AssetCompletion) error {
	if adapter.assets == nil {
		return errors.New("channel asset completion unavailable")
	}
	if completion.State == "retryable_failed" {
		completion.State = "final_failed"
	}
	if completion.State == "executed" {
		if adapter.bindings == nil || adapter.digester == nil || adapter.corpID == "" {
			return errors.New("channel state binding unavailable")
		}
		var snapshot channelstore.PublishedAssetSnapshot
		// Source lookup is scoped by the effect reference without exposing any
		// provider identifier to EER.
		var err error
		// The artifact completion and State binding share EER's current tx.
		snapshot, err = adapter.assets.ReadPublishedAssetByEffect(ctx, completion.EffectRef)
		if err != nil {
			return err
		}
		if err = applyChannelAssetBinding(ctx, snapshot.Asset, snapshot.StateValue, completion.EffectRef, completion.CompletedAt, adapter.bindings, adapter.digester, adapter.corpID); err != nil {
			return err
		}
	}
	return adapter.assets.CompletePublishedAsset(ctx, completion)
}

func applyChannelAssetBinding(ctx context.Context, asset channelstore.AcquisitionAsset, state, effectRef string, completedAt time.Time, bindings channelStateBindingWriter, digester wecom.StateDigester, corpID string) error {
	if bindings == nil || digester == nil || corpID == "" {
		return errors.New("channel state binding unavailable")
	}
	if asset.SupersedesAssetID != nil {
		if err := bindings.EndBindingForAssetID(ctx, *asset.SupersedesAssetID, completedAt); err != nil {
			return err
		}
	}
	if asset.Operation == "delete" {
		return nil
	}
	if state == "" {
		state = "ca-" + strings.TrimPrefix(asset.SourceRefDigest, "sha256:")[:48]
	}
	digest, err := digester.DigestState(corpID, state)
	if err != nil {
		return err
	}
	bindingDigest := sha256.Sum256([]byte(asset.SourceRefDigest + "\x00" + effectRef))
	_, _, err = bindings.PutBinding(ctx, channelstore.StateBinding{CorpID: corpID, DigestKeyVersion: 1, StateDigest: digest, ChannelID: asset.ChannelID, AssetKind: asset.Kind, AssetVersion: asset.AssetVersion, BindingDigest: bindingDigest, ActiveFrom: completedAt})
	return err
}
