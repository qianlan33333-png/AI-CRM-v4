package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type imageDeleteMemory struct {
	images      map[int64]bool
	receipts    map[string]ImageDeleteReceipt
	references  ImageDeleteReferences
	agents      []int64
	channels    []int64
	radarLinks  []int64
	events      []eventport.Event
	failEvent   bool
	failReaders bool
	mediaReads  int
}

type imageDeleteMemoryUOW struct{ state *imageDeleteMemory }
type imageDeleteMemoryStore struct{ state *imageDeleteMemory }
type imageDeleteMemoryAutomation struct{ state *imageDeleteMemory }
type imageDeleteMemoryContact struct{ state *imageDeleteMemory }
type imageDeleteMemoryRadar struct{ state *imageDeleteMemory }
type imageDeleteMemoryEvents struct{ state *imageDeleteMemory }

func (u imageDeleteMemoryUOW) Within(ctx context.Context, run func(context.Context) error) error {
	backupImages := make(map[int64]bool, len(u.state.images))
	for id, exists := range u.state.images {
		backupImages[id] = exists
	}
	backupReceipts := make(map[string]ImageDeleteReceipt, len(u.state.receipts))
	for key, receipt := range u.state.receipts {
		backupReceipts[key] = cloneImageDeleteReceipt(receipt)
	}
	backupEvents := append([]eventport.Event(nil), u.state.events...)
	if err := run(ctx); err != nil {
		u.state.images, u.state.receipts, u.state.events = backupImages, backupReceipts, backupEvents
		return err
	}
	return nil
}

