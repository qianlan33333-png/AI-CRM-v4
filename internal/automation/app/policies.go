package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var (
	ErrRuntimeInvalid     = errors.New("invalid automation runtime request")
	ErrRuntimeNotFound    = errors.New("automation runtime record not found")
	ErrRuntimeConflict    = errors.New("automation runtime conflict")
	ErrRuntimeNotReady    = errors.New("automation runtime not ready")
	ErrRuntimeUnavailable = errors.New("automation runtime unavailable")
)

type RuntimeStore interface {
	ListPolicies(context.Context) ([]automationdomain.Policy, error)
	Policy(context.Context, int64) (automationdomain.Policy, error)
	CreatePolicy(context.Context, automationdomain.Policy) (automationdomain.Policy, error)
	LockPolicy(context.Context, int64) (automationdomain.Policy, error)
	NextPolicyVersion(context.Context, int64) (int64, error)
	CreatePolicyVersion(context.Context, automationdomain.PolicyVersion) (automationdomain.PolicyVersion, error)
	SetCurrentPolicyVersion(context.Context, int64, int64, int64, int64, time.Time) (automationdomain.Policy, error)
	CurrentPolicyVersion(context.Context, int64) (automationdomain.PolicyVersion, error)
	SetPolicyLifecycle(context.Context, int64, int64, int64, automationdomain.PolicyLifecycle, time.Time) (automationdomain.Policy, error)
	ActivePoliciesForPackage(context.Context, int64) ([]automationdomain.PolicyVersion, error)
	EnrollmentForSource(context.Context, int64, [32]byte, int64) (automationdomain.Enrollment, bool, error)
	CreateEnrollment(context.Context, automationdomain.Enrollment) (automationdomain.Enrollment, bool, error)
	RuntimeReceipt(context.Context, string, string, [32]byte, [32]byte) (RuntimeReceipt, bool, error)
	ReserveRuntime(context.Context, RuntimeReservation) (RuntimeReceipt, bool, error)
	CompleteRuntime(context.Context, int64, json.RawMessage, time.Time) error
	AppendRuntimeFact(context.Context, RuntimeFact) error
	CreatePreview(context.Context, automationdomain.RunPreview) (automationdomain.RunPreview, error)
	PreviewByDigest(context.Context, [32]byte) (automationdomain.RunPreview, error)
	CreateRun(context.Context, automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient) (automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient, error)
	BindRecipientEffect(context.Context, int64, string, time.Time) error
	ListRuns(context.Context, int64, int) ([]automationdomain.RuntimeRun, string, error)
	Run(context.Context, int64) (automationdomain.RuntimeRun, error)
	RunRecipients(context.Context, int64, int64, int) ([]automationdomain.RuntimeRecipient, string, error)
	RecipientForEffect(context.Context, int64, string) (automationdomain.RuntimeRecipient, error)
	CreateRunReconciliation(context.Context, automationdomain.RunReconciliation) (automationdomain.RunReconciliation, error)
	CancelRun(context.Context, int64, time.Time) (automationdomain.RuntimeRun, error)
	CreateGenerationItems(context.Context, []automationdomain.GenerationItem) ([]automationdomain.GenerationItem, error)
	BindGenerationEffect(context.Context, int64, string, time.Time) error
	GenerationByEffect(context.Context, string) (automationdomain.GenerationItem, error)
	SettleGeneration(context.Context, automationport.GenerationCompletion) (automationdomain.GenerationItem, automationdomain.RuntimeRun, bool, error)
	GenerationItemsForPlan(context.Context, int64) ([]automationdomain.GenerationItem, error)
	AttachGenerationPlan(context.Context, int64, int64, time.Time) error
	GenerationProgress(context.Context, int64) (automationport.GenerationProgress, error)
	GenerationItems(context.Context, int64, int64, int) ([]automationport.GenerationItem, string, error)
}
type reviewPlanGateway interface {
	aiassistantport.TransactionalIntake
	aiassistantport.Reader
}

