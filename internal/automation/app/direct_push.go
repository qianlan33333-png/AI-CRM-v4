package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrDirectPushInvalid      = errors.New("invalid audience direct push")
	ErrDirectPushNotFound     = errors.New("audience direct push not found")
	ErrDirectPushConflict     = errors.New("audience direct push conflict")
	ErrDirectPushUnavailable  = errors.New("audience direct push unavailable")
	ErrDirectPushObserveLater = errors.New("audience direct push observation should retry")
)

type DirectPushConfig struct {
	PackageID         int64
	WebhookReference  string
	Enabled           bool
	MaxPerCustomer24h int
	Version           int64
}

type DirectPushItemDraft struct {
	PackageID, SnapshotID, IdentityID, StaffID, SenderSetVersion, MiniProgramID int64
	CustomerID                                                                  customerdomain.CustomerID
	ClientReference                                                             string
	ContentSnapshot                                                             json.RawMessage
	ContentDigest                                                               [32]byte
	CreatedAt                                                                   time.Time
}

type DirectPushReconciliationCandidate struct {
	ID               int64
	ContentReference string
}

type DirectPushObservation struct {
	ID              int64
	CustomerID      customerdomain.CustomerID
	ContentSnapshot json.RawMessage
	SentAt          time.Time
	DueAt           time.Time
}

type DirectPushObservationScheduler interface {
	ScheduleDirectPushObservationWithin(context.Context, int64, time.Time) error
}

type DirectPushStore interface {
	DirectPushConfigByReference(context.Context, string, bool) (DirectPushConfig, error)
	DirectPushConfigByPackage(context.Context, int64, bool) (DirectPushConfig, error)
	ExistingDirectPushConfigReceipt(context.Context, string) ([32]byte, automationport.DirectPushConfigView, bool, error)
	PutDirectPushConfig(context.Context, DirectPushConfig, int64, int64, string, [32]byte, time.Time) (DirectPushConfig, error)
	ExistingDirectPushBatch(context.Context, int64, [32]byte) ([32]byte, automationport.DirectPushBatchResult, bool, error)
	CreateDirectPushBatch(context.Context, DirectPushConfig, [32]byte, [32]byte, int, time.Time) (int64, error)
	CountRecentDirectPushes(context.Context, int64, customerdomain.CustomerID, time.Time) (int, error)
	LockDirectPushRate(context.Context, int64, customerdomain.CustomerID) error
	CreateDirectPushItem(context.Context, int64, DirectPushItemDraft) (int64, error)
	BindDirectPushEffect(context.Context, int64, outboundport.MessageAcceptance, time.Time) error
	CompleteDirectPushBatch(context.Context, int64, automationport.DirectPushBatchResult, [32]byte, time.Time) error
	DirectPushStatuses(context.Context, int64, []int64) ([]automationport.DirectPushStatus, error)
	DirectPushReconciliationCandidates(context.Context, int) ([]DirectPushReconciliationCandidate, error)
	MarkDirectPushDelivery(context.Context, int64, automationport.DirectPushDeliveryEvidence, time.Time) (bool, error)
	DirectPushObservation(context.Context, int64) (DirectPushObservation, error)
	RecordDirectPushObservation(context.Context, int64, automationport.DirectPushOpenResult, time.Time) error
	DirectPushAdminRecords(context.Context, int64, int) ([]automationport.DirectPushAdminRecord, error)
}

func (s *DirectPushRuntime) DirectPushConfig(ctx context.Context, packageID int64) (automationport.DirectPushConfigView, error) {
	if s == nil || packageID < 1 {
		return automationport.DirectPushConfigView{}, ErrDirectPushInvalid
	}
	var config DirectPushConfig
	err := s.uow.Within(ctx, func(tx context.Context) error {
		exists, err := s.eligibility.DirectPushPackageExists(tx, packageID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrDirectPushNotFound
		}
		config, err = s.store.DirectPushConfigByPackage(tx, packageID, false)
		return err
	})
	if errors.Is(err, ErrDirectPushNotFound) {
		return automationport.DirectPushConfigView{PackageID: packageID, ClientID: "aicrm-audience-direct-push", MaxPerCustomer24h: 1}, nil
	}
	return directPushConfigView(config), err
}

