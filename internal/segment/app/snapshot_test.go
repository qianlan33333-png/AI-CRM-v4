package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type directUOW struct{}

func (directUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type enqueueStub struct{ calls int }

func (e *enqueueStub) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	e.calls++
	return 88, nil
}

type memberEventEnqueueStub struct{ calls int }

func (e *memberEventEnqueueStub) EnqueueMemberEventsWithin(context.Context, segmentport.SnapshotID) (int64, error) {
	e.calls++
	return 89, nil
}

type refreshStoreStub struct {
	RefreshStore
	run         segmentdomain.RefreshRun
	config      segmentdomain.ConfigurationVersion
	lifecycle   segmentdomain.Lifecycle
	lockCalls   int
	staged      int
	batches     int
	memberFacts []segmentdomain.SnapshotMemberFact
	published   int64
	facts       int
	events      int64
}

func (s *refreshStoreStub) GetPackage(context.Context, int64) (segmentdomain.Package, error) {
	lifecycle := s.lifecycle
	if lifecycle == "" {
		lifecycle = segmentdomain.Paused
	}
	return segmentdomain.Package{ID: 1, Lifecycle: lifecycle}, nil
}
func (s *refreshStoreStub) LockPackage(ctx context.Context, id int64) (segmentdomain.Package, error) {
	s.lockCalls++
	return s.GetPackage(ctx, id)
}
func (s *refreshStoreStub) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, nil
}
func (s *refreshStoreStub) Configuration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, nil
}
func (s *refreshStoreStub) ReserveRefresh(_ context.Context, run segmentdomain.RefreshRun) (segmentdomain.RefreshRun, bool, error) {
	if s.run.ID > 0 && s.run.SourceKeyDigest == run.SourceKeyDigest {
		return s.run, false, nil
	}
	run.ID = 7
	s.run = run
	return run, true, nil
}
func (s *refreshStoreStub) AttachRefreshJob(_ context.Context, runID, jobID int64, _ time.Time) (segmentdomain.RefreshRun, error) {
	s.run.ID = runID
	s.run.State = segmentdomain.RefreshQueued
	s.run.RiverJobID = &jobID
	return s.run, nil
}
func (s *refreshStoreStub) Refresh(context.Context, int64) (segmentdomain.RefreshRun, error) {
	return s.run, nil
}
func (s *refreshStoreStub) BeginRefresh(context.Context, int64, time.Time) (segmentdomain.RefreshRun, segmentdomain.Snapshot, error) {
	s.run.State = segmentdomain.RefreshEvaluating
	return s.run, segmentdomain.Snapshot{ID: 9}, nil
}
func (s *refreshStoreStub) StageRefreshBatch(_ context.Context, _ int64, _ int, ids []customerdomain.CustomerID, _ [32]byte, _ time.Time) error {
	s.staged += len(ids)
	s.batches++
	return nil
}
func (s *refreshStoreStub) StageRefreshBatchWithMemberFacts(_ context.Context, _ int64, _ int, facts []segmentdomain.SnapshotMemberFact, _ [32]byte, _ time.Time) error {
	s.memberFacts = append(s.memberFacts, facts...)
	s.staged += len(facts)
	s.batches++
	return nil
}
func (s *refreshStoreStub) PublishRefresh(_ context.Context, _ int64, count int64, _ [32]byte, _ [32]byte, _ int64, _ time.Time) (segmentdomain.PublishedRefresh, error) {
	s.published = count
	s.run.State = segmentdomain.RefreshPublished
	return segmentdomain.PublishedRefresh{Snapshot: segmentdomain.Snapshot{ID: 9, PackageID: 1, ConfigurationVersionID: 3, State: "published", ReferenceTime: s.run.ReferenceTime, MemberCount: count}}, nil
}
func (s *refreshStoreStub) PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error) {
	return segmentport.Snapshot{}, false, nil
}
func (s *refreshStoreStub) AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error) {
	s.facts++
	return 1, nil
}
func (s *refreshStoreStub) CreateMemberEnteredEvents(context.Context, segmentdomain.Snapshot, *int64, int64, time.Time) (int64, error) {
	return s.events, nil
}

type passthroughCanonical struct{}

func (passthroughCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

func TestRefreshAcceptanceQueuesInTheSameUnitOfWork(t *testing.T) {
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4}}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{}, passthroughCanonical{})
	enqueue := &enqueueStub{}
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, enqueue, &memberEventEnqueueStub{})
	service.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	run, err := service.AcceptRefresh(context.Background(), RefreshCommand{PackageID: 1, Actor: 4, IdempotencyKey: "refresh-command-0001", ReferenceTime: time.Unix(900, 0).UTC()})
	if err != nil || run.State != segmentdomain.RefreshQueued || enqueue.calls != 1 || store.facts != 1 || run.RiverJobID == nil || *run.RiverJobID != 88 {
		t.Fatalf("run=%+v enqueue=%d facts=%d err=%v", run, enqueue.calls, store.facts, err)
	}
}

