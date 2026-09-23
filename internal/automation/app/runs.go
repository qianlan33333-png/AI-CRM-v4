package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type RunEffectReconcileCommand struct {
	RunID, Actor, Generation, Fence int64
	EffectID, IdempotencyKey        string
	LeaseExpiresAt                  time.Time
	EvidenceDigest, Resolution      string
}

type RunConfirmCommand struct {
	PackageID             int64
	PackageVersion        int64
	SnapshotID            int64
	AgentID               int64
	AgentPublishedVersion int64
	PreviewDigest         string
	Actor                 int64
	IdempotencyKey        string
}

func (s *RuntimeService) CreateBroadcastPreview(ctx context.Context, packageID, actor int64) (automationdomain.RunPreview, error) {
	if s == nil || packageID < 1 || actor < 1 {
		return automationdomain.RunPreview{}, ErrRuntimeInvalid
	}
	configuration, err := s.audiences.AudienceExecutionConfiguration(ctx, segmentport.PackageID(packageID))
	if err != nil {
		return automationdomain.RunPreview{}, ErrRuntimeUnavailable
	}
	if !configuration.Ready || configuration.Snapshot.ID < 1 || len(configuration.SenderStaffIDs) < 1 {
		return automationdomain.RunPreview{}, ErrRuntimeNotReady
	}
	if configuration.Snapshot.MemberCount < 1 {
		return automationdomain.RunPreview{}, ErrRuntimeNotReady
	}
	now := s.now().UTC()
	var preview automationdomain.RunPreview
	err = s.uow.Within(ctx, func(tx context.Context) error {
		runtimeConfig, e := s.runtimeConfigWithin(tx)
		if e != nil {
			return e
		}
		if configuration.Snapshot.MemberCount > int64(runtimeConfig.AutomationMaxRecipients) {
			return ErrRuntimeNotReady
		}
		digestInput, _ := json.Marshal([]any{configuration.PackageID, configuration.PackageVersion, configuration.Snapshot.ID, configuration.ConfigurationVersionID, configuration.AgentID, configuration.AgentPublishedVersion, configuration.BindingVersion, configuration.SenderSetVersion, configuration.Snapshot.MemberCount, runtimeConfig.Revision, runtimeConfig.AutomationMaxRecipients, now.UnixNano()})
		preview = automationdomain.RunPreview{PackageID: packageID, PackageVersion: configuration.PackageVersion, SnapshotID: int64(configuration.Snapshot.ID), ConfigurationVersionID: int64(configuration.ConfigurationVersionID), AgentID: configuration.AgentID, AgentPublishedVersion: configuration.AgentPublishedVersion, BindingVersion: configuration.BindingVersion, SenderSetVersion: configuration.SenderSetVersion, TargetCount: configuration.Snapshot.MemberCount, RuntimeConfigObserved: true, RuntimeConfigRevision: runtimeConfig.Revision, MaxRecipientsPerRun: runtimeConfig.AutomationMaxRecipients, PreviewDigest: sha256.Sum256(digestInput), CreatedBy: actor, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
		preview, e = s.store.CreatePreview(tx, preview)
		if e != nil {
			return e
		}
		if e = s.recordRuntimeConfigUsage(tx, runtimeConfig, "api", "preview", "automation_preview", preview.ID, now); e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"preview_id": preview.ID, "package_id": packageID, "snapshot_id": preview.SnapshotID, "target_count": preview.TargetCount, "runtime_config_revision": preview.RuntimeConfigRevision})
		return s.store.AppendRuntimeFact(tx, runtimeFact("preview", preview.ID, "create", "automation.run.previewed.v1", actor, hex.EncodeToString(preview.PreviewDigest[:]), now, payload))
	})
	return preview, runtimeClassify(err)
}
func (s *RuntimeService) ConfirmRun(ctx context.Context, c RunConfirmCommand) (automationdomain.RuntimeRun, error) {
	if s == nil || s.reviewPlans == nil || s.content == nil || c.PackageID < 1 || c.PackageVersion < 1 || c.SnapshotID < 1 || c.AgentID < 1 || c.AgentPublishedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	rawDigest, err := hex.DecodeString(c.PreviewDigest)
	if err != nil || len(rawDigest) != 32 {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	var digest [32]byte
	copy(digest[:], rawDigest)
	payload, _ := json.Marshal(c)
	keyDigest, payloadDigest := sha256.Sum256([]byte(c.IdempotencyKey)), sha256.Sum256(payload)
	var receipt RuntimeReceipt
	var found bool
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		receipt, found, e = s.store.RuntimeReceipt(tx, "confirm_run", fmt.Sprintf("admin:%d", c.Actor), keyDigest, payloadDigest)
		return e
	})
	if err != nil {
		return automationdomain.RuntimeRun{}, runtimeClassify(err)
	}
	if found {
		var replay automationdomain.RuntimeRun
		if receipt.State != "completed" || len(receipt.Result) == 0 || json.Unmarshal(receipt.Result, &replay) != nil {
			return automationdomain.RuntimeRun{}, ErrRuntimeConflict
		}
		return replay, nil
	}
	var preview automationdomain.RunPreview
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		preview, e = s.store.PreviewByDigest(tx, digest)
		return e
	})
	if err != nil {
		return automationdomain.RuntimeRun{}, runtimeClassify(err)
	}
	now := s.now().UTC()
	if !now.Before(preview.ExpiresAt) || preview.PackageID != c.PackageID || preview.PackageVersion != c.PackageVersion || preview.SnapshotID != c.SnapshotID || preview.AgentID != c.AgentID || preview.AgentPublishedVersion != c.AgentPublishedVersion {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	configuration, err := s.audiences.AudienceExecutionConfiguration(ctx, segmentport.PackageID(c.PackageID))
	if err != nil || !configuration.Ready {
		return automationdomain.RuntimeRun{}, ErrRuntimeNotReady
	}
	if configuration.PackageVersion != preview.PackageVersion || int64(configuration.Snapshot.ID) != preview.SnapshotID || configuration.AgentID != preview.AgentID || configuration.AgentPublishedVersion != preview.AgentPublishedVersion || configuration.BindingVersion != preview.BindingVersion || configuration.SenderSetVersion != preview.SenderSetVersion {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	members := []segmentport.Member{}
	cursor := ""
	for {
		page, e := s.snapshots.Members(ctx, segmentport.SnapshotID(preview.SnapshotID), cursor, 1000)
		if e != nil {
			return automationdomain.RuntimeRun{}, ErrRuntimeUnavailable
		}
		members = append(members, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if int64(len(members)) != preview.TargetCount || len(members) == 0 || len(members) > aiassistantport.MaxRecipients {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	if preview.RuntimeConfigObserved && (preview.RuntimeConfigRevision < 0 || preview.MaxRecipientsPerRun < 1 || preview.MaxRecipientsPerRun > aiassistantport.MaxRecipients || len(members) > preview.MaxRecipientsPerRun) {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	recipients := make([]aiassistantport.RecipientCandidate, len(members))
	for i, item := range members {
		recipients[i] = aiassistantport.RecipientCandidate{CustomerID: customerdomain.CustomerID(item.CustomerID), StaffID: configuration.SenderStaffIDs[i%len(configuration.SenderStaffIDs)]}
	}
	published, contentFound, err := s.content.OutboundPublishedContent(ctx, automationport.AgentID(c.AgentID), c.AgentPublishedVersion)
	if err != nil {
		return automationdomain.RuntimeRun{}, ErrRuntimeUnavailable
	}
	if !contentFound {
		return s.confirmDynamicRun(ctx, c, preview, digest, recipients, payload, now)
	}
	if published.ContentDigest != configuration.ContentDigest {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	blocks, err := reviewContentBlocks(published.Content)
	if err != nil {
		return automationdomain.RuntimeRun{}, err
	}
	for i := range recipients {
		recipients[i].Content = append([]aiassistantport.ContentBlock(nil), blocks...)
	}
	var run automationdomain.RuntimeRun
	err = s.runtimeMutation(ctx, "confirm_run", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		plan, e := s.reviewPlans.CreatePlanWithin(tx, aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: c.Actor}, IdempotencyKey: "automation-manual-review-" + c.IdempotencyKey, Name: "Audience broadcast " + strconv.FormatInt(c.PackageID, 10), SourceKind: "automation.manual_audience_run.v1", SourceDigest: effectport.Hash("automation.manual-audience-run", hex.EncodeToString(digest[:])), Recipients: recipients, OccurredAt: now})
		if e != nil {
			return run, RuntimeFact{}, e
		}
		if plan.Plan.ID < 1 {
			return run, RuntimeFact{}, ErrRuntimeUnavailable
		}
		run = automationdomain.RuntimeRun{PackageID: c.PackageID, PackageVersion: c.PackageVersion, SnapshotID: c.SnapshotID, AgentID: c.AgentID, AgentPublishedVersion: c.AgentPublishedVersion, AIPlanID: int64(plan.Plan.ID), BindingVersion: preview.BindingVersion, SenderSetVersion: preview.SenderSetVersion, RuntimeConfigObserved: preview.RuntimeConfigObserved, RuntimeConfigRevision: preview.RuntimeConfigRevision, MaxRecipientsPerRun: preview.MaxRecipientsPerRun, PreviewDigest: digest, State: automationport.RunPendingReview, TargetCount: int64(len(recipients)), CreatedBy: c.Actor, CreatedAt: now, UpdatedAt: now}
		created, createdRecipients, e := s.store.CreateRun(tx, run, nil)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		if len(createdRecipients) != 0 {
			return created, RuntimeFact{}, ErrRuntimeConflict
		}
		if preview.RuntimeConfigObserved {
			frozen := configport.EffectiveSnapshot{Revision: preview.RuntimeConfigRevision, Source: configport.RuntimeSourceEnvironmentDefault, AutomationMaxRecipients: preview.MaxRecipientsPerRun}
			if preview.RuntimeConfigRevision > 0 {
				frozen.Source = configport.RuntimeSourcePublished
			}
			if e = s.recordRuntimeConfigUsage(tx, frozen, "api", "confirm", "automation_run", created.ID, now); e != nil {
				return created, RuntimeFact{}, e
			}
		}
		return created, runtimeFact("run", created.ID, "confirm", "automation.run.pending_review.v1", c.Actor, c.IdempotencyKey, now), nil
	}, &run)
	return run, runtimeClassify(err)
}
func reviewContentBlocks(content automationport.FixedContentPackage) ([]aiassistantport.ContentBlock, error) {
	if len(content.DynamicMiniprogramCard) != 0 {
		return nil, ErrRuntimeNotReady
	}
	blocks := make([]aiassistantport.ContentBlock, 0, 1+len(content.ImageLibraryIDs)+len(content.MiniprogramLibraryIDs)+len(content.AttachmentLibraryIDs)+len(content.GroupInviteLibraryIDs))
	if text := strings.TrimSpace(content.ContentText); text != "" {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentText, Text: text})
	}
	for _, id := range content.ImageLibraryIDs {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentImage, MaterialKind: "image", MaterialID: id})
	}
	for _, id := range content.MiniprogramLibraryIDs {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentMiniProgram, MaterialKind: "miniprogram", MaterialID: id})
	}
	for _, id := range content.AttachmentLibraryIDs {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentAttachment, MaterialKind: "attachment", MaterialID: id})
	}
	for _, id := range content.GroupInviteLibraryIDs {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentLink, MaterialKind: "group_invite", MaterialID: id})
	}
	if len(blocks) == 0 {
		return nil, ErrRuntimeNotReady
	}
	return blocks, nil
}
func PreviewDigestString(p automationdomain.RunPreview) string {
	return hex.EncodeToString(p.PreviewDigest[:])
}
func (s *RuntimeService) ListRuns(ctx context.Context, cursor int64, limit int) ([]automationdomain.RuntimeRun, string, error) {
	if cursor < 0 || limit < 1 || limit > 100 {
		return nil, "", ErrRuntimeInvalid
	}
	var out []automationdomain.RuntimeRun
	var next string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, next, e = s.store.ListRuns(tx, cursor, limit)
		return e
	})
	if err != nil {
		return out, next, runtimeClassify(err)
	}
	for index := range out {
		if err = s.projectAIPlanState(ctx, &out[index]); err != nil {
			return nil, "", err
		}
		if err = s.projectGenerationProgress(ctx, &out[index]); err != nil {
			return nil, "", err
		}
	}
	return out, next, nil
}
func (s *RuntimeService) Run(ctx context.Context, id int64) (automationdomain.RuntimeRun, error) {
	if id < 1 {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	var out automationdomain.RuntimeRun
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.Run(tx, id); return e })
	if err != nil {
		return out, runtimeClassify(err)
	}
	if err = s.projectAIPlanState(ctx, &out); err != nil {
		return automationdomain.RuntimeRun{}, err
	}
	if err = s.projectGenerationProgress(ctx, &out); err != nil {
		return automationdomain.RuntimeRun{}, err
	}
	return out, nil
}

