package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aiport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/provider"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	jobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type agentPolicyAudience struct {
	configuration segmentport.ExecutionConfiguration
}

func (a agentPolicyAudience) AudienceExecutionConfiguration(context.Context, segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	return a.configuration, nil
}

type agentPolicySnapshots struct{}

func (agentPolicySnapshots) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (agentPolicySnapshots) Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (agentPolicySnapshots) Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error) {
	return segmentport.MemberPage{}, nil
}

type agentPolicyContext struct{ calls int }

func (c *agentPolicyContext) FreezeGenerationContext(context.Context, customerdomain.CustomerID) (automationport.GenerationContext, error) {
	c.calls++
	return automationport.GenerationContext{Questionnaire: "最新合成问卷：正在探索副业方向"}, nil
}

type agentPolicyReview struct {
	aiport.TransactionalIntake
	aiport.Reader
}
type agentPolicyFailAccept struct {
	inner effectport.TransactionalAccepter
	fail  bool
}

func (a *agentPolicyFailAccept) AcceptAndQueueWithin(ctx context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	p, r, err := a.inner.AcceptAndQueueWithin(ctx, c)
	if err == nil && a.fail {
		a.fail = false
		return p, r, errors.New("synthetic generation accept rollback")
	}
	return p, r, err
}

type agentPolicyFailSend struct {
	inner outboundport.TransactionalMessageAccepter
	fail  bool
}

func (a *agentPolicyFailSend) AcceptMessageWithin(ctx context.Context, c outboundport.MessageIntent) (outboundport.MessageAcceptance, error) {
	r, err := a.inner.AcceptMessageWithin(ctx, c)
	if err == nil && a.fail {
		a.fail = false
		return r, errors.New("synthetic outbound accept rollback")
	}
	return r, err
}

