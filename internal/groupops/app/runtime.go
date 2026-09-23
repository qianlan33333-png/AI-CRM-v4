package app

// This file contains the Group Ops runtime/application seam.  It accepts
// immutable local plan snapshots and hands opaque group-message intents to the
// External Effects port.  It deliberately does not resolve customers,
// audiences, OneID identities, or Provider credentials.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrProviderDisabled                    = errors.New("group ops provider is disabled")
	ErrRuntimeInvalid                      = errors.New("invalid Group Ops runtime command")
	ErrMiniProgramCoverUnsupported         = errors.New("Group Ops mini program cover is unsupported")
	ErrMiniProgramCoverResolverUnavailable = errors.New("Group Ops mini program cover resolver is unavailable")
)

// RuntimeService coordinates local run/execution facts with the stable EER
// transaction accepter.  A nil directory source is intentional: reads and
// refreshes fail closed instead of inventing a group or sender.
type RuntimeService struct {
	catalog    groupopsport.Catalog
	uow        platformport.UnitOfWork
	plans      Store
	runtime    groupopsport.RuntimeStore
	effects    effectport.TransactionalAccepter
	staff      groupopsport.EligibleStaffReader
	directory  groupopsport.GroupDirectorySource
	senders    groupopsport.ExecutionSenderResolver
	materials  groupopsport.MaterialSnapshotResolver
	evidence   groupopsport.ReconciliationEvidenceVerifier
	reconciler groupopsport.ExternalReconciler
	now        func() time.Time
	// dispatchEnabled means the local runtime may accept an EER intent. It is
	// never used as evidence that a Provider call or delivery occurred.
	dispatchEnabled bool
}

func NewRuntimeService(uow platformport.UnitOfWork, plans Store, runtime groupopsport.RuntimeStore, effects effectport.TransactionalAccepter, staff groupopsport.EligibleStaffReader, directory groupopsport.GroupDirectorySource, senders groupopsport.ExecutionSenderResolver, evidence groupopsport.ReconciliationEvidenceVerifier, reconciler groupopsport.ExternalReconciler, materials ...groupopsport.MaterialSnapshotResolver) *RuntimeService {
	var materialResolver groupopsport.MaterialSnapshotResolver
	if len(materials) > 0 {
		materialResolver = materials[0]
	}
	return &RuntimeService{uow: uow, plans: plans, runtime: runtime, effects: effects, staff: staff, directory: directory, senders: senders, materials: materialResolver, evidence: evidence, reconciler: reconciler, now: time.Now}
}

// SetCatalog connects legacy refresh callers to the single durable catalog service.
func (s *RuntimeService) SetCatalog(c groupopsport.Catalog) { s.catalog = c }

func (s *RuntimeService) SetDispatchEnabled(enabled bool) {
	if s != nil {
		s.dispatchEnabled = enabled
	}
}

// SetEvidenceVerifier is composition-only wiring for the opt-in provider
// read capability. The default verifier remains fail-closed.
func (s *RuntimeService) SetEvidenceVerifier(verifier groupopsport.ReconciliationEvidenceVerifier) {
	if s != nil && verifier != nil {
		s.evidence = verifier
	}
}

func (s *RuntimeService) safety() groupopsport.RuntimeSafety {
	if s != nil && s.dispatchEnabled {
		return groupopsport.DispatchEnabledRuntimeSafety()
	}
	return groupopsport.DisabledRuntimeSafety()
}

func (s *RuntimeService) ready() bool {
	return s != nil && s.uow != nil && s.plans != nil && s.runtime != nil && s.effects != nil && s.senders != nil
}

func (s *RuntimeService) nowUTC() time.Time {
	if s == nil || s.now == nil {
		return time.Time{}
	}
	return s.now().UTC()
}

