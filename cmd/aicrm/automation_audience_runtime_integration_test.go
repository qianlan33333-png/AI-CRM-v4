package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// TestAudienceRefreshToAutomationProviderAndReadOnlyHistoryPostgreSQL proves
// the composed ownership path: a real Segment River refresh creates durable
// entered facts, an approved policy consumes them through its River worker,
// outbound accepts EER effects, and the existing result API reads the
// projected history without a write. The adapter is deliberately local and
// deterministic; no Provider credential or customer identity is fabricated.
func TestAudienceRefreshToAutomationProviderAndReadOnlyHistoryPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := automationAudienceRuntimePool(t)
	defer cleanup()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	segmentRepo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	automationRepo, err := automationstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	configRepo, err := configstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	aiRepo, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaRepo, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	// Use the same Media facade as composition: ReadImageVariant's store row is
	// adapted at the app boundary before the outbound payload reader consumes it.
	mediaService, err := mediaapp.NewHTTPFacade(mediaRepo)
	if err != nil {
		t.Fatal(err)
	}
	aiService, err := aiassistantapp.NewService(uow, aiRepo, automationAudienceAIRecipients{}, automationAudienceAIStaff{}, aiMaterialAdapter{capturer: mediaRepo, references: mediaRepo}, automationAudienceAIIdentities{}, identityquery.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	refreshWorker := segment.NewAudienceRefreshWorker()
	memberWorker := segment.NewAudienceMemberEventDispatchWorker()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](workers, refreshWorker); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](workers, memberWorker); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	refreshJobs, err := segment.NewRiverRefreshEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	memberJobs, err := segment.NewRiverMemberEventEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	staffID := automationAudienceInsertProviderStaff(t, ctx, native)
	materials := automationAudienceCreateMedia(t, ctx, mediaRepo, staffID)
	// Capture an automatic intent's source before a Media owner edit, then
	// prove the later payload reader refuses the changed mini-program fields.
	// The actual agent below is published after this edit and therefore has its
	// own current snapshot; this is deliberately a capture-then-change test.
	preEditContent := automationport.OutboundPublishedContent{AgentID: 1, PublishedVersion: 1, Content: automationport.FixedContentPackage{ContentText: "runtime hello", ImageLibraryIDs: []int64{materials.imageID}, MiniprogramLibraryIDs: []int64{materials.miniID}, AttachmentLibraryIDs: []int64{materials.attachmentID}, GroupInviteLibraryIDs: []int64{materials.inviteID}}}
	freezer := automationOutboundContentFreezer{capturer: mediaRepo}
	var preEditSnapshot json.RawMessage
	var preEditDigest [32]byte
	if err = uow.Within(ctx, func(tx context.Context) error {
		var freezeErr error
		preEditSnapshot, preEditDigest, freezeErr = freezer.FreezeOutboundContent(tx, preEditContent)
		return freezeErr
	}); err != nil {
		t.Fatal(err)
	}
	frozenReader := automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{images: mediaService, materials: mediaRepo, attachments: mediaService, uow: uow, capturer: mediaRepo}}
	if _, err = frozenReader.LoadFrozenAutomationMessagePayload(ctx, preEditSnapshot, preEditDigest); err != nil {
		t.Fatalf("valid frozen automatic payload could not be prepared: %v", err)
	}
	assertFrozenUnavailable := func(reason string) {
		t.Helper()
		if _, readErr := frozenReader.LoadFrozenAutomationMessagePayload(ctx, preEditSnapshot, preEditDigest); readErr == nil {
			t.Fatalf("%s was accepted for an already-frozen automatic payload", reason)
		}
	}
	if _, err = mediaRepo.UpdateAttachment(ctx, materials.attachmentID, staffID, "audience-runtime-pdf-disable-0001", map[string]any{"expected_version": float64(1), "enabled": false}); err != nil {
		t.Fatal(err)
	}
	assertFrozenUnavailable("disabled PDF")
	if _, err = mediaRepo.UpdateAttachment(ctx, materials.attachmentID, staffID, "audience-runtime-pdf-enable-0001", map[string]any{"expected_version": float64(2), "enabled": true}); err != nil {
		t.Fatal(err)
	}
	if _, err = mediaRepo.UpdateMiniProgram(ctx, materials.miniID, staffID, "audience-runtime-mini-disable-0001", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	assertFrozenUnavailable("disabled mini program")
	if _, err = mediaRepo.UpdateMiniProgram(ctx, materials.miniID, staffID, "audience-runtime-mini-enable-0001", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	// This same image is both the image attachment and the mini/invite cover,
	// so disabling it exercises dependent thumbnail availability as well.
	if _, err = mediaRepo.UpdateImage(ctx, materials.imageID, staffID, "audience-runtime-image-disable-0001", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	assertFrozenUnavailable("disabled image or dependent cover")
	if _, err = mediaRepo.UpdateImage(ctx, materials.imageID, staffID, "audience-runtime-image-enable-0001", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	if _, err = mediaRepo.ArchiveGroupInvite(ctx, materials.inviteID, staffID, "audience-runtime-invite-archive-0001"); err != nil {
		t.Fatal(err)
	}
	assertFrozenUnavailable("archived group invite")
	replacement, createErr := mediaRepo.CreateGroupInvite(ctx, staffID, "audience-runtime-invite-replacement-0001", map[string]any{"name": "Runtime invite", "title": "Join runtime group", "description": "Runtime group", "join_url": "https://work.weixin.qq.com/gm/runtime", "cover_image_id": float64(materials.imageID), "enabled": true})
	if createErr != nil {
		t.Fatal(createErr)
	}
	var validInvite bool
	materials.inviteID, validInvite = replacement["id"].(int64)
	if !validInvite || materials.inviteID < 1 {
		t.Fatalf("replacement invite=%+v", replacement)
	}
	if _, err = mediaRepo.UpdateMiniProgram(ctx, materials.miniID, staffID, "audience-runtime-mini-edit-0001", map[string]any{"title": "Runtime card revised"}); err != nil {
		t.Fatal(err)
	}
	assertFrozenUnavailable("changed mini-program title")
	customerIDs := automationAudienceInsertProviderCustomers(t, ctx, native)
	source := &automationAudienceSource{}
	source.Set(customerIDs[:1])
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, source, automationAudienceCanonical{})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, segmentRepo, evaluator, refreshJobs, memberJobs)
	if err != nil {
		t.Fatal(err)
	}
	if err = refreshWorker.BindService(snapshots); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)
	packageID := automationAudiencePackage(t, ctx, uow, segmentRepo, now)
	automationService := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepo, mediaRepo, mediaRepo, mediaRepo, mediaRepo, automationRepo)
	agent := automationAudiencePublishedAgent(t, ctx, automationService, staffID, materials)
	publishedAgent, found, err := automationService.PublishedAgent(ctx, agent.ID)
	if err != nil || !found {
		t.Fatalf("published agent=%+v found=%v err=%v", agent, found, err)
	}
	segmentStaff := automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}
	execution, err := segmentapp.NewExecutionService(uow, segmentRepo, automationService, segmentStaff, true)
	if err != nil {
		t.Fatal(err)
	}
	combined := automationAudienceCombinedDigest(publishedAgent.ContentDigest, publishedAgent.MaterialsDigest)
	binding, err := execution.PutBinding(ctx, segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: 2, AgentID: agent.ID, ExpectedPublishedVersion: publishedAgent.PublishedVersion, ExpectedAgentDigest: combined, Actor: staffID, IdempotencyKey: "audience-runtime-binding-0001"})
	if err != nil || binding.ID < 1 {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	if _, err = execution.ReplaceSenders(ctx, segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: 3, ProviderMemberIDs: []string{"sender-a"}, Actor: staffID, IdempotencyKey: "audience-runtime-senders-0001"}); err != nil {
		t.Fatal(err)
	}

	messages, err := outbound.NewMessageService(native, uow, effects, automationRepo)
	if err != nil {
		t.Fatal(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(native, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindOutbound(privateWriter, true); err != nil {
		t.Fatal(err)
	}
	wecomServer := newAutomationAudienceWeComServer(t)
	defer wecomServer.Close()
	writer, err := wecomadapter.NewDirectory(wecomadapter.Config{
		Enabled: true, CorpID: "runtime-corp", ContactSecret: "runtime-contact-secret",
		APIBase: wecomServer.URL, HTTPClient: wecomServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	frozenPayloads := &automationAudienceFrozenPayloadRecorder{inner: automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{images: mediaService, materials: mediaRepo, attachments: mediaService, uow: uow, capturer: mediaRepo}}}
	messageProvider, err := outbound.NewMessageProvider(outbound.MessageProviderConfig{
		Enabled: true, CorpScope: "wecom-corp:runtime-corp", Executions: messages,
		// Match the composition root: a Provider reads after its effect
		// transaction committed, so the Identity Owner needs this read adapter
		// to bind its own local transaction.
		Identities: outboundIdentityAdapter{uow: uow, reader: identityquery.NewPostgreSQL()},
		Staff:      segmentStaff, Content: automationService,
		Payloads: frozenPayloads, Writer: writer,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(true, privateWriter, automationAudiencePrivateTarget{}, aiPrivatePayloadReader{content: aiRepo, images: mediaService, materials: mediaRepo, attachments: mediaService, uow: uow, capturer: mediaRepo}, writer)
	if err != nil {
		t.Fatal(err)
	}
	provider := &automationAudienceRecordingProvider{inner: outbound.NewProviderRouterWithMessages(nil, nil, messageProvider).WithPrivateMessage(privateProvider)}
	effectWorker := externaleffects.NewWorker(nil, provider)
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, effectWorker); err != nil {
		t.Fatal(err)
	}
	privateCompletion, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiRepo)
	if err != nil {
		t.Fatal(err)
	}
	router, err := outbound.NewCompletionRouterWithPrivate(nil, nil, privateCompletion)
	if err != nil {
		t.Fatal(err)
	}
	router.WithAutomationMessage(messages)
	if err = effects.SetCompletionSink(router); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := configapp.NewRuntimeReleaseService(uow, configRepo, configRepo, 100)
	if err != nil {
		t.Fatal(err)
	}
	limitValue, err := json.Marshal(2)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := runtimeConfig.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: 0, Settings: []configport.RuntimeSetting{{Key: configport.AutomationOperationsMaxRecipientsPerRun, Value: limitValue}}, Actor: "runtime-fixture", IdempotencyKey: "runtime-config-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := runtimeConfig.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: "runtime-fixture", IdempotencyKey: "runtime-config-validate-0001"})
	if err != nil {
		t.Fatal(err)
	}
	publishedRuntimeConfig, err := runtimeConfig.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: validated.ID, ExpectedBaseRevision: 0, ExpectedChecksum: validated.Checksum, Actor: "runtime-fixture", IdempotencyKey: "runtime-config-publish-0001"})
	if err != nil {
		t.Fatal(err)
	}
	runtimeService, err := automationapp.NewRuntimeService(uow, automationRepo, execution, snapshots, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetRuntimeConfig(runtimeConfig, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetMessageAccepter(messages); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetReviewPlanIntake(aiService, automationService); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetOutboundContentFreezer(automationOutboundContentFreezer{capturer: mediaRepo}); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetEffectReconciler(effects); err != nil {
		t.Fatal(err)
	}
	if err = memberWorker.Bind(snapshots, automationAudienceEnrollmentSink{runtime: runtimeService}); err != nil {
		t.Fatal(err)
	}
	if err = effectWorker.BindRepository(effects); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(native, workers, segment.AudienceRefreshQueue, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	stop := automationAudienceStartRuntime(t, runtime)
	// Keep the River runtime owned by this fixture alive until every assertion
	// completes, while also stopping it if a preceding t.Fatal aborts early.
	// Otherwise its maintenance goroutines can outlive the PostgreSQL pool.
	var stopOnce sync.Once
	stopRuntime := func() { stopOnce.Do(stop) }
	// The pool and schema cleanup below are ordinary defers, which run before
	// t.Cleanup. Register a matching defer here so an early fatal stops River
	// before either closes its PostgreSQL resources.
	defer stopRuntime()
	t.Cleanup(stopRuntime)

	approval := staffID
	actionConfig, err := json.Marshal(map[string]int64{"agent_id": int64(agent.ID)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := runtimeService.CreatePolicy(ctx, automationapp.PolicyCommand{Code: "audience-entry", Name: "Audience entry", PackageID: segmentport.PackageID(packageID), TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionOutboundMessage, ActionConfig: actionConfig, QuietHours: automationAudienceNonBlockingQuietHours(time.Now()), SingleRunLimit: 100, ApprovalStaffID: &approval, Actor: staffID, IdempotencyKey: "audience-runtime-policy-0001"})
	if err != nil {
		t.Fatal(err)
	}
	// Policy activation requires a real published snapshot. Publish an empty
	// baseline first; this has no active policy and therefore creates no send.
	source.Set(nil)
	baseline, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-baseline-0001", ReferenceTime: now.Add(-time.Minute)})
	if err != nil || baseline.RiverJobID == nil {
		t.Fatalf("accept baseline=%+v err=%v", baseline, err)
	}
	automationAudienceEventually(t, "empty baseline snapshot", func() bool {
		snapshot, found, readErr := snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		return readErr == nil && found && snapshot.MemberCount == 0
	})
	precheck, precheckErr := execution.Precheck(ctx, packageID)
	if precheckErr != nil || !precheck.Ready {
		t.Fatalf("baseline execution precheck=%+v err=%v", precheck, precheckErr)
	}
	configuration, configurationErr := execution.AudienceExecutionConfiguration(ctx, segmentport.PackageID(packageID))
	if configurationErr != nil || !configuration.Ready {
		diagnostic, hasDiagnostic := segmentapp.PersistenceFailure(configurationErr)
		t.Fatalf("baseline execution configuration=%+v err=%v diagnostic=%+v has_diagnostic=%t", configuration, configurationErr, diagnostic, hasDiagnostic)
	}
	if _, err = runtimeService.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: policy.ID, ExpectedVersion: policy.Version, Actor: staffID, Target: automationdomain.PolicyActive, IdempotencyKey: "audience-runtime-activate-0001"}); err != nil {
		t.Fatal(err)
	}
	source.Set(customerIDs[:1])
	refresh, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-refresh-0001", ReferenceTime: now})
	if err != nil || refresh.RiverJobID == nil {
		t.Fatalf("accept refresh=%+v err=%v", refresh, err)
	}
	var published segmentport.Snapshot
	automationAudienceEventuallyWithDiagnostics(t, "initial audience and automatic delivery", func() bool {
		var found bool
		published, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || published.MemberCount != 1 {
			return false
		}
		var complete int
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment' AND state='provider_accepted'`).Scan(&complete) != nil {
			return false
		}
		return complete == 1 && wecomServer.Uploads() == 3
	}, func() string { return automationAudienceRuntimeDiagnostics(ctx, native, provider, frozenPayloads) })

	// A later Config release must not turn the exact, already-delivered member
	// event into a new action digest. The stored Automation enrollment is the
	// frozen receipt; no second run, external intent, or Config usage is valid.
	var replayEvent segmentport.MemberEnteredV1
	if err = native.QueryRow(ctx, `SELECT event_id,package_id,snapshot_id,configuration_version_id,customer_id,occurred_at FROM segment_audience_member_events WHERE snapshot_id=$1 AND customer_id=$2`, published.ID, customerIDs[0]).Scan(&replayEvent.EventID, &replayEvent.PackageID, &replayEvent.SnapshotID, &replayEvent.ConfigurationVersionID, &replayEvent.CustomerID, &replayEvent.OccurredAt); err != nil {
		t.Fatal(err)
	}
	var originalEnrollment, originalRun, originalIntents, originalV1Uses int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&originalEnrollment); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs`).Scan(&originalRun); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&originalIntents); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1 AND role='worker' AND operation='execution'`, publishedRuntimeConfig.ID).Scan(&originalV1Uses); err != nil {
		t.Fatal(err)
	}
	if originalEnrollment != 1 || originalRun != 1 || originalIntents != 1 || originalV1Uses != 1 {
		t.Fatalf("initial automatic facts enrollment/run/intents/v1uses=%d/%d/%d/%d", originalEnrollment, originalRun, originalIntents, originalV1Uses)
	}
	v2Limit, marshalErr := json.Marshal(3)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	v2Draft, e := runtimeConfig.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: publishedRuntimeConfig.ID, Settings: []configport.RuntimeSetting{{Key: configport.AutomationOperationsMaxRecipientsPerRun, Value: v2Limit}}, Actor: "runtime-fixture-v2", IdempotencyKey: "runtime-config-create-0002"})
	if e != nil {
		t.Fatal(e)
	}
	v2Validated, e := runtimeConfig.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: v2Draft.ID, Actor: "runtime-fixture-v2", IdempotencyKey: "runtime-config-validate-0002"})
	if e != nil {
		t.Fatal(e)
	}
	publishedRuntimeConfigV2, e := runtimeConfig.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: v2Validated.ID, ExpectedBaseRevision: publishedRuntimeConfig.ID, ExpectedChecksum: v2Validated.Checksum, Actor: "runtime-fixture-v2", IdempotencyKey: "runtime-config-publish-0002"})
	if e != nil {
		t.Fatal(e)
	}
	replayedEnrollment, e := runtimeService.EnrollAudienceMember(ctx, replayEvent)
	if e != nil || len(replayedEnrollment) != 1 {
		t.Fatalf("cross-config member replay enrollments=%+v err=%v", replayedEnrollment, e)
	}
	var afterEnrollment, afterRun, afterIntents, afterV1Uses, v2ReplayUses int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&afterEnrollment); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs`).Scan(&afterRun); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&afterIntents); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1 AND role='worker' AND operation='execution'`, publishedRuntimeConfig.ID).Scan(&afterV1Uses); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1`, publishedRuntimeConfigV2.ID).Scan(&v2ReplayUses); err != nil {
		t.Fatal(err)
	}
	if afterEnrollment != originalEnrollment || afterRun != originalRun || afterIntents != originalIntents || afterV1Uses != originalV1Uses || v2ReplayUses != 0 {
		t.Fatalf("replay changed automatic facts enrollment/run/intents/v1uses/v2uses=%d/%d/%d/%d/%d", afterEnrollment, afterRun, afterIntents, afterV1Uses, v2ReplayUses)
	}
	// The Config release may advance, but the same event ID cannot be reused
	// with a different Segment snapshot. It is a source-fact conflict, not a
	// replay, and it leaves all frozen effects and usage untouched.
	changedSnapshot := replayEvent
	changedSnapshot.SnapshotID++
	if _, e = runtimeService.EnrollAudienceMember(ctx, changedSnapshot); !errors.Is(e, automationapp.ErrRuntimeConflict) {
		t.Fatalf("changed member source snapshot err=%v want runtime conflict", e)
	}
	var afterChangedEnrollment, afterChangedRun, afterChangedIntents, afterChangedV1Uses, afterChangedV2Uses int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&afterChangedEnrollment); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs`).Scan(&afterChangedRun); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&afterChangedIntents); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1 AND role='worker' AND operation='execution'`, publishedRuntimeConfig.ID).Scan(&afterChangedV1Uses); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1`, publishedRuntimeConfigV2.ID).Scan(&afterChangedV2Uses); err != nil {
		t.Fatal(err)
	}
	if afterChangedEnrollment != originalEnrollment || afterChangedRun != originalRun || afterChangedIntents != originalIntents || afterChangedV1Uses != originalV1Uses || afterChangedV2Uses != 0 {
		t.Fatalf("changed source mutated enrollment/run/intents/v1uses/v2uses=%d/%d/%d/%d/%d", afterChangedEnrollment, afterChangedRun, afterChangedIntents, afterChangedV1Uses, afterChangedV2Uses)
	}
	// Incremental evaluation contains only the new result. Segment merges it
	// with the prior snapshot, so the original member remains present.
	source.Set(customerIDs[1:])
	incremental, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-incremental-0001", RefreshKind: segmentdomain.RefreshIncremental, ReferenceTime: now.Add(time.Minute)})
	if err != nil || incremental.RiverJobID == nil {
		t.Fatalf("accept incremental=%+v err=%v", incremental, err)
	}
	automationAudienceEventually(t, "incremental membership merge and automatic delivery", func() bool {
		var found bool
		published, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || published.MemberCount != 2 {
			return false
		}
		var prior, added, complete int
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_snapshot_members WHERE snapshot_id=$1 AND customer_id=$2`, published.ID, customerIDs[0]).Scan(&prior) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_snapshot_members WHERE snapshot_id=$1 AND customer_id=$2`, published.ID, customerIDs[1]).Scan(&added) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment' AND state='provider_accepted'`).Scan(&complete) != nil {
			return false
		}
		return prior == 1 && added == 1 && complete == 2 && wecomServer.Uploads() == 6
	})
	var uniqueReferences, providerReceipts int
	if err = native.QueryRow(ctx, `SELECT count(DISTINCT content_reference) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&uniqueReferences); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_receipts WHERE message_id<>''`).Scan(&providerReceipts); err != nil {
		t.Fatal(err)
	}
	if uniqueReferences != 2 || providerReceipts != 2 {
		t.Fatalf("automatic recipient references/receipts=%d/%d, want 2/2", uniqueReferences, providerReceipts)
	}
	stopRuntime()
	var enrollments, automaticEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&enrollments); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&automaticEffects); err != nil {
		t.Fatal(err)
	}
	if enrollments != 2 || automaticEffects != 2 {
		t.Fatalf("entered events created enrollments=%d intents=%d", enrollments, automaticEffects)
	}
	var v1WorkerUses, v2WorkerUses, v1FrozenWorkerRuns, v2FrozenWorkerRuns int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1 AND role='worker' AND operation='execution'`, publishedRuntimeConfig.ID).Scan(&v1WorkerUses); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage WHERE revision=$1 AND role='worker' AND operation='execution'`, publishedRuntimeConfigV2.ID).Scan(&v2WorkerUses); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE runtime_config_observed AND runtime_config_revision=$1 AND max_recipients_per_run=2`, publishedRuntimeConfig.ID).Scan(&v1FrozenWorkerRuns); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE runtime_config_observed AND runtime_config_revision=$1 AND max_recipients_per_run=3`, publishedRuntimeConfigV2.ID).Scan(&v2FrozenWorkerRuns); err != nil {
		t.Fatal(err)
	}
	if v1WorkerUses != 1 || v2WorkerUses != 1 || v1FrozenWorkerRuns != 1 || v2FrozenWorkerRuns != 1 {
		t.Fatalf("worker Config v1 uses/runs v2 uses/runs=%d/%d %d/%d want 1/1 1/1", v1WorkerUses, v1FrozenWorkerRuns, v2WorkerUses, v2FrozenWorkerRuns)
	}

	runtimeHandler, err := automationhttp.NewRuntimeHandler(runtimeService, automationAudienceSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	// The browser test loads the frozen detail HTML and its production host
	// script. Only its ordinary bootstrap reads are stubbed; Preview, Confirm,
	// and the resulting run list travel to this real Runtime HTTP handler and
	// the PostgreSQL fixture.
	// The frozen page receives the actual PostgreSQL preview response only
	// after this bounded fixture delay.  Its journey must wait for the rendered
	// success or error state rather than assume a fixed browser idle interval.
	const manualBroadcastPreviewResponseDelay = 600 * time.Millisecond
	detailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/admin/ai-audience/packages/"+automationAudienceInt(packageID)+"/broadcast-previews" {
			time.Sleep(manualBroadcastPreviewResponseDelay)
		}
		runtimeHandler.ServeHTTP(w, r)
	}))
	defer detailServer.Close()
	_, testFile, _, callerOK := goruntime.Caller(0)
	if !callerOK {
		t.Fatal("cannot locate runtime audience test source")
	}
	detailScript := filepath.Join(filepath.Dir(testFile), "..", "..", "internal", "webshell", "static", "admin_console", "admin_audience_manual_broadcast_pg.test.mjs")
	detailUI := exec.CommandContext(ctx, "node", detailScript)
	detailUI.Env = append(os.Environ(), "AICRM_RUNTIME_TEST_URL="+detailServer.URL, "AICRM_RUNTIME_TEST_PACKAGE_ID="+automationAudienceInt(packageID))
	detailOutput, detailErr := detailUI.CombinedOutput()
	if detailErr != nil {
		t.Fatalf("original detail manual broadcast journey: %v output=%s", detailErr, detailOutput)
	}
	runs, _, err := runtimeService.ListRuns(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var manual automationdomain.RuntimeRun
	for _, candidate := range runs {
		if candidate.PackageID == packageID && candidate.AIPlanID > 0 {
			manual = candidate
			break
		}
	}
	if manual.ID < 1 || manual.TargetCount != 2 || manual.State != automationport.RunPendingReview || manual.AIPlanID < 1 || !manual.RuntimeConfigObserved || manual.RuntimeConfigRevision != publishedRuntimeConfigV2.ID || manual.MaxRecipientsPerRun != 3 {
		t.Fatalf("original detail manual run=%+v ui=%s", manual, detailOutput)
	}
	var previewUses, confirmUses int
	if err = native.QueryRow(ctx, `SELECT count(*) FILTER (WHERE operation='preview'),count(*) FILTER (WHERE operation='confirm') FROM config_runtime_usage WHERE revision=$1 AND role='api'`, publishedRuntimeConfigV2.ID).Scan(&previewUses, &confirmUses); err != nil {
		t.Fatal(err)
	}
	if previewUses != 1 || confirmUses != 1 {
		t.Fatalf("API config usage preview/confirm=%d/%d", previewUses, confirmUses)
	}
	plan, err := aiService.GetPlan(ctx, aiassistantport.PlanID(manual.AIPlanID))
	if err != nil || plan.State != aiassistantport.PlanPendingReview || plan.TargetCount != 2 {
		t.Fatalf("manual plan=%+v err=%v", plan, err)
	}
	var effectCount, runRecipientCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil || effectCount != 2 {
		t.Fatalf("confirmation effects=%d err=%v", effectCount, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_run_recipients WHERE run_id=$1`, manual.ID).Scan(&runRecipientCount); err != nil || runRecipientCount != 0 {
		t.Fatalf("confirmation runtime recipients=%d err=%v", runRecipientCount, err)
	}
	approvalPreview, err := aiService.PreviewApproval(ctx, aiassistantport.PreviewApprovalCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, ExpectedVersion: plan.Version})
	if err != nil || approvalPreview.EligibleCount != 2 {
		t.Fatalf("review preview=%+v err=%v", approvalPreview, err)
	}
	if _, err = aiService.ApprovePlan(ctx, aiassistantport.ApprovePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: staffID}, PlanID: plan.ID, ExpectedVersion: plan.Version, PreviewDigest: approvalPreview.PreviewDigest, IdempotencyKey: "audience-runtime-manual-approve-0001"}); err != nil {
		t.Fatal(err)
	}
	if _, _, projectionErr := runtimeService.ListRuns(ctx, 0, 100); projectionErr != nil {
		planValue, planErr := aiService.GetPlan(ctx, plan.ID)
		recipients, recipientsErr := aiService.ListRecipients(ctx, aiassistantport.RecipientPageQuery{PlanID: plan.ID, Limit: 100})
		t.Fatalf("approved AI plan cannot project into Automation: projection=%v plan=%+v plan_err=%v recipients=%+v recipients_err=%v", projectionErr, planValue, planErr, recipients, recipientsErr)
	}
	stop = automationAudienceStartRuntime(t, runtime)
	stopOnce = sync.Once{}
	automationAudienceEventuallyWithDiagnostics(t, "approved manual effects", func() bool {
		var accepted, unknown int
		if native.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='provider_accepted'),count(*) FILTER (WHERE state='outcome_unknown') FROM outbound_private_message_intents`).Scan(&accepted, &unknown) != nil {
			return false
		}
		// The fourth local Provider request is deliberately disconnected. Its
		// effect is unknown and must not be retried with a new key.
		return accepted == 1 && unknown == 1 && wecomServer.Calls() == 4 && wecomServer.Uploads() == 12
	}, func() string { return automationAudienceRuntimeDiagnostics(ctx, native, provider, frozenPayloads) })
	// Provider receipt persistence and the AI completion projection are separate
	// committed steps. Do not read the Automation history until the existing
	// completion router has updated the plan/recipient facts that its read-only
	// projection consumes.
	automationAudienceEventuallyWithDiagnostics(t, "manual AI completion projection", func() bool {
		var accepted, unknown int
		if native.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE binding.state='provider_accepted' AND recipient.execution_state='provider_accepted'),
			count(*) FILTER (WHERE binding.state='outcome_unknown' AND recipient.execution_state='outcome_unknown')
			FROM ai_assistant_plan_recipients recipient
			JOIN ai_assistant_effect_bindings binding ON binding.recipient_id=recipient.id
			WHERE recipient.plan_id=$1`, plan.ID).Scan(&accepted, &unknown) != nil || accepted != 1 || unknown != 1 {
			return false
		}
		projected, projectionErr := runtimeService.Run(ctx, manual.ID)
		return projectionErr == nil && projected.AIPlanState == string(aiassistantport.PlanNeedsAttention) && projected.State == automationport.RunOutcomeUnknown && projected.OutcomeUnknownCount == 1
	}, func() string { return automationAudienceRuntimeDiagnostics(ctx, native, provider, frozenPayloads) })
	stopRuntime()
	readHandler := runtimeHandler
	var before, after int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, contains string }{
		{"/api/admin/automation-runs?limit=100", `"ai_plan_state":"needs_attention"`},
		{"/api/admin/automation-runs/" + automationAudienceInt(manual.ID), `"state":"outcome_unknown"`},
		{"/api/admin/automation-runs/" + automationAudienceInt(manual.ID), `"outcome_unknown_count":1`},
		{"/api/admin/automation-runs/" + automationAudienceInt(manual.ID), `"ai_plan_state":"needs_attention"`},
		{"/api/admin/automation-runs/" + automationAudienceInt(manual.ID) + "/recipients?limit=100", "items"},
	} {
		req := httptest.NewRequest(http.MethodGet, check.path, nil)
		res := httptest.NewRecorder()
		readHandler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !json.Valid(res.Body.Bytes()) || !strings.Contains(res.Body.String(), check.contains) {
			t.Fatalf("read history %s status=%d body=%s", check.path, res.Code, res.Body.String())
		}
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("history GET wrote audit rows: before=%d after=%d", before, after)
	}
	// Daily exits are Segment facts only. A third runtime start processes the
	// persisted daily job, but no member-entered event or outbound send appears.
	source.Set(customerIDs[:1])
	stop = automationAudienceStartRuntime(t, runtime)
	stopOnce = sync.Once{}
	daily, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-daily-exit-0001", RefreshKind: segmentdomain.RefreshDaily, ReferenceTime: now.Add(time.Hour)})
	if err != nil || daily.RiverJobID == nil {
		t.Fatalf("accept daily=%+v err=%v", daily, err)
	}
	var exitSnapshot segmentport.Snapshot
	automationAudienceEventually(t, "daily exit without entered send", func() bool {
		var found bool
		exitSnapshot, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || exitSnapshot.ID == published.ID {
			return false
		}
		var exits, entered, intents int
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events WHERE snapshot_id=$1 AND customer_id=$2`, exitSnapshot.ID, customerIDs[1]).Scan(&exits) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events WHERE snapshot_id=$1`, exitSnapshot.ID).Scan(&entered) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents`).Scan(&intents) != nil {
			return false
		}
		return exits == 1 && entered == 0 && intents == 2 && wecomServer.Calls() == 4
	})
	stopRuntime()
}