type RuntimeService struct {
	uow               platformport.UnitOfWork
	store             RuntimeStore
	audiences         segmentport.ExecutionConfigurationReader
	snapshots         segmentport.SnapshotReader
	messages          outboundport.TransactionalMessageAccepter
	effects           effectport.TransactionalReconciler
	reviewPlans       reviewPlanGateway
	content           automationport.OutboundPublishedContentReader
	contentFreezer    automationport.OutboundContentFreezer
	generationEffects effectport.TransactionalAccepter
	generationContext automationport.GenerationContextReader
	generationAgents  automationport.PublishedGenerationReader
	generationPolicy  automationport.GenerationModelPolicyReader
	runtimeConfig     configport.EffectiveReader
	runtimeUsage      configport.UsageRecorder
	now               func() time.Time
}
type PolicyCommand struct {
	Code, Name                string
	PolicyID, ExpectedVersion int64
	PackageID                 segmentport.PackageID
	TriggerKind               automationport.TriggerKind
	ActionKind                automationport.ActionKind
	ActionConfig, QuietHours  json.RawMessage
	SingleRunLimit            int
	ApprovalStaffID           *int64
	Actor                     int64
	IdempotencyKey            string
}
type PolicyLifecycleCommand struct {
	PolicyID, ExpectedVersion, Actor int64
	Target                           automationdomain.PolicyLifecycle
	IdempotencyKey                   string
}

// NewRuntimeService keeps the pre-release construction contract for focused
// tests and legacy composition callers. Production composition uses
// NewRuntimeServiceWithRuntimeConfig so Config owns the effective value.
func NewRuntimeService(uow platformport.UnitOfWork, store RuntimeStore, audiences segmentport.ExecutionConfigurationReader, snapshots segmentport.SnapshotReader, recipientLimit int) (*RuntimeService, error) {
	if recipientLimit < 1 || recipientLimit > aiassistantport.MaxRecipients {
		return nil, ErrRuntimeNotReady
	}
	return newRuntimeService(uow, store, audiences, snapshots, fixedRuntimeConfig{snapshot: configport.EffectiveSnapshot{Revision: 0, Source: configport.RuntimeSourceEnvironmentDefault, AutomationMaxRecipients: recipientLimit}}, nil)
}

// NewRuntimeServiceWithRuntimeConfig is the production seam: the same typed
// Config snapshot is frozen into manual previews and existing River-dispatched
// member-event runs. Usage rows are written only at those real boundaries.
func NewRuntimeServiceWithRuntimeConfig(uow platformport.UnitOfWork, store RuntimeStore, audiences segmentport.ExecutionConfigurationReader, snapshots segmentport.SnapshotReader, reader configport.EffectiveReader, usage configport.UsageRecorder) (*RuntimeService, error) {
	return newRuntimeService(uow, store, audiences, snapshots, reader, usage)
}

func newRuntimeService(uow platformport.UnitOfWork, store RuntimeStore, audiences segmentport.ExecutionConfigurationReader, snapshots segmentport.SnapshotReader, reader configport.EffectiveReader, usage configport.UsageRecorder) (*RuntimeService, error) {
	if uow == nil || store == nil || audiences == nil || snapshots == nil || reader == nil {
		return nil, ErrRuntimeNotReady
	}
	return &RuntimeService{uow: uow, store: store, audiences: audiences, snapshots: snapshots, runtimeConfig: reader, runtimeUsage: usage, now: time.Now}, nil
}

type fixedRuntimeConfig struct{ snapshot configport.EffectiveSnapshot }

func (f fixedRuntimeConfig) EffectiveSnapshot(context.Context) (configport.EffectiveSnapshot, error) {
	return f.snapshot, nil
}
func (f fixedRuntimeConfig) EffectiveSnapshotWithin(context.Context) (configport.EffectiveSnapshot, error) {
	return f.snapshot, nil
}

func validRuntimeConfigSnapshot(snapshot configport.EffectiveSnapshot) bool {
	return (snapshot.Source == configport.RuntimeSourceEnvironmentDefault || snapshot.Source == configport.RuntimeSourcePublished) && snapshot.Revision >= 0 && snapshot.AutomationMaxRecipients >= 1 && snapshot.AutomationMaxRecipients <= aiassistantport.MaxRecipients
}

