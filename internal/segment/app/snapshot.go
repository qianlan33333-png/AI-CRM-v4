package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type RefreshStore interface {
	GetPackage(context.Context, int64) (segmentdomain.Package, error)
	CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
	Configuration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
	ReserveRefresh(context.Context, segmentdomain.RefreshRun) (segmentdomain.RefreshRun, bool, error)
	AttachRefreshJob(context.Context, int64, int64, time.Time) (segmentdomain.RefreshRun, error)
	Refresh(context.Context, int64) (segmentdomain.RefreshRun, error)
	BeginRefresh(context.Context, int64, time.Time) (segmentdomain.RefreshRun, segmentdomain.Snapshot, error)
	StageRefreshBatch(context.Context, int64, int, []customerdomain.CustomerID, [32]byte, time.Time) error
	PublishRefresh(context.Context, int64, int64, [32]byte, [32]byte, int64, time.Time) (segmentdomain.PublishedRefresh, error)
	FailRefresh(context.Context, int64, string, time.Time) error
	AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error)
	PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error)
	Snapshot(context.Context, segmentport.SnapshotID) (segmentport.Snapshot, bool, error)
	Members(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberPage, error)
	CreateMemberEnteredEvents(context.Context, segmentdomain.Snapshot, *int64, int64, time.Time) (int64, error)
	MemberEvents(context.Context, segmentport.SnapshotID, string, int) (segmentport.MemberEventPage, error)
}

type RefreshEnqueuer interface {
	EnqueueRefreshWithin(context.Context, int64) (int64, error)
}

type MemberEventEnqueuer interface {
	EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error)
}

type SnapshotService struct {
	uow       platformport.UnitOfWork
	store     RefreshStore
	evaluator *Evaluator
	enqueuer  RefreshEnqueuer
	events    MemberEventEnqueuer
	now       func() time.Time
}

type RefreshCommand struct {
	PackageID      int64                     `json:"package_id"`
	Actor          int64                     `json:"actor"`
	MutationActor  segmentport.MutationActor `json:"-"`
	IdempotencyKey string                    `json:"-"`
	ReferenceTime  time.Time                 `json:"reference_time"`
	RefreshKind    segmentdomain.RefreshKind `json:"refresh_kind"`
}

type Preview struct {
	PackageID              int64              `json:"package_id"`
	ConfigurationVersionID int64              `json:"configuration_version_id"`
	ReferenceTime          time.Time          `json:"reference_time"`
	MemberCount            int                `json:"member_count"`
	MemberDigest           string             `json:"member_digest"`
	Watermarks             []WatermarkSummary `json:"watermarks"`
	WatermarkDigest        string             `json:"watermark_digest"`
}

type WatermarkSummary struct {
	Source string    `json:"source"`
	AsOf   time.Time `json:"as_of"`
	Fresh  bool      `json:"fresh"`
}

func NewSnapshotService(uow platformport.UnitOfWork, store RefreshStore, evaluator *Evaluator, enqueuer RefreshEnqueuer, events MemberEventEnqueuer) (*SnapshotService, error) {
	if uow == nil || store == nil || evaluator == nil || enqueuer == nil || events == nil {
		return nil, ErrNotReady
	}
	return &SnapshotService{uow: uow, store: store, evaluator: evaluator, enqueuer: enqueuer, events: events, now: time.Now}, nil
}

func (s *SnapshotService) Preview(ctx context.Context, packageID int64, reference time.Time) (Preview, error) {
	if s == nil || packageID < 1 {
		return Preview{}, ErrInvalid
	}
	if reference.IsZero() {
		reference = s.now().UTC()
	} else {
		reference = reference.UTC()
	}
	var config segmentdomain.ConfigurationVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		config, e = s.store.CurrentConfiguration(tx, packageID)
		return e
	})
	if err != nil {
		return Preview{}, classify(err)
	}
	evaluation, err := s.evaluator.Evaluate(ctx, config.Definition, reference)
	if err != nil {
		return Preview{}, classify(err)
	}
	return makePreview(packageID, config.ID, evaluation), nil
}

// PreviewDefinition evaluates an unsaved, already-canonical definition for an
// existing package. It deliberately creates no configuration version, refresh
// run, snapshot, or external effect; the form preview must not mutate state.
func (s *SnapshotService) PreviewDefinition(ctx context.Context, packageID int64, definition json.RawMessage, reference time.Time) (Preview, error) {
	if s == nil || packageID < 1 || len(definition) == 0 {
		return Preview{}, ErrInvalid
	}
	if reference.IsZero() {
		reference = s.now().UTC()
	} else {
		reference = reference.UTC()
	}
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		_, err := s.store.GetPackage(tx, packageID)
		return err
	}); err != nil {
		return Preview{}, classify(err)
	}
	evaluation, err := s.evaluator.Evaluate(ctx, definition, reference)
	if err != nil {
		return Preview{}, classify(err)
	}
	return makePreview(packageID, 0, evaluation), nil
}