type automationAudienceSource struct {
	mu  sync.RWMutex
	ids []customerdomain.CustomerID
}

func (s *automationAudienceSource) Set(ids []customerdomain.CustomerID) {
	s.mu.Lock()
	s.ids = append([]customerdomain.CustomerID(nil), ids...)
	s.mu.Unlock()
}

func (s *automationAudienceSource) Evaluate(_ context.Context, _ segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	s.mu.RLock()
	ids := append([]customerdomain.CustomerID(nil), s.ids...)
	s.mu.RUnlock()
	return segmentport.Evaluation{CustomerIDs: ids, ReferenceAt: reference.UTC()}, nil
}

type automationAudienceCanonical struct{}

func (automationAudienceCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

// These fixtures provide only already-canonical Customer and active-staff
// facts to the real AI Plan service. The plan, receipt, audit and outbox still
// use its PostgreSQL Owner store in the shared transaction.
type automationAudienceAIRecipients struct{}

func (automationAudienceAIRecipients) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "runtime customer", OneIDLabel: "CID"}, nil
}

type automationAudienceAIStaff struct{}

func (automationAudienceAIStaff) StaffSnapshot(_ context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{ID: id, DisplayName: "runtime staff", Active: true}, nil
}

// The runtime journey supplies canonical staff IDs. It deliberately has no
// trusted Provider userid mapping, so a new external-id path must fail closed.
func (automationAudienceAIStaff) StaffByWeComUserID(_ context.Context, _ string) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{}, errors.New("runtime fixture cannot resolve WeCom staff userid")
}

