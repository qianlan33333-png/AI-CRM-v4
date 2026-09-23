package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiexcel "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/excel"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type audienceDirectPushTargetResolver struct {
	uow        platformport.UnitOfWork
	identities identityport.Resolver
	staff      accessport.Repository
	unionScope string
}

func (a audienceDirectPushTargetResolver) ResolveDirectPushTarget(ctx context.Context, unionID, senderUserID string) (automationport.DirectPushResolvedTarget, string, error) {
	if a.identities == nil || a.uow == nil || a.staff == nil || a.unionScope == "" {
		return automationport.DirectPushResolvedTarget{}, "", errors.New("direct push identity adapter unavailable")
	}
	var result identityport.ResolveResult
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var resolveErr error
		result, resolveErr = a.identities.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: a.unionScope, Value: unionID, Assurance: identitydomain.AssuranceDeclared, Source: "audience-direct-push"})
		return resolveErr
	})
	if errors.Is(err, identitydomain.ErrInvalidReference) {
		return automationport.DirectPushResolvedTarget{}, "identity_invalid", nil
	}
	if err != nil {
		return automationport.DirectPushResolvedTarget{}, "", err
	}
	switch result.Status {
	case identityport.ResolveNotFound:
		return automationport.DirectPushResolvedTarget{}, "identity_unresolved", nil
	case identityport.ResolveConflict:
		return automationport.DirectPushResolvedTarget{}, "identity_conflict", nil
	case identityport.ResolveFound:
	default:
		return automationport.DirectPushResolvedTarget{}, "identity_unresolved", nil
	}
	var staffID int64
	err = a.uow.Within(ctx, func(tx context.Context) error {
		user, lookupErr := a.staff.UserByWeComUserID(tx, senderUserID, false)
		if errors.Is(lookupErr, accessdomain.ErrNotFound) {
			return nil
		}
		if lookupErr != nil {
			return lookupErr
		}
		if user.Active && user.WeComUserID == senderUserID {
			staffID = user.ID
		}
		return nil
	})
	if err != nil {
		return automationport.DirectPushResolvedTarget{}, "", err
	}
	if staffID < 1 {
		return automationport.DirectPushResolvedTarget{}, "sender_unavailable", nil
	}
	return automationport.DirectPushResolvedTarget{CustomerID: result.CustomerID, IdentityID: result.IdentityID, StaffID: staffID}, "", nil
}

type directPushEligibilityAdapter struct {
	reader segmentport.DirectPushEligibilityReader
}

func (a directPushEligibilityAdapter) DirectPushEligibility(ctx context.Context, packageID int64, customerID customerdomain.CustomerID, staffID int64) (automationport.DirectPushEligibility, string, error) {
	value, code, err := a.reader.DirectPushEligibility(ctx, segmentport.PackageID(packageID), customerID, staffID)
	return automationport.DirectPushEligibility{SnapshotID: value.SnapshotID, SenderSetVersion: value.SenderSetVersion}, code, err
}

func (a directPushEligibilityAdapter) DirectPushPackageExists(ctx context.Context, packageID int64) (bool, error) {
	return a.reader.DirectPushPackageExists(ctx, segmentport.PackageID(packageID))
}

func (a automationOutboundContentFreezer) FreezeDirectPushContent(ctx context.Context, message string, miniProgramID int64) (json.RawMessage, [32]byte, error) {
	message = strings.TrimSpace(message)
	if a.capturer == nil || a.materials == nil || message == "" || miniProgramID < 1 {
		return nil, [32]byte{}, errors.New("direct push content unavailable")
	}
	captured, err := a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: "miniprogram", ID: miniProgramID}}})
	if err != nil || mediaport.ValidateGroupOpsMaterialSourceSnapshot(captured) != nil || len(captured.References) != 1 {
		return nil, [32]byte{}, errors.New("direct push material unavailable")
	}
	materialReader, ok := a.materials.(interface {
		MiniProgramWithin(context.Context, int64) (map[string]any, error)
	})
	if !ok {
		return nil, [32]byte{}, errors.New("direct push material reader unavailable")
	}
	material, err := materialReader.MiniProgramWithin(ctx, miniProgramID)
	if err != nil {
		return nil, [32]byte{}, errors.New("direct push material unavailable")
	}
	frozen := captured.References[0]
	version, ok := number(material["version"])
	if !ok || frozen.ThumbnailImageID < 1 || !effectport.ValidDigest(effectport.Digest(frozen.ThumbnailSourceDigest)) ||
		strings.TrimSpace(frozen.ProviderFields.AppID) == "" || strings.TrimSpace(frozen.ProviderFields.PagePath) == "" || strings.TrimSpace(frozen.ProviderFields.Title) == "" {
		return nil, [32]byte{}, errors.New("direct push material snapshot unavailable")
	}
	source := frozenAutomationMaterialSource{
		Kind: "miniprogram", ID: miniProgramID, SourceDigest: frozen.SourceDigest,
		Name: text(material["name"]), Version: version,
		AppID: frozen.ProviderFields.AppID, PagePath: frozen.ProviderFields.PagePath, Title: frozen.ProviderFields.Title,
		ThumbnailImageID: frozen.ThumbnailImageID, ThumbnailSourceDigest: frozen.ThumbnailSourceDigest,
	}
	snapshot := frozenAutomationContent{SchemaVersion: 1, ContentText: message, Sources: []frozenAutomationMaterialSource{source}, ObservationPath: strings.TrimSpace(source.PagePath)}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