func (s *RuntimeService) projectGenerationProgress(ctx context.Context, run *automationdomain.RuntimeRun) error {
	if s == nil || run == nil || run.ID < 1 {
		return ErrRuntimeInvalid
	}
	var progress automationport.GenerationProgress
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		progress, readErr = s.store.GenerationProgress(tx, run.ID)
		return readErr
	})
	if err != nil {
		return ErrRuntimeUnavailable
	}
	run.Generation = progress
	return nil
}

// projectAIPlanState is a read-only projection of the existing AI Assistant
// plan. Automation never writes the plan lifecycle or invents a second
// dispatch state machine; a pending review remains pending only while its
// actual plan is reviewable.
func (s *RuntimeService) projectAIPlanState(ctx context.Context, run *automationdomain.RuntimeRun) error {
	if run == nil || run.AIPlanID < 1 {
		return nil
	}
	if s.reviewPlans == nil {
		return ErrRuntimeNotReady
	}
	plan, err := s.reviewPlans.GetPlan(ctx, aiassistantport.PlanID(run.AIPlanID))
	if err != nil {
		return ErrRuntimeUnavailable
	}
	run.AIPlanState = string(plan.State)
	switch plan.State {
	case aiassistantport.PlanPendingReview, aiassistantport.PlanPartiallyApproved:
		run.State = automationport.RunPendingReview
	case aiassistantport.PlanApproved, aiassistantport.PlanDispatching:
		run.State = automationport.RunExecuting
	case aiassistantport.PlanCompleted:
		run.State = automationport.RunCompleted
	case aiassistantport.PlanCompletedWithFailures:
		run.State = automationport.RunPartialFailed
	case aiassistantport.PlanNeedsAttention:
		unknown, retryable, readErr := s.aiAttentionCounts(ctx, aiassistantport.PlanID(run.AIPlanID))
		if readErr != nil {
			return ErrRuntimeUnavailable
		}
		run.OutcomeUnknownCount = int64(unknown)
		// AI marks both retryable failures and uncertain Provider outcomes as
		// needing attention. Preserve that distinction in the Automation read
		// model: only an actual uncertain recipient makes the run unknown.
		if unknown > 0 {
			run.State = automationport.RunOutcomeUnknown
		} else if retryable > 0 {
			run.State = automationport.RunPartialFailed
		} else {
			return ErrRuntimeUnavailable
		}
	case aiassistantport.PlanRejected:
		run.State = automationport.RunCancelled
	default:
		return ErrRuntimeUnavailable
	}
	return nil
}