func (s *DirectPushRuntime) ConfigureDirectPush(ctx context.Context, command automationport.DirectPushConfigCommand) (automationport.DirectPushConfigView, error) {
	if s == nil || command.PackageID < 1 || command.Actor < 1 || command.MaxPerCustomer24h < 1 || command.MaxPerCustomer24h > 100 || command.ExpectedVersion < 0 || len(command.IdempotencyKey) < 8 || len(command.IdempotencyKey) > 200 || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		return automationport.DirectPushConfigView{}, ErrDirectPushInvalid
	}
	payload, _ := json.Marshal(map[string]any{"package_id": command.PackageID, "enabled": command.Enabled, "max_per_customer_24h": command.MaxPerCustomer24h, "expected_version": command.ExpectedVersion})
	digest := sha256.Sum256(payload)
	reference, err := newDirectPushWebhookReference()
	if err != nil {
		return automationport.DirectPushConfigView{}, ErrDirectPushUnavailable
	}
	var result automationport.DirectPushConfigView
	err = s.uow.Within(ctx, func(tx context.Context) error {
		priorDigest, prior, found, readErr := s.store.ExistingDirectPushConfigReceipt(tx, command.IdempotencyKey)
		if readErr != nil {
			return readErr
		}
		if found {
			if priorDigest != digest {
				return ErrDirectPushConflict
			}
			result = prior
			return nil
		}
		exists, existsErr := s.eligibility.DirectPushPackageExists(tx, command.PackageID)
		if existsErr != nil {
			return existsErr
		}
		if !exists {
			return ErrDirectPushNotFound
		}
		current, currentErr := s.store.DirectPushConfigByPackage(tx, command.PackageID, true)
		if currentErr == nil {
			reference = current.WebhookReference
		} else if !errors.Is(currentErr, ErrDirectPushNotFound) {
			return currentErr
		}
		updated, updateErr := s.store.PutDirectPushConfig(tx, DirectPushConfig{PackageID: command.PackageID, WebhookReference: reference, Enabled: command.Enabled, MaxPerCustomer24h: command.MaxPerCustomer24h}, command.ExpectedVersion, command.Actor, command.IdempotencyKey, digest, s.now().UTC())
		result = directPushConfigView(updated)
		return updateErr
	})
	return result, err
}

func (s *DirectPushRuntime) DirectPushAdminRecords(ctx context.Context, packageID int64, limit int) ([]automationport.DirectPushAdminRecord, error) {
	if s == nil || packageID < 1 || limit < 1 || limit > 500 {
		return nil, ErrDirectPushInvalid
	}
	var items []automationport.DirectPushAdminRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, err = s.store.DirectPushAdminRecords(tx, packageID, limit)
		return err
	})
	return items, err
}

func directPushConfigView(config DirectPushConfig) automationport.DirectPushConfigView {
	path := ""
	if config.WebhookReference != "" {
		path = "/api/automation/audience/webhooks/" + config.WebhookReference
	}
	return automationport.DirectPushConfigView{PackageID: config.PackageID, WebhookReference: config.WebhookReference, WebhookPath: path, ClientID: "aicrm-audience-direct-push", Enabled: config.Enabled, MaxPerCustomer24h: config.MaxPerCustomer24h, Version: config.Version}
}

func newDirectPushWebhookReference() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "awh_" + hex.EncodeToString(raw), nil
}

type DirectPushRuntime struct {
	uow         platformport.UnitOfWork
	store       DirectPushStore
	targets     automationport.DirectPushTargetResolver
	eligibility automationport.DirectPushEligibilityReader
	freezer     automationport.DirectPushContentFreezer
	outbound    outboundport.TransactionalMessageAccepter
	deliveries  automationport.DirectPushDeliveryReconciler
	opens       automationport.DirectPushOpenReader
	scheduler   DirectPushObservationScheduler
	now         func() time.Time
}