func configuredRefreshKind(value segmentdomain.RefreshKind, refreshMode string) segmentdomain.RefreshKind {
	if value != "" {
		return value
	}
	// Legacy custom schedules historically refresh the complete membership even
	// when the caller did not name a scheduling kind.
	if refreshMode == "legacy_custom" {
		return segmentdomain.RefreshLegacy
	}
	return segmentdomain.RefreshManual
}

func (s *SnapshotService) AcceptRefresh(ctx context.Context, command RefreshCommand) (segmentdomain.RefreshRun, error) {
	actor, err := mutationActor(command.Actor, command.MutationActor)
	if s == nil || err != nil || command.PackageID < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	if command.RefreshKind != "" && !segmentdomain.ValidRefreshKind(command.RefreshKind) {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	if command.ReferenceTime.IsZero() {
		command.ReferenceTime = s.now().UTC()
	} else {
		command.ReferenceTime = command.ReferenceTime.UTC()
	}
	now := s.now().UTC()
	source := sha256.Sum256([]byte(actorScopedIdempotencyMaterial(actor, command.IdempotencyKey)))
	var result segmentdomain.RefreshRun
	err = s.uow.Within(ctx, func(tx context.Context) error {
		pkg, e := s.store.GetPackage(tx, command.PackageID)
		if e != nil {
			return e
		}
		if pkg.Lifecycle == segmentdomain.Archived {
			return ErrConflict
		}
		config, e := s.store.CurrentConfiguration(tx, command.PackageID)
		if e != nil {
			return e
		}
		if isCoreDefinition(config.Definition) {
			return ErrConflict
		}
		kind := configuredRefreshKind(command.RefreshKind, config.RefreshMode)
		if !segmentdomain.ValidRefreshKind(kind) {
			return ErrInvalid
		}
		run, owned, e := s.store.ReserveRefresh(tx, segmentdomain.RefreshRun{PackageID: command.PackageID, ConfigurationVersionID: config.ID, SourceKeyDigest: source, ReferenceTime: command.ReferenceTime, RefreshKind: kind, CreatedAt: now, UpdatedAt: now})
		if e != nil {
			return e
		}
		if !owned {
			result = run
			return nil
		}
		jobID, e := s.enqueuer.EnqueueRefreshWithin(tx, run.ID)
		if e != nil {
			return e
		}
		run, e = s.store.AttachRefreshJob(tx, run.ID, jobID, now)
		if e != nil {
			return e
		}
		_, e = s.store.AppendMutationFacts(tx, fact("refresh_run", run.ID, "accept", "audience.refresh.accepted.v1", actor, command.IdempotencyKey, now))
		result = run
		return e
	})
	return result, classify(err)
}

// AcceptRefreshWithin is the same-domain atomic seam used by a verified
// inbound fact while its receipt, outbox fact and River job share one UoW.
func (s *SnapshotService) AcceptRefreshWithin(ctx context.Context, command RefreshCommand) (segmentdomain.RefreshRun, error) {
	actor, err := mutationActor(command.Actor, command.MutationActor)
	if s == nil || err != nil || command.PackageID < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	if command.RefreshKind != "" && !segmentdomain.ValidRefreshKind(command.RefreshKind) {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	if command.ReferenceTime.IsZero() {
		command.ReferenceTime = s.now().UTC()
	} else {
		command.ReferenceTime = command.ReferenceTime.UTC()
	}
	now := s.now().UTC()
	source := sha256.Sum256([]byte(actorScopedIdempotencyMaterial(actor, command.IdempotencyKey)))
	pkg, err := s.store.GetPackage(ctx, command.PackageID)
	if err != nil {
		return segmentdomain.RefreshRun{}, classify(err)
	}
	if pkg.Lifecycle == segmentdomain.Archived {
		return segmentdomain.RefreshRun{}, ErrConflict
	}
	config, err := s.store.CurrentConfiguration(ctx, command.PackageID)
	if err != nil {
		return segmentdomain.RefreshRun{}, classify(err)
	}
	if isCoreDefinition(config.Definition) {
		return segmentdomain.RefreshRun{}, ErrConflict
	}
	kind := configuredRefreshKind(command.RefreshKind, config.RefreshMode)
	if !segmentdomain.ValidRefreshKind(kind) {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	run, owned, err := s.store.ReserveRefresh(ctx, segmentdomain.RefreshRun{PackageID: command.PackageID, ConfigurationVersionID: config.ID, SourceKeyDigest: source, ReferenceTime: command.ReferenceTime, RefreshKind: kind, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return segmentdomain.RefreshRun{}, classify(err)
	}
	if !owned {
		return run, nil
	}
	jobID, err := s.enqueuer.EnqueueRefreshWithin(ctx, run.ID)
	if err != nil {
		return segmentdomain.RefreshRun{}, err
	}
	run, err = s.store.AttachRefreshJob(ctx, run.ID, jobID, now)
	if err != nil {
		return segmentdomain.RefreshRun{}, classify(err)
	}
	_, err = s.store.AppendMutationFacts(ctx, fact("refresh_run", run.ID, "accept", "audience.refresh.accepted.v1", actor, command.IdempotencyKey, now))
	return run, classify(err)
}
func (s *SnapshotService) GetRefresh(ctx context.Context, runID int64) (segmentdomain.RefreshRun, error) {
	if s == nil || runID < 1 {
		return segmentdomain.RefreshRun{}, ErrInvalid
	}
	var run segmentdomain.RefreshRun
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; run, e = s.store.Refresh(tx, runID); return e })
	return run, classify(err)
}

func (s *SnapshotService) ProcessRefresh(ctx context.Context, runID int64) error {
	if s == nil || runID < 1 {
		return ErrInvalid
	}
	current, err := s.GetRefresh(ctx, runID)
	if err != nil {
		return err
	}
	if current.State == segmentdomain.RefreshPublished || current.State == segmentdomain.RefreshFailed {
		return nil
	}
	var run segmentdomain.RefreshRun
	var config segmentdomain.ConfigurationVersion
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		run, _, e = s.store.BeginRefresh(tx, runID, s.now().UTC())
		if e == nil {
			config, e = s.store.Configuration(tx, run.ConfigurationVersionID)
		}
		return e
	})
	if err != nil {
		return classify(err)
	}
	evaluation, err := s.evaluator.Evaluate(ctx, config.Definition, run.ReferenceTime)
	if err != nil {
		return err
	}
	for start, ordinal := 0, 0; start < len(evaluation.CustomerIDs); start, ordinal = start+1000, ordinal+1 {
		end := start + 1000
		if end > len(evaluation.CustomerIDs) {
			end = len(evaluation.CustomerIDs)
		}
		batch := evaluation.CustomerIDs[start:end]
		digest := segmentdomain.DigestMembers(batch)
		err = s.uow.Within(ctx, func(tx context.Context) error {
			return s.store.StageRefreshBatch(tx, runID, ordinal, batch, digest, s.now().UTC())
		})
		if err != nil {
			return classify(err)
		}
	}
	actor, actorErr := storedMutationActor(config.CreatedBy, config.CreatedActorKind, config.CreatedActorRef)
	if actorErr != nil {
		return ErrInvalid
	}
	memberDigest := segmentdomain.DigestMembers(evaluation.CustomerIDs)
	watermarkDigest := digestWatermarks(evaluation.Watermarks)
	err = s.uow.Within(ctx, func(tx context.Context) error {
		published, e := s.publishRefresh(tx, runID, int64(len(evaluation.CustomerIDs)), memberDigest, watermarkDigest, actor, s.now().UTC())
		if e != nil {
			return e
		}
		created, e := s.createMemberEnteredEvents(tx, published.Snapshot, published.PreviousSnapshotID, actor, published.Snapshot.ReferenceTime)
		if e != nil || created == 0 {
			return e
		}
		_, e = s.events.EnqueueMemberEventsWithin(tx, segmentport.SnapshotID(published.Snapshot.ID))
		return e
	})
	return classify(err)
}

