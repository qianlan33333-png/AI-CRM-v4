package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type policyGenerationInput struct {
	agent   automationport.PublishedGeneration
	context automationport.GenerationContext
	model   automationport.GenerationModelPolicy
}

func (s *RuntimeService) preparePolicyGeneration(ctx context.Context, customerID customerdomain.CustomerID, configuration segmentport.ExecutionConfiguration) (*policyGenerationInput, error) {
	if !dynamicGenerationEnabled(s) || s.messages == nil {
		return nil, ErrRuntimeNotReady
	}
	if _, ok := s.contentFreezer.(automationport.GeneratedOutboundContentFreezer); !ok {
		return nil, ErrRuntimeNotReady
	}
	agent, found, err := s.generationAgents.PublishedGeneration(ctx, automationport.AgentID(configuration.AgentID), configuration.AgentPublishedVersion)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	if !found || !agent.Valid() || agent.ContentDigest != configuration.ContentDigest {
		return nil, ErrRuntimeNotReady
	}
	model, err := s.generationPolicy.GenerationModelPolicy(ctx)
	if err != nil || !model.Valid() {
		return nil, ErrRuntimeNotReady
	}
	value, err := s.generationContext.FreezeGenerationContext(ctx, customerID)
	if err != nil || !value.Valid() || value.PolicyDelivery != nil {
		return nil, ErrRuntimeUnavailable
	}
	return &policyGenerationInput{agent: agent, context: value, model: model}, nil
}

// Called only in the member-event accepting UoW, after its original once
// reservation. No model call occurs here. The same owner item and EER job used
// by manual Agent runs carry this one automatic continuation.
func (s *RuntimeService) acceptEnrollmentGeneration(ctx context.Context, event segmentport.MemberEnteredV1, eventDigest [32]byte, version automationdomain.PolicyVersion, configuration segmentport.ExecutionConfiguration, input policyGenerationInput, enrollment automationdomain.Enrollment, actionDigest [32]byte, runtimeConfig configport.EffectiveSnapshot, actor int64, now time.Time) error {
	if len(configuration.SenderStaffIDs) == 0 || !validRuntimeConfigSnapshot(runtimeConfig) || runtimeConfig.AutomationMaxRecipients < 1 {
		return ErrRuntimeNotReady
	}
	freezer := s.contentFreezer.(automationport.GeneratedOutboundContentFreezer)
	template, templateDigest, err := freezer.FreezeGenerationTemplate(ctx, automationport.OutboundPublishedContent{AgentID: input.agent.AgentID, PublishedVersion: input.agent.PublishedVersion, Content: input.agent.Materials, ContentDigest: input.agent.ContentDigest, MaterialsDigest: input.agent.MaterialsDigest})
	if err != nil || len(template) == 0 || sha256.Sum256(template) != templateDigest {
		return ErrRuntimeNotReady
	}
	previewDigest := sha256.Sum256(append(append(append([]byte{}, eventDigest[:]...), version.Digest[:]...), actionDigest[:]...))
	run := automationdomain.RuntimeRun{PolicyID: version.PolicyID, PolicyVersion: version.Version, PackageID: int64(event.PackageID), PackageVersion: configuration.PackageVersion, SnapshotID: int64(event.SnapshotID), AgentID: configuration.AgentID, AgentPublishedVersion: configuration.AgentPublishedVersion, BindingVersion: configuration.BindingVersion, SenderSetVersion: configuration.SenderSetVersion, RuntimeConfigObserved: true, RuntimeConfigRevision: runtimeConfig.Revision, MaxRecipientsPerRun: runtimeConfig.AutomationMaxRecipients, PreviewDigest: previewDigest, State: automationport.RunPreparing, TargetCount: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	senderIndex := int((int64(event.CustomerID) - 1) % int64(len(configuration.SenderStaffIDs)))
	created, recipients, err := s.store.CreateRun(ctx, run, []automationdomain.RuntimeRecipient{{CustomerID: int64(event.CustomerID), SenderStaffID: configuration.SenderStaffIDs[senderIndex], State: automationport.RecipientAccepted}})
	if err != nil {
		return err
	}
	if len(recipients) != 1 || recipients[0].ID < 1 {
		return ErrRuntimeConflict
	}
	recipient := recipients[0]
	value := input.context
	value.PolicyDelivery = &automationport.GeneratedPolicyDelivery{EnrollmentID: enrollment.ID, RecipientID: recipient.ID, ContentTemplate: template, TemplateDigest: templateDigest, PolicyDigest: version.Digest, QuietHours: version.QuietHours}
	contextRaw, err := json.Marshal(value)
	if err != nil || !value.Valid() {
		return ErrRuntimeConflict
	}
	policyRaw, _ := json.Marshal(input.model)
	source := generationDigest("automation.policy-generation.source.v1", strconv64(enrollment.ID), strconv64(recipient.ID))
	target := generationDigest("automation.policy-generation.target.v1", strconv64(recipient.CustomerID), strconv64(recipient.SenderStaffID))
	payload := generationDigest("automation.policy-generation.payload.v1", input.agent.AgentCode, input.agent.RolePrompt, input.agent.TaskPrompt, string(contextRaw))
	policy := generationDigest("automation.policy-generation.model-policy.v1", string(policyRaw))
	key := generationDigest("automation.policy-generation.accept.v1", strconv64(enrollment.ID), strconv64(recipient.ID))
	items, err := s.store.CreateGenerationItems(ctx, []automationdomain.GenerationItem{{RunID: created.ID, CustomerID: recipient.CustomerID, SenderStaffID: recipient.SenderStaffID, AgentID: configuration.AgentID, AgentPublishedVersion: configuration.AgentPublishedVersion, AgentCode: input.agent.AgentCode, RolePrompt: input.agent.RolePrompt, TaskPrompt: input.agent.TaskPrompt, Context: value, ModelPolicy: input.model, SourceDigest: source, TargetDigest: target, PayloadDigest: payload, PolicyDigest: policy, ReceiptKeyDigest: key, State: "accepted", CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		return err
	}
	if len(items) != 1 {
		return ErrRuntimeConflict
	}
	envelope := effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, SourceRefDigest: generationEffectDigest("automation.dynamic-text.source", source), TargetRefDigest: generationEffectDigest("automation.dynamic-text.target", target), PayloadDigest: generationEffectDigest("automation.dynamic-text.payload", payload), PolicyVersionHash: generationEffectDigest("automation.dynamic-text.policy", policy)}
	projection, receipt, err := s.generationEffects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: generationEffectDigest("automation.dynamic-text.accept", key), Envelope: envelope})
	if err != nil {
		return err
	}
	if projection.ID == "" || receipt.QueueReceiptID == "" {
		return ErrRuntimeUnavailable
	}
	if err = s.store.BindGenerationEffect(ctx, items[0].ID, projection.ID, now); err != nil {
		return err
	}
	if err = s.recordRuntimeConfigUsage(ctx, runtimeConfig, "worker", "policy_generation", "automation_run", created.ID, now); err != nil {
		return err
	}
	factPayload, _ := json.Marshal(map[string]any{"run_id": created.ID, "enrollment_id": enrollment.ID, "policy_id": version.PolicyID, "generation_effect_id": projection.ID})
	return s.store.AppendRuntimeFact(ctx, runtimeFact("run", created.ID, "enroll", "automation.run.policy_generation_queued.v1", actor, hex.EncodeToString(previewDigest[:]), now, factPayload))
}