func NewDirectPushRuntime(uow platformport.UnitOfWork, store DirectPushStore, targets automationport.DirectPushTargetResolver, eligibility automationport.DirectPushEligibilityReader, freezer automationport.DirectPushContentFreezer, outbound outboundport.TransactionalMessageAccepter) (*DirectPushRuntime, error) {
	if uow == nil || store == nil || targets == nil || eligibility == nil || freezer == nil || outbound == nil {
		return nil, ErrDirectPushInvalid
	}
	return &DirectPushRuntime{uow: uow, store: store, targets: targets, eligibility: eligibility, freezer: freezer, outbound: outbound, now: time.Now}, nil
}

func (s *DirectPushRuntime) BindObservation(deliveries automationport.DirectPushDeliveryReconciler, opens automationport.DirectPushOpenReader, scheduler DirectPushObservationScheduler) error {
	if s == nil || s.deliveries != nil || s.opens != nil || s.scheduler != nil || deliveries == nil || opens == nil || scheduler == nil {
		return ErrDirectPushInvalid
	}
	s.deliveries, s.opens, s.scheduler = deliveries, opens, scheduler
	return nil
}

func (s *DirectPushRuntime) ReconcileDirectPushes(ctx context.Context) error {
	if s == nil || s.deliveries == nil || s.scheduler == nil {
		return ErrDirectPushUnavailable
	}
	var candidates []DirectPushReconciliationCandidate
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		candidates, err = s.store.DirectPushReconciliationCandidates(tx, 500)
		return err
	}); err != nil {
		return err
	}
	for _, candidate := range candidates {
		evidence, err := s.deliveries.ReconcileDirectPushDelivery(ctx, candidate.ContentReference)
		if err != nil || !evidence.Ready {
			continue
		}
		if err = s.uow.Within(ctx, func(tx context.Context) error {
			changed, markErr := s.store.MarkDirectPushDelivery(tx, candidate.ID, evidence, s.now().UTC())
			if markErr != nil || !changed || !evidence.Delivered || evidence.SentAt == nil {
				return markErr
			}
			return s.scheduler.ScheduleDirectPushObservationWithin(tx, candidate.ID, evidence.SentAt.UTC().Add(24*time.Hour))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *DirectPushRuntime) ObserveDirectPush(ctx context.Context, itemID int64) error {
	if s == nil || s.opens == nil || itemID < 1 {
		return ErrDirectPushUnavailable
	}
	var observation DirectPushObservation
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		observation, err = s.store.DirectPushObservation(tx, itemID)
		return err
	}); err != nil {
		return err
	}
	now := s.now().UTC()
	if now.Before(observation.DueAt) {
		return ErrDirectPushObserveLater
	}
	result, err := s.opens.ObserveDirectPushOpen(ctx, observation.CustomerID, observation.ContentSnapshot, observation.SentAt, observation.DueAt)
	if err != nil {
		result = automationport.DirectPushOpenResult{State: "unavailable", Reason: "source_unavailable"}
	}
	retryableUnavailable := result.Reason == "coverage_incomplete" || result.Reason == "source_unavailable" || result.Reason == "invalid_source_event"
	if result.State == "unavailable" && retryableUnavailable && now.Before(observation.SentAt.Add(48*time.Hour)) {
		return ErrDirectPushObserveLater
	}
	if result.State != "opened" && result.State != "not_opened" && result.State != "unavailable" {
		result = automationport.DirectPushOpenResult{State: "unavailable", Reason: "invalid_observation"}
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		return s.store.RecordDirectPushObservation(tx, itemID, result, now)
	})
}

type resolvedDirectPush struct {
	target automationport.DirectPushResolvedTarget
	code   string
}