type audienceDirectPushDeliveryAdapter struct {
	receipts outboundport.MessageDeliveryStore
	provider outboundport.PrivateMessageDeliveryReader
}

func (a audienceDirectPushDeliveryAdapter) ReconcileDirectPushDelivery(ctx context.Context, reference string) (automationport.DirectPushDeliveryEvidence, error) {
	if a.receipts == nil || a.provider == nil {
		return automationport.DirectPushDeliveryEvidence{}, errors.New("delivery reconciliation unavailable")
	}
	receipt, found, err := a.receipts.AutomationMessageReceipt(ctx, reference)
	if err != nil || !found || receipt.MessageID == "" {
		return automationport.DirectPushDeliveryEvidence{}, err
	}
	if receipt.Status == nil || *receipt.Status == 0 {
		receipt, err = outboundport.ReconcilePrivateMessageDelivery(ctx, a.provider, receipt)
		if err != nil {
			return automationport.DirectPushDeliveryEvidence{}, err
		}
		if receipt.Status != nil {
			if err = a.receipts.SaveAutomationMessageDelivery(ctx, reference, receipt); err != nil {
				return automationport.DirectPushDeliveryEvidence{}, err
			}
		}
	}
	if receipt.Status == nil || *receipt.Status == 0 {
		return automationport.DirectPushDeliveryEvidence{}, nil
	}
	if *receipt.Status == 1 && receipt.SentAt != nil {
		return automationport.DirectPushDeliveryEvidence{Ready: true, Delivered: true, SentAt: receipt.SentAt}, nil
	}
	return automationport.DirectPushDeliveryEvidence{Ready: true, Failed: true, FailureCode: fmt.Sprintf("wecom_status_%d", *receipt.Status)}, nil
}

type audienceDirectPushOpenAdapter struct {
	uow        platformport.UnitOfWork
	identities identityport.ExternalIdentityValueReader
	excel      *aiexcel.Client
	unionScope string
}

func (a audienceDirectPushOpenAdapter) ObserveDirectPushOpen(ctx context.Context, customerID customerdomain.CustomerID, raw json.RawMessage, start, end time.Time) (automationport.DirectPushOpenResult, error) {
	var snapshot struct {
		ObservationPath string `json:"observation_path"`
	}
	if json.Unmarshal(raw, &snapshot) != nil || strings.TrimSpace(snapshot.ObservationPath) == "" {
		return automationport.DirectPushOpenResult{State: "unavailable", Reason: "unsupported_path"}, nil
	}
	if a.uow == nil || a.identities == nil || a.excel == nil || a.unionScope == "" {
		return automationport.DirectPushOpenResult{State: "unavailable", Reason: "source_unconfigured"}, nil
	}
	var unionID string
	var found bool
	if err := a.uow.Within(ctx, func(tx context.Context) error {
		var err error
		unionID, found, err = a.identities.VerifiedExternalIdentityValue(tx, customerID, identitydomain.KindUnionID, a.unionScope)
		return err
	}); err != nil {
		return automationport.DirectPushOpenResult{}, err
	}
	if !found {
		return automationport.DirectPushOpenResult{State: "unavailable", Reason: "identity_unavailable"}, nil
	}
	var output struct {
		State    string     `json:"state"`
		Reason   string     `json:"reason"`
		OpenedAt *time.Time `json:"opened_at"`
	}
	if err := a.excel.JSON(ctx, "/content-opens", map[string]any{"unionid": unionID, "path": snapshot.ObservationPath, "start": start.UTC().Format(time.RFC3339Nano), "end": end.UTC().Format(time.RFC3339Nano)}, &output); err != nil {
		return automationport.DirectPushOpenResult{}, err
	}
	return automationport.DirectPushOpenResult{State: output.State, Reason: output.Reason, OpenedAt: output.OpenedAt}, nil
}

var _ automationport.DirectPushTargetResolver = audienceDirectPushTargetResolver{}
var _ automationport.DirectPushEligibilityReader = directPushEligibilityAdapter{}
var _ automationport.DirectPushContentFreezer = automationOutboundContentFreezer{}
var _ automationport.DirectPushDeliveryReconciler = audienceDirectPushDeliveryAdapter{}
var _ automationport.DirectPushOpenReader = audienceDirectPushOpenAdapter{}