type actorRefreshStore interface {
	PublishRefreshWithActor(context.Context, int64, int64, [32]byte, [32]byte, segmentstore.Actor, time.Time) (segmentdomain.PublishedRefresh, error)
	CreateMemberEnteredEventsWithActor(context.Context, segmentdomain.Snapshot, *int64, segmentstore.Actor, time.Time) (int64, error)
}

func (s *SnapshotService) publishRefresh(ctx context.Context, runID, expectedCount int64, expectedMemberDigest, watermarkDigest [32]byte, actor segmentport.MutationActor, now time.Time) (segmentdomain.PublishedRefresh, error) {
	if withActor, ok := s.store.(actorRefreshStore); ok {
		return withActor.PublishRefreshWithActor(ctx, runID, expectedCount, expectedMemberDigest, watermarkDigest, storeActor(actor), now)
	}
	if actor.Kind != segmentport.MutationActorAdmin {
		return segmentdomain.PublishedRefresh{}, ErrInvalid
	}
	return s.store.PublishRefresh(ctx, runID, expectedCount, expectedMemberDigest, watermarkDigest, actor.StaffID, now)
}

func (s *SnapshotService) createMemberEnteredEvents(ctx context.Context, snapshot segmentdomain.Snapshot, previousSnapshotID *int64, actor segmentport.MutationActor, now time.Time) (int64, error) {
	if withActor, ok := s.store.(actorRefreshStore); ok {
		return withActor.CreateMemberEnteredEventsWithActor(ctx, snapshot, previousSnapshotID, storeActor(actor), now)
	}
	if actor.Kind != segmentport.MutationActorAdmin {
		return 0, ErrInvalid
	}
	return s.store.CreateMemberEnteredEvents(ctx, snapshot, previousSnapshotID, actor.StaffID, now)
}

