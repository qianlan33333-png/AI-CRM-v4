package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func generationDigest(parts ...string) [32]byte {
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func generationEffectDigest(namespace string, value [32]byte) effectport.Digest {
	return effectport.Hash(namespace, hex.EncodeToString(value[:]))
}

func dynamicGenerationEnabled(s *RuntimeService) bool {
	return s != nil && s.generationEffects != nil && s.generationContext != nil && s.generationAgents != nil && s.generationPolicy != nil && s.reviewPlans != nil
}

// confirmDynamicRun freezes all customer context before acceptance, then
// creates the Automation run, every owner item, and every EER/River job in
// exactly one PostgreSQL Unit of Work. The provider cannot see mutable current
// context because it only receives a later dispatch read of those frozen rows.
func (s *RuntimeService) confirmDynamicRun(ctx context.Context, c RunConfirmCommand, preview automationdomain.RunPreview, digest [32]byte, recipients []aiassistantport.RecipientCandidate, payload json.RawMessage, now time.Time) (automationdomain.RuntimeRun, error) {
	if !dynamicGenerationEnabled(s) {
		return automationdomain.RuntimeRun{}, ErrRuntimeNotReady
	}
	published, found, err := s.generationAgents.PublishedGeneration(ctx, automationport.AgentID(c.AgentID), c.AgentPublishedVersion)
	if err != nil {
		return automationdomain.RuntimeRun{}, ErrRuntimeUnavailable
	}
	if !found || !published.Valid() {
		return automationdomain.RuntimeRun{}, ErrRuntimeNotReady
	}
	policy, err := s.generationPolicy.GenerationModelPolicy(ctx)
	if err != nil || !policy.Valid() {
		return automationdomain.RuntimeRun{}, ErrRuntimeNotReady
	}
	contexts := make([]automationport.GenerationContext, len(recipients))
	for index := range recipients {
		contexts[index], err = s.generationContext.FreezeGenerationContext(ctx, recipients[index].CustomerID)
		if err != nil || !contexts[index].Valid() {
			return automationdomain.RuntimeRun{}, ErrRuntimeUnavailable
		}
	}
	var run automationdomain.RuntimeRun
	err = s.runtimeMutation(ctx, "confirm_run", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		run = automationdomain.RuntimeRun{PackageID: c.PackageID, PackageVersion: c.PackageVersion, SnapshotID: c.SnapshotID, AgentID: c.AgentID, AgentPublishedVersion: c.AgentPublishedVersion, BindingVersion: preview.BindingVersion, SenderSetVersion: preview.SenderSetVersion, RuntimeConfigObserved: preview.RuntimeConfigObserved, RuntimeConfigRevision: preview.RuntimeConfigRevision, MaxRecipientsPerRun: preview.MaxRecipientsPerRun, PreviewDigest: digest, State: automationport.RunPreparing, TargetCount: int64(len(recipients)), CreatedBy: c.Actor, CreatedAt: now, UpdatedAt: now}
		created, existing, createErr := s.store.CreateRun(tx, run, nil)
		if createErr != nil || len(existing) != 0 {
			if createErr != nil {
				return run, RuntimeFact{}, createErr
			}
			return run, RuntimeFact{}, ErrRuntimeConflict
		}
		run = created
		items := make([]automationdomain.GenerationItem, len(recipients))
		for index := range recipients {
			contextRaw, _ := json.Marshal(contexts[index])
			policyRaw, _ := json.Marshal(policy)
			source := generationDigest("automation.dynamic-text.source.v1", strconv64(run.ID), hex.EncodeToString(digest[:]), strconv64(int64(recipients[index].CustomerID)), strconv64(recipients[index].StaffID))
			target := generationDigest("automation.dynamic-text.target.v1", strconv64(int64(recipients[index].CustomerID)), strconv64(recipients[index].StaffID))
			payloadDigest := generationDigest("automation.dynamic-text.payload.v1", published.AgentCode, published.RolePrompt, published.TaskPrompt, string(contextRaw))
			policyDigest := generationDigest("automation.dynamic-text.policy.v1", string(policyRaw))
			receipt := generationDigest("automation.dynamic-text.accept.v1", c.IdempotencyKey, strconv64(run.ID), strconv64(int64(recipients[index].CustomerID)), strconv64(recipients[index].StaffID))
			items[index] = automationdomain.GenerationItem{RunID: run.ID, CustomerID: int64(recipients[index].CustomerID), SenderStaffID: recipients[index].StaffID, AgentID: c.AgentID, AgentPublishedVersion: c.AgentPublishedVersion, AgentCode: published.AgentCode, RolePrompt: published.RolePrompt, TaskPrompt: published.TaskPrompt, Context: contexts[index], ModelPolicy: policy, SourceDigest: source, TargetDigest: target, PayloadDigest: payloadDigest, PolicyDigest: policyDigest, ReceiptKeyDigest: receipt, State: "accepted", CreatedAt: now, UpdatedAt: now}
		}
		createdItems, createItemsErr := s.store.CreateGenerationItems(tx, items)
		if createItemsErr != nil || len(createdItems) != len(items) {
			if createItemsErr != nil {
				return run, RuntimeFact{}, createItemsErr
			}
			return run, RuntimeFact{}, ErrRuntimeConflict
		}
		for index := range createdItems {
			item := createdItems[index]
			envelope := effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, SourceRefDigest: generationEffectDigest("automation.dynamic-text.source", item.SourceDigest), TargetRefDigest: generationEffectDigest("automation.dynamic-text.target", item.TargetDigest), PayloadDigest: generationEffectDigest("automation.dynamic-text.payload", item.PayloadDigest), PolicyVersionHash: generationEffectDigest("automation.dynamic-text.policy", item.PolicyDigest)}
			projection, receipt, acceptErr := s.generationEffects.AcceptAndQueueWithin(tx, effectport.AcceptCommand{ReceiptKey: generationEffectDigest("automation.dynamic-text.accept", item.ReceiptKeyDigest), Envelope: envelope})
			if acceptErr != nil || projection.ID == "" || receipt.QueueReceiptID == "" {
				if acceptErr != nil {
					return run, RuntimeFact{}, acceptErr
				}
				return run, RuntimeFact{}, ErrRuntimeUnavailable
			}
			if bindErr := s.store.BindGenerationEffect(tx, item.ID, projection.ID, now); bindErr != nil {
				return run, RuntimeFact{}, bindErr
			}
		}
		if preview.RuntimeConfigObserved {
			frozen := configport.EffectiveSnapshot{Revision: preview.RuntimeConfigRevision, Source: configport.RuntimeSourceEnvironmentDefault, AutomationMaxRecipients: preview.MaxRecipientsPerRun}
			if preview.RuntimeConfigRevision > 0 {
				frozen.Source = configport.RuntimeSourcePublished
			}
			if usageErr := s.recordRuntimeConfigUsage(tx, frozen, "api", "confirm_dynamic", "automation_run", run.ID, now); usageErr != nil {
				return run, RuntimeFact{}, usageErr
			}
		}
		return run, runtimeFact("run", run.ID, "confirm_dynamic", "automation.run.dynamic_generation_queued.v1", c.Actor, c.IdempotencyKey, now), nil
	}, &run)
	return run, runtimeClassify(err)
}