type automationAudienceAIIdentities struct{}

func (automationAudienceAIIdentities) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}

type automationAudiencePrivateTarget struct{}

func (automationAudiencePrivateTarget) ResolvePrivateMessageTarget(_ context.Context, customerID customerdomain.CustomerID, staffID int64) (outbound.PrivateMessageTarget, error) {
	if customerID < 1 || staffID < 1 {
		return outbound.PrivateMessageTarget{}, errors.New("invalid private-message fixture target")
	}
	return outbound.PrivateMessageTarget{ExternalUserID: "runtime-external-" + strconv.FormatInt(int64(customerID), 10), StaffUserID: "sender-a"}, nil
}

type automationAudienceEnrollmentSink struct{ runtime *automationapp.RuntimeService }

func (s automationAudienceEnrollmentSink) HandleAudienceMemberEntered(ctx context.Context, e segmentport.MemberEnteredV1) error {
	_, err := s.runtime.EnrollAudienceMember(ctx, e)
	return err
}

// automationAudienceRecordingProvider preserves the exact adapter error in a
// failing integration fixture. It delegates every runtime call unchanged.
type automationAudienceRecordingProvider struct {
	inner effectport.ProviderAdapter
	mu    sync.Mutex
	err   error
}

// Test-only recorder exposes the fail-closed preparation reason in the
// fixture diagnostic; production stores no material metadata or error text.
type automationAudienceFrozenPayloadRecorder struct {
	inner outboundport.FrozenAutomationMessagePayloadReader
	mu    sync.Mutex
	err   error
}