func (s *RuntimeService) runtimeConfigWithin(ctx context.Context) (configport.EffectiveSnapshot, error) {
	if s == nil || s.runtimeConfig == nil {
		return configport.EffectiveSnapshot{}, ErrRuntimeNotReady
	}
	snapshot, err := s.runtimeConfig.EffectiveSnapshotWithin(ctx)
	if err != nil || !validRuntimeConfigSnapshot(snapshot) {
		return configport.EffectiveSnapshot{}, ErrRuntimeUnavailable
	}
	return snapshot, nil
}

func (s *RuntimeService) recordRuntimeConfigUsage(ctx context.Context, snapshot configport.EffectiveSnapshot, role, operation, subjectKind string, subjectID int64, now time.Time) error {
	if s.runtimeUsage == nil {
		return nil
	}
	if err := s.runtimeUsage.RecordRuntimeUsage(ctx, configport.RuntimeUsage{Snapshot: snapshot, Consumer: string(configport.AutomationOperationsMaxRecipientsPerRun), Role: role, Operation: operation, SubjectKind: subjectKind, SubjectID: subjectID, UsedAt: now.UTC()}); err != nil {
		return ErrRuntimeUnavailable
	}
	return nil
}

// SetRuntimeConfig is composition-time wiring only. The service is not
// exposed until cmd/aicrm has bound the Config-owned reader and usage writer.
func (s *RuntimeService) SetRuntimeConfig(reader configport.EffectiveReader, usage configport.UsageRecorder) error {
	if s == nil || reader == nil || usage == nil {
		return ErrRuntimeNotReady
	}
	s.runtimeConfig, s.runtimeUsage = reader, usage
	return nil
}

func (s *RuntimeService) SetMessageAccepter(messages outboundport.TransactionalMessageAccepter) error {
	if s == nil || messages == nil {
		return ErrRuntimeNotReady
	}
	s.messages = messages
	return nil
}

func (s *RuntimeService) SetEffectReconciler(effects effectport.TransactionalReconciler) error {
	if s == nil || effects == nil {
		return ErrRuntimeNotReady
	}
	s.effects = effects
	return nil
}

// SetReviewPlanIntake binds the existing AI Assistant review owner for manual
// audience broadcasts. It is intentionally separate from the automatic
// outbound accepter: a manual confirmation must create a pending plan and
// accept no external effect until that plan is approved.
func (s *RuntimeService) SetReviewPlanIntake(intake reviewPlanGateway, content automationport.OutboundPublishedContentReader) error {
	if s == nil || intake == nil || content == nil {
		return ErrRuntimeNotReady
	}
	s.reviewPlans, s.content = intake, content
	return nil
}

// SetDynamicGenerationDependencies deliberately binds only stable read ports,
// the existing transactional accepter, and the existing AI review intake. No
// caller receives a provider client, queue, or identity-write capability.
func (s *RuntimeService) SetDynamicGenerationDependencies(effects effectport.TransactionalAccepter, contexts automationport.GenerationContextReader, agents automationport.PublishedGenerationReader, policy automationport.GenerationModelPolicyReader) error {
	if s == nil || effects == nil || contexts == nil || agents == nil || policy == nil {
		return ErrRuntimeNotReady
	}
	s.generationEffects, s.generationContext, s.generationAgents, s.generationPolicy = effects, contexts, agents, policy
	return nil
}

// SetOutboundContentFreezer keeps the automatic path inside the existing
// Automation UoW while delegating Media source capture through a stable port.
func (s *RuntimeService) SetOutboundContentFreezer(freezer automationport.OutboundContentFreezer) error {
	if s == nil || freezer == nil {
		return ErrRuntimeNotReady
	}
	s.contentFreezer = freezer
	return nil
}