func (s *DirectPushRuntime) AcceptDirectPush(ctx context.Context, command automationport.DirectPushCommand) (automationport.DirectPushBatchResult, error) {
	if s == nil || !validDirectPushCommand(command) {
		return automationport.DirectPushBatchResult{}, ErrDirectPushInvalid
	}
	var config DirectPushConfig
	var replay automationport.DirectPushBatchResult
	var exists bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		config, err = s.store.DirectPushConfigByReference(tx, command.WebhookReference, false)
		if err != nil {
			return err
		}
		if !config.Enabled {
			return ErrDirectPushUnavailable
		}
		var prior [32]byte
		prior, replay, exists, err = s.store.ExistingDirectPushBatch(tx, config.PackageID, command.EventIDDigest)
		if err != nil {
			return err
		}
		if exists && prior != command.PayloadDigest {
			return ErrDirectPushConflict
		}
		return nil
	})
	if err != nil {
		return automationport.DirectPushBatchResult{}, err
	}
	if exists {
		replay.Replayed = true
		return replay, nil
	}

	resolved := make([]resolvedDirectPush, len(command.Items))
	for index, item := range command.Items {
		target, code, resolveErr := s.targets.ResolveDirectPushTarget(ctx, item.UnionID, item.SenderUserID)
		if resolveErr != nil {
			return automationport.DirectPushBatchResult{}, ErrDirectPushUnavailable
		}
		resolved[index] = resolvedDirectPush{target: target, code: code}
	}

	result := automationport.DirectPushBatchResult{Items: make([]automationport.DirectPushItemResult, len(command.Items))}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, err := s.store.DirectPushConfigByReference(tx, command.WebhookReference, true)
		if err != nil || !locked.Enabled {
			return ErrDirectPushUnavailable
		}
		var prior [32]byte
		prior, replay, exists, err = s.store.ExistingDirectPushBatch(tx, locked.PackageID, command.EventIDDigest)
		if err != nil {
			return err
		}
		if exists {
			if prior != command.PayloadDigest {
				return ErrDirectPushConflict
			}
			result = replay
			result.Replayed = true
			return nil
		}
		batchID, err := s.store.CreateDirectPushBatch(tx, locked, command.EventIDDigest, command.PayloadDigest, len(command.Items), command.AcceptedAt)
		if err != nil {
			return err
		}
		result.BatchID = formatDirectPushID("apb", batchID)
		for index, item := range command.Items {
			entry := automationport.DirectPushItemResult{Index: index, ClientReference: item.ClientReference, State: "rejected"}
			if resolved[index].code != "" {
				entry.Code = resolved[index].code
				result.Items[index] = entry
				result.RejectedCount++
				continue
			}
			eligibility, code, readErr := s.eligibility.DirectPushEligibility(tx, locked.PackageID, resolved[index].target.CustomerID, resolved[index].target.StaffID)
			if readErr != nil {
				return readErr
			}
			if code != "" {
				entry.Code = code
				result.Items[index] = entry
				result.RejectedCount++
				continue
			}
			if err = s.store.LockDirectPushRate(tx, locked.PackageID, resolved[index].target.CustomerID); err != nil {
				return err
			}
			count, countErr := s.store.CountRecentDirectPushes(tx, locked.PackageID, resolved[index].target.CustomerID, command.AcceptedAt.Add(-24*time.Hour))
			if countErr != nil {
				return countErr
			}
			if count >= locked.MaxPerCustomer24h {
				entry.Code = "rate_limit_24h"
				result.Items[index] = entry
				result.RejectedCount++
				continue
			}
			snapshot, digest, freezeErr := s.freezer.FreezeDirectPushContent(tx, item.Text, item.MiniProgramID)
			if freezeErr != nil {
				entry.Code = "miniprogram_unavailable"
				result.Items[index] = entry
				result.RejectedCount++
				continue
			}
			itemID, createErr := s.store.CreateDirectPushItem(tx, batchID, DirectPushItemDraft{PackageID: locked.PackageID, SnapshotID: eligibility.SnapshotID, IdentityID: resolved[index].target.IdentityID, CustomerID: resolved[index].target.CustomerID, StaffID: resolved[index].target.StaffID, SenderSetVersion: eligibility.SenderSetVersion, MiniProgramID: item.MiniProgramID, ClientReference: item.ClientReference, ContentSnapshot: snapshot, ContentDigest: digest, CreatedAt: command.AcceptedAt})
			if createErr != nil {
				return createErr
			}
			acceptance, acceptErr := s.outbound.AcceptMessageWithin(tx, outboundport.MessageIntent{SourceKind: "audience_direct_push", SourceID: itemID, RunRecipientID: itemID, CustomerID: resolved[index].target.CustomerID, SenderStaffID: resolved[index].target.StaffID, ContentReference: formatDirectPushID("audience-direct-push", itemID), SourceDigest: sha256.Sum256([]byte(fmt.Sprintf("audience-direct-push:%d", itemID))), TargetDigest: sha256.Sum256([]byte(fmt.Sprintf("customer:%d:staff:%d", resolved[index].target.CustomerID, resolved[index].target.StaffID))), PayloadDigest: digest, ContentSnapshot: snapshot, ContentSnapshotDigest: digest, PolicyDigest: sha256.Sum256([]byte("audience-direct-push-policy:v1")), ReceiptKey: fmt.Sprintf("audience-direct-push-item-%d", itemID)})
			if acceptErr != nil {
				return acceptErr
			}
			if err = s.store.BindDirectPushEffect(tx, itemID, acceptance, command.AcceptedAt); err != nil {
				return err
			}
			entry.PushID, entry.State = formatDirectPushID("ap", itemID), "accepted"
			result.Items[index] = entry
			result.AcceptedCount++
		}
		return s.store.CompleteDirectPushBatch(tx, batchID, result, command.PayloadDigest, command.AcceptedAt)
	})
	return result, err
}

