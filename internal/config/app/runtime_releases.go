package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	config "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrRuntimeReleaseNotFound = configport.ErrRuntimeReleaseNotFound
	ErrRuntimeReleaseConflict = configport.ErrRuntimeReleaseConflict
	ErrRuntimeReleaseInvalid  = configport.ErrRuntimeReleaseInvalid
)

type runtimeReleaseRepository interface {
	ActiveRuntimeRevision(context.Context, bool) (int64, error)
	ActiveRuntimeRelease(context.Context) (configport.RuntimeRelease, bool, error)
	GetRuntimeRelease(context.Context, int64, bool) (configport.RuntimeRelease, error)
	ListRuntimeReleases(context.Context, int) ([]configport.RuntimeRelease, error)
	InsertRuntimeRelease(context.Context, configport.RuntimeRelease) (configport.RuntimeRelease, error)
	SetRuntimeReleaseValidation(context.Context, int64, configport.RuntimeReleaseState, []configport.RuntimeValidationIssue, time.Time) (configport.RuntimeRelease, error)
	PublishRuntimeRelease(context.Context, int64, int64, string, time.Time) (configport.RuntimeRelease, error)
	SetActiveRuntimeRelease(context.Context, int64, time.Time) error
	AppendRuntimeReleaseAudit(context.Context, int64, string, string, string, time.Time) error
	ReserveRuntimeReleaseCommand(context.Context, string, string, string, []byte, time.Time) (configport.RuntimeReleaseReceipt, bool, error)
	CompleteRuntimeReleaseCommand(context.Context, int64, int64, time.Time) error
	InsertRuntimeUsage(context.Context, configport.RuntimeUsage) error
	ListRuntimeUsage(context.Context, int64, int) ([]configport.RuntimeUsage, error)
	InsertRuntimeApplication(context.Context, configport.RuntimeApplication) error
	ListRuntimeApplications(context.Context, int) ([]configport.RuntimeApplication, error)
}

// RuntimeReleaseService owns Config's draft/validation/publish/rollback state.
// Runtime values are immutable versions; changing active is a pointer update,
// never an environment-file or Provider operation.
type RuntimeReleaseService struct {
	uow          platformport.UnitOfWork
	repo         runtimeReleaseRepository
	events       configport.EventAppender
	defaultLimit int
	defaults     []configport.RuntimeSetting
	protected    map[string]bool
	guards       RuntimeActivationGuards
	now          func() time.Time
}

type RuntimeActivationGuards struct {
	WeComEnabled, MessageArchiveEnabled, AutomationProviderEnabled, AIDispatchEnabled             bool
	AIAssistantIntakeEnabled, AIAgentGenerationEnabled, WeChatPayEnabled, WeChatPayH5OAuthEnabled bool
	WeChatShopEnabled, AlipayEnabled, SurveyOAuthEnabled                                          bool
}

type RuntimeReleaseOption func(*RuntimeReleaseService) error

func WithRuntimeActivationGuards(guards RuntimeActivationGuards) RuntimeReleaseOption {
	return func(service *RuntimeReleaseService) error { service.guards = guards; return nil }
}

func WithProtectedReferencePresence(presence map[string]bool) RuntimeReleaseOption {
	return func(service *RuntimeReleaseService) error {
		service.protected = make(map[string]bool, len(presence))
		for reference, configured := range presence {
			if !strings.HasPrefix(reference, "environment://") {
				return ErrRuntimeReleaseInvalid
			}
			service.protected[reference] = configured
		}
		return nil
	}
}

// WithRuntimeDefaults supplies the closed environment snapshot used until a
// version overrides a key. It is intentionally values-only: secrets never
// enter Config's release tables or its public Port.
func WithRuntimeDefaults(settings []configport.RuntimeSetting) RuntimeReleaseOption {
	return func(service *RuntimeReleaseService) error {
		canonical, issues := config.ValidateRuntimeSettings(settings)
		if len(issues) != 0 {
			return ErrRuntimeReleaseInvalid
		}
		service.defaults = canonical
		return nil
	}
}