func (r *automationAudienceFrozenPayloadRecorder) LoadFrozenAutomationMessagePayload(ctx context.Context, raw json.RawMessage, digest [32]byte) (outbound.PrivateMessagePayload, error) {
	payload, err := r.inner.LoadFrozenAutomationMessagePayload(ctx, raw, digest)
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
	return payload, err
}
func (r *automationAudienceFrozenPayloadRecorder) Error() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		return ""
	}
	return r.err.Error()
}

func (p *automationAudienceRecordingProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	result, err := p.inner.Execute(ctx, envelope, attempt)
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	return result, err
}

func (p *automationAudienceRecordingProvider) Error() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		return ""
	}
	return p.err.Error()
}

type automationAudienceWeComServer struct {
	*httptest.Server
	t       *testing.T
	mu      sync.Mutex
	calls   int
	uploads int
	errs    []string
}

func newAutomationAudienceWeComServer(t *testing.T) *automationAudienceWeComServer {
	t.Helper()
	s := &automationAudienceWeComServer{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *automationAudienceWeComServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/cgi-bin/gettoken":
		if r.URL.Query().Get("corpid") != "runtime-corp" || (r.URL.Query().Get("corpsecret") != "runtime-contact-secret" && r.URL.Query().Get("corpsecret") != "runtime-app-secret") {
			s.record("unexpected token request")
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"access_token":"runtime-token","expires_in":7200}`))
	case "/cgi-bin/externalcontact/add_msg_template":
		if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "runtime-token" {
			s.record("unexpected message endpoint or token")
			http.Error(w, "bad message request", http.StatusBadRequest)
			return
		}
		var body struct {
			ChatType string   `json:"chat_type"`
			External []string `json:"external_userid"`
			Sender   string   `json:"sender"`
			Text     struct {
				Content string `json:"content"`
			} `json:"text"`
			Attachments []struct {
				MessageType string `json:"msgtype"`
				Image       struct {
					MediaID string `json:"media_id"`
				} `json:"image"`
				MiniProgram struct {
					Title      string `json:"title"`
					PicMediaID string `json:"pic_media_id"`
					AppID      string `json:"appid"`
					Page       string `json:"page"`
				} `json:"miniprogram"`
				File struct {
					MediaID string `json:"media_id"`
				} `json:"file"`
				Link struct{ Title, Desc, URL string } `json:"link"`
			} `json:"attachments"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			s.record("invalid message JSON")
			http.Error(w, "bad message body", http.StatusBadRequest)
			return
		}
		validAttachments := len(body.Attachments) == 4 && body.Attachments[0].MessageType == "image" && body.Attachments[0].Image.MediaID != "" && body.Attachments[1].MessageType == "miniprogram" && body.Attachments[1].MiniProgram.Title == "Runtime card revised" && body.Attachments[1].MiniProgram.PicMediaID != "" && body.Attachments[1].MiniProgram.AppID == "wx-runtime" && body.Attachments[1].MiniProgram.Page == "pages/runtime" && body.Attachments[2].MessageType == "file" && body.Attachments[2].File.MediaID != "" && body.Attachments[3].MessageType == "link" && body.Attachments[3].Link.Title == "Join runtime group" && body.Attachments[3].Link.Desc == "Runtime group" && body.Attachments[3].Link.URL == "https://work.weixin.qq.com/gm/runtime"
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || body.ChatType != "single" || len(body.External) != 1 || (body.External[0] != "runtime-external-1" && body.External[0] != "runtime-external-2") || body.Sender != "sender-a" || body.Text.Content != "runtime hello" || !validAttachments {
			s.record("invalid signed message body")
			http.Error(w, "bad message body", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls++
		call := s.calls
		s.mu.Unlock()
		if call == 4 {
			h, ok := w.(http.Hijacker)
			if !ok {
				s.record("httptest response writer cannot hijack")
				return
			}
			conn, _, err := h.Hijack()
			if err != nil {
				s.record("cannot terminate fourth provider response")
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"errcode":0,"msgid":"runtime-msg-%d"}`, call)))
	case "/cgi-bin/media/upload":
		if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "runtime-token" || (r.URL.Query().Get("type") != "image" && r.URL.Query().Get("type") != "file") {
			s.record("unexpected media upload")
			http.Error(w, "bad media upload", http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.MultipartForm == nil || len(r.MultipartForm.File["media"]) != 1 {
			s.record("invalid media upload form")
			http.Error(w, "bad media upload form", http.StatusBadRequest)
			return
		}
		file, err := r.MultipartForm.File["media"][0].Open()
		if err != nil {
			s.record("media upload read failure")
			http.Error(w, "bad media upload file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil || len(content) < 6 {
			s.record("empty media upload")
			http.Error(w, "bad media upload bytes", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.uploads++
		upload := s.uploads
		s.mu.Unlock()
		_, _ = w.Write([]byte(fmt.Sprintf(`{"errcode":0,"media_id":"runtime-upload-%d","created_at":%d}`, upload, time.Now().UTC().Unix())))
	default:
		s.record("unexpected provider path " + r.URL.Path)
		http.NotFound(w, r)
	}
}

func (s *automationAudienceWeComServer) record(value string) {
	s.mu.Lock()
	s.errs = append(s.errs, value)
	s.mu.Unlock()
}
func (s *automationAudienceWeComServer) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) != 0 {
		s.t.Errorf("provider assertions: %s", strings.Join(s.errs, "; "))
	}
	return s.calls
}
func (s *automationAudienceWeComServer) Uploads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploads
}

func automationAudienceInsertProviderStaff(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,login_enabled) VALUES('runtime-staff','$argon2id$runtime','Runtime sender','sender-a',false) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func automationAudienceInsertProviderCustomers(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []customerdomain.CustomerID {
	t.Helper()
	ids := make([]customerdomain.CustomerID, 0, 2)
	for _, external := range []string{"runtime-external-1", "runtime-external-2"} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:runtime-corp',$2,'verified','runtime-fixture',1,clock_timestamp())`, id, external); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, customerdomain.CustomerID(id))
	}
	return ids
}