func (s *RuntimeService) PreviewRunDue(ctx context.Context, planID int64) (groupopsport.RunDuePreview, error) {
	if s == nil || s.uow == nil || s.plans == nil || s.runtime == nil || planID < 1 {
		return groupopsport.RunDuePreview{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.RunDuePreview{}, ErrUnavailable
	}
	var result groupopsport.RunDuePreview
	err := s.uow.Within(ctx, func(tx context.Context) error {
		detail, err := s.plans.Get(tx, planID)
		if err != nil {
			return err
		}
		result = groupopsport.RunDuePreview{PlanID: planID, PlanStatus: detail.Plan.Status, SnapshotRevision: detail.Plan.Revision, EvaluatedAt: now, Blockers: []string{}, RuntimeSafety: s.safety()}
		validation := contentValidation(detail)
		if detail.Plan.Status != groupopsport.PlanActive {
			result.Blockers = append(result.Blockers, "plan_not_active")
		}
		result.Blockers = append(result.Blockers, validation.IssueCodes...)
		result.Blockers = append(result.Blockers, s.materialBlockers(tx, detail, now)...)
		if len(result.Blockers) == 0 {
			keys, keyErr := s.runtime.ListExecutionKeys(tx, planID, detail.Plan.Revision)
			if keyErr != nil {
				return keyErr
			}
			existing := make(map[string]struct{}, len(keys))
			for _, key := range keys {
				existing[executionKeyString(key.NodeID, key.TargetReference)] = struct{}{}
			}
			result.DueExecutionCount = int32(countMessageDrafts(detail, existing))
			result.NextDueAt = nextMessageDue(detail, now)
		}
		return nil
	})
	if err != nil {
		return groupopsport.RunDuePreview{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) RunDue(ctx context.Context, command groupopsport.RunDueCommand) (groupopsport.RunSummary, error) {
	if command.ActorID < 1 {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	return s.AcceptPlan(ctx, groupopsport.AcceptPlanCommand{PlanID: command.PlanID, Trigger: groupopsport.RunTriggerDue, AcceptedBy: "admin:" + strconv.FormatInt(command.ActorID, 10), IdempotencyKey: command.IdempotencyKey})
}

func (s *RuntimeService) AcceptBroadcast(ctx context.Context, planID, actorID int64, key string) (groupopsport.RunSummary, error) {
	if actorID < 1 {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	return s.AcceptPlan(ctx, groupopsport.AcceptPlanCommand{PlanID: planID, Trigger: groupopsport.RunTriggerBroadcast, AcceptedBy: "admin:" + strconv.FormatInt(actorID, 10), IdempotencyKey: key})
}

func (s *RuntimeService) AcceptPlan(ctx context.Context, command groupopsport.AcceptPlanCommand) (groupopsport.RunSummary, error) {
	if !s.ready() || command.PlanID < 1 || !validRuntimeKey(command.IdempotencyKey) || command.AcceptedBy == "" || len(command.AcceptedBy) > 140 {
		return groupopsport.RunSummary{}, invalidOrUnavailableRuntime(s)
	}
	if !s.dispatchEnabled {
		return groupopsport.RunSummary{}, ErrProviderDisabled
	}
	if command.Trigger != groupopsport.RunTriggerDue && command.Trigger != groupopsport.RunTriggerBroadcast && command.Trigger != groupopsport.RunTriggerWebhook {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.RunSummary{}, ErrUnavailable
	}
	var summary groupopsport.RunSummary
	err := s.uow.Within(ctx, func(tx context.Context) error {
		detail, err := s.plans.Lock(tx, command.PlanID)
		if err != nil {
			return err
		}
		if groupopsdomain.ValidateDetail(detail) != nil {
			return ErrUnavailable
		}
		validation := contentValidation(detail)
		if detail.Plan.Status != groupopsport.PlanActive || !validation.Valid {
			return ErrStateConflict
		}
		sourceKey := sha256.Sum256([]byte(strings.Join([]string{"group-ops.run.v1", strconv.FormatInt(command.PlanID, 10), strconv.FormatInt(detail.Plan.Revision, 10), string(command.Trigger), command.IdempotencyKey}, "\x00")))
		run, err := s.runtime.ReserveRun(tx, groupopsport.RunReservation{PlanID: command.PlanID, Trigger: command.Trigger, SourceKeyDigest: sourceKey, PlanRevision: detail.Plan.Revision, ScheduledFor: now, AcceptedAt: now, AcceptedBy: command.AcceptedBy})
		if err != nil {
			return err
		}
		keys, err := s.runtime.ListExecutionKeys(tx, command.PlanID, detail.Plan.Revision)
		if err != nil {
			return err
		}
		existing := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			existing[executionKeyString(key.NodeID, key.TargetReference)] = struct{}{}
		}
		drafts, err := s.buildDrafts(tx, detail, run, existing, now)
		if err != nil {
			return err
		}
		if _, err = s.runtime.CreateExecutionIntents(tx, drafts); err != nil {
			return err
		}
		initial, intentErr := s.runtime.InitialExecutionIntents(tx, run.ID)
		if intentErr != nil {
			return intentErr
		}
		for _, draft := range initial {
			projection, receipt, acceptErr := s.effects.AcceptAndQueueWithin(tx, groupOpsEffectAcceptCommand(draft, command.IdempotencyKey))
			if acceptErr != nil {
				return acceptErr
			}
			if projection.ID == "" || projection.QueueJobID < 1 || receipt.ID == "" || receipt.QueueReceiptID == "" {
				return ErrUnavailable
			}
			draft.ExternalEffectID = projection.ID
			if _, err = s.runtime.InsertExecution(tx, draft); err != nil {
				return err
			}
			if err = s.runtime.BindAcceptedExecutionIntent(tx, draft.IntentID, projection.ID); err != nil {
				return err
			}
		}
		summary, err = s.runtime.ReadRunSummary(tx, run.ID)
		if err != nil {
			return err
		}
		summary.RuntimeSafety = s.safety()
		return nil
	})
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	return summary, nil
}

// AcceptWebhook accepts only a typed, signed dynamic message. It does not
// traverse plan nodes: a webhook run freezes its own message and its selected
// subset of bound targets. Existing runs are read before any dynamic Media
// resolution so a repeated event cannot mint a changed cover or effect.
func (s *RuntimeService) AcceptWebhook(ctx context.Context, webhookReference, key string, inbound groupopsport.WebhookInboundCommand) (groupopsport.RunSummary, error) {
	if !s.ready() || !validRuntimeKey(key) || !validOpaqueReference(webhookReference) || inbound.WebhookReference != webhookReference || groupopsdomain.ValidateWebhookInbound(inbound) != nil {
		return groupopsport.RunSummary{}, invalidOrUnavailableRuntime(s)
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.RunSummary{}, ErrUnavailable
	}
	payloadDigest, digestErr := webhookPayloadDigest(inbound)
	if digestErr != nil {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}

	var planID int64
	var prior groupopsport.RunSummary
	var found bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		planID, err = s.runtime.FindPlanByWebhookReference(tx, webhookReference)
		if err != nil {
			return err
		}
		run, exists, findErr := s.runtime.FindRunBySourceKey(tx, planID, groupopsport.RunTriggerWebhook, webhookRunSourceKey(planID, webhookReference, key))
		if findErr != nil || !exists {
			if findErr != nil {
				return findErr
			}
			detail, detailErr := s.plans.Get(tx, planID)
			if detailErr != nil {
				return detailErr
			}
			if !validDynamicWebhookDetail(detail, webhookReference, inbound.TargetChatReferences) {
				return ErrStateConflict
			}
			return nil
		}
		if run.WebhookPayloadDigest != payloadDigest {
			return ErrConflict
		}
		prior, findErr = s.runtime.ReadRunSummary(tx, run.ID)
		if findErr == nil {
			prior.RuntimeSafety = s.safety()
			found = true
		}
		return findErr
	})
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	if found {
		return prior, nil
	}
	if !s.dispatchEnabled {
		return groupopsport.RunSummary{}, ErrProviderDisabled
	}

	preparedMessages, err := s.prepareWebhookMessageSnapshot(ctx, inbound)
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	var summary groupopsport.RunSummary
	err = s.uow.Within(ctx, func(tx context.Context) error {
		detail, lockErr := s.plans.Lock(tx, planID)
		if lockErr != nil {
			return lockErr
		}
		if groupopsdomain.ValidateDetail(detail) != nil {
			return ErrUnavailable
		}
		sourceKey := webhookRunSourceKey(planID, webhookReference, key)
		run, reserveErr := s.runtime.ReserveRun(tx, groupopsport.RunReservation{PlanID: planID, Trigger: groupopsport.RunTriggerWebhook, SourceKeyDigest: sourceKey, WebhookPayloadDigest: payloadDigest, PlanRevision: detail.Plan.Revision, ScheduledFor: now, AcceptedAt: now, AcceptedBy: "webhook:" + webhookReference})
		if reserveErr != nil {
			return reserveErr
		}
		if run.WebhookPayloadDigest != payloadDigest {
			return ErrConflict
		}
		// A concurrent first request may have completed while this request was
		// resolving local Media facts. Return its sealed result before checking
		// mutable plan state or creating another intent.
		existing, readErr := s.runtime.ReadRunSummary(tx, run.ID)
		if readErr != nil {
			return readErr
		}
		if len(existing.Executions) != 0 || len(existing.PendingIntents) != 0 {
			existing.RuntimeSafety = s.safety()
			summary = existing
			return nil
		}
		if !validDynamicWebhookDetail(detail, webhookReference, inbound.TargetChatReferences) {
			return ErrStateConflict
		}
		materialPlan, contentRaw, materializeErr := s.materializeWebhookMessageSnapshot(tx, detail, run, preparedMessages)
		if materializeErr != nil {
			return materializeErr
		}
		drafts, draftErr := s.buildWebhookDrafts(tx, detail, run, inbound.TargetChatReferences, materialPlan, contentRaw, now)
		if draftErr != nil {
			return draftErr
		}
		if _, draftErr = s.runtime.CreateExecutionIntents(tx, drafts); draftErr != nil {
			return draftErr
		}
		initial, initialErr := s.runtime.InitialExecutionIntents(tx, run.ID)
		if initialErr != nil {
			return initialErr
		}
		for _, draft := range initial {
			projection, receipt, acceptErr := s.effects.AcceptAndQueueWithin(tx, groupOpsEffectAcceptCommand(draft, key))
			if acceptErr != nil {
				return acceptErr
			}
			if projection.ID == "" || projection.QueueJobID < 1 || receipt.ID == "" || receipt.QueueReceiptID == "" {
				return ErrUnavailable
			}
			draft.ExternalEffectID = projection.ID
			if _, insertErr := s.runtime.InsertExecution(tx, draft); insertErr != nil {
				return insertErr
			}
			if bindErr := s.runtime.BindAcceptedExecutionIntent(tx, draft.IntentID, projection.ID); bindErr != nil {
				return bindErr
			}
		}
		summary, readErr = s.runtime.ReadRunSummary(tx, run.ID)
		if readErr != nil {
			return readErr
		}
		summary.RuntimeSafety = s.safety()
		return nil
	})
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	return summary, nil
}

func webhookRunSourceKey(planID int64, webhookReference, key string) [sha256.Size]byte {
	// Deliberately excludes plan revision and payload digest: protocol replay
	// owns payload conflict detection, while this run key means an event can
	// never send twice after a plan edit.
	return sha256.Sum256([]byte(strings.Join([]string{"group-ops.webhook-run.v2", strconv.FormatInt(planID, 10), webhookReference, key}, "\x00")))
}

func webhookPayloadDigest(inbound groupopsport.WebhookInboundCommand) (string, error) {
	raw, err := json.Marshal(inbound)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalRuntimeJSON(raw)
	if err != nil {
		return "", err
	}
	return string(effectport.Hash("group-ops.webhook.payload.v1", string(canonical))), nil
}

func validDynamicWebhookDetail(detail groupopsport.Detail, webhookReference string, targets []string) bool {
	return groupopsdomain.ValidateDetail(detail) == nil && detail.Plan.Type == groupopsport.PlanTypeWebhook && detail.Plan.Status == groupopsport.PlanActive && detail.WebhookDescriptor.Configured && detail.WebhookDescriptor.Reference == webhookReference && contentValidation(detail).Valid && webhookTargetsAreBound(detail.GroupAssets, targets)
}

func webhookTargetsAreBound(bindings []groupopsport.GroupAsset, targets []string) bool {
	bound := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		bound[binding.AssetRef] = struct{}{}
	}
	for _, target := range targets {
		if _, exists := bound[target]; !exists {
			return false
		}
	}
	return true
}

type webhookMessagePreparation struct {
	messageText string
	attachments []webhookPreparedAttachment
}

type webhookPreparedAttachment struct {
	reference groupopsport.MaterialReference
	prepared  *mediaport.PreparedWebhookMiniProgram
}

func (s *RuntimeService) prepareWebhookMessageSnapshot(ctx context.Context, inbound groupopsport.WebhookInboundCommand) (webhookMessagePreparation, error) {
	prepared := webhookMessagePreparation{attachments: make([]webhookPreparedAttachment, 0, len(inbound.Messages))}
	for _, message := range inbound.Messages {
		switch message.Type {
		case "text":
			prepared.messageText = message.Text
		case "image":
			prepared.attachments = append(prepared.attachments, webhookPreparedAttachment{reference: groupopsport.MaterialReference{Kind: "image", ID: message.ImageID}})
		case "file":
			prepared.attachments = append(prepared.attachments, webhookPreparedAttachment{reference: groupopsport.MaterialReference{Kind: "attachment", ID: message.AttachmentID}})
		case "miniprogram":
			if message.MiniProgramID > 0 {
				prepared.attachments = append(prepared.attachments, webhookPreparedAttachment{reference: groupopsport.MaterialReference{Kind: "miniprogram", ID: message.MiniProgramID}})
				continue
			}
			resolver, ok := s.materials.(mediaport.WebhookMiniProgramResolver)
			if !ok || resolver == nil {
				return webhookMessagePreparation{}, ErrMiniProgramCoverResolverUnavailable
			}
			cover, resolveErr := resolver.PrepareWebhookMiniProgram(ctx, mediaport.WebhookMiniProgramRequest{AppID: message.AppID, Path: message.Path, Title: message.Title})
			if errors.Is(resolveErr, mediaport.ErrWebhookMiniProgramUnsupported) {
				return webhookMessagePreparation{}, ErrMiniProgramCoverUnsupported
			}
			if resolveErr != nil {
				return webhookMessagePreparation{}, ErrMiniProgramCoverResolverUnavailable
			}
			prepared.attachments = append(prepared.attachments, webhookPreparedAttachment{prepared: &cover})
		default:
			return webhookMessagePreparation{}, ErrRuntimeInvalid
		}
	}
	return prepared, nil
}

func (s *RuntimeService) materializeWebhookMessageSnapshot(ctx context.Context, detail groupopsport.Detail, run groupopsport.Run, prepared webhookMessagePreparation) (groupopsport.MaterialPlan, json.RawMessage, error) {
	materialPlan := groupopsport.MaterialPlan{References: make([]groupopsport.MaterialReference, 0, len(prepared.attachments))}
	resolver, _ := s.materials.(mediaport.WebhookMiniProgramResolver)
	for index, attachment := range prepared.attachments {
		reference := attachment.reference
		if attachment.prepared != nil {
			if resolver == nil || detail.Plan.UpdatedBy < 1 {
				return groupopsport.MaterialPlan{}, nil, ErrMiniProgramCoverResolverUnavailable
			}
			resolved, err := resolver.MaterializeWebhookMiniProgramWithin(ctx, *attachment.prepared, mediaport.WebhookMiniProgramMaterialization{Actor: detail.Plan.UpdatedBy, IdempotencyKey: webhookLessonMaterializationKey(run.ID, index)})
			if err != nil || resolved.Kind != "miniprogram" || resolved.ID < 1 {
				return groupopsport.MaterialPlan{}, nil, ErrMiniProgramCoverResolverUnavailable
			}
			reference = groupopsport.MaterialReference{Kind: resolved.Kind, ID: resolved.ID}
		}
		materialPlan.References = append(materialPlan.References, reference)
	}
	if groupopsdomain.ValidateMaterialPlan(materialPlan) != nil {
		return groupopsport.MaterialPlan{}, nil, ErrRuntimeInvalid
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version":   2,
		"kind":             "webhook_message",
		"message_text":     prepared.messageText,
		"attachment_order": materialPlan.References,
	})
	if err != nil {
		return groupopsport.MaterialPlan{}, nil, ErrRuntimeInvalid
	}
	canonical, err := canonicalRuntimeJSON(raw)
	if err != nil {
		return groupopsport.MaterialPlan{}, nil, ErrRuntimeInvalid
	}
	return materialPlan, canonical, nil
}