func (s imageDeleteMemoryStore) LockImageForDelete(_ context.Context, id int64) (bool, error) {
	return s.state.images[id], nil
}
func (s imageDeleteMemoryStore) ListImageDeleteMediaReferences(_ context.Context, _ int64) (ImageDeleteReferences, error) {
	s.state.mediaReads++
	if s.state.failReaders {
		return ImageDeleteReferences{}, errors.New("media reader failed")
	}
	return cloneImageDeleteReferences(s.state.references), nil
}
func (s imageDeleteMemoryStore) GetImageDeleteReceipt(_ context.Context, reservation ImageDeleteReservation) (ImageDeleteReceipt, bool, error) {
	receipt, exists := s.state.receipts[imageDeleteReceiptKey(reservation)]
	return cloneImageDeleteReceipt(receipt), exists, nil
}
func (s imageDeleteMemoryStore) ReserveImageDelete(_ context.Context, reservation ImageDeleteReservation) (ImageDeleteReceipt, bool, error) {
	key := imageDeleteReceiptKey(reservation)
	if receipt, exists := s.state.receipts[key]; exists {
		return cloneImageDeleteReceipt(receipt), false, nil
	}
	receipt := ImageDeleteReceipt{ID: int64(len(s.state.receipts) + 1), ActorScope: reservation.ActorScope, BusinessKey: reservation.BusinessKey,
		KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	s.state.receipts[key] = receipt
	return receipt, true, nil
}
func (s imageDeleteMemoryStore) DeleteImage(_ context.Context, id int64) (int64, error) {
	if !s.state.images[id] {
		return 0, nil
	}
	delete(s.state.images, id)
	return 1, nil
}
func (s imageDeleteMemoryStore) CompleteImageDelete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (ImageDeleteReceipt, error) {
	for key, receipt := range s.state.receipts {
		if receipt.ID == id {
			receipt.State, receipt.ResultSnapshot = "completed", append(json.RawMessage{}, snapshot...)
			s.state.receipts[key] = receipt
			return cloneImageDeleteReceipt(receipt), nil
		}
	}
	return ImageDeleteReceipt{}, ErrImageDeleteUnavailable
}
func (r imageDeleteMemoryAutomation) ListImageReferenceAgentIDs(_ context.Context, _ int64) ([]int64, error) {
	if r.state.failReaders {
		return nil, errors.New("automation reader failed")
	}
	return append([]int64{}, r.state.agents...), nil
}
func (r imageDeleteMemoryContact) ListImageReferenceChannelIDs(_ context.Context, _ int64) ([]int64, error) {
	if r.state.failReaders {
		return nil, errors.New("contact reader failed")
	}
	return append([]int64{}, r.state.channels...), nil
}
func (r imageDeleteMemoryRadar) ListImageReferenceLinkIDs(_ context.Context, _ int64) ([]int64, error) {
	if r.state.failReaders {
		return nil, errors.New("radar reader failed")
	}
	return append([]int64{}, r.state.radarLinks...), nil
}
func (e imageDeleteMemoryEvents) Append(_ context.Context, event eventport.Event) (eventport.EventID, error) {
	if e.state.failEvent {
		return 0, errors.New("event write failed")
	}
	e.state.events = append(e.state.events, event)
	return eventport.EventID(len(e.state.events)), nil
}

func TestImageDeleteActorScopedReplayConflictReferencesAndRollback(t *testing.T) {
	state := &imageDeleteMemory{images: map[int64]bool{42: true}, receipts: map[string]ImageDeleteReceipt{}, references: emptyImageDeleteReferences(), agents: []int64{}, channels: []int64{}}
	service := newImageDeleteMemoryService(state)
	command := ImageDeleteCommand{ImageID: 42, Actor: 7, IdempotencyKey: "delete-image-command-0001"}
	result, err := service.DeleteImage(context.Background(), command)
	if err != nil || !result.Deleted || !result.HardDeleted || state.images[42] || len(state.receipts) != 1 || len(state.events) != 1 || state.events[0].Type != "media.image_deleted" {
		t.Fatalf("result=%#v err=%v state=%#v", result, err, state)
	}
	var payload map[string]any
	if json.Unmarshal(state.events[0].Payload, &payload) != nil || len(payload) != 2 || payload["image_id"] != float64(42) || payload["actor"] != float64(7) {
		t.Fatalf("event=%#v payload=%s", state.events[0], state.events[0].Payload)
	}
	replay, err := service.DeleteImage(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replay, result) || len(state.events) != 1 || len(state.receipts) != 1 {
		t.Fatalf("replay=%#v err=%v state=%#v", replay, err, state)
	}
	changed := command
	changed.Force = true
	// A corrupted legacy state with an image surviving a completed receipt
	// still must not disclose its references for a changed actor/key command.
	state.images[42] = true
	state.references = ImageDeleteReferences{Miniprograms: []int64{5}, CampaignSteps: []int64{}, GroupInvites: []int64{}, AutomationAgents: []int64{}, Channels: []int64{}, ImportPreflights: []int64{}}
	readsBeforeConflict := state.mediaReads
	if _, err = service.DeleteImage(context.Background(), changed); !errors.Is(err, ErrImageDeleteConflict) || len(state.events) != 1 || state.mediaReads != readsBeforeConflict {
		t.Fatalf("changed command err=%v state=%#v", err, state)
	}

	delete(state.images, 42)
	state.images[43] = true
	state.references = ImageDeleteReferences{Miniprograms: []int64{5}, CampaignSteps: []int64{}, GroupInvites: []int64{9}, AutomationAgents: []int64{}, Channels: []int64{}, ImportPreflights: []int64{12}}
	blocked, err := service.DeleteImage(context.Background(), ImageDeleteCommand{ImageID: 43, Actor: 7, IdempotencyKey: "delete-image-command-0002", Force: true})
	if !errors.Is(err, ErrImageHasReferences) || !state.images[43] || blocked.References.Miniprograms[0] != 5 || len(state.receipts) != 1 || len(state.events) != 1 {
		t.Fatalf("blocked=%#v err=%v state=%#v", blocked, err, state)
	}

	state.references = emptyImageDeleteReferences()
	state.radarLinks = []int64{21}
	state.images[44] = true
	blockedByRadar, err := service.DeleteImage(context.Background(), ImageDeleteCommand{ImageID: 44, Actor: 7, IdempotencyKey: "delete-image-command-radar"})
	if !errors.Is(err, ErrImageHasReferences) || len(blockedByRadar.References.RadarLinks) != 1 || blockedByRadar.References.RadarLinks[0] != 21 || !state.images[44] {
		t.Fatalf("radar blocked=%#v err=%v state=%#v", blockedByRadar, err, state)
	}
	state.radarLinks = []int64{}
	state.images[44] = true
	state.failEvent = true
	if _, err = service.DeleteImage(context.Background(), ImageDeleteCommand{ImageID: 44, Actor: 7, IdempotencyKey: "delete-image-command-0003"}); !errors.Is(err, ErrImageDeleteUnavailable) || !state.images[44] || len(state.receipts) != 1 || len(state.events) != 1 {
		t.Fatalf("rollback err=%v state=%#v", err, state)
	}
}

func TestImageDeleteRejectsUnorderedOrUnavailableReferenceReads(t *testing.T) {
	state := &imageDeleteMemory{images: map[int64]bool{42: true}, receipts: map[string]ImageDeleteReceipt{}, references: ImageDeleteReferences{Miniprograms: []int64{9, 5}, CampaignSteps: []int64{}, GroupInvites: []int64{}, AutomationAgents: []int64{}, Channels: []int64{}, ImportPreflights: []int64{}}, agents: []int64{}, channels: []int64{}}
	service := newImageDeleteMemoryService(state)
	if _, err := service.DeleteImage(context.Background(), ImageDeleteCommand{ImageID: 42, Actor: 7, IdempotencyKey: "delete-image-command-0004"}); !errors.Is(err, ErrImageDeleteUnavailable) || !state.images[42] || len(state.receipts) != 0 {
		t.Fatalf("unordered err=%v state=%#v", err, state)
	}
	state.references = emptyImageDeleteReferences()
	state.failReaders = true
	if _, err := service.DeleteImage(context.Background(), ImageDeleteCommand{ImageID: 42, Actor: 7, IdempotencyKey: "delete-image-command-0005"}); !errors.Is(err, ErrImageDeleteUnavailable) || !state.images[42] || len(state.receipts) != 0 {
		t.Fatalf("reader err=%v state=%#v", err, state)
	}
}

func newImageDeleteMemoryService(state *imageDeleteMemory) *ImageDeleteService {
	service := NewImageDeleteService(imageDeleteMemoryUOW{state}, imageDeleteMemoryStore{state}, imageDeleteMemoryAutomation{state}, imageDeleteMemoryContact{state}, imageDeleteMemoryRadar{state}, imageDeleteMemoryEvents{state})
	service.now = func() time.Time { return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC) }
	return service
}

func imageDeleteReceiptKey(reservation ImageDeleteReservation) string {
	return reservation.ActorScope + ":" + fmt.Sprintf("%x", reservation.KeyDigest)
}
func cloneImageDeleteReceipt(receipt ImageDeleteReceipt) ImageDeleteReceipt {
	receipt.ResultSnapshot = append(json.RawMessage{}, receipt.ResultSnapshot...)
	return receipt
}
func cloneImageDeleteReferences(value ImageDeleteReferences) ImageDeleteReferences {
	return ImageDeleteReferences{Miniprograms: append([]int64{}, value.Miniprograms...), CampaignSteps: append([]int64{}, value.CampaignSteps...),
		GroupInvites: append([]int64{}, value.GroupInvites...), AutomationAgents: append([]int64{}, value.AutomationAgents...), Channels: append([]int64{}, value.Channels...), RadarLinks: append([]int64{}, value.RadarLinks...), ImportPreflights: append([]int64{}, value.ImportPreflights...)}
}
