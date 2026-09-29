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
	calls   int
	command aiassistantport.CreatePlanCommand
}

func (s *previewDriftReviewPlans) CreatePlanWithin(_ context.Context, command aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	s.calls++
	s.command = command
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

type previewExecutionConfigurationSequence struct {
	items []segmentport.ExecutionConfiguration
	reads int
}

func (r *previewExecutionConfigurationSequence) AudienceExecutionConfiguration(_ context.Context, _ segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	index := r.reads
	r.reads++
	if index >= len(r.items) {
		index = len(r.items) - 1
	}
	return r.items[index], nil
}

type previewScopeSnapshots struct {
	members     []segmentport.Member
	memberReads []segmentport.SnapshotID
}

func (r *previewScopeSnapshots) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (r *previewScopeSnapshots) Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (r *previewScopeSnapshots) Members(_ context.Context, id segmentport.SnapshotID, _ string, _ int) (segmentport.MemberPage, error) {
	r.memberReads = append(r.memberReads, id)
	return segmentport.MemberPage{Items: append([]segmentport.Member(nil), r.members...)}, nil
}

func previewFreezeConfiguration() segmentport.ExecutionConfiguration {
	return segmentport.ExecutionConfiguration{
		PackageID: 17, PackageVersion: 4, ConfigurationVersionID: 5,
		Snapshot: segmentport.Snapshot{ID: 71, PackageID: 17, MemberCount: 3, State: segmentport.SnapshotPublished},
		AgentID:  23, AgentPublishedVersion: 6, ContentDigest: [32]byte{1},
		BindingVersion: 7, SenderSetVersion: 8, SenderStaffIDs: []int64{31, 32}, Ready: true,
	}
}

func previewFreezeMembers() []segmentport.Member {
	return []segmentport.Member{
		{SnapshotID: 71, CustomerID: customerdomain.CustomerID(101)},
		{SnapshotID: 71, CustomerID: customerdomain.CustomerID(102)},
		{SnapshotID: 71, CustomerID: customerdomain.CustomerID(103)},
	}
}

func newPreviewFreezeFixture(t *testing.T, configs []segmentport.ExecutionConfiguration, members []segmentport.Member, now *time.Time) (*RuntimeService, *previewDriftStore, *previewDriftReviewPlans, *previewScopeSnapshots) {
	t.Helper()
	store := &previewDriftStore{}
	audience := &previewExecutionConfigurationSequence{items: configs}
	snapshots := &previewScopeSnapshots{members: members}
	config := configport.EffectiveSnapshot{Revision: 8, Source: configport.RuntimeSourcePublished, AutomationMaxRecipients: 10}
	configReader := &runtimeConfigSequence{snapshots: []configport.EffectiveSnapshot{config, config}}
	service, err := NewRuntimeServiceWithRuntimeConfig(directRuntimeUOW{}, store, audience, snapshots, configReader, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return *now }
	plans := &previewDriftReviewPlans{}
	content := previewDriftContent{found: true, content: automationport.OutboundPublishedContent{
		AgentID: automationport.AgentID(configs[0].AgentID), PublishedVersion: configs[0].AgentPublishedVersion,
		Content: automationport.FixedContentPackage{ContentText: "预览批准后仍须再次核验当前设置。"}, ContentDigest: configs[0].ContentDigest,
	}}
	if err = service.SetReviewPlanIntake(plans, content); err != nil {
		t.Fatal(err)
	}
	return service, store, plans, snapshots
}

func confirmPreviewCommand(preview automationdomain.RunPreview) RunConfirmCommand {
	return RunConfirmCommand{
		PackageID: preview.PackageID, PackageVersion: preview.PackageVersion,
		SnapshotID: preview.SnapshotID, AgentID: preview.AgentID,
		AgentPublishedVersion: preview.AgentPublishedVersion, PreviewDigest: PreviewDigestString(preview),
		Actor: 42, IdempotencyKey: "preview-freeze-confirm-0001",
	}
}

func TestPreviewFreezeKeepsSnapshotRecipientsAndSenderAssignmentsUntilExpiry(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	configuration := previewFreezeConfiguration()
	service, store, plans, snapshots := newPreviewFreezeFixture(t, []segmentport.ExecutionConfiguration{configuration, configuration}, previewFreezeMembers(), &now)
	preview, err := service.CreateBroadcastPreview(context.Background(), 17, 42)
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}
	if preview.ExpiresAt != now.Add(15*time.Minute) || preview.PackageVersion != configuration.PackageVersion || preview.ConfigurationVersionID != int64(configuration.ConfigurationVersionID) || preview.SnapshotID != int64(configuration.Snapshot.ID) || preview.AgentID != configuration.AgentID || preview.AgentPublishedVersion != configuration.AgentPublishedVersion || preview.BindingVersion != configuration.BindingVersion || preview.SenderSetVersion != configuration.SenderSetVersion || preview.TargetCount != 3 {
		t.Fatalf("preview did not freeze its scope/version/window: %+v", preview)
	}

	now = preview.ExpiresAt.Add(-time.Second)
	run, err := service.ConfirmRun(context.Background(), confirmPreviewCommand(preview))
	if err != nil || run.State != automationport.RunPendingReview || run.AIPlanID != 94 || store.runCount != 1 || plans.calls != 1 {
		t.Fatalf("confirmation run=%+v run_count=%d plan_calls=%d err=%v", run, store.runCount, plans.calls, err)
	}
	if len(snapshots.memberReads) != 1 || snapshots.memberReads[0] != segmentport.SnapshotID(preview.SnapshotID) {
		t.Fatalf("snapshot member reads=%v, want only frozen snapshot %d", snapshots.memberReads, preview.SnapshotID)
	}
	want := []aiassistantport.RecipientCandidate{
		{CustomerID: 101, StaffID: 31, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "预览批准后仍须再次核验当前设置。"}}},
		{CustomerID: 102, StaffID: 32, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "预览批准后仍须再次核验当前设置。"}}},
		{CustomerID: 103, StaffID: 31, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "预览批准后仍须再次核验当前设置。"}}},
	}
	if plans.command.IdempotencyKey != "automation-manual-review-preview-freeze-confirm-0001" || len(plans.command.Recipients) != len(want) {
		t.Fatalf("review plan command=%+v", plans.command)
	}
	for index := range want {
		got := plans.command.Recipients[index]
		if got.CustomerID != want[index].CustomerID || got.StaffID != want[index].StaffID || len(got.Content) != 1 || got.Content[0] != want[index].Content[0] {
			t.Fatalf("review recipient[%d]=%+v want=%+v", index, got, want[index])
		}
	}
}