func strconv64(value int64) string { return fmt.Sprintf("%d", value) }

// GenerationDispatch is the read-only Owner bridge used by the provider
// adapter after EER has durably recorded an attempted call.
func (s *RuntimeService) GenerationDispatch(ctx context.Context, effectID string) (automationport.GenerationDispatch, bool, error) {
	if s == nil || !strings.HasPrefix(effectID, "eer_") {
		return automationport.GenerationDispatch{}, false, ErrRuntimeInvalid
	}
	var item automationdomain.GenerationItem
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		item, readErr = s.store.GenerationByEffect(tx, effectID)
		return readErr
	})
	if err != nil {
		if err == ErrRuntimeNotFound {
			return automationport.GenerationDispatch{}, false, nil
		}
		return automationport.GenerationDispatch{}, false, runtimeClassify(err)
	}
	if item.State != "queued" || item.EffectID != effectID {
		return automationport.GenerationDispatch{}, false, nil
	}
	return automationport.GenerationDispatch{ItemID: item.ID, RunID: item.RunID, EffectID: effectID, AgentCode: item.AgentCode, RolePrompt: item.RolePrompt, TaskPrompt: item.TaskPrompt, Context: item.Context, ModelPolicy: item.ModelPolicy, PayloadDigest: generationEffectDigest("automation.dynamic-text.payload", item.PayloadDigest), AcceptedAt: item.CreatedAt}, true, nil
}

// CompleteGeneration is the Automation side of the EER completion router. It
// stores a valid model result and atomically creates exactly one existing AI
// Assistant pending-review plan when the last item has settled successfully.
func (s *RuntimeService) CompleteGeneration(ctx context.Context, completion automationport.GenerationCompletion) error {
	if s == nil || s.reviewPlans == nil {
		return ErrRuntimeNotReady
	}
	_, run, createPlan, err := s.store.SettleGeneration(ctx, completion)
	if err != nil || !createPlan {
		return err
	}
	items, err := s.store.GenerationItemsForPlan(ctx, run.ID)
	if err != nil || len(items) == 0 {
		if err != nil {
			return err
		}
		return ErrRuntimeConflict
	}
	recipients := make([]aiassistantport.RecipientCandidate, 0, len(items))
	for _, generated := range items {
		if generated.GeneratedText == "" {
			return ErrRuntimeConflict
		}
		recipients = append(recipients, aiassistantport.RecipientCandidate{CustomerID: customerdomain.CustomerID(generated.CustomerID), StaffID: generated.SenderStaffID, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: generated.GeneratedText}}})
	}
	source := effectport.Hash("automation.dynamic-text.review-plan.v1", strconv64(run.ID), hex.EncodeToString(run.PreviewDigest[:]))
	plan, err := s.reviewPlans.CreatePlanWithin(ctx, aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: run.CreatedBy}, IdempotencyKey: "automation-dynamic-review-" + strconv64(run.ID), Name: "Audience dynamic text " + strconv64(run.PackageID), SourceKind: "automation.dynamic_text_generation.v1", SourceDigest: source, Recipients: recipients, OccurredAt: completion.CompletedAt.UTC()})
	if err != nil || plan.Plan.ID < 1 {
		if err != nil {
			return err
		}
		return ErrRuntimeUnavailable
	}
	if err = s.store.AttachGenerationPlan(ctx, run.ID, int64(plan.Plan.ID), completion.CompletedAt); err != nil {
		return err
	}
	return nil
}

func (s *RuntimeService) GenerationItems(ctx context.Context, runID, cursor int64, limit int) ([]automationport.GenerationItem, string, error) {
	if s == nil || runID < 1 {
		return nil, "", ErrRuntimeInvalid
	}
	var items []automationport.GenerationItem
	var next string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		items, next, readErr = s.store.GenerationItems(tx, runID, cursor, limit)
		return readErr
	})
	return items, next, runtimeClassify(err)
}

var _ automationport.GenerationDispatchReader = (*RuntimeService)(nil)
var _ automationport.GenerationCompletionWriter = (*RuntimeService)(nil)