func webhookLessonMaterializationKey(runID int64, index int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{"group-ops.webhook.lesson-card.material.v1", strconv.FormatInt(runID, 10), strconv.Itoa(index)}, "\x00")))
	return "groupops_webhook_lesson_" + hex.EncodeToString(sum[:])
}

func (s *RuntimeService) buildWebhookDrafts(tx context.Context, detail groupopsport.Detail, run groupopsport.Run, targets []string, materialPlan groupopsport.MaterialPlan, contentRaw json.RawMessage, now time.Time) ([]groupopsport.ExecutionDraft, error) {
	materialRaw, _, materialSourceRaw, materialSourceDigest, err := s.resolveMaterialSnapshot(tx, materialPlan, now)
	if err != nil {
		return nil, ErrUnavailable
	}
	materialRaw, err = canonicalRuntimeJSON(materialRaw)
	if err != nil {
		return nil, ErrRuntimeInvalid
	}
	materialDigest := string(effectport.Hash("group-ops.material.snapshot.v1", string(materialRaw)))
	contentDigest := string(effectport.Hash("group-ops.content.snapshot.v1", string(contentRaw)))
	drafts := make([]groupopsport.ExecutionDraft, 0, len(targets))
	for _, target := range targets {
		sender, found, senderErr := s.senders.ResolveExecutionSender(tx, target)
		if senderErr != nil {
			return nil, ErrUnavailable
		}
		if !found || sender == "" {
			return nil, ErrUnavailable
		}
		keyDigest := sha256.Sum256([]byte(strings.Join([]string{"group-ops.webhook.execution.v1", strconv.FormatInt(run.ID, 10), target}, "\x00")))
		drafts = append(drafts, groupopsport.ExecutionDraft{RunID: run.ID, PlanID: run.PlanID, PlanRevision: run.PlanRevision, NodePosition: 1, TargetReference: target, SenderUserID: sender, TargetDigest: string(effectport.Hash("group-ops.target", target)), ContentSnapshot: contentRaw, ContentDigest: contentDigest, MaterialSnapshot: materialRaw, MaterialDigest: materialDigest, MaterialSourceSnapshot: materialSourceRaw, MaterialSourceDigest: materialSourceDigest, ExecutionKeyDigest: keyDigest, ScheduledFor: now, CreatedAt: now})
	}
	if len(drafts) == 0 {
		return nil, ErrStateConflict
	}
	return drafts, nil
}