func (s *RuntimeService) aiAttentionCounts(ctx context.Context, planID aiassistantport.PlanID) (unknown, retryable int, err error) {
	cursor := ""
	for {
		// Leave Limit at the stable AI Reader default. That boundary owns its
		// page cap (currently 50), while this consumer follows its opaque cursor.
		page, readErr := s.reviewPlans.ListRecipients(ctx, aiassistantport.RecipientPageQuery{PlanID: planID, Cursor: cursor})
		if readErr != nil {
			return 0, 0, readErr
		}
		for _, recipient := range page.Items {
			switch recipient.ExecutionState {
			case aiassistantport.ExecutionOutcomeUnknown:
				unknown++
			case aiassistantport.ExecutionRetryableFailed:
				retryable++
			}
		}
		if page.NextCursor == "" {
			return unknown, retryable, nil
		}
		cursor = page.NextCursor
	}
}
func (s *RuntimeService) RunRecipients(ctx context.Context, id, cursor int64, limit int) ([]automationdomain.RuntimeRecipient, string, error) {
	if id < 1 || cursor < 0 || limit < 1 || limit > 100 {
		return nil, "", ErrRuntimeInvalid
	}
	var out []automationdomain.RuntimeRecipient
	var next string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, next, e = s.store.RunRecipients(tx, id, cursor, limit)
		return e
	})
	return out, next, runtimeClassify(err)
}