// Full PG/River/EER/Outbound owner chain. The model is loopback and the
// outbound effect stays queued, so no customer or real Provider is contacted.
func TestAgentMemberPolicyGenerationAndAtomicOutboundPostgreSQL(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "unknown", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			native, cleanup := automationAudienceRuntimePool(t)
			defer cleanup()
			pool, err := postgres.Wrap(native, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			uow, err := postgres.NewUnitOfWork(pool)
			if err != nil {
				t.Fatal(err)
			}
			repo, err := automationstore.NewPostgreSQL(native, uow)
			if err != nil {
				t.Fatal(err)
			}
			media, err := mediastore.NewPostgreSQL(native, uow)
			if err != nil {
				t.Fatal(err)
			}
			staff := automationAudienceInsertProviderStaff(t, ctx, native)
			customers := automationAudienceInsertProviderCustomers(t, ctx, native)
			materials := automationAudienceCreateMedia(t, ctx, media, staff)
			agents := automationapp.NewAgentServiceWithMediaReferences(uow, repo, media, media, media, media, repo)
			agent, err := agents.Create(ctx, automationport.CreateCommand{Actor: staff, IdempotencyKey: "agent-policy-create-0001", Agent: automationport.Agent{AgentName: "个性化关心", AgentCode: "policy-personalized", AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusPaused, DraftRolePrompt: "你是运营助理，只写简洁温暖的私信。", DraftTaskPrompt: "结合最新问卷，写一句理解对方的话和一个可直接复制的问题，不用填空。", FixedContentPackage: automationport.FixedContentPackage{MiniprogramLibraryIDs: []int64{materials.miniID}}}})
			if err != nil {
				t.Fatal(err)
			}
			agent, err = agents.SetStatus(ctx, automationport.MutationCommand{ID: agent.ID, Actor: staff, IdempotencyKey: "agent-policy-active-0001"}, automationport.AgentStatusActive)
			if err != nil {
				t.Fatal(err)
			}
			published, found, err := agents.PublishedAgent(ctx, agent.ID)
			if err != nil || !found {
				t.Fatal(err)
			}
			configuration := segmentport.ExecutionConfiguration{PackageID: 33, PackageVersion: 4, ConfigurationVersionID: 48, Snapshot: segmentport.Snapshot{ID: 901, PackageID: 33, State: segmentport.SnapshotPublished, MemberCount: 1}, AgentID: int64(agent.ID), AgentPublishedVersion: agent.PublishedVersion, BindingVersion: 1, SenderSetVersion: 1, SenderStaffIDs: []int64{staff}, ContentDigest: published.ContentDigest, Ready: true}
			runtime, err := automationapp.NewRuntimeService(uow, repo, agentPolicyAudience{configuration}, agentPolicySnapshots{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			workers := river.NewWorkers()
			if err = river.AddWorkerSafely[effects.EffectJobArgs](workers, effects.NewWorker(nil, nil)); err != nil {
				t.Fatal(err)
			}
			client, err := jobqueue.NewInsertClient(native, workers)
			if err != nil {
				t.Fatal(err)
			}
			eer, err := effects.NewRepository(native, client)
			if err != nil {
				t.Fatal(err)
			}
			messages, err := outbound.NewMessageService(native, uow, eer, repo)
			if err != nil {
				t.Fatal(err)
			}
			send := &agentPolicyFailSend{inner: messages}
			if err = runtime.SetMessageAccepter(send); err != nil {
				t.Fatal(err)
			}
			if err = runtime.SetLockedCanonicalLineageReader(identityquery.NewPostgreSQL()); err != nil {
				t.Fatal(err)
			}
			if err = runtime.SetReviewPlanIntake(agentPolicyReview{}, agents); err != nil {
				t.Fatal(err)
			}
			if err = runtime.SetOutboundContentFreezer(automationOutboundContentFreezer{capturer: media}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "最新合成问卷") || strings.Contains(request.Messages[1].Content, "policy_delivery") {
					t.Error("model did not receive approved questionnaire-only context")
				}
				if mode == "rejected" {
					http.Error(w, "synthetic rejection", 401)
					return
				}
				if mode == "unknown" {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"看到你正在探索副业方向，先挑一个小方向验证就好。可以直接问黄小璨：根据你对我的了解，我适合先验证哪个副业方向？"}}]}`)
			}))
			defer model.Close()
			provider, err := automationprovider.NewGenerationProvider(automationprovider.GenerationConfig{Enabled: true, BaseURL: model.URL, Model: "synthetic-model", APIKey: "synthetic-key", Timeout: time.Second, Client: model.Client()}, runtime)
			if err != nil {
				t.Fatal(err)
			}
			contexts := &agentPolicyContext{}
			accept := &agentPolicyFailAccept{inner: eer, fail: true}
			if err = runtime.SetDynamicGenerationDependencies(accept, contexts, agents, provider); err != nil {
				t.Fatal(err)
			}
			sink, err := automationprovider.NewGenerationCompletionSink(runtime)
			if err != nil {
				t.Fatal(err)
			}
			if err = eer.SetCompletionSink(sink); err != nil {
				t.Fatal(err)
			}
			policy, err := runtime.CreatePolicy(ctx, automationapp.PolicyCommand{Code: "hxc-b-agent", Name: "B Agent", PackageID: 33, TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(fmt.Sprintf(`{"agent_id":%d,"once_per_customer":true}`, agent.ID)), QuietHours: json.RawMessage(`{}`), SingleRunLimit: 1, ApprovalStaffID: &staff, Actor: staff, IdempotencyKey: "agent-policy-policy-create-0001"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = runtime.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: policy.ID, ExpectedVersion: policy.Version, Target: automationdomain.PolicyActive, Actor: staff, IdempotencyKey: "agent-policy-policy-active-0001"})
			if err != nil {
				t.Fatal(err)
			}
			event := segmentport.MemberEnteredV1{EventID: "audmem_agent_policy_001", PackageID: 33, SnapshotID: 901, ConfigurationVersionID: 48, CustomerID: customerdomain.CustomerID(customers[0]), OccurredAt: time.Now().UTC()}
			if rows, e := runtime.EnrollAudienceMember(ctx, event); e == nil || len(rows) != 0 {
				t.Fatal("generation acceptance failure did not roll back")
			}
			var leaked int
			if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_enrollments)+(SELECT count(*) FROM automation_runs)+(SELECT count(*) FROM automation_generation_items)+(SELECT count(*) FROM external_effects)+(SELECT count(*) FROM river_job)+(SELECT count(*) FROM automation_runtime_operation_receipts WHERE operation='member_event_customer_once_accepted_v2')`).Scan(&leaked); err != nil || leaked != 0 {
				t.Fatalf("rollback leaked %d: %v", leaked, err)
			}
			rows, err := runtime.EnrollAudienceMember(ctx, event)
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			if replay, e := runtime.EnrollAudienceMember(ctx, event); e != nil || len(replay) != 1 || replay[0].ID != rows[0].ID {
				t.Fatal("source replay changed enrollment", e)
			}
			event.EventID = "audmem_agent_policy_reentry"
			if replay, e := runtime.EnrollAudienceMember(ctx, event); e != nil || len(replay) != 0 {
				t.Fatal("reentry duplicated generation", e)
			}
			var item automationdomain.GenerationItem
			var effectID, jobID int64
			if err = native.QueryRow(ctx, `SELECT e.id,j.id FROM external_effects e JOIN river_job j ON (j.args->>'effect_id')::bigint=e.id WHERE e.kind='ai_agent_generate'`).Scan(&effectID, &jobID); err != nil {
				t.Fatal(err)
			}
			effectRef := fmt.Sprintf("eer_%d", effectID)
			if err = uow.Within(ctx, func(tx context.Context) error {
				var e error
				item, e = repo.GenerationByEffect(tx, effectRef)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			if item.Context.PolicyDelivery == nil || !strings.Contains(item.Context.Questionnaire, "最新合成问卷") {
				t.Fatal("frozen context absent")
			}
			if mode == "cancelled" {
				if _, err = runtime.CancelRun(ctx, item.RunID, staff, "agent-policy-cancel-0001"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "success" {
				send.fail = true
				text := "synthetic completion rollback"
				artifact := effectport.ResultArtifact{Kind: "automation.ai_agent_generate.text.v1", Payload: []byte(text)}
				artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, text)
				completion := automationport.GenerationCompletion{EffectID: effectRef, State: effectport.StateExecuted, Attempt: effectport.Attempt{Number: 1}, ReceiptDigest: effectport.Hash("synthetic-completion"), Artifact: artifact, CompletedAt: time.Now().UTC()}
				if err = uow.Within(ctx, func(tx context.Context) error { return runtime.CompleteGeneration(tx, completion) }); err == nil {
					t.Fatal("outbound acceptance failure was hidden")
				}
				var state string
				if err = native.QueryRow(ctx, `SELECT state FROM automation_generation_items WHERE id=$1`, item.ID).Scan(&state); err != nil || state != "queued" {
					t.Fatal("completion did not roll back", state, err)
				}
				if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents`).Scan(&leaked); err != nil || leaked != 0 {
					t.Fatal("outbound leaked", leaked, err)
				}
			}
			worker := effects.NewWorker(eer, provider)
			job := &river.Job[effects.EffectJobArgs]{JobRow: &rivertype.JobRow{ID: jobID}, Args: effects.EffectJobArgs{EffectID: effectID, Generation: 1}}
			if err = worker.Work(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err = worker.Work(ctx, job); err != nil {
				t.Fatal("worker replay", err)
			}
			var outputCount, genCount, onceCount int
			if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_message_intents),(SELECT count(*) FROM automation_generation_items),(SELECT count(*) FROM automation_runtime_operation_receipts WHERE operation='member_event_customer_once_accepted_v2')`).Scan(&outputCount, &genCount, &onceCount); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if mode == "cancelled" {
				wantCalls = 0
			}
			if calls != wantCalls || genCount != 1 || onceCount != 1 {
				t.Fatalf("model/generation/once=%d/%d/%d", calls, genCount, onceCount)
			}
			if mode == "success" {
				var raw []byte
				var runState string
				if err = native.QueryRow(ctx, `SELECT content_snapshot FROM outbound_message_intents`).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var content frozenAutomationContent
				if json.Unmarshal(raw, &content) != nil || outputCount != 1 || len(content.Sources) != 1 || content.Sources[0].ID != materials.miniID || !strings.Contains(content.ContentText, "副业方向") || strings.Contains(content.ContentText, "____") {
					t.Fatal("personalized text or frozen card lost")
				}
				if err = native.QueryRow(ctx, `SELECT state FROM automation_runs`).Scan(&runState); err != nil || runState != "executing" {
					t.Fatal("automatic result entered human review", runState, err)
				}
			} else if outputCount != 0 {
				t.Fatal("failed or unknown generation sent content")
			}
			var plans int
			if err = native.QueryRow(ctx, `SELECT count(*) FROM ai_assistant_plans`).Scan(&plans); err != nil || plans != 0 {
				t.Fatal("automatic run created a manual review plan", err)
			}
		})
	}
}