func (s *DirectPushRuntime) DirectPushStatuses(ctx context.Context, reference string, pushIDs []string) ([]automationport.DirectPushStatus, error) {
	if s == nil || strings.TrimSpace(reference) == "" || len(pushIDs) < 1 || len(pushIDs) > automationport.MaxDirectPushItems {
		return nil, ErrDirectPushInvalid
	}
	ids := make([]int64, len(pushIDs))
	seen := map[int64]bool{}
	for i, value := range pushIDs {
		id, ok := parseDirectPushID("ap", value)
		if !ok || seen[id] {
			return nil, ErrDirectPushInvalid
		}
		seen[id] = true
		ids[i] = id
	}
	var out []automationport.DirectPushStatus
	err := s.uow.Within(ctx, func(tx context.Context) error {
		config, err := s.store.DirectPushConfigByReference(tx, reference, false)
		if err != nil {
			return ErrDirectPushUnavailable
		}
		out, err = s.store.DirectPushStatuses(tx, config.PackageID, ids)
		return err
	})
	return out, err
}

func validDirectPushCommand(c automationport.DirectPushCommand) bool {
	if strings.TrimSpace(c.WebhookReference) == "" || len(c.Items) < 1 || len(c.Items) > automationport.MaxDirectPushItems || c.EventIDDigest == ([32]byte{}) || c.PayloadDigest == ([32]byte{}) || c.AcceptedAt.IsZero() {
		return false
	}
	for _, item := range c.Items {
		if item.UnionID != strings.TrimSpace(item.UnionID) || item.SenderUserID != strings.TrimSpace(item.SenderUserID) ||
			item.Text != strings.TrimSpace(item.Text) || item.UnionID == "" || len(item.UnionID) > 256 || item.Text == "" ||
			len(item.Text) > 8000 || item.MiniProgramID < 1 || item.SenderUserID == "" || len(item.SenderUserID) > 256 ||
			len(item.ClientReference) > 128 {
			return false
		}
	}
	return true
}

func formatDirectPushID(prefix string, id int64) string {
	return prefix + "_" + strconv.FormatInt(id, 10)
}
func parseDirectPushID(prefix, value string) (int64, bool) {
	raw := strings.TrimPrefix(value, prefix+"_")
	if raw == value {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

var _ automationport.DirectPushService = (*DirectPushRuntime)(nil)
var _ automationport.DirectPushAdminService = (*DirectPushRuntime)(nil)