func (s *RuntimeService) EffectReconciliationCandidate(ctx context.Context, runID int64, effectID string) (effectport.ReconciliationCandidate, error) {
	if s == nil || s.effects == nil || runID < 1 || effectID == "" {
		return effectport.ReconciliationCandidate{}, ErrRuntimeInvalid
	}
	var recipient automationdomain.RuntimeRecipient
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		recipient, e = s.store.RecipientForEffect(tx, runID, effectID)
		return e
	})
	if err != nil {
		return effectport.ReconciliationCandidate{}, runtimeClassify(err)
	}
	if recipient.State != automationport.RecipientOutcomeUnknown {
		return effectport.ReconciliationCandidate{}, ErrRuntimeConflict
	}
	candidate, err := s.effects.ReconciliationCandidate(ctx, effectID)
	if err != nil {
		return effectport.ReconciliationCandidate{}, runtimeClassify(err)
	}
	if candidate.Owner != effectport.OwnerOutbound || candidate.Kind != effectport.KindAutomationMessage || candidate.State != effectport.StateUnknown {
		return effectport.ReconciliationCandidate{}, ErrRuntimeConflict
	}
	return candidate, nil
}

func (s *RuntimeService) ReconcileRunEffect(ctx context.Context, command RunEffectReconcileCommand) (automationdomain.RunReconciliation, error) {
	if s == nil || s.effects == nil || command.RunID < 1 || command.Actor < 1 || command.Generation < 1 || command.Fence < 1 || command.EffectID == "" || command.LeaseExpiresAt.IsZero() || !validRuntimeMutation(command.Actor, command.IdempotencyKey) || !validReconciliationResolution(command.Resolution) {
		return automationdomain.RunReconciliation{}, ErrRuntimeInvalid
	}
	rawEvidence, err := hex.DecodeString(strings.ToLower(command.EvidenceDigest))
	if err != nil || len(rawEvidence) != sha256.Size || hex.EncodeToString(rawEvidence) != command.EvidenceDigest {
		return automationdomain.RunReconciliation{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	if now.Before(command.LeaseExpiresAt.UTC()) {
		return automationdomain.RunReconciliation{}, ErrRuntimeConflict
	}
	candidate, err := s.EffectReconciliationCandidate(ctx, command.RunID, command.EffectID)
	if err != nil {
		return automationdomain.RunReconciliation{}, err
	}
	if candidate.Generation != command.Generation || candidate.Fence != command.Fence || !candidate.LeaseExpiresAt.Equal(command.LeaseExpiresAt.UTC()) {
		return automationdomain.RunReconciliation{}, ErrRuntimeConflict
	}
	var evidence [32]byte
	copy(evidence[:], rawEvidence)
	payload, _ := json.Marshal(command)
	var output automationdomain.RunReconciliation
	err = s.runtimeMutation(ctx, "reconcile_effect", command.Actor, command.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		recipient, e := s.store.RecipientForEffect(tx, command.RunID, command.EffectID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if recipient.State != automationport.RecipientOutcomeUnknown {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		output, e = s.store.CreateRunReconciliation(tx, automationdomain.RunReconciliation{RunID: command.RunID, RecipientID: recipient.ID, EffectID: command.EffectID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt.UTC(), EvidenceDigest: evidence, Resolution: command.Resolution, ActorID: command.Actor, ReceiptDigest: sha256.Sum256([]byte(command.IdempotencyKey)), CreatedAt: now})
		if e != nil {
			return output, RuntimeFact{}, e
		}
		projection, e := s.effects.ReconcileEffectWithin(tx, effectport.ReconcileCommand{EffectID: command.EffectID, ReceiptKey: effectport.Hash("automation.effect.reconcile", command.EffectID, command.IdempotencyKey), EvidenceDigest: effectport.Digest("sha256:" + command.EvidenceDigest), ActorAdminUserID: command.Actor, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt.UTC()})
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if projection.State != effectport.StateReconciled {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		factPayload, _ := json.Marshal(map[string]any{"reconciliation_id": output.ID, "run_id": output.RunID, "recipient_id": output.RecipientID, "effect_id": output.EffectID, "generation": output.Generation, "fence": output.Fence, "resolution": output.Resolution})
		return output, runtimeFact("recipient", recipient.ID, "reconcile", "automation.recipient.reconciled.v1", command.Actor, command.IdempotencyKey, now, factPayload), nil
	}, &output)
	return output, runtimeClassify(err)
}

func validReconciliationResolution(value string) bool {
	return value == "provider_accepted" || value == "delivery_proven" || value == "final_failed"
}
func (s *RuntimeService) CancelRun(ctx context.Context, id, actor int64, key string) (automationdomain.RuntimeRun, error) {
	if id < 1 || !validRuntimeMutation(actor, key) {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(map[string]any{"run_id": id})
	var out automationdomain.RuntimeRun
	err := s.runtimeMutation(ctx, "cancel_run", actor, key, payload, func(tx context.Context) (any, RuntimeFact, error) {
		var e error
		out, e = s.store.CancelRun(tx, id, now)
		return out, runtimeFact("run", id, "cancel", "automation.run.cancelled.v1", actor, key, now), e
	}, &out)
	return out, runtimeClassify(err)
}
func validateCountEquation(run automationdomain.RuntimeRun, recipients int) error {
	if run.TargetCount != int64(recipients)+run.SkippedCount {
		return fmt.Errorf("run count equation: %w", ErrRuntimeConflict)
	}
	return nil
}