// EER invokes this inside the generation completion transaction. Output and
// the outbound acceptance therefore settle together or roll back together.
func (s *RuntimeService) completePolicyGeneration(ctx context.Context, run automationdomain.RuntimeRun, now time.Time) error {
	freezer, ok := s.contentFreezer.(automationport.GeneratedOutboundContentFreezer)
	if !ok || s.messages == nil || run.TargetCount != 1 {
		return ErrRuntimeNotReady
	}
	items, err := s.store.GenerationItemsForPlan(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(items) != 1 {
		return ErrRuntimeConflict
	}
	item := items[0]
	delivery := item.Context.PolicyDelivery
	if delivery == nil || !item.Context.Valid() || item.RunID != run.ID || item.AgentID != run.AgentID || item.AgentPublishedVersion != run.AgentPublishedVersion || !automationport.ValidGeneratedText(item.GeneratedText, item.RolePrompt, item.TaskPrompt) {
		return ErrRuntimeConflict
	}
	content, digest, err := freezer.CompleteGeneratedContent(delivery.ContentTemplate, delivery.TemplateDigest, item.GeneratedText)
	if err != nil || sha256.Sum256(content) != digest {
		return ErrRuntimeConflict
	}
	sourceDigest := sha256.Sum256([]byte(fmt.Sprintf("automation-enrollment:%d:recipient:%d", delivery.EnrollmentID, delivery.RecipientID)))
	targetDigest := sha256.Sum256([]byte(fmt.Sprintf("customer:%d", item.CustomerID)))
	accepted, err := s.messages.AcceptMessageWithin(ctx, outboundport.MessageIntent{SourceKind: "automation_enrollment", SourceID: delivery.EnrollmentID, RunRecipientID: delivery.RecipientID, CustomerID: customerdomain.CustomerID(item.CustomerID), SenderStaffID: item.SenderStaffID, AgentID: item.AgentID, AgentPublishedVersion: item.AgentPublishedVersion, ContentReference: fmt.Sprintf("automation-agent:%d:published:%d:enrollment:%d:recipient:%d", item.AgentID, item.AgentPublishedVersion, delivery.EnrollmentID, delivery.RecipientID), SourceDigest: sourceDigest, TargetDigest: targetDigest, PayloadDigest: digest, ContentSnapshot: content, ContentSnapshotDigest: digest, PolicyDigest: delivery.PolicyDigest, ReceiptKey: fmt.Sprintf("automation-enrollment-%d-recipient-%d", delivery.EnrollmentID, delivery.RecipientID), ScheduledAt: nextAllowedExecution(now, delivery.QuietHours)})
	if err != nil {
		return err
	}
	if err = s.store.BindRecipientEffect(ctx, delivery.RecipientID, accepted.EffectID, now); err != nil {
		return err
	}
	if err = s.store.StartGeneratedPolicyRun(ctx, run.ID, now); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"run_id": run.ID, "enrollment_id": delivery.EnrollmentID, "recipient_id": delivery.RecipientID, "effect_id": accepted.EffectID})
	return s.store.AppendRuntimeFact(ctx, runtimeFact("run", run.ID, "generation_complete", "automation.run.generated_outbound_queued.v1", run.CreatedBy, "automation-generated-send-"+strconv64(run.ID), now, payload))
}