func NewRuntimeReleaseService(uow platformport.UnitOfWork, repo runtimeReleaseRepository, events configport.EventAppender, defaultLimit int, options ...RuntimeReleaseOption) (*RuntimeReleaseService, error) {
	if uow == nil || repo == nil || events == nil || defaultLimit < 1 || defaultLimit > 5000 {
		return nil, ErrRuntimeReleaseInvalid
	}
	value, _ := json.Marshal(defaultLimit)
	service := &RuntimeReleaseService{uow: uow, repo: repo, events: events, defaultLimit: defaultLimit, defaults: []configport.RuntimeSetting{{Key: configport.AutomationOperationsMaxRecipientsPerRun, Value: value}}, now: time.Now}
	for _, option := range options {
		if option == nil || option(service) != nil {
			return nil, ErrRuntimeReleaseInvalid
		}
	}
	return service, nil
}

func (s *RuntimeReleaseService) EffectiveSnapshot(ctx context.Context) (out configport.EffectiveSnapshot, err error) {
	if !s.ready() {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.effectiveFromActive(tx)
		return e
	})
	if err != nil {
		return configport.EffectiveSnapshot{}, classifyRuntimeRelease(err)
	}
	return out, nil
}

func (s *RuntimeReleaseService) effectiveFromActive(ctx context.Context) (configport.EffectiveSnapshot, error) {
	// Read the active pointer and its immutable release in one statement. A
	// concurrent publish may supersede the previous release immediately after
	// this read, but the returned revision remains a valid frozen snapshot.
	release, found, err := s.repo.ActiveRuntimeRelease(ctx)
	if err != nil {
		return configport.EffectiveSnapshot{}, err
	}
	if !found {
		return s.snapshot(0, configport.RuntimeSourceEnvironmentDefault, nil, nil)
	}
	if release.State != configport.RuntimeReleasePublished {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseConflict
	}
	at := release.PublishedAt.UTC()
	return s.snapshot(release.ID, configport.RuntimeSourcePublished, release.Settings, &at)
}

// EffectiveSnapshotWithin reads through the caller's existing UoW. This keeps
// a consumer's frozen business record and Config usage fact atomic without
// exposing a database type through the stable Config Port.
func (s *RuntimeReleaseService) EffectiveSnapshotWithin(ctx context.Context) (configport.EffectiveSnapshot, error) {
	if !s.ready() {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseInvalid
	}
	snapshot, err := s.effectiveFromActive(ctx)
	if err != nil {
		return configport.EffectiveSnapshot{}, classifyRuntimeRelease(err)
	}
	return snapshot, nil
}

func (s *RuntimeReleaseService) RecordRuntimeUsage(ctx context.Context, use configport.RuntimeUsage) error {
	if !s.ready() || !validUsage(use) {
		return ErrRuntimeReleaseInvalid
	}
	// The repository requires the caller's UoW transaction. Callers record a
	// usage only after the corresponding business record exists in that UoW.
	if err := s.repo.InsertRuntimeUsage(ctx, use); err != nil {
		return classifyRuntimeRelease(err)
	}
	return nil
}

func (s *RuntimeReleaseService) ListRuntimeReleases(ctx context.Context, limit int) (out configport.RuntimeReleasePage, err error) {
	if !s.ready() || limit < 1 || limit > 100 {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		// Read the active pointer together with its immutable values. A separate
		// pointer lookup followed by GetRuntimeRelease could see a supersede in
		// between under READ COMMITTED and turn an otherwise valid page into a
		// false conflict.
		out.Effective, e = s.effectiveFromActive(tx)
		if e != nil {
			return e
		}
		out.ActiveRevision = out.Effective.Revision
		out.Releases, e = s.repo.ListRuntimeReleases(tx, limit)
		return e
	})
	if err != nil {
		return configport.RuntimeReleasePage{}, classifyRuntimeRelease(err)
	}
	return out, nil
}