func TestPreviewFreezeRejectsScopeAgentSenderAndPackageVersionDrift(t *testing.T) {
	tests := []struct {
		name   string
		change func(*segmentport.ExecutionConfiguration)
	}{
		{name: "customer_snapshot", change: func(value *segmentport.ExecutionConfiguration) { value.Snapshot.ID++ }},
		{name: "package_configuration", change: func(value *segmentport.ExecutionConfiguration) { value.PackageVersion++ }},
		{name: "agent_published_version", change: func(value *segmentport.ExecutionConfiguration) { value.AgentPublishedVersion++ }},
		{name: "sender_binding_version", change: func(value *segmentport.ExecutionConfiguration) { value.BindingVersion++ }},
		{name: "sender_set_version", change: func(value *segmentport.ExecutionConfiguration) { value.SenderSetVersion++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
			original := previewFreezeConfiguration()
			current := original
			test.change(&current)
			service, store, plans, _ := newPreviewFreezeFixture(t, []segmentport.ExecutionConfiguration{original, current}, previewFreezeMembers(), &now)
			preview, err := service.CreateBroadcastPreview(context.Background(), 17, 42)
			if err != nil {
				t.Fatalf("create preview: %v", err)
			}
			factsBeforeConfirm := store.factCount
			now = now.Add(time.Minute)
			if _, err = service.ConfirmRun(context.Background(), confirmPreviewCommand(preview)); !errors.Is(err, ErrRuntimeConflict) {
				t.Fatalf("drift confirmation err=%v, want ErrRuntimeConflict", err)
			}
			if store.runCount != 0 || store.itemCount != 0 || store.completionCount != 0 || store.factCount != factsBeforeConfirm || plans.calls != 0 {
				t.Fatalf("drift created business results: runs=%d items=%d receipts=%d new_facts=%d plans=%d", store.runCount, store.itemCount, store.completionCount, store.factCount-factsBeforeConfirm, plans.calls)
			}
		})
	}
}

func TestPreviewFreezeRejectsConfirmationAtFifteenMinuteExpiry(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Nanosecond} {
		t.Run(map[time.Duration]string{0: "exact_expiry", time.Nanosecond: "after_expiry"}[offset], func(t *testing.T) {
			now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
			configuration := previewFreezeConfiguration()
			service, store, plans, _ := newPreviewFreezeFixture(t, []segmentport.ExecutionConfiguration{configuration, configuration}, previewFreezeMembers(), &now)
			preview, err := service.CreateBroadcastPreview(context.Background(), 17, 42)
			if err != nil {
				t.Fatalf("create preview: %v", err)
			}
			factsBeforeConfirm := store.factCount
			now = preview.ExpiresAt.Add(offset)
			command := confirmPreviewCommand(preview)
			for attempt := 1; attempt <= 2; attempt++ {
				if _, err = service.ConfirmRun(context.Background(), command); !errors.Is(err, ErrRuntimeConflict) {
					t.Fatalf("expired confirmation attempt %d err=%v, want ErrRuntimeConflict", attempt, err)
				}
			}
			if store.runCount != 0 || store.completionCount != 0 || store.factCount != factsBeforeConfirm || plans.calls != 0 {
				t.Fatalf("expired preview created business results: runs=%d receipts=%d new_facts=%d plans=%d", store.runCount, store.completionCount, store.factCount-factsBeforeConfirm, plans.calls)
			}
		})
	}
}

var _ platformport.UnitOfWork = directRuntimeUOW{}
