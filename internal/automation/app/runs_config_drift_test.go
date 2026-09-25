package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type directRuntimeUOW struct{}

func (directRuntimeUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type runtimeConfigSequence struct {
	snapshots []configport.EffectiveSnapshot
	reads     int
}

func (r *runtimeConfigSequence) EffectiveSnapshot(context.Context) (configport.EffectiveSnapshot, error) {
	return r.current(), nil
}

func (r *runtimeConfigSequence) EffectiveSnapshotWithin(context.Context) (configport.EffectiveSnapshot, error) {
	snapshot := r.current()
	r.reads++
	return snapshot, nil
}

func (r *runtimeConfigSequence) current() configport.EffectiveSnapshot {
	index := r.reads
	if index >= len(r.snapshots) {
		index = len(r.snapshots) - 1
	}
	return r.snapshots[index]
}

type previewDriftAudience struct {
	configuration segmentport.ExecutionConfiguration
}

func (r previewDriftAudience) AudienceExecutionConfiguration(context.Context, segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	return r.configuration, nil
}

type previewDriftSnapshots struct{ members []segmentport.Member }

func (r previewDriftSnapshots) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (r previewDriftSnapshots) Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (r previewDriftSnapshots) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{Items: append([]segmentport.Member(nil), r.members...)}, nil
}

type previewDriftStore struct {
	RuntimeStore
	preview         automationdomain.RunPreview
	factCount       int
	completionCount int
	runCount        int
	itemCount       int
}

func (s *previewDriftStore) CreatePreview(_ context.Context, preview automationdomain.RunPreview) (automationdomain.RunPreview, error) {
	preview.ID = 91
	s.preview = preview
	return preview, nil
}
func (s *previewDriftStore) PreviewByDigest(_ context.Context, digest [32]byte) (automationdomain.RunPreview, error) {
	if s.preview.ID == 0 || s.preview.PreviewDigest != digest {
		return automationdomain.RunPreview{}, ErrRuntimeNotFound
	}
	return s.preview, nil
}
func (s *previewDriftStore) RuntimeReceipt(context.Context, string, string, [32]byte, [32]byte) (RuntimeReceipt, bool, error) {
	return RuntimeReceipt{}, false, nil
}
func (s *previewDriftStore) ReserveRuntime(context.Context, RuntimeReservation) (RuntimeReceipt, bool, error) {
	return RuntimeReceipt{ID: 92, State: "pending"}, true, nil
}
func (s *previewDriftStore) CompleteRuntime(context.Context, int64, json.RawMessage, time.Time) error {
	s.completionCount++
	return nil
}
func (s *previewDriftStore) AppendRuntimeFact(_ context.Context, fact RuntimeFact) error {
	s.factCount++
	return nil
}
func (s *previewDriftStore) CreateRun(_ context.Context, run automationdomain.RuntimeRun, recipients []automationdomain.RuntimeRecipient) (automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient, error) {
	s.runCount++
	run.ID = 93
	return run, recipients, nil
}
func (s *previewDriftStore) CreateGenerationItems(_ context.Context, items []automationdomain.GenerationItem) ([]automationdomain.GenerationItem, error) {
	s.itemCount += len(items)
	return items, nil
}
func (*previewDriftStore) BindGenerationEffect(context.Context, int64, string, time.Time) error {
	return nil
}

type previewDriftReviewPlans struct {
	aiassistantport.TransactionalIntake
	aiassistantport.Reader
	calls int
}

func (s *previewDriftReviewPlans) CreatePlanWithin(context.Context, aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	s.calls++
	return aiassistantport.CreatePlanResult{Plan: aiassistantport.Plan{ID: 94}}, nil
}

type previewDriftContent struct {
	content automationport.OutboundPublishedContent
	found   bool
}

func (r previewDriftContent) OutboundPublishedContent(context.Context, automationport.AgentID, int64) (automationport.OutboundPublishedContent, bool, error) {
	return r.content, r.found, nil
}

type previewDriftGenerationEffects struct{ calls int }

func (s *previewDriftGenerationEffects) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	s.calls++
	return effectport.Projection{ID: "eer_95", State: effectport.StateQueued}, effectport.Receipt{QueueReceiptID: "queue_96"}, nil
}

type previewDriftGenerationContexts struct{}

func (previewDriftGenerationContexts) FreezeGenerationContext(context.Context, customerdomain.CustomerID) (automationport.GenerationContext, error) {
	return automationport.GenerationContext{}, nil
}

type previewDriftGenerationAgents struct{}

func (previewDriftGenerationAgents) PublishedGeneration(_ context.Context, id automationport.AgentID, version int64) (automationport.PublishedGeneration, bool, error) {
	return automationport.PublishedGeneration{AgentID: id, PublishedVersion: version, AgentCode: "dynamic_text", RolePrompt: "简洁地帮助客户", TaskPrompt: "根据已知信息给出建议"}, true, nil
}

