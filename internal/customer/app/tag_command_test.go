package app

import (
	"context"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"strconv"
	"testing"
	"time"
)

type tagTestUOW struct{}

func (tagTestUOW) Within(c context.Context, f func(context.Context) error) error { return f(c) }

type tagTestStore struct {
	result  customerport.TagCommandResult
	digest  [32]byte
	found   bool
	next    int64
	creates int
	state   string
}

func (s *tagTestStore) FindTagCommand(context.Context, string, string, string) (customerport.TagCommandResult, [32]byte, bool, error) {
	return s.result, s.digest, s.found, nil
}
func (*tagTestStore) LockTagCommandTargets(context.Context, []customerport.TagCommandTarget) error {
	return nil
}
func (*tagTestStore) EnsureTagCommandTargetsIdle(context.Context, []customerport.TagCommandTarget) error {
	return nil
}
func (s *tagTestStore) SetTagCommandState(_ context.Context, _ int64, state string) error {
	s.state = state
	return nil
}
func (s *tagTestStore) CreateTagCommand(_ context.Context, _ customerport.TagCommand, digest [32]byte) (int64, error) {
	s.next++
	s.creates++
	s.digest = digest
	return s.next, nil
}
func (s *tagTestStore) CreateRejectedTagCommandLine(_ context.Context, _ int64, t customerport.TagCommandTarget, r string) (customerport.TagCommandLine, error) {
	return customerport.TagCommandLine{CustomerID: t.CustomerID, State: "rejected", RejectReason: r}, nil
}
func (s *tagTestStore) CreateTagCommandLine(_ context.Context, _ int64, t customerport.FrozenTagCommandTarget, _ string, e effectport.Projection, receipt effectport.Receipt) (customerport.TagCommandLine, error) {
	return customerport.TagCommandLine{CustomerID: t.CustomerID, StaffID: t.StaffID, AddTagIDs: t.AddTagIDs, RemoveTagIDs: t.RemoveTagIDs, BindingDigest: t.BindingDigest, TargetDigest: t.TargetDigest, EffectRef: e.ID, AcceptReceiptRef: receipt.ID, QueueReceiptRef: receipt.QueueReceiptID, State: string(e.State)}, nil
}

type tagGate struct{}

func (tagGate) FreezeTagCommandTarget(_ context.Context, t customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	t.StaffID = 9
	return customerport.FrozenTagCommandTarget{TagCommandTarget: t, BindingDigest: string(effectport.Hash("binding")), TargetDigest: string(effectport.Hash("target"))}, nil
}

type tagEffects struct{ calls int }

func (e *tagEffects) AcceptAndQueueWithin(_ context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	e.calls++
	return effectport.Projection{ID: "eer_" + strconv.Itoa(e.calls), State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_" + strconv.Itoa(e.calls), QueueReceiptID: "eeropq_" + strconv.Itoa(e.calls)}, nil
}

type tagAudit struct{}

func (tagAudit) Append(_ context.Context, e platformaudit.Event) (platformaudit.Event, error) {
	return e, nil
}

type tagOutbox struct{}

func (tagOutbox) Append(_ context.Context, event platformoutbox.Event) (platformoutbox.Event, error) {
	event.ID = 1
	return event, nil
}

var _ platformport.UnitOfWork = tagTestUOW{}

func TestTagCommandOneEffectPerCustomerAndStableReplayDigest(t *testing.T) {
	store := &tagTestStore{}
	effects := &tagEffects{}
	svc, err := NewTagCommandService(tagTestUOW{}, store, effects, tagGate{}, tagAudit{}, tagOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	base := customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "key-00000001", IdempotencyKey: "key-00000001", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{
		{CustomerID: customerdomain.CustomerID(4), AddTagIDs: []int64{2, 1}, RemoveTagIDs: []int64{5}},
		{CustomerID: customerdomain.CustomerID(3), AddTagIDs: []int64{2, 1}, RemoveTagIDs: []int64{4}},
	}}
	first, err := svc.SubmitTagCommand(context.Background(), base)
	if err != nil || len(first.Lines) != 2 || effects.calls != 2 || store.creates != 1 {
		t.Fatalf("first=%+v err=%v calls=%d creates=%d", first, err, effects.calls, store.creates)
	}
	if first.Lines[0].CustomerID != 3 || first.Lines[0].StaffID != 9 || len(first.Lines[0].AddTagIDs) != 2 {
		t.Fatalf("first line=%+v", first.Lines[0])
	}
	// A same-key replay has a different clock value but precisely the same
	// business payload. It returns the original rows without accepting effects.
	store.result, store.found = first, true
	replayed := base
	replayed.OccurredAt = base.OccurredAt.Add(5 * time.Minute)
	again, err := svc.SubmitTagCommand(context.Background(), replayed)
	if err != nil || again.ID != first.ID || effects.calls != 2 || store.creates != 1 {
		t.Fatalf("again=%+v err=%v calls=%d creates=%d", again, err, effects.calls, store.creates)
	}
	changed := replayed
	changed.Targets[0].AddTagIDs = []int64{99}
	if _, err = svc.SubmitTagCommand(context.Background(), changed); !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("payload drift err=%v", err)
	}
}

type rejectTagGate struct{}

func (rejectTagGate) FreezeTagCommandTarget(context.Context, customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	return customerport.FrozenTagCommandTarget{}, errors.New("unavailable")
}

func TestTagCommandAllRejectedStartsRejected(t *testing.T) {
	store := &tagTestStore{}
	effects := &tagEffects{}
	svc, err := NewTagCommandService(tagTestUOW{}, store, effects, rejectTagGate{}, tagAudit{}, tagOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.SubmitTagCommand(context.Background(), customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "all-rejected", IdempotencyKey: "all-rejected", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}, {CustomerID: 2, AddTagIDs: []int64{1}}}})
	if err != nil || result.State != "rejected" || store.state != "rejected" || effects.calls != 0 || len(result.Lines) != 2 {
		t.Fatalf("result=%+v state=%q effects=%d err=%v", result, store.state, effects.calls, err)
	}
}

func TestTagCommandRejectsCombinedWeComTagLimitBeforePreviewOrAcceptance(t *testing.T) {
	store := &tagTestStore{}
	effects := &tagEffects{}
	svc, err := NewTagCommandService(tagTestUOW{}, store, effects, tagGate{}, tagAudit{}, tagOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	add := make([]int64, 100)
	for index := range add {
		add[index] = int64(index + 1)
	}
	command := customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "combined-tag-limit", IdempotencyKey: "combined-tag-limit", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: add, RemoveTagIDs: []int64{101}}}}
	if _, err = svc.PreviewTagCommand(context.Background(), command); !errors.Is(err, customerport.ErrTagCommandInvalid) {
		t.Fatalf("preview err=%v", err)
	}
	if _, err = svc.SubmitTagCommand(context.Background(), command); !errors.Is(err, customerport.ErrTagCommandInvalid) || effects.calls != 0 || store.creates != 0 {
		t.Fatalf("submit err=%v effects=%d creates=%d", err, effects.calls, store.creates)
	}
}