func TestRefreshAcceptanceKeepsLegacyCustomManualRefreshComplete(t *testing.T) {
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, RefreshMode: "legacy_custom", CreatedBy: 4}}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{}, passthroughCanonical{})
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, &enqueueStub{}, &memberEventEnqueueStub{})
	_, err := service.AcceptRefresh(context.Background(), RefreshCommand{PackageID: 1, Actor: 4, IdempotencyKey: "legacy-refresh-command", ReferenceTime: time.Unix(900, 0).UTC()})
	if err != nil || store.run.RefreshKind != segmentdomain.RefreshLegacy {
		t.Fatalf("refresh kind=%q err=%v", store.run.RefreshKind, err)
	}
}

func TestFullRefreshUsesCompleteKindAndRequiresPausedPackage(t *testing.T) {
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	makeService := func(lifecycle segmentdomain.Lifecycle) (*SnapshotService, *refreshStoreStub, *enqueueStub) {
		store := &refreshStoreStub{lifecycle: lifecycle, config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4}}
		evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{}, passthroughCanonical{})
		enqueue := &enqueueStub{}
		service, _ := NewSnapshotService(directUOW{}, store, evaluator, enqueue, &memberEventEnqueueStub{})
		return service, store, enqueue
	}
	pausedService, pausedStore, pausedEnqueue := makeService(segmentdomain.Paused)
	run, err := pausedService.AcceptRefresh(context.Background(), RefreshCommand{
		PackageID: 1, Actor: 4, IdempotencyKey: "full-refresh-paused-0001", ReferenceTime: time.Unix(900, 0).UTC(), FullRefresh: true,
	})
	if err != nil || run.RefreshKind != segmentdomain.RefreshDaily || pausedStore.run.RefreshKind != segmentdomain.RefreshDaily || pausedEnqueue.calls != 1 || pausedStore.lockCalls != 1 {
		t.Fatalf("paused full refresh run=%+v stored=%q queued=%d err=%v", run, pausedStore.run.RefreshKind, pausedEnqueue.calls, err)
	}
	replayed, replayErr := pausedService.AcceptRefresh(context.Background(), RefreshCommand{
		PackageID: 1, Actor: 4, IdempotencyKey: "full-refresh-paused-0001", ReferenceTime: time.Unix(900, 0).UTC(), FullRefresh: true,
	})
	if replayErr != nil || replayed.ID != run.ID || pausedEnqueue.calls != 1 {
		t.Fatalf("same-key full refresh replay=%+v queued=%d err=%v", replayed, pausedEnqueue.calls, replayErr)
	}
	for _, lifecycle := range []segmentdomain.Lifecycle{segmentdomain.Active, segmentdomain.Archived} {
		t.Run(string(lifecycle), func(t *testing.T) {
			service, store, enqueue := makeService(lifecycle)
			_, callErr := service.AcceptRefresh(context.Background(), RefreshCommand{
				PackageID: 1, Actor: 4, IdempotencyKey: "full-refresh-paused-0001", ReferenceTime: time.Unix(900, 0).UTC(), FullRefresh: true,
			})
			if callErr != ErrConflict || enqueue.calls != 0 || store.run.ID != 0 || store.lockCalls != 1 {
				t.Fatalf("lifecycle=%s run=%+v queued=%d err=%v", lifecycle, store.run, enqueue.calls, callErr)
			}
		})
	}
}

func TestRefreshStagesHundredThousandMembersAndPublishesOnce(t *testing.T) {
	ids := make([]customerdomain.CustomerID, 100000)
	for i := range ids {
		ids[i] = customerdomain.CustomerID(i + 1)
	}
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{run: segmentdomain.RefreshRun{ID: 7, PackageID: 1, ConfigurationVersionID: 3, State: segmentdomain.RefreshQueued, ReferenceTime: time.Unix(900, 0).UTC()}, config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4}, events: 100000}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{ids: ids}, passthroughCanonical{})
	eventQueue := &memberEventEnqueueStub{}
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, &enqueueStub{}, eventQueue)
	if err := service.ProcessRefresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if store.staged != 100000 || store.batches != 100 || store.published != 100000 || eventQueue.calls != 1 {
		t.Fatalf("staged=%d batches=%d published=%d event_jobs=%d", store.staged, store.batches, store.published, eventQueue.calls)
	}
	if err := service.ProcessRefresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if store.batches != 100 {
		t.Fatalf("published replay staged more batches: %d", store.batches)
	}
}

func TestRefreshStagesFirstPaidEvidenceWithTheMember(t *testing.T) {
	paidAt := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ids := []customerdomain.CustomerID{71}
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{
		run:    segmentdomain.RefreshRun{ID: 7, PackageID: 1, ConfigurationVersionID: 3, State: segmentdomain.RefreshQueued, ReferenceTime: paidAt.Add(time.Hour)},
		config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4},
		events: 1,
	}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{ids: ids, firstPaidAt: map[customerdomain.CustomerID]time.Time{71: paidAt}}, passthroughCanonical{})
	eventQueue := &memberEventEnqueueStub{}
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, &enqueueStub{}, eventQueue)
	service.now = func() time.Time { return paidAt.Add(2 * time.Hour) }
	if err := service.ProcessRefresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(store.memberFacts) != 1 || store.memberFacts[0].CustomerID != 71 || store.memberFacts[0].FirstPaidAt == nil || !store.memberFacts[0].FirstPaidAt.Equal(paidAt) || eventQueue.calls != 1 {
		t.Fatalf("staged member facts=%+v event_jobs=%d", store.memberFacts, eventQueue.calls)
	}
}