func canonicalRuntimeJSON(raw []byte) (json.RawMessage, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil, ErrRuntimeInvalid
	}
	normalized, err := json.Marshal(value)
	return json.RawMessage(normalized), err
}

func groupOpsEffectAcceptCommand(draft groupopsport.ExecutionDraft, key string) effectport.AcceptCommand {
	return effectport.AcceptCommand{ReceiptKey: effectport.Hash("group-ops.accept.v1", strconv.FormatInt(draft.PlanID, 10), strconv.FormatInt(draft.RunID, 10), strconv.FormatInt(draft.NodeID, 10), draft.TargetReference, key), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindGroupMessage, SourceRefDigest: effectport.Hash("group-ops.run", strconv.FormatInt(draft.RunID, 10)), TargetRefDigest: effectport.Hash("group-ops.target", draft.TargetReference), PayloadDigest: effectport.Hash("group-ops.payload", draft.ContentDigest, draft.MaterialDigest, draft.SenderUserID), PolicyVersionHash: effectport.Hash("group-ops.policy", "v1")}, ScheduledAt: draft.ScheduledFor}
}

func (s *RuntimeService) buildDrafts(tx context.Context, detail groupopsport.Detail, run groupopsport.Run, existing map[string]struct{}, now time.Time) ([]groupopsport.ExecutionDraft, error) {
	assets := append([]groupopsport.GroupAsset{}, detail.GroupAssets...)
	sort.SliceStable(assets, func(i, j int) bool { return assets[i].AssetRef < assets[j].AssetRef })
	drafts := make([]groupopsport.ExecutionDraft, 0)
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		if !nodeRuntimeEnabled(node) {
			continue
		}
		if node.MaterialRef != "" {
			return nil, ErrStateConflict
		}
		contentRaw, err := json.Marshal(struct {
			SchemaVersion    int32  `json:"schema_version"`
			NodeID           int64  `json:"node_id"`
			Position         int32  `json:"position"`
			Kind             string `json:"kind"`
			DayIndex         int32  `json:"day_index,omitempty"`
			TriggerTimeLabel string `json:"trigger_time_label,omitempty"`
			ActionTitle      string `json:"action_title,omitempty"`
			MessageText      string `json:"message_text,omitempty"`
		}{1, node.ID, node.Position, string(node.Kind), node.DayIndex, node.TriggerTimeLabel, node.ActionTitle, node.MessageText})
		if err != nil {
			return nil, ErrRuntimeInvalid
		}
		contentRaw, err = canonicalRuntimeJSON(contentRaw)
		if err != nil {
			return nil, ErrRuntimeInvalid
		}
		scheduledFor := nodeScheduledFor(now, node, delay)
		materialRaw, _, materialSourceRaw, materialSourceDigest, err := s.resolveMaterialSnapshot(tx, node.MaterialPlan, scheduledFor)
		if err != nil {
			// Material is owned by Media and must be frozen before an EER
			// intent exists. A missing/changed source is an unavailable
			// dependency, not permission to manufacture a kind/id digest.
			return nil, ErrUnavailable
		}
		materialRaw, err = canonicalRuntimeJSON(materialRaw)
		if err != nil {
			return nil, ErrRuntimeInvalid
		}
		materialDigest := string(effectport.Hash("group-ops.material.snapshot.v1", string(materialRaw)))
		contentDigest := string(effectport.Hash("group-ops.content.snapshot.v1", string(contentRaw)))
		for _, asset := range assets {
			key := executionKeyString(node.ID, asset.AssetRef)
			if _, ok := existing[key]; ok {
				continue
			}
			sender, found, err := s.senders.ResolveExecutionSender(tx, asset.AssetRef)
			if err != nil {
				return nil, ErrUnavailable
			}
			if !found || sender == "" {
				// A plan member is an editing scope, not a sender fallback. An
				// owner-less/unknown group must never be guessed.
				return nil, ErrUnavailable
			}
			keyDigest := sha256.Sum256([]byte(strings.Join([]string{"group-ops.execution.v1", strconv.FormatInt(run.ID, 10), strconv.FormatInt(node.ID, 10), asset.AssetRef, strconv.FormatInt(run.PlanRevision, 10)}, "\x00")))
			drafts = append(drafts, groupopsport.ExecutionDraft{RunID: run.ID, PlanID: run.PlanID, PlanRevision: run.PlanRevision, NodeID: node.ID, NodePosition: node.Position, TargetReference: asset.AssetRef, SenderUserID: sender, TargetDigest: string(effectport.Hash("group-ops.target", asset.AssetRef)), ContentSnapshot: contentRaw, ContentDigest: contentDigest, MaterialSnapshot: materialRaw, MaterialDigest: materialDigest, MaterialSourceSnapshot: materialSourceRaw, MaterialSourceDigest: materialSourceDigest, ExecutionKeyDigest: keyDigest, ScheduledFor: scheduledFor, CreatedAt: now})
		}
	}
	if len(drafts) == 0 && len(existing) == 0 {
		return nil, ErrStateConflict
	}
	return drafts, nil
}

