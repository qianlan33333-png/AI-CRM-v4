package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
)

type ownerHandoffDirectUOW struct{}

func (ownerHandoffDirectUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type ownerHandoffStaffStub map[int64]accessdomain.User

func (s ownerHandoffStaffStub) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	user, ok := s[id]
	if !ok {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

type ownerHandoffResolverStub struct {
	candidates []customerport.OwnerHandoffCandidate
}

func (s ownerHandoffResolverStub) ResolveOwnerHandoffCandidates(_ context.Context, _ customerport.OwnerHandoffMode, _, _ int64, _ string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	if len(ids) != len(s.candidates) {
		return nil, errors.New("wrong candidate request")
	}
	return append([]customerport.OwnerHandoffCandidate(nil), s.candidates...), nil
}

type ownerHandoffAuditStub struct{}

func (ownerHandoffAuditStub) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	return event, nil
}

type ownerHandoffOutboxStub struct{}

func (ownerHandoffOutboxStub) Append(_ context.Context, event platformoutbox.Event) (platformoutbox.Event, error) {
	return event, nil
}

type ownerHandoffStoreStub struct {
	preview customerport.OwnerHandoffPreviewRecord
	owners  map[customerdomain.CustomerID]customerport.LocalOwner
	batch   customerport.OwnerHandoffBatch
}

func (s *ownerHandoffStoreStub) CreateOwnerHandoffPreview(_ context.Context, draft customerport.OwnerHandoffPreviewRecord) (customerport.OwnerHandoffPreview, error) {
	s.preview = draft
	s.preview.Preview.Rows = []customerport.OwnerHandoffPreviewRow{{Line: 1, CustomerID: draft.Candidates[0].CustomerID, ExpectedOwnerID: draft.Candidates[0].ExpectedLocalOwnerID, ExpectedVersion: draft.Candidates[0].ExpectedLocalVersion, State: draft.Candidates[0].State}}
	return s.preview.Preview, nil
}
func (s *ownerHandoffStoreStub) LoadOwnerHandoffPreview(_ context.Context, _ string, _ bool) (customerport.OwnerHandoffPreviewRecord, error) {
	return s.preview, nil
}
func (s *ownerHandoffStoreStub) OwnerHandoffBatchByIdempotency(context.Context, int64, string) (customerport.OwnerHandoffBatch, [32]byte, bool, error) {
	return customerport.OwnerHandoffBatch{}, [32]byte{}, false, nil
}
func (s *ownerHandoffStoreStub) LockOwnerHandoffCustomersAndRejectActiveWeCom(context.Context, []customerdomain.CustomerID) error {
	return nil
}
func (s *ownerHandoffStoreStub) LoadOwnerHandoffBatchSegment(_ context.Context, batchID string, segment int64, _ int) (customerport.OwnerHandoffBatchSegment, error) {
	if batchID != s.batch.ID || segment != 0 {
		return customerport.OwnerHandoffBatchSegment{BatchID: batchID}, nil
	}
	result := customerport.OwnerHandoffBatchSegment{BatchID: batchID, PreviewID: s.preview.Preview.ID, ActorID: s.preview.ActorAdminUserID, TargetStaffID: s.preview.Preview.TargetStaffID, Mode: s.preview.Preview.Mode}
	for index, line := range s.batch.Lines {
		if line.State == "queued" {
			result.Lines = append(result.Lines, customerport.OwnerHandoffSegmentLine{OwnerHandoffLine: line, ExpectedLocalVersion: s.preview.Candidates[index].ExpectedLocalVersion})
		}
	}
	return result, nil
}
func (s *ownerHandoffStoreStub) SetOwnerHandoffLineState(_ context.Context, batchID string, line int64, state string) error {
	if batchID != s.batch.ID {
		return ErrOwnerHandoffDrift
	}
	for index := range s.batch.Lines {
		if s.batch.Lines[index].Line == line {
			s.batch.Lines[index].State = state
			return nil
		}
	}
	return ErrOwnerHandoffDrift
}
func (s *ownerHandoffStoreStub) RecomputeOwnerHandoffBatchState(context.Context, string) error {
	return nil
}

type ownerHandoffBatchEnqueuerStub struct{}

func (ownerHandoffBatchEnqueuerStub) EnqueueOwnerHandoffBatchWithin(context.Context, string, int64) error {
	return nil
}
func (s *ownerHandoffStoreStub) LocalOwner(_ context.Context, id customerdomain.CustomerID, _ bool) (customerport.LocalOwner, bool, error) {
	v, ok := s.owners[id]
	return v, ok, nil
}
func (s *ownerHandoffStoreStub) AssignLocalOwner(_ context.Context, id customerdomain.CustomerID, target, expected int64, source string, at time.Time) (customerport.LocalOwner, error) {
	old, found := s.owners[id]
	if found && old.Version != expected {
		return customerport.LocalOwner{}, ErrOwnerHandoffDrift
	}
	if !found && expected != 0 {
		return customerport.LocalOwner{}, ErrOwnerHandoffDrift
	}
	version := int64(1)
	if found {
		version = old.Version + 1
	}
	v := customerport.LocalOwner{CustomerID: id, StaffID: target, Version: version, Source: source, UpdatedAt: at}
	s.owners[id] = v
	return v, nil
}
func (s *ownerHandoffStoreStub) CreateWeComOwnerHandoffBatch(_ context.Context, draft customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error) {
	s.batch = customerport.OwnerHandoffBatch{ID: "batch-1", Mode: draft.Preview.Preview.Mode, State: "accepted", Lines: draft.Lines}
	return s.batch, nil
}
func (s *ownerHandoffStoreStub) BindOwnerHandoffEffect(_ context.Context, binding customerport.OwnerHandoffEffectBinding) error {
	for i := range s.batch.Lines {
		for _, line := range binding.Lines {
			if s.batch.Lines[i].Line == line {
				s.batch.Lines[i].EffectID = binding.EffectID
			}
		}
	}
	return nil
}

func (s *ownerHandoffStoreStub) CreateLocalOnlyOwnerHandoffBatch(_ context.Context, draft customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error) {
	s.batch = customerport.OwnerHandoffBatch{ID: "batch-1", Mode: draft.Preview.Preview.Mode, State: "accepted", Lines: draft.Lines}
	return s.batch, nil
}

func TestOwnerHandoffLocalOnlyAllowsInactiveSourceButRevalidatesFrozenRelation(t *testing.T) {
	digest := [32]byte{1}
	store := &ownerHandoffStoreStub{owners: map[customerdomain.CustomerID]customerport.LocalOwner{}}
	service, err := NewOwnerHandoffService(ownerHandoffDirectUOW{}, store, ownerHandoffStaffStub{1: {ID: 1, Active: false}, 2: {ID: 2, Active: true}}, ownerHandoffResolverStub{candidates: []customerport.OwnerHandoffCandidate{{CustomerID: 7, RelationshipDigest: digest, State: "ready"}}}, ownerHandoffAuditStub{}, ownerHandoffOutboxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(ownerHandoffBatchEnqueuerStub{}); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	service.newID = func() (string, error) { return "preview-1", nil }
	preview, err := service.PreviewOwnerHandoff(context.Background(), customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: 1, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: 1, TargetStaffID: 2, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{7}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-key"})
	if err != nil || preview.ID != "preview-1" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	batch, err := service.ConfirmOwnerHandoff(context.Background(), customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: 1, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-key"})
	if err == nil {
		err = service.ProcessOwnerHandoffBatch(context.Background(), batch.ID, 0)
	}
	if err != nil || store.owners[7].StaffID != 2 {
		t.Fatalf("batch=%+v owner=%+v err=%v", batch, store.owners[7], err)
	}
}

func TestOwnerHandoffConfirmRejectsRelationDriftAndInactiveTarget(t *testing.T) {
	frozen := [32]byte{1}
	changed := [32]byte{2}
	store := &ownerHandoffStoreStub{owners: map[customerdomain.CustomerID]customerport.LocalOwner{}}
	resolver := ownerHandoffResolverStub{candidates: []customerport.OwnerHandoffCandidate{{CustomerID: 8, RelationshipDigest: frozen, State: "ready"}}}
	service, err := NewOwnerHandoffService(ownerHandoffDirectUOW{}, store, ownerHandoffStaffStub{1: {ID: 1}, 2: {ID: 2, Active: true}}, resolver, ownerHandoffAuditStub{}, ownerHandoffOutboxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(ownerHandoffBatchEnqueuerStub{}); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
	service.newID = func() (string, error) { return "preview-2", nil }
	preview, err := service.PreviewOwnerHandoff(context.Background(), customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: 1, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: 1, TargetStaffID: 2, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{8}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-key"})
	if err != nil {
		t.Fatal(err)
	}
	service.resolver = ownerHandoffResolverStub{candidates: []customerport.OwnerHandoffCandidate{{CustomerID: 8, RelationshipDigest: changed, State: "ready"}}}
	_, err = service.ConfirmOwnerHandoff(context.Background(), customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: 1, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-key"})
	if !errors.Is(err, ErrOwnerHandoffDrift) {
		t.Fatalf("expected drift got %v", err)
	}
}