type previewDriftGenerationPolicy struct{}

func (previewDriftGenerationPolicy) GenerationModelPolicy(context.Context) (automationport.GenerationModelPolicy, error) {
	return automationport.GenerationModelPolicy{Mode: "enabled", Endpoint: "https://model.example.test/chat/completions", Model: "test-model", Temperature: 0.4}, nil
}

func TestConfirmRunRejectsRuntimeConfigDriftForFixedAndDynamicContent(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		name := "fixed_content"
		if dynamic {
			name = "dynamic_generation"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
			oldConfig := configport.EffectiveSnapshot{Revision: 8, Source: configport.RuntimeSourcePublished, AutomationMaxRecipients: 10}
			currentConfig := configport.EffectiveSnapshot{Revision: 9, Source: configport.RuntimeSourcePublished, AutomationMaxRecipients: 2}
			configReader := &runtimeConfigSequence{snapshots: []configport.EffectiveSnapshot{oldConfig, currentConfig}}
			store := &previewDriftStore{}
			members := []segmentport.Member{
				{SnapshotID: 71, CustomerID: customerdomain.CustomerID(101)},
				{SnapshotID: 71, CustomerID: customerdomain.CustomerID(102)},
				{SnapshotID: 71, CustomerID: customerdomain.CustomerID(103)},
			}
			contentDigest := [32]byte{1}
			audience := previewDriftAudience{configuration: segmentport.ExecutionConfiguration{
				PackageID: 17, PackageVersion: 4, ConfigurationVersionID: 5,
				Snapshot: segmentport.Snapshot{ID: 71, PackageID: 17, MemberCount: int64(len(members)), State: segmentport.SnapshotPublished},
				AgentID:  23, AgentPublishedVersion: 6, ContentDigest: contentDigest,
				BindingVersion: 7, SenderSetVersion: 8, SenderStaffIDs: []int64{31}, Ready: true,
			}}
			service, err := NewRuntimeServiceWithRuntimeConfig(directRuntimeUOW{}, store, audience, previewDriftSnapshots{members: members}, configReader, nil)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			plans := &previewDriftReviewPlans{}
			content := previewDriftContent{found: !dynamic, content: automationport.OutboundPublishedContent{
				AgentID: automationport.AgentID(audience.configuration.AgentID), PublishedVersion: audience.configuration.AgentPublishedVersion,
				Content: automationport.FixedContentPackage{ContentText: "预览批准后仍须再次核验当前设置。"}, ContentDigest: contentDigest,
			}}
			if err = service.SetReviewPlanIntake(plans, content); err != nil {
				t.Fatal(err)
			}
			effects := &previewDriftGenerationEffects{}
			if dynamic {
				if err = service.SetDynamicGenerationDependencies(effects, previewDriftGenerationContexts{}, previewDriftGenerationAgents{}, previewDriftGenerationPolicy{}); err != nil {
					t.Fatal(err)
				}
			}

			preview, err := service.CreateBroadcastPreview(ctx, 17, 42)
			if err != nil {
				t.Fatalf("create preview: %v", err)
			}
			if preview.RuntimeConfigRevision != oldConfig.Revision || preview.MaxRecipientsPerRun != oldConfig.AutomationMaxRecipients {
				t.Fatalf("preview did not freeze Config: %+v", preview)
			}
			factsBeforeConfirm := store.factCount
			_, err = service.ConfirmRun(ctx, RunConfirmCommand{
				PackageID: 17, PackageVersion: audience.configuration.PackageVersion,
				SnapshotID: int64(audience.configuration.Snapshot.ID), AgentID: audience.configuration.AgentID,
				AgentPublishedVersion: audience.configuration.AgentPublishedVersion,
				PreviewDigest:         hex.EncodeToString(preview.PreviewDigest[:]), Actor: 42, IdempotencyKey: "config-drift-confirm-0001",
			})
			if !errors.Is(err, ErrRuntimeConflict) {
				t.Fatalf("stale preview confirm error=%v, want ErrRuntimeConflict", err)
			}
			if store.runCount != 0 || store.itemCount != 0 || store.completionCount != 0 || store.factCount != factsBeforeConfirm || plans.calls != 0 || effects.calls != 0 {
				t.Fatalf("stale preview created effects: runs=%d generation_items=%d runtime_receipts=%d facts=%d review_plans=%d external_effects=%d", store.runCount, store.itemCount, store.completionCount, store.factCount-factsBeforeConfirm, plans.calls, effects.calls)
			}
		})
	}
}

var _ platformport.UnitOfWork = directRuntimeUOW{}