func (s *RuntimeService) resolveMaterialSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, json.RawMessage, string, error) {
	if len(plan.References) == 0 {
		raw := json.RawMessage(`{"schema_version":1,"references":[]}`)
		// Keep the empty intent on the same typed Media facts shape used for
		// non-empty plans, so a later readiness reader and JSONB round trip see
		// one canonical schema.
		facts, err := canonicalRuntimeJSON(json.RawMessage(`{"schema_version":1,"sources":{"schema_version":1,"references":[]},"preparations":[]}`))
		if err != nil {
			return nil, "", nil, "", err
		}
		return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), facts, string(effectport.Hash("group-ops.material.intent.v1", string(facts))), nil
	}
	if s == nil || s.materials == nil {
		return nil, "", nil, "", ErrUnavailable
	}
	resolver, ok := s.materials.(groupopsport.MaterialIntentSnapshotResolver)
	if !ok {
		return nil, "", nil, "", ErrUnavailable
	}
	raw, digest, sourceRaw, sourceDigest, err := resolver.ResolveMaterialIntentSnapshot(ctx, plan, requiredThrough)
	if err != nil || !validMaterialSnapshotResult(raw, digest) || len(sourceRaw) == 0 || !json.Valid(sourceRaw) || !effectport.ValidDigest(effectport.Digest(sourceDigest)) {
		return nil, "", nil, "", ErrUnavailable
	}
	canonicalSource, canonicalErr := canonicalRuntimeJSON(sourceRaw)
	if canonicalErr != nil || sourceDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalSource))) {
		return nil, "", nil, "", ErrUnavailable
	}
	return append(json.RawMessage(nil), raw...), digest, canonicalSource, sourceDigest, nil
}

func validMaterialSnapshotResult(raw json.RawMessage, digest string) bool {
	if len(raw) == 0 || !json.Valid(raw) || !effectport.ValidDigest(effectport.Digest(digest)) {
		return false
	}
	return digest == string(effectport.Hash("group-ops.material.snapshot.v1", string(raw)))
}

func (s *RuntimeService) materialBlockers(ctx context.Context, detail groupopsport.Detail, now time.Time) []string {
	blockers := make([]string, 0)
	seen := make(map[string]struct{})
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		if !nodeRuntimeEnabled(node) || len(node.MaterialPlan.References) == 0 {
			continue
		}
		_, _, _, _, err := s.resolveMaterialSnapshot(ctx, node.MaterialPlan, nodeScheduledFor(now, node, delay))
		if err == nil {
			continue
		}
		const code = "material_snapshot_unavailable"
		if _, ok := seen[code]; !ok {
			seen[code] = struct{}{}
			blockers = append(blockers, code)
		}
	}
	return blockers
}