func (s *RuntimeReleaseService) RuntimeRelease(ctx context.Context, id int64) (out configport.RuntimeRelease, err error) {
	if !s.ready() || id < 1 {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.GetRuntimeRelease(tx, id, false)
		return e
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) CreateRuntimeReleaseDraft(ctx context.Context, command configport.RuntimeReleaseDraftCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) || command.ExpectedBaseRevision < 0 {
		return out, ErrRuntimeReleaseInvalid
	}
	settings, issues := config.ValidateRuntimeSettings(command.Settings)
	if len(issues) != 0 {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("create", command.ExpectedBaseRevision, 0, settings)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.create", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision {
			return ErrRuntimeReleaseConflict
		}
		baseSnapshot, e := s.effectiveWithin(tx, active)
		if e != nil {
			return e
		}
		// A standard release is a full immutable snapshot. A one-field request
		// changes only that field while retaining every active bound identity,
		// integration and policy value. The explicit legacy recovery command is
		// the sole exception because the prior binary only accepts one key.
		settings, e = mergeRuntimeSettings(baseSnapshot.Settings, settings)
		if e != nil {
			return e
		}
		out = configport.RuntimeRelease{State: configport.RuntimeReleaseDraft, BaseRevision: active, Settings: settings, Checksum: releaseChecksum(settings), CreatedBy: command.Actor, CreatedAt: now}
		out, e = s.repo.InsertRuntimeRelease(tx, out)
		if e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, "created", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) ValidateRuntimeRelease(ctx context.Context, command configport.RuntimeReleaseMutationCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("validate", 0, command.ReleaseID, nil)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.validate", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		current, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, true)
		if e != nil {
			return e
		}
		if current.State != configport.RuntimeReleaseDraft && current.State != configport.RuntimeReleaseValidationFailed && current.State != configport.RuntimeReleaseValidated {
			return ErrRuntimeReleaseConflict
		}
		_, issues := config.ValidateRuntimeSettings(current.Settings)
		if len(issues) == 0 {
			activeSnapshot, snapshotErr := s.effectiveFromActive(tx)
			if snapshotErr != nil {
				return snapshotErr
			}
			issues, e = runtimeReleaseValidationIssues(s.defaults, activeSnapshot.Settings, s.guards, current.Settings)
			if e != nil {
				return e
			}
		}
		state, action := configport.RuntimeReleaseValidated, "validated"
		if len(issues) != 0 {
			state, action = configport.RuntimeReleaseValidationFailed, "validation_failed"
		}
		out, e = s.repo.SetRuntimeReleaseValidation(tx, current.ID, state, issues, now)
		if e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, action, command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) PublishRuntimeRelease(ctx context.Context, command configport.RuntimeReleasePublishCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || command.ExpectedBaseRevision < 0 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) || !validChecksum(command.ExpectedChecksum) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("publish", command.ExpectedBaseRevision, command.ReleaseID, nil)
	payload = append(payload, []byte(command.ExpectedChecksum)...)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.publish", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		current, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision || current.BaseRevision != active || current.State != configport.RuntimeReleaseValidated || current.Checksum != command.ExpectedChecksum {
			return ErrRuntimeReleaseConflict
		}
		if _, issues := config.ValidateRuntimeSettings(current.Settings); len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		scopeBaseline, baselineErr := s.effectiveWithin(tx, active)
		if baselineErr != nil {
			return baselineErr
		}
		issues, validationErr := runtimeReleaseValidationIssues(s.defaults, scopeBaseline.Settings, s.guards, current.Settings)
		if validationErr != nil || len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		out, e = s.repo.PublishRuntimeRelease(tx, current.ID, active, command.Actor, now)
		if e != nil {
			return e
		}
		if e = s.repo.SetActiveRuntimeRelease(tx, out.ID, now); e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, "published", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ReleaseID  int64  `json:"release_id"`
			Revision   int64  `json:"revision"`
			RollbackOf *int64 `json:"rollback_of_release_id,omitempty"`
		}{out.ID, out.ID, out.RollbackOfReleaseID})
		if e != nil {
			return e
		}
		if _, e = s.events.Append(tx, configport.Event{Type: "runtime_release.published", Payload: payload, OccurredAt: now, IdempotencyKey: fmt.Sprintf("runtime_release.published:release:%d", out.ID)}); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) RollbackRuntimeRelease(ctx context.Context, command configport.RuntimeReleaseRollbackCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || command.ExpectedBaseRevision < 0 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("rollback", command.ExpectedBaseRevision, command.ReleaseID, nil)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.rollback", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision {
			return ErrRuntimeReleaseConflict
		}
		target, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, false)
		if e != nil {
			return e
		}
		if target.State != configport.RuntimeReleasePublished && target.State != configport.RuntimeReleaseSuperseded {
			return ErrRuntimeReleaseConflict
		}
		settings, issues := config.ValidateRuntimeSettings(target.Settings)
		if len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		scopeBaseline, baselineErr := s.effectiveWithin(tx, active)
		if baselineErr != nil {
			return baselineErr
		}
		issues, validationErr := runtimeReleaseValidationIssues(s.defaults, scopeBaseline.Settings, s.guards, settings)
		if validationErr != nil || len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		settings, e = mergeRuntimeSettings(s.defaults, settings)
		if e != nil {
			return e
		}
		rollbackOf := target.ID
		candidate := configport.RuntimeRelease{State: configport.RuntimeReleaseValidated, BaseRevision: active, RollbackOfReleaseID: &rollbackOf, Settings: settings, Checksum: releaseChecksum(settings), CreatedBy: command.Actor, CreatedAt: now, ValidatedAt: &now}
		candidate, e = s.repo.InsertRuntimeRelease(tx, candidate)
		if e != nil {
			return e
		}
		out, e = s.repo.PublishRuntimeRelease(tx, candidate.ID, active, command.Actor, now)
		if e != nil {
			return e
		}
		if e = s.repo.SetActiveRuntimeRelease(tx, out.ID, now); e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, candidate.ID, "rolled_back", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ReleaseID  int64 `json:"release_id"`
			Revision   int64 `json:"revision"`
			RollbackOf int64 `json:"rollback_of_release_id"`
		}{out.ID, out.ID, target.ID})
		if e != nil {
			return e
		}
		if _, e = s.events.Append(tx, configport.Event{Type: "runtime_release.rolled_back", Payload: payload, OccurredAt: now, IdempotencyKey: fmt.Sprintf("runtime_release.rolled_back:release:%d", out.ID)}); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