func (s *SnapshotService) FailRefresh(ctx context.Context, runID int64, code string) error {
	if s == nil || runID < 1 {
		return ErrInvalid
	}
	if code == "" {
		code = "refresh_unavailable"
	}
	if len(code) > 100 {
		code = code[:100]
	}
	return classify(s.uow.Within(ctx, func(tx context.Context) error { return s.store.FailRefresh(tx, runID, code, s.now().UTC()) }))
}

func makePreview(packageID, configurationID int64, e segmentport.Evaluation) Preview {
	m := segmentdomain.DigestMembers(e.CustomerIDs)
	w := digestWatermarks(e.Watermarks)
	summary := make([]WatermarkSummary, len(e.Watermarks))
	for i, item := range e.Watermarks {
		summary[i] = WatermarkSummary{item.Source, item.AsOf, item.Fresh}
	}
	sort.Slice(summary, func(i, j int) bool { return summary[i].Source < summary[j].Source })
	return Preview{packageID, configurationID, e.ReferenceAt, len(e.CustomerIDs), hex.EncodeToString(m[:]), summary, hex.EncodeToString(w[:])}
}
func digestWatermarks(values []segmentport.SourceWatermark) [32]byte {
	copyValues := append([]segmentport.SourceWatermark(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i].Source < copyValues[j].Source })
	raw, _ := json.Marshal(copyValues)
	return sha256.Sum256(raw)
}
func RefreshErrorCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrUnsupportedDefinition):
		return "definition_unsupported"
	case errors.Is(err, ErrConflict):
		return "configuration_drift"
	default:
		return "refresh_unavailable"
	}
}

func (s *SnapshotService) PublishedSnapshot(ctx context.Context, packageID segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	if s == nil || packageID < 1 {
		return segmentport.Snapshot{}, false, ErrInvalid
	}
	var value segmentport.Snapshot
	var found bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, found, e = s.store.PublishedSnapshot(tx, packageID)
		return e
	})
	return value, found, classify(err)
}
func (s *SnapshotService) Snapshot(ctx context.Context, snapshotID segmentport.SnapshotID) (segmentport.Snapshot, bool, error) {
	if s == nil || snapshotID < 1 {
		return segmentport.Snapshot{}, false, ErrInvalid
	}
	var value segmentport.Snapshot
	var found bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, found, e = s.store.Snapshot(tx, snapshotID)
		return e
	})
	return value, found, classify(err)
}
func (s *SnapshotService) Members(ctx context.Context, snapshotID segmentport.SnapshotID, cursor string, limit int) (segmentport.MemberPage, error) {
	if s == nil || snapshotID < 1 {
		return segmentport.MemberPage{}, ErrInvalid
	}
	var value segmentport.MemberPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, e = s.store.Members(tx, snapshotID, cursor, limit)
		return e
	})
	return value, classify(err)
}

func (s *SnapshotService) MemberEvents(ctx context.Context, snapshotID segmentport.SnapshotID, cursor string, limit int) (segmentport.MemberEventPage, error) {
	if s == nil || snapshotID < 1 {
		return segmentport.MemberEventPage{}, ErrInvalid
	}
	var value segmentport.MemberEventPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, e = s.store.MemberEvents(tx, snapshotID, cursor, limit)
		return e
	})
	return value, classify(err)
}

var _ segmentport.MemberEventReader = (*SnapshotService)(nil)