func (s *RuntimeService) ListExecutions(ctx context.Context, planID int64, limit, offset int32) (groupopsport.ExecutionPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || planID < 1 || limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumOffset {
		return groupopsport.ExecutionPage{}, invalidOrUnavailableRuntime(s)
	}
	var items []groupopsport.Execution
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, total, err = s.runtime.ListExecutions(tx, planID, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.ExecutionPage{}, classify(err)
	}
	if items == nil {
		items = []groupopsport.Execution{}
	}
	return groupopsport.ExecutionPage{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: int64(offset)+int64(len(items)) < total, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) ProjectExecutionOutcome(ctx context.Context, command groupopsport.ExecutionOutcomeCommand) (groupopsport.Execution, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ExecutionID < 1 || command.AttemptCount < 0 || !validExecutionState(command.State) || command.DeliveryProven && !command.ProviderAccepted {
		return groupopsport.Execution{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	var result groupopsport.Execution
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = s.runtime.RecordExecutionOutcome(tx, command.ExecutionID, command.State, command.ProviderAccepted, command.DeliveryProven, command.ProviderReceiptDigest, command.AttemptCount, now)
		return err
	})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) ManualReconcile(ctx context.Context, command groupopsport.ManualReconcileCommand) (groupopsport.Execution, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ExecutionID < 1 || command.ActorID < 1 || !validRuntimeKey(command.IdempotencyKey) || !effectport.ValidDigest(effectport.Digest(command.EvidenceDigest)) || command.Generation < 1 || command.Fence < 1 || command.LeaseExpiresAt.IsZero() {
		return groupopsport.Execution{}, ErrRuntimeInvalid
	}
	if s.evidence == nil || s.reconciler == nil {
		return groupopsport.Execution{}, ErrProviderDisabled
	}
	now := s.nowUTC()
	if now.Before(command.LeaseExpiresAt) {
		return groupopsport.Execution{}, ErrConflict
	}
	var existing groupopsport.Execution
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var loadErr error
		existing, loadErr = s.runtime.GetExecution(tx, command.ExecutionID)
		return loadErr
	})
	if err != nil || existing.State != groupopsport.ExecutionOutcomeUnknown || existing.ExternalEffectID == "" {
		if err != nil {
			return groupopsport.Execution{}, classify(err)
		}
		return groupopsport.Execution{}, ErrStateConflict
	}
	// The receipt query is a provider read. It must complete before the local
	// CAS transaction begins, so a slow WeCom page never holds a database lock.
	verified, verifyErr := s.evidence.VerifyReconciliationEvidence(ctx, groupopsport.ReconciliationEvidence{ExecutionID: existing.ID, ExternalEffectID: existing.ExternalEffectID, EvidenceDigest: command.EvidenceDigest})
	if verifyErr != nil || !effectport.ValidDigest(effectport.Digest(verified.EvidenceDigest)) || verified.EvidenceDigest != command.EvidenceDigest {
		return groupopsport.Execution{}, ErrConflict
	}
	var result groupopsport.Execution
	err = s.uow.Within(ctx, func(tx context.Context) error {
		current, err := s.runtime.GetExecution(tx, command.ExecutionID)
		if err != nil {
			return err
		}
		if current.State != groupopsport.ExecutionOutcomeUnknown || current.ExternalEffectID != existing.ExternalEffectID {
			return ErrStateConflict
		}
		if err = s.reconciler.ReconcileExternalEffect(tx, groupopsport.ExternalReconcileCommand{EffectID: existing.ExternalEffectID, ReceiptKey: command.IdempotencyKey, EvidenceDigest: command.EvidenceDigest, ActorID: command.ActorID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt}); err != nil {
			return err
		}
		result, err = s.runtime.ReconcileExecution(tx, current.ID, command.EvidenceDigest, verified.DeliveryProven, now)
		return err
	})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	return result, nil
}

// ReadProviderDelivery records a factual status for an already accepted
// WeCom task. It never changes the EER state and it never turns a missing
// msgid/outcome_unknown attempt into a resendable operation.
func (s *RuntimeService) ReadProviderDelivery(ctx context.Context, command groupopsport.ProviderDeliveryReadCommand) (groupopsport.Execution, error) {
	if s == nil || s.uow == nil || s.runtime == nil || s.plans == nil || command.ExecutionID < 1 || command.ActorID < 1 || !validRuntimeKey(command.IdempotencyKey) {
		return groupopsport.Execution{}, ErrRuntimeInvalid
	}
	reader, ok := s.evidence.(groupopsport.ProviderDeliveryReader)
	if !ok || reader == nil {
		return groupopsport.Execution{}, ErrProviderDisabled
	}
	var existing groupopsport.Execution
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		existing, err = s.runtime.GetExecution(tx, command.ExecutionID)
		return err
	}); err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	if existing.State != groupopsport.ExecutionProviderAccepted || !existing.ProviderAccepted || existing.ExternalEffectID == "" {
		return groupopsport.Execution{}, ErrStateConflict
	}
	// The trusted Provider read is intentionally repeatable. No receipt is
	// reserved before it succeeds, so a timeout or process exit cannot strand
	// an idempotency key in-progress. The final local fact and its receipt are
	// committed in one Unit of Work below.
	task, found, err := reader.ReadProviderDelivery(ctx, groupopsport.ReconciliationEvidence{ExecutionID: existing.ID, ExternalEffectID: existing.ExternalEffectID})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	if found && (task.DeliveryStatus == nil || !effectport.ValidDigest(effectport.Digest(task.DeliveryEvidenceDigest))) {
		return groupopsport.Execution{}, ErrConflict
	}
	payload := sha256.Sum256([]byte(strings.Join([]string{"group-ops.delivery-read.v1", strconv.FormatInt(existing.ID, 10), existing.ExternalEffectID}, "\x00")))
	var result groupopsport.Execution
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.plans.Reserve(tx, "execution_delivery_read", Reservation{ActorScope: "admin:" + strconv.FormatInt(command.ActorID, 10), KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: payload, CreatedAt: s.nowUTC()})
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
				return ErrConflict
			}
			return nil
		}
		current, getErr := s.runtime.GetExecution(tx, command.ExecutionID)
		if getErr != nil {
			return getErr
		}
		if current.State != groupopsport.ExecutionProviderAccepted || current.ExternalEffectID != existing.ExternalEffectID {
			return ErrStateConflict
		}
		if found {
			if recordErr := s.runtime.RecordGroupMessageDelivery(tx, task, task.DeliveryEvidenceDigest); recordErr != nil {
				return recordErr
			}
		}
		result, getErr = s.runtime.GetExecution(tx, command.ExecutionID)
		if getErr != nil {
			return getErr
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		_, completeErr := s.plans.Complete(tx, receipt.ID, raw, s.nowUTC())
		return completeErr
	})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) ListOperationMembers(ctx context.Context, pageSize int32, query string) (groupopsport.OperationMemberPage, error) {
	if s == nil || s.uow == nil || s.staff == nil || pageSize < 1 || pageSize > 100 {
		return groupopsport.OperationMemberPage{}, invalidOrUnavailableRuntime(s)
	}
	reader, ok := s.staff.(groupopsport.EligibleStaffReader)
	if !ok {
		return groupopsport.OperationMemberPage{}, ErrUnavailable
	}
	var items []groupopsport.OperationMember
	err := s.uow.Within(ctx, func(tx context.Context) error {
		local, listErr := reader.ListEligibleStaff(tx)
		if listErr != nil {
			return listErr
		}
		if directory, ok := s.runtime.(groupopsport.OperationMemberDirectoryStore); ok {
			stored, readErr := directory.ListOperationMemberDirectory(tx)
			if readErr != nil {
				return readErr
			}
			items = mergeOperationMemberDirectory(local, stored, len(stored) > 0)
			return nil
		}
		items = normalizeLocalOperationMembers(local)
		return nil
	})
	if err != nil {
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query != "" {
		matched := make([]groupopsport.OperationMember, 0, len(items))
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.DisplayName), query) || strings.Contains(strings.ToLower(item.SenderUserID), query) {
				matched = append(matched, item)
			}
		}
		items = matched
	}
	if len(items) > int(pageSize) {
		items = items[:pageSize]
	}
	if items == nil {
		items = []groupopsport.OperationMember{}
	}
	state, code := operationMemberProfileStatus(items)
	return groupopsport.OperationMemberPage{Scope: "group_ops", Items: items, PageSize: pageSize, ProfileReadState: state, ProfileReadErrorCode: code, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) RefreshOperationMembers(ctx context.Context, command groupopsport.OperationMemberRefreshCommand) (groupopsport.OperationMemberPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ActorID < 1 || !validRuntimeKey(command.IdempotencyKey) || command.PageSize < 1 || command.PageSize > 100 {
		return groupopsport.OperationMemberPage{}, invalidOrUnavailableRuntime(s)
	}
	if s.directory == nil {
		// The local Access-backed GET remains available. Refresh is a
		// provider/source read and must identify its disabled dependency as
		// a deterministic 503 rather than a malformed client command.
		return groupopsport.OperationMemberPage{}, ErrProviderDisabled
	}
	directoryStore, hasDirectoryStore := s.runtime.(groupopsport.OperationMemberDirectoryStore)
	if !hasDirectoryStore {
		return groupopsport.OperationMemberPage{}, ErrUnavailable
	}
	// Provider reads are deliberately outside the UoW. A failed or partial read
	// therefore cannot hold a database transaction or replace the prior local
	// projection.
	items, err := s.directory.RefreshOperationMembers(ctx, command.PageSize)
	if err != nil {
		// Preserve the last verified projection when the source is unavailable.
		// Returning the explicit status is not a successful refresh and creates
		// no receipt, so a stale name cannot masquerade as new provider data.
		page, priorErr := s.operationMembersFromStoredDirectory(ctx, command.PageSize, "unavailable", directoryProfileFailureCode(err))
		if priorErr == nil && len(page.Items) > 0 {
			return page, nil
		}
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	if items == nil || len(items) > int(command.PageSize) || len(items) == 0 {
		return groupopsport.OperationMemberPage{}, ErrConflict
	}
	now := s.nowUTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		raw, marshalErr := json.Marshal(items)
		if marshalErr != nil {
			return marshalErr
		}
		if replaceErr := directoryStore.ReplaceOperationMemberDirectory(tx, items, now); replaceErr != nil {
			return replaceErr
		}
		return s.runtime.RecordDirectoryRefresh(tx, "operation_members", command.ActorID, 0, sha256.Sum256([]byte(command.IdempotencyKey)), string(effectport.Hash("group-ops.operation-members.snapshot", string(raw))), int32(len(items)), true, now)
	})
	if err != nil {
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	return s.operationMembersFromStoredDirectory(ctx, command.PageSize, "", "")
}