func (s *RuntimeService) ListPolicies(ctx context.Context) ([]automationdomain.Policy, error) {
	var out []automationdomain.Policy
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.ListPolicies(tx); return e })
	return out, runtimeClassify(err)
}
func (s *RuntimeService) Policy(ctx context.Context, id int64) (automationdomain.Policy, automationdomain.PolicyVersion, error) {
	if id < 1 {
		return automationdomain.Policy{}, automationdomain.PolicyVersion{}, ErrRuntimeInvalid
	}
	var p automationdomain.Policy
	var v automationdomain.PolicyVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		p, e = s.store.Policy(tx, id)
		if e == nil {
			v, e = s.store.CurrentPolicyVersion(tx, id)
		}
		return e
	})
	return p, v, runtimeClassify(err)
}
func (s *RuntimeService) CreatePolicy(ctx context.Context, c PolicyCommand) (automationdomain.Policy, error) {
	if !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.Policy{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	p, err := automationdomain.NewPolicy(c.Code, c.Name, c.Actor, now)
	if err != nil {
		return p, ErrRuntimeInvalid
	}
	payload, _ := json.Marshal(c)
	var output automationdomain.Policy
	err = s.runtimeMutation(ctx, "create_policy", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		created, e := s.store.CreatePolicy(tx, p)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		v, e := automationdomain.NewPolicyVersion(created.ID, 1, c.PackageID, c.TriggerKind, c.ActionKind, c.ActionConfig, c.QuietHours, c.SingleRunLimit, c.ApprovalStaffID, c.Actor, now)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		v, e = s.store.CreatePolicyVersion(tx, v)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		created, e = s.store.SetCurrentPolicyVersion(tx, created.ID, v.ID, created.Version, c.Actor, now)
		return created, runtimeFact("policy", created.ID, "create", "automation.policy.created.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) PutPolicyVersion(ctx context.Context, c PolicyCommand) (automationdomain.PolicyVersion, error) {
	if c.PolicyID < 1 || c.ExpectedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.PolicyVersion{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	var output automationdomain.PolicyVersion
	err := s.runtimeMutation(ctx, "put_policy_version", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		p, e := s.store.LockPolicy(tx, c.PolicyID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if p.Version != c.ExpectedVersion || p.Lifecycle != automationdomain.PolicyPaused {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		version, e := s.store.NextPolicyVersion(tx, p.ID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		output, e = automationdomain.NewPolicyVersion(p.ID, version, c.PackageID, c.TriggerKind, c.ActionKind, c.ActionConfig, c.QuietHours, c.SingleRunLimit, c.ApprovalStaffID, c.Actor, now)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		output, e = s.store.CreatePolicyVersion(tx, output)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		_, e = s.store.SetCurrentPolicyVersion(tx, p.ID, output.ID, p.Version, c.Actor, now)
		return output, runtimeFact("policy", p.ID, "version", "automation.policy.versioned.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) TransitionPolicy(ctx context.Context, c PolicyLifecycleCommand) (automationdomain.Policy, error) {
	if c.PolicyID < 1 || c.ExpectedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) || (c.Target != automationdomain.PolicyActive && c.Target != automationdomain.PolicyPaused && c.Target != automationdomain.PolicyArchived) {
		return automationdomain.Policy{}, ErrRuntimeInvalid
	}
	if c.Target == automationdomain.PolicyActive {
		_, version, e := s.Policy(ctx, c.PolicyID)
		if e != nil {
			return automationdomain.Policy{}, e
		}
		configuration, e := s.audiences.AudienceExecutionConfiguration(ctx, version.PackageID)
		if e != nil {
			return automationdomain.Policy{}, ErrRuntimeUnavailable
		}
		if !configuration.Ready || !policyExecutionConfigurationMatches(version, configuration) {
			return automationdomain.Policy{}, ErrRuntimeNotReady
		}
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	var output automationdomain.Policy
	err := s.runtimeMutation(ctx, "transition_policy", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		p, e := s.store.LockPolicy(tx, c.PolicyID)
		if e != nil {
			return p, RuntimeFact{}, e
		}
		if p.Version != c.ExpectedVersion {
			return p, RuntimeFact{}, ErrRuntimeConflict
		}
		if c.Target == automationdomain.PolicyActive {
			v, loadErr := s.store.CurrentPolicyVersion(tx, p.ID)
			if loadErr != nil {
				return p, RuntimeFact{}, loadErr
			}
			if !v.TriggerEnabled {
				return p, RuntimeFact{}, ErrRuntimeNotReady
			}
		}
		p, e = s.store.SetPolicyLifecycle(tx, p.ID, p.Version, c.Actor, c.Target, now)
		return p, runtimeFact("policy", p.ID, "transition", "automation.policy.transitioned.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) EnrollAudienceMember(ctx context.Context, event segmentport.MemberEnteredV1) ([]automationdomain.Enrollment, error) {
	if s == nil || event.PackageID < 1 || event.CustomerID < 1 || event.EventID == "" || event.OccurredAt.IsZero() {
		return nil, ErrRuntimeInvalid
	}
	eventDigest := sha256.Sum256([]byte(event.EventID))
	observed := []automationdomain.PolicyVersion{}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		observed, e = s.store.ActivePoliciesForPackage(tx, int64(event.PackageID))
		return e
	})
	if err != nil || len(observed) == 0 {
		return nil, runtimeClassify(err)
	}
	// Check the immutable source receipt before reading current execution
	// configuration. A replay of the same member-entered fact must return its
	// already-frozen enrollment even if a later Config release is active.
	existingByVersion := make(map[int64]automationdomain.Enrollment, len(observed))
	err = s.uow.Within(ctx, func(tx context.Context) error {
		for _, version := range observed {
			existing, found, e := s.store.EnrollmentForSource(tx, version.ID, eventDigest, int64(event.CustomerID))
			if e != nil {
				return e
			}
			if found {
				if !enrollmentMatchesMemberEnteredEvent(existing, version, event) {
					return ErrRuntimeConflict
				}
				existingByVersion[version.ID] = existing
			}
		}
		return nil
	})
	if err != nil {
		return nil, runtimeClassify(err)
	}
	needsOutbound := false
	for _, version := range observed {
		_, replay := existingByVersion[version.ID]
		needsOutbound = needsOutbound || (!replay && version.ActionKind == automationport.ActionOutboundMessage)
	}
	var configuration segmentport.ExecutionConfiguration
	var published automationport.OutboundPublishedContent
	if needsOutbound {
		configuration, err = s.audiences.AudienceExecutionConfiguration(ctx, event.PackageID)
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
		if !configuration.Ready {
			return nil, ErrRuntimeNotReady
		}
		if s.content == nil || s.contentFreezer == nil {
			return nil, ErrRuntimeNotReady
		}
		var found bool
		published, found, err = s.content.OutboundPublishedContent(ctx, automationport.AgentID(configuration.AgentID), configuration.AgentPublishedVersion)
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
		if !found || published.ContentDigest != configuration.ContentDigest {
			return nil, ErrRuntimeNotReady
		}
	}
	observedByID := make(map[int64]automationdomain.PolicyVersion, len(observed))
	for _, version := range observed {
		observedByID[version.ID] = version
	}
	now := s.now().UTC()
	output := []automationdomain.Enrollment{}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		versions, e := s.store.ActivePoliciesForPackage(tx, int64(event.PackageID))
		if e != nil {
			return e
		}
		var runtimeConfig configport.EffectiveSnapshot
		if needsOutbound {
			runtimeConfig, e = s.runtimeConfigWithin(tx)
			if e != nil {
				return e
			}
		}
		for _, v := range versions {
			prior, wasObserved := observedByID[v.ID]
			if !wasObserved || prior.Digest != v.Digest {
				continue
			}
			// Re-read in the write transaction to close the check/create race. A
			// stored enrollment is a replay only when its immutable source facts
			// still match; a reused EventID may never change package, snapshot,
			// configuration, or customer merely because Config has advanced.
			if existing, found, lookupErr := s.store.EnrollmentForSource(tx, v.ID, eventDigest, int64(event.CustomerID)); lookupErr != nil {
				return lookupErr
			} else if found {
				if !enrollmentMatchesMemberEnteredEvent(existing, v, event) {
					return ErrRuntimeConflict
				}
				output = append(output, existing)
				continue
			}
			if v.ActionKind == automationport.ActionOutboundMessage && !policyExecutionConfigurationMatches(v, configuration) {
				return ErrRuntimeNotReady
			}
			snapshotFields := map[string]any{"action_kind": v.ActionKind, "action_config": json.RawMessage(v.ActionConfig), "package_id": event.PackageID, "snapshot_id": event.SnapshotID, "configuration_version_id": event.ConfigurationVersionID, "customer_id": event.CustomerID, "policy_version_id": v.ID, "policy_digest": hex.EncodeToString(v.Digest[:])}
			if v.ActionKind == automationport.ActionOutboundMessage {
				if runtimeConfig.AutomationMaxRecipients < 1 {
					return ErrRuntimeNotReady
				}
				snapshotFields["agent_id"] = configuration.AgentID
				snapshotFields["agent_published_version"] = configuration.AgentPublishedVersion
				snapshotFields["binding_version"] = configuration.BindingVersion
				snapshotFields["sender_set_version"] = configuration.SenderSetVersion
				snapshotFields["content_digest"] = hex.EncodeToString(configuration.ContentDigest[:])
				snapshotFields["runtime_config_revision"] = runtimeConfig.Revision
				snapshotFields["max_recipients_per_run"] = runtimeConfig.AutomationMaxRecipients
			}
			snapshot, _ := json.Marshal(snapshotFields)
			actionDigest := sha256.Sum256(snapshot)
			state := "accepted"
			if v.ActionKind == automationport.ActionRecord {
				state = "recorded"
			}
			enrollment, owned, e := s.store.CreateEnrollment(tx, automationdomain.Enrollment{PolicyID: v.PolicyID, PolicyVersionID: v.ID, SourceEventDigest: eventDigest, CustomerID: int64(event.CustomerID), ActionKind: v.ActionKind, ActionSnapshot: snapshot, ActionDigest: actionDigest, State: state, CreatedAt: now})
			if e != nil {
				return e
			}
			// A concurrent writer can win the unique tuple after the re-read above.
			// Its stored snapshot must satisfy the same source-fact check, without
			// comparing the current runtime Config revision or limit.
			if !enrollmentMatchesMemberEnteredEvent(enrollment, v, event) {
				return ErrRuntimeConflict
			}
			output = append(output, enrollment)
			if owned {
				payload, _ := json.Marshal(map[string]any{"enrollment_id": enrollment.ID, "policy_id": v.PolicyID, "customer_id": event.CustomerID, "action_kind": v.ActionKind})
				actor := v.CreatedBy
				if v.ApprovalStaffID != nil {
					actor = *v.ApprovalStaffID
				}
				if e = s.store.AppendRuntimeFact(tx, runtimeFact("enrollment", enrollment.ID, "enroll", "automation.enrollment.created.v1", actor, hex.EncodeToString(eventDigest[:])+fmt.Sprint(v.ID), now, payload)); e != nil {
					return e
				}
				if v.ActionKind == automationport.ActionOutboundMessage {
					if e = s.acceptEnrollmentMessage(tx, event, v, configuration, published, enrollment, actionDigest, runtimeConfig, actor, now); e != nil {
						return e
					}
				}
			}
		}
		return nil
	})
	return output, runtimeClassify(err)
}

// enrollmentMatchesMemberEnteredEvent validates only immutable Segment facts
// captured in an enrollment. Runtime Config values deliberately remain outside
// this comparison: the historical snapshot retains them for execution, while a
// replay of the same source event may occur after a later release is active.
func enrollmentMatchesMemberEnteredEvent(enrollment automationdomain.Enrollment, version automationdomain.PolicyVersion, event segmentport.MemberEnteredV1) bool {
	if enrollment.PolicyID != version.PolicyID || enrollment.PolicyVersionID != version.ID || enrollment.CustomerID != int64(event.CustomerID) || enrollment.ActionKind != version.ActionKind {
		return false
	}
	var frozen struct {
		ActionKind             automationport.ActionKind `json:"action_kind"`
		PackageID              int64                     `json:"package_id"`
		SnapshotID             int64                     `json:"snapshot_id"`
		ConfigurationVersionID int64                     `json:"configuration_version_id"`
		CustomerID             int64                     `json:"customer_id"`
		PolicyVersionID        int64                     `json:"policy_version_id"`
	}
	if json.Unmarshal(enrollment.ActionSnapshot, &frozen) != nil {
		return false
	}
	return frozen.ActionKind == version.ActionKind &&
		frozen.PackageID == int64(event.PackageID) &&
		frozen.SnapshotID == int64(event.SnapshotID) &&
		frozen.ConfigurationVersionID == int64(event.ConfigurationVersionID) &&
		frozen.CustomerID == int64(event.CustomerID) &&
		frozen.PolicyVersionID == version.ID
}

func (s *RuntimeService) acceptEnrollmentMessage(ctx context.Context, event segmentport.MemberEnteredV1, version automationdomain.PolicyVersion, configuration segmentport.ExecutionConfiguration, published automationport.OutboundPublishedContent, enrollment automationdomain.Enrollment, actionDigest [32]byte, runtimeConfig configport.EffectiveSnapshot, actor int64, now time.Time) error {
	if s.messages == nil || len(configuration.SenderStaffIDs) == 0 {
		return ErrRuntimeNotReady
	}
	contentSnapshot, contentSnapshotDigest, err := s.contentFreezer.FreezeOutboundContent(ctx, published)
	if err != nil || len(contentSnapshot) == 0 || contentSnapshotDigest == ([32]byte{}) {
		return ErrRuntimeNotReady
	}
	eventDigest := sha256.Sum256([]byte(event.EventID))
	previewDigest := sha256.Sum256(append(append(append([]byte{}, eventDigest[:]...), version.Digest[:]...), actionDigest[:]...))
	if !validRuntimeConfigSnapshot(runtimeConfig) || runtimeConfig.AutomationMaxRecipients < 1 {
		return ErrRuntimeNotReady
	}
	run := automationdomain.RuntimeRun{PolicyID: version.PolicyID, PolicyVersion: version.Version, PackageID: int64(event.PackageID), PackageVersion: configuration.PackageVersion, SnapshotID: int64(event.SnapshotID), AgentID: configuration.AgentID, AgentPublishedVersion: configuration.AgentPublishedVersion, BindingVersion: configuration.BindingVersion, SenderSetVersion: configuration.SenderSetVersion, RuntimeConfigObserved: true, RuntimeConfigRevision: runtimeConfig.Revision, MaxRecipientsPerRun: runtimeConfig.AutomationMaxRecipients, PreviewDigest: previewDigest, State: automationport.RunExecuting, TargetCount: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	senderIndex := int((int64(event.CustomerID) - 1) % int64(len(configuration.SenderStaffIDs)))
	recipients := []automationdomain.RuntimeRecipient{{CustomerID: int64(event.CustomerID), SenderStaffID: configuration.SenderStaffIDs[senderIndex], State: automationport.RecipientAccepted}}
	created, createdRecipients, err := s.store.CreateRun(ctx, run, recipients)
	if err != nil || len(createdRecipients) != 1 {
		if err != nil {
			return err
		}
		return ErrRuntimeConflict
	}
	recipient := createdRecipients[0]
	sourceDigest := sha256.Sum256([]byte(fmt.Sprintf("automation-enrollment:%d:recipient:%d", enrollment.ID, recipient.ID)))
	targetDigest := sha256.Sum256([]byte(fmt.Sprintf("customer:%d", recipient.CustomerID)))
	acceptance, err := s.messages.AcceptMessageWithin(ctx, outboundport.MessageIntent{SourceKind: "automation_enrollment", SourceID: enrollment.ID, RunRecipientID: recipient.ID, CustomerID: customerdomain.CustomerID(recipient.CustomerID), SenderStaffID: recipient.SenderStaffID, AgentID: created.AgentID, AgentPublishedVersion: created.AgentPublishedVersion, ContentReference: fmt.Sprintf("automation-agent:%d:published:%d", created.AgentID, created.AgentPublishedVersion), SourceDigest: sourceDigest, TargetDigest: targetDigest, PayloadDigest: configuration.ContentDigest, ContentSnapshot: contentSnapshot, ContentSnapshotDigest: contentSnapshotDigest, PolicyDigest: version.Digest, ReceiptKey: fmt.Sprintf("automation-enrollment-%d-recipient-%d", enrollment.ID, recipient.ID), ScheduledAt: nextAllowedExecution(now, version.QuietHours)})
	if err != nil {
		return err
	}
	if err = s.store.BindRecipientEffect(ctx, recipient.ID, acceptance.EffectID, now); err != nil {
		return err
	}
	if err = s.recordRuntimeConfigUsage(ctx, runtimeConfig, "worker", "execution", "automation_run", created.ID, now); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"run_id": created.ID, "enrollment_id": enrollment.ID, "policy_id": version.PolicyID, "policy_version": version.Version, "recipient_id": recipient.ID, "effect_id": acceptance.EffectID, "runtime_config_revision": runtimeConfig.Revision})
	return s.store.AppendRuntimeFact(ctx, runtimeFact("run", created.ID, "enroll", "automation.run.queued.v1", actor, fmt.Sprintf("%x", previewDigest), now, payload))
}

func policyExecutionConfigurationMatches(version automationdomain.PolicyVersion, configuration segmentport.ExecutionConfiguration) bool {
	if version.PackageID != configuration.PackageID || !configuration.Ready {
		return false
	}
	if version.ActionKind != automationport.ActionOutboundMessage {
		return true
	}
	var action struct {
		AgentID int64 `json:"agent_id"`
	}
	return json.Unmarshal(version.ActionConfig, &action) == nil && action.AgentID == configuration.AgentID && configuration.AgentPublishedVersion > 0 && len(configuration.SenderStaffIDs) > 0
}

func nextAllowedExecution(now time.Time, raw json.RawMessage) time.Time {
	var quiet automationdomain.QuietHours
	if json.Unmarshal(raw, &quiet) != nil {
		return time.Time{}
	}
	location, err := time.LoadLocation(quiet.Timezone)
	if err != nil {
		return time.Time{}
	}
	startClock, startErr := time.Parse("15:04", quiet.Start)
	endClock, endErr := time.Parse("15:04", quiet.End)
	if startErr != nil || endErr != nil || quiet.Start == quiet.End {
		return time.Time{}
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), startClock.Hour(), startClock.Minute(), 0, 0, location)
	end := time.Date(local.Year(), local.Month(), local.Day(), endClock.Hour(), endClock.Minute(), 0, 0, location)
	if end.After(start) {
		if !local.Before(start) && local.Before(end) {
			return end.UTC()
		}
		return time.Time{}
	}
	if !local.Before(start) {
		return end.AddDate(0, 0, 1).UTC()
	}
	if local.Before(end) {
		return end.UTC()
	}
	return time.Time{}
}
func (s *RuntimeService) runtimeMutation(ctx context.Context, operation string, actor int64, key string, payload json.RawMessage, apply func(context.Context) (any, RuntimeFact, error), target any) error {
	if s == nil || !validRuntimeMutation(actor, key) {
		return ErrRuntimeInvalid
	}
	now := s.now().UTC()
	return s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.ReserveRuntime(tx, RuntimeReservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now})
		if e != nil {
			return e
		}
		if !owned {
			if receipt.State != "completed" || len(receipt.Result) == 0 {
				return ErrRuntimeConflict
			}
			return json.Unmarshal(receipt.Result, target)
		}
		value, fact, e := apply(tx)
		if e != nil {
			return e
		}
		result, e := json.Marshal(value)
		if e != nil {
			return e
		}
		if e = s.store.AppendRuntimeFact(tx, fact); e != nil {
			return e
		}
		if e = s.store.CompleteRuntime(tx, receipt.ID, result, now); e != nil {
			return e
		}
		return json.Unmarshal(result, target)
	})
}
func runtimeFact(kind string, id int64, operation, event string, actor int64, key string, at time.Time, payload ...json.RawMessage) RuntimeFact {
	body := json.RawMessage(nil)
	if len(payload) > 0 {
		body = payload[0]
	} else {
		body, _ = json.Marshal(map[string]any{"resource_id": id})
	}
	return RuntimeFact{Kind: kind, ID: id, Operation: operation, EventType: event, Actor: actor, Payload: body, Key: operation + ":" + key, At: at}
}
func validRuntimeMutation(actor int64, key string) bool {
	return actor > 0 && len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(key) == key
}
func runtimeClassify(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrRuntimeInvalid), errors.Is(err, ErrRuntimeNotFound), errors.Is(err, ErrRuntimeConflict), errors.Is(err, ErrRuntimeNotReady):
		return err
	case errors.Is(err, effectport.ErrReconciliationNotFound):
		return ErrRuntimeNotFound
	case errors.Is(err, effectport.ErrReconciliationConflict):
		return ErrRuntimeConflict
	case errors.Is(err, automationdomain.ErrInvalidPolicy):
		return ErrRuntimeInvalid
	default:
		return ErrRuntimeUnavailable
	}
}