// PrepareLegacyRuntimeRecovery publishes the exact one-key Config snapshot
// understood by the binary immediately before the expanded runtime catalog.
// It is deliberately an explicit, CAS-protected recovery action: operators
// must first make all newer settings equal to their protected deployment
// defaults, or the old binary could silently run with different payment,
// identity, or delivery behavior.
func (s *RuntimeReleaseService) PrepareLegacyRuntimeRecovery(ctx context.Context, command configport.RuntimeReleaseLegacyRecoveryCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ExpectedBaseRevision < 0 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("legacy_binary_recovery", command.ExpectedBaseRevision, 0, nil)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.legacy_binary_recovery", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision {
			return ErrRuntimeReleaseConflict
		}
		activeSnapshot, e := s.effectiveWithin(tx, active)
		if e != nil {
			return e
		}
		limit, e := json.Marshal(activeSnapshot.AutomationMaxRecipients)
		if e != nil {
			return e
		}
		settings := []configport.RuntimeSetting{{Key: configport.AutomationOperationsMaxRecipientsPerRun, Value: limit}}
		candidateSettings, e := mergeRuntimeSettings(s.defaults, settings)
		if e != nil || !legacyRuntimeRecoveryCompatible(activeSnapshot.Settings, candidateSettings) {
			return ErrRuntimeReleaseConflict
		}
		issues, e := runtimeReleaseValidationIssues(s.defaults, activeSnapshot.Settings, s.guards, settings)
		if e != nil || len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		out = configport.RuntimeRelease{State: configport.RuntimeReleaseValidated, BaseRevision: active, Settings: settings, Checksum: releaseChecksum(settings), CreatedBy: command.Actor, CreatedAt: now, ValidatedAt: &now}
		out, e = s.repo.InsertRuntimeRelease(tx, out)
		if e != nil {
			return e
		}
		out, e = s.repo.PublishRuntimeRelease(tx, out.ID, active, command.Actor, now)
		if e != nil {
			return e
		}
		if e = s.repo.SetActiveRuntimeRelease(tx, out.ID, now); e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, "legacy_binary_recovery_published", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ReleaseID int64 `json:"release_id"`
			Revision  int64 `json:"revision"`
		}{out.ID, out.ID})
		if e != nil {
			return e
		}
		if _, e = s.events.Append(tx, configport.Event{Type: "runtime_release.legacy_binary_recovery_published", Payload: payload, OccurredAt: now, IdempotencyKey: fmt.Sprintf("runtime_release.legacy_binary_recovery:release:%d", out.ID)}); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func legacyRuntimeRecoveryCompatible(active, candidate []configport.RuntimeSetting) bool {
	activeValues := make(map[configport.RuntimeSettingKey]json.RawMessage, len(active))
	candidateValues := make(map[configport.RuntimeSettingKey]json.RawMessage, len(candidate))
	for _, value := range active {
		activeValues[value.Key] = value.Value
	}
	for _, value := range candidate {
		candidateValues[value.Key] = value.Value
	}
	for key, activeValue := range activeValues {
		if key != configport.AutomationOperationsMaxRecipientsPerRun && !bytes.Equal(activeValue, candidateValues[key]) {
			return false
		}
	}
	for key, candidateValue := range candidateValues {
		if key != configport.AutomationOperationsMaxRecipientsPerRun && !bytes.Equal(candidateValue, activeValues[key]) {
			return false
		}
	}
	return true
}