func (s *RuntimeService) operationMembersFromStoredDirectory(ctx context.Context, pageSize int32, forcedState, forcedCode string) (groupopsport.OperationMemberPage, error) {
	reader, ok := s.staff.(groupopsport.EligibleStaffReader)
	directory, stored := s.runtime.(groupopsport.OperationMemberDirectoryStore)
	if !ok || !stored {
		return groupopsport.OperationMemberPage{}, ErrUnavailable
	}
	var items []groupopsport.OperationMember
	err := s.uow.Within(ctx, func(tx context.Context) error {
		local, listErr := reader.ListEligibleStaff(tx)
		if listErr != nil {
			return listErr
		}
		projection, readErr := directory.ListOperationMemberDirectory(tx)
		if readErr != nil {
			return readErr
		}
		items = mergeOperationMemberDirectory(local, projection, true)
		return nil
	})
	if err != nil {
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	if len(items) > int(pageSize) {
		items = items[:pageSize]
	}
	if items == nil {
		items = []groupopsport.OperationMember{}
	}
	state, code := operationMemberProfileStatus(items)
	if forcedState != "" {
		state, code = forcedState, forcedCode
		for index := range items {
			items[index].ProfileReadState, items[index].ProfileReadErrorCode = state, code
		}
	}
	return groupopsport.OperationMemberPage{Scope: "group_ops", Items: items, PageSize: pageSize, ProfileReadState: state, ProfileReadErrorCode: code, RuntimeSafety: s.safety()}, nil
}

func normalizeLocalOperationMembers(items []groupopsport.OperationMember) []groupopsport.OperationMember {
	result := make([]groupopsport.OperationMember, 0, len(items))
	for _, item := range items {
		item.Active = true
		if item.NameSource == "" {
			item.NameSource = "local_fallback"
		}
		result = append(result, item)
	}
	return result
}

func mergeOperationMemberDirectory(local, projection []groupopsport.OperationMember, requireProjection bool) []groupopsport.OperationMember {
	byBinding := make(map[string]groupopsport.OperationMember, len(projection))
	for _, item := range projection {
		if item.Active {
			byBinding[strconv.FormatInt(item.StaffID, 10)+"\x00"+item.SenderUserID] = item
		}
	}
	items := make([]groupopsport.OperationMember, 0, len(local))
	for _, item := range local {
		key := strconv.FormatInt(item.StaffID, 10) + "\x00" + item.SenderUserID
		if stored, found := byBinding[key]; found {
			stored.Active = true
			items = append(items, stored)
			continue
		}
		if !requireProjection {
			item.Active = true
			item.NameSource = "local_fallback"
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].StaffID < items[j].StaffID })
	return items
}

func operationMemberProfileStatus(items []groupopsport.OperationMember) (string, string) {
	for _, item := range items {
		if item.ProfileReadState == "unavailable" {
			if item.ProfileReadErrorCode != "" {
				return "unavailable", item.ProfileReadErrorCode
			}
			return "unavailable", "provider_profile_unavailable"
		}
	}
	if len(items) > 0 {
		return "ready", ""
	}
	return "", ""
}

func directoryProfileFailureCode(err error) string {
	type directoryFailure interface{ DirectoryFailureCode() string }
	var failure directoryFailure
	if errors.As(err, &failure) && failure.DirectoryFailureCode() != "" {
		return failure.DirectoryFailureCode()
	}
	return "provider_profile_unavailable"
}

func (s *RuntimeService) ListGroups(ctx context.Context, owner int64, query string, limit, offset int32) (groupopsport.GroupDirectoryPage, error) {
	query = strings.TrimSpace(query)
	if s == nil || s.uow == nil || s.runtime == nil || owner < 0 || !validDirectoryQuery(query) || limit < 1 || limit > 200 || offset < 0 || offset > MaximumOffset {
		return groupopsport.GroupDirectoryPage{}, invalidOrUnavailableRuntime(s)
	}
	var items []groupopsport.GroupDirectoryItem
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, total, err = s.runtime.ListDirectoryGroups(tx, owner, query, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.GroupDirectoryPage{}, classify(err)
	}
	if items == nil {
		items = []groupopsport.GroupDirectoryItem{}
	}
	return groupopsport.GroupDirectoryPage{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: int64(offset)+int64(len(items)) < total, RuntimeSafety: s.safety()}, nil
}