type automationAudienceMaterials struct {
	imageID, miniID, attachmentID, inviteID int64
}

func automationAudienceCreateMedia(t *testing.T, ctx context.Context, repository *mediastore.Repository, actor int64) automationAudienceMaterials {
	t.Helper()
	image, err := repository.CreateImage(ctx, actor, "audience-runtime-image-0001", mediastore.ImageInput{FileName: "runtime.png", MIME: "image/png", Name: "Runtime image", Content: automationAudiencePNG(t), Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID, ok := image["id"].(int64)
	if !ok || imageID < 1 {
		t.Fatalf("image=%+v", image)
	}
	attachment, err := repository.CreateAttachment(ctx, actor, "audience-runtime-pdf-0001", mediastore.AttachmentInput{FileName: "runtime.pdf", Name: "Runtime PDF", Content: []byte("%PDF-1.4\nruntime fixture\n"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	attachmentID, ok := attachment["id"].(int64)
	if !ok || attachmentID < 1 {
		t.Fatalf("attachment=%+v", attachment)
	}
	mini, err := repository.CreateMiniProgram(ctx, actor, "audience-runtime-mini-0001", map[string]any{"name": "Runtime mini", "appid": "wx-runtime", "pagepath": "pages/runtime", "title": "Runtime card", "thumb_image_id": float64(imageID), "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	miniID, ok := mini["id"].(int64)
	if !ok || miniID < 1 {
		t.Fatalf("mini=%+v", mini)
	}
	invite, err := repository.CreateGroupInvite(ctx, actor, "audience-runtime-invite-0001", map[string]any{"name": "Runtime invite", "title": "Join runtime group", "description": "Runtime group", "join_url": "https://work.weixin.qq.com/gm/runtime", "cover_image_id": float64(imageID), "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	inviteID, ok := invite["id"].(int64)
	if !ok || inviteID < 1 {
		t.Fatalf("invite=%+v", invite)
	}
	return automationAudienceMaterials{imageID: imageID, miniID: miniID, attachmentID: attachmentID, inviteID: inviteID}
}

func automationAudiencePNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 30, G: 120, B: 240, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func automationAudiencePublishedAgent(t *testing.T, ctx context.Context, service *automationapp.Service, actor int64, materials automationAudienceMaterials) automationport.Agent {
	t.Helper()
	agent, err := service.Create(ctx, automationport.CreateCommand{Agent: automationport.Agent{AgentName: "Runtime fixed script", AgentCode: "runtime-fixed-script", AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusPaused, DraftRolePrompt: "runtime role", DraftTaskPrompt: "runtime task", FixedContentPackage: automationport.FixedContentPackage{ContentText: "runtime hello", ImageLibraryIDs: []int64{materials.imageID}, MiniprogramLibraryIDs: []int64{materials.miniID}, AttachmentLibraryIDs: []int64{materials.attachmentID}, GroupInviteLibraryIDs: []int64{materials.inviteID}}}, Actor: actor, IdempotencyKey: "audience-runtime-agent-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err = service.SetStatus(ctx, automationport.MutationCommand{ID: agent.ID, Actor: actor, IdempotencyKey: "audience-runtime-agent-active-0001"}, automationport.AgentStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

type automationAudienceSecurity struct{}

func (automationAudienceSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (automationAudienceSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return automationAudienceSecurity{}.Authenticate(context.Background(), nil)
}

func automationAudiencePackage(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *segmentstore.Repository, now time.Time) int64 {
	t.Helper()
	var packageID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		group, err := segmentdomain.NewGroup("automation runtime", 1, 1, now)
		if err != nil {
			return err
		}
		group, err = repo.CreateGroup(tx, group)
		if err != nil {
			return err
		}
		pkg, err := segmentdomain.NewPackage("automation-runtime", "automation runtime", &group.ID, 1, now)
		if err != nil {
			return err
		}
		pkg, err = repo.CreatePackage(tx, pkg)
		if err != nil {
			return err
		}
		config, err := segmentdomain.NewConfigurationVersion(pkg.ID, 1, json.RawMessage(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`), "", "manual", 1, now)
		if err != nil {
			return err
		}
		config, err = repo.CreateConfigurationVersion(tx, config)
		if err != nil {
			return err
		}
		pkg, err = repo.SetCurrentConfiguration(tx, pkg.ID, config.ID, pkg.Version, 1, now)
		if err != nil {
			return err
		}
		packageID = pkg.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return packageID
}
func automationAudienceCombinedDigest(content, materials [32]byte) string {
	raw := append(append([]byte{}, content[:]...), materials[:]...)
	out := sha256.Sum256(raw)
	return hex.EncodeToString(out[:])
}
func automationAudienceInt(v int64) string { return strconv.FormatInt(v, 10) }

func automationAudienceStartRuntime(t *testing.T, runtime *platformjobqueue.Runtime) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("River runtime stop=%v", err)
		}
	}
}

func automationAudienceEventually(t *testing.T, label string, ready func() bool) {
	automationAudienceEventuallyWithDiagnostics(t, label, ready, nil)
}

func automationAudienceEventuallyWithDiagnostics(t *testing.T, label string, ready func() bool, diagnostics func() string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			if diagnostics != nil {
				t.Fatalf("timed out waiting for %s; diagnostics=%s", label, diagnostics())
			}
			t.Fatalf("timed out waiting for %s", label)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// automationAudienceRuntimeDiagnostics retains the failing worker state in the
// integration-test output. It reads only the fixture schema and is never part
// of the runtime path.
func automationAudienceRuntimeDiagnostics(ctx context.Context, pool *pgxpool.Pool, provider *automationAudienceRecordingProvider, payloads *automationAudienceFrozenPayloadRecorder) string {
	if pool == nil {
		return "pool unavailable"
	}
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT json_build_object(
  'snapshots', (SELECT COALESCE(json_agg(to_jsonb(s) ORDER BY id), '[]'::json) FROM segment_audience_snapshots s),
  'events', (SELECT COALESCE(json_agg(to_jsonb(e) ORDER BY id), '[]'::json) FROM segment_audience_member_events e),
  'enrollments', (SELECT COALESCE(json_agg(to_jsonb(e) ORDER BY id), '[]'::json) FROM automation_enrollments e),
  'runs', (SELECT COALESCE(json_agg(to_jsonb(r) ORDER BY id), '[]'::json) FROM automation_runs r),
  'intents', (SELECT COALESCE(json_agg(to_jsonb(i) ORDER BY id), '[]'::json) FROM outbound_message_intents i),
  -- Keep AI completion diagnosis structural: fixture IDs, states, and attempt
  -- metadata only. It deliberately excludes source payload/content fields.
  'ai_plans', (SELECT COALESCE(json_agg(json_build_object('id',p.id,'state',p.state,'version',p.version,'target_count',p.target_count,'needs_attention_count',p.needs_attention_count) ORDER BY p.id), '[]'::json) FROM ai_assistant_plans p),
  'ai_recipients', (SELECT COALESCE(json_agg(json_build_object('id',r.id,'plan_id',r.plan_id,'review_state',r.review_state,'execution_state',r.execution_state,'version',r.version) ORDER BY r.id), '[]'::json) FROM ai_assistant_plan_recipients r),
  'ai_bindings', (SELECT COALESCE(json_agg(json_build_object('recipient_id',b.recipient_id,'effect_id',b.external_effect_id,'state',b.state,'generation',b.generation,'fence',b.fence,'attempt_count',b.attempt_count,'provider_accepted',b.provider_accepted,'delivery_proven',b.delivery_proven) ORDER BY b.recipient_id), '[]'::json) FROM ai_assistant_effect_bindings b),
  'effects', (SELECT COALESCE(json_agg(json_build_object('id',e.id,'owner',e.owner,'kind',e.kind,'state',e.state,'generation',e.generation,'attempt_count',e.attempt_count,'lease_fence',e.lease_fence) ORDER BY e.id), '[]'::json) FROM external_effects e),
  'river_jobs', (SELECT COALESCE(json_agg(to_jsonb(j) ORDER BY id), '[]'::json) FROM river_job j)
)`).Scan(&raw)
	if err != nil {
		return "diagnostics query: " + err.Error()
	}
	if (provider == nil || provider.Error() == "") && (payloads == nil || payloads.Error() == "") {
		return string(raw)
	}
	return string(raw) + "; provider_error=" + provider.Error() + "; frozen_payload_error=" + payloads.Error()
}

// automationAudienceNonBlockingQuietHours keeps the real River journey away
// from its own quiet period. Runtime enrollment intentionally uses the wall
// clock, so a fixed 22:00-08:00 UTC policy made this fixture wait until 08:00
// whenever CI happened to run overnight. Scheduling semantics, including the
// cross-midnight case, are asserted with fixed clocks in automation/app.
func automationAudienceNonBlockingQuietHours(now time.Time) json.RawMessage {
	start := now.UTC().Add(12 * time.Hour).Truncate(time.Minute)
	end := start.Add(time.Minute)
	return json.RawMessage(fmt.Sprintf(`{"timezone":"UTC","start":"%02d:%02d","end":"%02d:%02d"}`,
		start.Hour(), start.Minute(), end.Hour(), end.Minute()))
}

func automationAudienceRuntimePool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Automation audience runtime PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 8)
	if _, err = rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	schema := "automation_audience_runtime_" + hex.EncodeToString(bytes)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate automation audience journey")
	}
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0005_external_effects.sql", "0007_media.sql", "0013_automation_agents.sql", "0015_config_adminops.sql", "0036_ai_assistant_review.sql", "0037_outbound_private_messages.sql", "0039_segment_audience_configuration.sql", "0040_segment_audience_snapshots.sql", "0041_segment_audience_webhooks.sql", "0042_segment_audience_execution_bindings.sql", "0043_automation_runtime.sql", "0044_outbound_automation_messages.sql", "0045_segment_audience_member_events.sql", "0046_automation_run_reconciliations.sql", "0048_segment_audience_schedule_state.sql", "0053_segment_audience_member_event_fact_kinds.sql", "0083_segment_audience_refresh_modes.sql", "0085_segment_audience_refresh_kind.sql", "0087_automation_manual_ai_review.sql", "0089_outbound_message_content_snapshots.sql", "0094_runtime_config_releases.sql", "0097_segment_audience_mutation_actor.sql", "0100_ai_assistant_machine_actor.sql", "0115_automation_dynamic_text_generation.sql", "0120_excel_batches.sql", "0121_excel_delivery_receipts.sql", "0124_operation_excel_batch_lifecycle.sql", "0125_outbound_material_preparation.sql", "0126_media_material_source_snapshots.sql", "0201_automation_audience_direct_push.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("%s: %v", name, execErr)
		}
	}
	if err = ensureAccessLoginFixtureSchema(ctx, native); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