func (s *RuntimeReleaseService) ListRuntimeUsage(ctx context.Context, revision int64, limit int) (out []configport.RuntimeUsage, err error) {
	if !s.ready() || revision < 0 || limit < 1 || limit > 100 {
		return nil, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.ListRuntimeUsage(tx, revision, limit)
		return e
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) RecordRuntimeApplication(ctx context.Context, application configport.RuntimeApplication) error {
	if !s.ready() || !validApplication(application) {
		return ErrRuntimeReleaseInvalid
	}
	return classifyRuntimeRelease(s.uow.Within(ctx, func(tx context.Context) error {
		return s.repo.InsertRuntimeApplication(tx, application)
	}))
}

func (s *RuntimeReleaseService) ListRuntimeApplications(ctx context.Context, limit int) (out []configport.RuntimeApplication, err error) {
	if !s.ready() || limit < 1 || limit > 100 {
		return nil, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.ListRuntimeApplications(tx, limit)
		return e
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) ProtectedReferenceStatuses(_ context.Context) ([]configport.ProtectedReferenceStatus, error) {
	if !s.ready() {
		return nil, ErrRuntimeReleaseInvalid
	}
	keys := make([]string, 0, len(s.protected))
	for reference := range s.protected {
		keys = append(keys, reference)
	}
	sort.Strings(keys)
	out := make([]configport.ProtectedReferenceStatus, 0, len(keys))
	for _, reference := range keys {
		out = append(out, configport.ProtectedReferenceStatus{Reference: reference, Configured: s.protected[reference]})
	}
	return out, nil
}

func (s *RuntimeReleaseService) effectiveWithin(ctx context.Context, revision int64) (configport.EffectiveSnapshot, error) {
	if revision == 0 {
		return s.snapshot(0, configport.RuntimeSourceEnvironmentDefault, nil, nil)
	}
	release, err := s.repo.GetRuntimeRelease(ctx, revision, false)
	if err != nil || release.State != configport.RuntimeReleasePublished {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseConflict
	}
	at := release.PublishedAt.UTC()
	return s.snapshot(release.ID, configport.RuntimeSourcePublished, release.Settings, &at)
}

func (s *RuntimeReleaseService) snapshot(revision int64, source configport.RuntimeSource, releaseSettings []configport.RuntimeSetting, publishedAt *time.Time) (configport.EffectiveSnapshot, error) {
	settings, err := mergeRuntimeSettings(s.defaults, releaseSettings)
	if err != nil {
		return configport.EffectiveSnapshot{}, err
	}
	limit, err := runtimeLimit(settings)
	if err != nil {
		return configport.EffectiveSnapshot{}, err
	}
	return configport.EffectiveSnapshot{Revision: revision, Source: source, AutomationMaxRecipients: limit, PublishedAt: publishedAt, Settings: settings, Checksum: releaseChecksum(settings)}, nil
}

func (s *RuntimeReleaseService) replayRuntimeReceipt(ctx context.Context, receipt configport.RuntimeReleaseReceipt, digest []byte, out *configport.RuntimeRelease) error {
	if !bytes.Equal(receipt.PayloadDigest, digest) || receipt.State != "completed" || receipt.ReleaseID < 1 {
		return ErrRuntimeReleaseConflict
	}
	value, err := s.repo.GetRuntimeRelease(ctx, receipt.ReleaseID, false)
	if err != nil {
		return err
	}
	*out = value
	return nil
}

func runtimeLimit(settings []configport.RuntimeSetting) (int, error) {
	canonical, issues := config.ValidateRuntimeSettings(settings)
	if len(issues) != 0 {
		return 0, ErrRuntimeReleaseInvalid
	}
	for _, setting := range canonical {
		if setting.Key == configport.AutomationOperationsMaxRecipientsPerRun {
			var value int
			if err := json.Unmarshal(setting.Value, &value); err == nil && value >= 1 && value <= 5000 {
				return value, nil
			}
		}
	}
	return 0, ErrRuntimeReleaseInvalid
}

// ValidateEffectiveRuntimeSettings validates the exact non-secret snapshot
// that Composition is about to apply. It is intentionally the same validation
// path as release publish, so a changed protected deployment contract prevents
// adapter construction and therefore prevents an application fact from being
// recorded for a configuration that was not actually applied.
func ValidateEffectiveRuntimeSettings(settings []configport.RuntimeSetting, guards RuntimeActivationGuards) error {
	canonical, issues := config.ValidateRuntimeSettings(settings)
	if len(issues) != 0 {
		return ErrRuntimeReleaseInvalid
	}
	issues, err := runtimeReleaseValidationIssues(canonical, canonical, guards, canonical)
	if err != nil || len(issues) != 0 {
		return ErrRuntimeReleaseInvalid
	}
	return nil
}

func runtimeReleaseValidationIssues(defaults, scopeBaseline []configport.RuntimeSetting, guards RuntimeActivationGuards, settings []configport.RuntimeSetting) ([]configport.RuntimeValidationIssue, error) {
	effective, err := mergeRuntimeSettings(scopeBaseline, settings)
	if err != nil {
		return nil, err
	}
	deploymentBaseline := make(map[configport.RuntimeSettingKey]json.RawMessage, len(defaults))
	for _, value := range defaults {
		deploymentBaseline[value.Key] = value.Value
	}
	boundBaseline := make(map[configport.RuntimeSettingKey]json.RawMessage, len(scopeBaseline))
	for _, value := range scopeBaseline {
		boundBaseline[value.Key] = value.Value
	}
	issues := config.ValidateRuntimeDependencies(effective)
	boolAt := func(key configport.RuntimeSettingKey) bool {
		for _, setting := range effective {
			if setting.Key == key {
				var value bool
				return json.Unmarshal(setting.Value, &value) == nil && value
			}
		}
		return false
	}
	stringAt := func(key configport.RuntimeSettingKey) string {
		for _, setting := range effective {
			if setting.Key == key {
				var value string
				_ = json.Unmarshal(setting.Value, &value)
				return value
			}
		}
		return ""
	}
	guard := func(enabled bool, key configport.RuntimeSettingKey, allowed bool, message string) {
		if enabled && !allowed {
			issues = append(issues, configport.RuntimeValidationIssue{Key: key, Error: message})
		}
	}
	guard(boolAt(configport.WeComEnabled), configport.WeComEnabled, guards.WeComEnabled, "需已有企业微信受保护凭据和签名密钥")
	guard(boolAt(configport.MessageArchiveEnabled), configport.MessageArchiveEnabled, guards.MessageArchiveEnabled, "需已有会话存档受保护部署配置")
	guard(stringAt(configport.AutomationOperationsProviderMode) != "" && stringAt(configport.AutomationOperationsProviderMode) != "disabled", configport.AutomationOperationsProviderMode, guards.AutomationProviderEnabled, "需已有固定发送授权和受保护接入凭据")
	guard(boolAt(configport.AIAssistantDispatchEnabled), configport.AIAssistantDispatchEnabled, guards.AIDispatchEnabled, "需已有私信发送授权和受保护通讯录凭据")
	guard(boolAt(configport.AIAssistantIntakeEnabled), configport.AIAssistantIntakeEnabled, guards.AIAssistantIntakeEnabled, "需已有 AI 接入受保护凭据")
	guard(boolAt(configport.AIAgentGenerationEnabled), configport.AIAgentGenerationEnabled, guards.AIAgentGenerationEnabled, "需已有 AI 生成 Provider endpoint、model 和受保护凭据")
	guard(boolAt(configport.WeChatPayProviderEnabled), configport.WeChatPayProviderEnabled, guards.WeChatPayEnabled, "需已有支付受保护凭据")
	guard(boolAt(configport.WeChatPayH5OAuthEnabled), configport.WeChatPayH5OAuthEnabled, guards.WeChatPayH5OAuthEnabled, "需已有 H5 OAuth 受保护凭据")
	guard(boolAt(configport.WeChatShopProviderEnabled), configport.WeChatShopProviderEnabled, guards.WeChatShopEnabled, "需已有微信小店 AppSecret、回调 Token 和专属 EncodingAESKey")
	guard(boolAt(configport.AlipayProviderEnabled), configport.AlipayProviderEnabled, guards.AlipayEnabled, "需已有支付宝应用私钥和公钥/证书校验材料")
	guard(boolAt(configport.SurveyOAuthEnabled), configport.SurveyOAuthEnabled, guards.SurveyOAuthEnabled, "需已有公众号 OAuth 受保护凭据")
	for _, value := range effective {
		deploymentValue := deploymentBaseline[value.Key]
		if config.ProtectedRuntimeSetting(value.Key) && !bytes.Equal(value.Value, deploymentValue) {
			issues = append(issues, configport.RuntimeValidationIssue{Key: value.Key, Error: "身份作用域或授权由受控部署维护，不能在配置中心修改"})
		}
		boundValue := boundBaseline[value.Key]
		if config.ScopeBoundRuntimeSetting(value.Key) && runtimeScopeBound(boundValue) && !bytes.Equal(value.Value, boundValue) {
			issues = append(issues, configport.RuntimeValidationIssue{Key: value.Key, Error: "已绑定身份或接入标识，请走明确的身份或接入迁移流程"})
		}
	}
	return issues, nil
}

func runtimeScopeBound(value json.RawMessage) bool {
	var scope string
	return json.Unmarshal(value, &scope) == nil && strings.TrimSpace(scope) != ""
}

func mergeRuntimeSettings(defaults, overrides []configport.RuntimeSetting) ([]configport.RuntimeSetting, error) {
	merged := make(map[configport.RuntimeSettingKey]json.RawMessage, len(defaults)+len(overrides))
	for _, set := range defaults {
		merged[set.Key] = append(json.RawMessage(nil), set.Value...)
	}
	for _, set := range overrides {
		merged[set.Key] = append(json.RawMessage(nil), set.Value...)
	}
	settings := make([]configport.RuntimeSetting, 0, len(merged))
	for key, value := range merged {
		settings = append(settings, configport.RuntimeSetting{Key: key, Value: value})
	}
	canonical, issues := config.ValidateRuntimeSettings(settings)
	if len(issues) != 0 {
		return nil, ErrRuntimeReleaseInvalid
	}
	return canonical, nil
}
func releaseChecksum(settings []configport.RuntimeSetting) string {
	copySettings := append([]configport.RuntimeSetting(nil), settings...)
	sort.Slice(copySettings, func(i, j int) bool { return copySettings[i].Key < copySettings[j].Key })
	payload, _ := json.Marshal(copySettings)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
func releasePayload(action string, base, id int64, settings []configport.RuntimeSetting) []byte {
	payload, _ := json.Marshal(struct {
		Action   string `json:"action"`
		Base, ID int64
		Settings []configport.RuntimeSetting `json:"settings,omitempty"`
	}{action, base, id, settings})
	return payload
}
func validReleaseActor(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 200
}
func validRuntimeKey(value string) bool {
	return len(value) >= 8 && len(value) <= 200 && strings.TrimSpace(value) == value
}
func validChecksum(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
func validUsage(use configport.RuntimeUsage) bool {
	return (use.Snapshot.Source == configport.RuntimeSourceEnvironmentDefault || use.Snapshot.Source == configport.RuntimeSourcePublished) && use.Snapshot.Revision >= 0 && use.Snapshot.AutomationMaxRecipients >= 1 && use.Snapshot.AutomationMaxRecipients <= 5000 && use.Consumer == string(configport.AutomationOperationsMaxRecipientsPerRun) && (use.Role == "api" || use.Role == "worker" || use.Role == "effects-worker") && (use.Operation == "preview" || use.Operation == "confirm" || use.Operation == "execution") && (use.SubjectKind == "automation_preview" || use.SubjectKind == "automation_run") && use.SubjectID > 0 && !use.UsedAt.IsZero()
}
func validApplication(application configport.RuntimeApplication) bool {
	return application.Revision >= 0 && (application.Source == configport.RuntimeSourceEnvironmentDefault || application.Source == configport.RuntimeSourcePublished) && (application.Role == "api" || application.Role == "worker" || application.Role == "effects-worker") && application.ReleaseSHA != "" && strings.TrimSpace(application.ReleaseSHA) == application.ReleaseSHA && len(application.ReleaseSHA) <= 200 && validChecksum(application.SnapshotChecksum) && !application.AppliedAt.IsZero()
}
func (s *RuntimeReleaseService) ready() bool {
	return s != nil && s.uow != nil && s.repo != nil && s.events != nil && s.defaultLimit >= 1 && s.defaultLimit <= 5000 && s.now != nil
}
func classifyRuntimeRelease(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrRuntimeReleaseInvalid) || errors.Is(err, ErrRuntimeReleaseConflict) || errors.Is(err, ErrRuntimeReleaseNotFound) {
		return err
	}
	return err
}

var _ configport.EffectiveReader = (*RuntimeReleaseService)(nil)
var _ configport.UsageRecorder = (*RuntimeReleaseService)(nil)
var _ configport.RuntimeReleaseApplication = (*RuntimeReleaseService)(nil)