func validDirectoryQuery(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 160
}

func (s *RuntimeService) RefreshGroups(ctx context.Context, command groupopsport.GroupRefreshCommand) (groupopsport.GroupDirectoryPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.OwnerStaffID < 1 || command.ActorID < 1 || command.Limit < 1 || command.Limit > 200 || !validRuntimeKey(command.IdempotencyKey) {
		return groupopsport.GroupDirectoryPage{}, invalidOrUnavailableRuntime(s)
	}
	if s.catalog != nil {
		status, err := s.catalog.RequestCatalogSync(ctx, true)
		if err != nil {
			return groupopsport.GroupDirectoryPage{}, err
		}
		page, err := s.ListGroups(ctx, command.OwnerStaffID, "", command.Limit, 0)
		page.CatalogSync = &status
		return page, err
	}
	if s.directory == nil {
		// No real directory source is wired in the id-dev composition. Do
		// not delete/replace the local projection or guess an owner.
		return groupopsport.GroupDirectoryPage{}, ErrProviderDisabled
	}
	// The full provider snapshot is fetched before the persistence UoW. Only a
	// complete snapshot may replace the current directory, which prevents a
	// paging or transport failure from deleting existing group bindings.
	snapshot, readErr := s.directory.ListOwnedGroups(ctx, command.OwnerStaffID, command.Limit)
	if readErr != nil {
		var diagnostic *GroupDirectoryReadError
		if errors.As(readErr, &diagnostic) {
			return groupopsport.GroupDirectoryPage{}, diagnostic
		}
		return groupopsport.GroupDirectoryPage{}, classify(readErr)
	}
	if !snapshot.Complete {
		return groupopsport.GroupDirectoryPage{}, ErrConflict
	}
	now := s.nowUTC()
	items := append([]groupopsport.GroupDirectoryItem(nil), snapshot.Items...)
	err := s.uow.Within(ctx, func(tx context.Context) error {
		for _, item := range snapshot.Items {
			if item.OwnerStaffID != command.OwnerStaffID || !validOpaqueReference(item.ChatReference) || item.MemberCount < 0 || (item.ExternalMemberCount != nil && (*item.ExternalMemberCount < 0 || *item.ExternalMemberCount > item.MemberCount)) {
				return ErrConflict
			}
		}
		raw, err := json.Marshal(snapshot.Items)
		if err != nil {
			return err
		}
		if err = s.runtime.ReplaceDirectoryGroups(tx, command.OwnerStaffID, snapshot.Items, now); err != nil {
			return err
		}
		if err = s.runtime.RecordDirectoryRefresh(tx, "groups", command.ActorID, command.OwnerStaffID, sha256.Sum256([]byte(command.IdempotencyKey)), string(effectport.Hash("group-ops.groups.snapshot", string(raw))), int32(len(snapshot.Items)), true, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return groupopsport.GroupDirectoryPage{}, classify(err)
	}
	pageItems := items
	if len(pageItems) > int(command.Limit) {
		pageItems = pageItems[:command.Limit]
	}
	return groupopsport.GroupDirectoryPage{Items: pageItems, Total: int64(len(items)), Limit: command.Limit, Offset: 0, HasMore: len(pageItems) < len(items), RuntimeSafety: s.safety()}, nil
}

func countMessageDrafts(detail groupopsport.Detail, existing map[string]struct{}) int {
	count := 0
	for _, node := range detail.Nodes {
		if node.Kind != groupopsport.NodeMessage || !nodeRuntimeEnabled(node) {
			continue
		}
		for _, asset := range detail.GroupAssets {
			if _, ok := existing[executionKeyString(node.ID, asset.AssetRef)]; !ok {
				count++
			}
		}
	}
	return count
}

func nextMessageDue(detail groupopsport.Detail, now time.Time) *time.Time {
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		if !nodeRuntimeEnabled(node) {
			continue
		}
		value := nodeScheduledFor(now, node, delay)
		return &value
	}
	return nil
}

func nodeRuntimeEnabled(node groupopsport.Node) bool {
	return node.Status == "" || node.Status == "active"
}

// nodeScheduledFor anchors standard-plan day/time fields to the accepted run.
// The legacy schema had only relative delay nodes, so a missing schedule keeps
// that behavior. Group joining time is intentionally not inferred: V3 stores
// no authoritative join event for an opaque group directory reference.
func nodeScheduledFor(runAcceptedAt time.Time, node groupopsport.Node, priorDelay time.Duration) time.Time {
	if node.ScheduleSemantics == "relative_delay" || node.ScheduledTime == "" || node.DayIndex < 1 || !validScheduledTime(node.ScheduledTime) {
		return runAcceptedAt.Add(priorDelay)
	}
	hour := int(node.ScheduledTime[0]-'0')*10 + int(node.ScheduledTime[1]-'0')
	minute := int(node.ScheduledTime[3]-'0')*10 + int(node.ScheduledTime[4]-'0')
	china := time.FixedZone("Asia/Shanghai", 8*60*60)
	local := runAcceptedAt.In(china)
	return time.Date(local.Year(), local.Month(), local.Day()+int(node.DayIndex)-1, hour, minute, 0, 0, china).UTC().Add(priorDelay)
}

func executionKeyString(nodeID int64, target string) string {
	return strconv.FormatInt(nodeID, 10) + "\x00" + target
}

func validRuntimeKey(value string) bool {
	return value != "" && len(value) >= 16 && len(value) <= 128 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func validOpaqueReference(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}

func validExecutionState(value groupopsport.ExecutionState) bool {
	switch value {
	case groupopsport.ExecutionAccepted, groupopsport.ExecutionProviderAccepted, groupopsport.ExecutionDeliveryProven, groupopsport.ExecutionOutcomeUnknown, groupopsport.ExecutionReconciled, groupopsport.ExecutionFinalFailed:
		return true
	default:
		return false
	}
}

func invalidOrUnavailableRuntime(s *RuntimeService) error {
	if s == nil || !s.ready() {
		return ErrUnavailable
	}
	return ErrRuntimeInvalid
}

var _ groupopsport.ExecutionOutcomeProjector = (*RuntimeService)(nil)
