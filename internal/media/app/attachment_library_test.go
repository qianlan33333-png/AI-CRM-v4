package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	eventport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type attachmentMemory struct {
	attachments map[int64]mediaport.Attachment
	blobs       map[int64]AttachmentBlob
	receipts    map[string]AttachmentMutationReceipt
	events      []eventport.Event
	nextID      int64
	agents      []int64
	channels    []int64
	radarLinks  []int64
	failReaders bool
	failEvents  bool
}

type attachmentMemoryUOW struct{ state *attachmentMemory }
type attachmentMemoryStore struct{ state *attachmentMemory }
type attachmentMemoryAutomation struct{ state *attachmentMemory }
type attachmentMemoryContact struct{ state *attachmentMemory }
type attachmentMemoryRadar struct{ state *attachmentMemory }
type attachmentMemoryEvents struct{ state *attachmentMemory }

func (u attachmentMemoryUOW) Within(ctx context.Context, run func(context.Context) error) error {
	backup := cloneAttachmentMemory(*u.state)
	if err := run(ctx); err != nil {
		*u.state = backup
		return err
	}
	return nil
}

func (store attachmentMemoryStore) ReserveAttachmentMutation(_ context.Context, reservation AttachmentMutationReservation) (AttachmentMutationReceipt, bool, error) {
	key := attachmentReceiptKey(reservation)
	if receipt, exists := store.state.receipts[key]; exists {
		return cloneAttachmentReceipt(receipt), false, nil
	}
	receipt := AttachmentMutationReceipt{ID: int64(len(store.state.receipts) + 1), Operation: reservation.Operation, ActorScope: reservation.ActorScope, BusinessKey: reservation.BusinessKey, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	store.state.receipts[key] = receipt
	return receipt, true, nil
}

func (store attachmentMemoryStore) CreateAttachment(_ context.Context, input AttachmentCreateInput) (mediaport.Attachment, error) {
	id := store.state.nextID
	store.state.nextID++
	attachment := mediaport.Attachment{ID: id, Name: input.Command.Name, FileName: input.Command.FileName, MimeType: input.MediaType, FileSize: int64(len(input.Command.Content)), Description: input.Command.Description, Tags: cloneAttachmentTags(input.Command.Tags), Enabled: attachmentEnabled(input.Command.Enabled), Version: 1, CreatedBy: input.Command.Actor, UpdatedBy: input.Command.Actor, CreatedAt: input.Now, UpdatedAt: input.Now}
	store.state.attachments[id] = cloneAttachment(attachment)
	store.state.blobs[id] = AttachmentBlob{Attachment: cloneAttachment(attachment), Content: append([]byte(nil), input.Command.Content...), Checksum: input.Checksum}
	return cloneAttachment(attachment), nil
}

func (store attachmentMemoryStore) GetAttachment(_ context.Context, id int64) (mediaport.Attachment, error) {
	attachment, exists := store.state.attachments[id]
	if !exists {
		return mediaport.Attachment{}, ErrAttachmentNotFound
	}
	return cloneAttachment(attachment), nil
}

func (store attachmentMemoryStore) GetAttachmentForUpdate(ctx context.Context, id int64) (mediaport.Attachment, error) {
	return store.GetAttachment(ctx, id)
}

func (store attachmentMemoryStore) ListAttachments(_ context.Context, query mediaport.AttachmentListQuery) (AttachmentListRead, error) {
	ids := make([]int64, 0, len(store.state.attachments))
	for id := range store.state.attachments {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] > ids[right] })
	items := make([]mediaport.Attachment, 0, len(ids))
	for _, id := range ids {
		attachment := store.state.attachments[id]
		if query.EnabledOnly && !attachment.Enabled {
			continue
		}
		if query.Search != "" && !strings.Contains(strings.ToLower(attachment.Name+"\x00"+attachment.FileName), strings.ToLower(query.Search)) {
			continue
		}
		items = append(items, cloneAttachment(attachment))
	}
	total := int64(len(items))
	start := query.Offset
	if start > total {
		return AttachmentListRead{Total: total, Items: []mediaport.Attachment{}}, nil
	}
	end := start + query.Limit
	if end > total {
		end = total
	}
	return AttachmentListRead{Total: total, Items: append([]mediaport.Attachment{}, items[start:end]...)}, nil
}

func (store attachmentMemoryStore) ReadAttachment(_ context.Context, id int64) (AttachmentBlob, error) {
	blob, exists := store.state.blobs[id]
	if !exists {
		return AttachmentBlob{}, ErrAttachmentNotFound
	}
	blob.Attachment = cloneAttachment(blob.Attachment)
	blob.Content = append([]byte(nil), blob.Content...)
	return blob, nil
}

func (store attachmentMemoryStore) UpdateAttachment(_ context.Context, input AttachmentUpdateInput) (mediaport.Attachment, error) {
	current, exists := store.state.attachments[input.Attachment.ID]
	if !exists {
		return mediaport.Attachment{}, ErrAttachmentNotFound
	}
	if current.Version != input.ExpectedVersion {
		return mediaport.Attachment{}, ErrAttachmentConflict
	}
	store.state.attachments[input.Attachment.ID] = cloneAttachment(input.Attachment)
	blob := store.state.blobs[input.Attachment.ID]
	blob.Attachment = cloneAttachment(input.Attachment)
	store.state.blobs[input.Attachment.ID] = blob
	return cloneAttachment(input.Attachment), nil
}

func (store attachmentMemoryStore) DeleteAttachment(_ context.Context, id int64) (int64, error) {
	if _, exists := store.state.attachments[id]; !exists {
		return 0, nil
	}
	delete(store.state.attachments, id)
	delete(store.state.blobs, id)
	return 1, nil
}

func (store attachmentMemoryStore) CompleteAttachmentMutation(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (AttachmentMutationReceipt, error) {
	for key, receipt := range store.state.receipts {
		if receipt.ID != id || receipt.State != "in_progress" {
			continue
		}
		receipt.State = "completed"
		receipt.ResultSnapshot = append(json.RawMessage(nil), snapshot...)
		store.state.receipts[key] = receipt
		return cloneAttachmentReceipt(receipt), nil
	}
	return AttachmentMutationReceipt{}, ErrAttachmentUnavailable
}

func (reader attachmentMemoryAutomation) ListAttachmentReferenceAgentIDs(_ context.Context, _ int64) ([]int64, error) {
	if reader.state.failReaders {
		return nil, errors.New("automation read failure")
	}
	return append([]int64{}, reader.state.agents...), nil
}

func (reader attachmentMemoryContact) ListAttachmentReferenceChannelIDs(_ context.Context, _ int64) ([]int64, error) {
	if reader.state.failReaders {
		return nil, errors.New("channel read failure")
	}
	return append([]int64{}, reader.state.channels...), nil
}

func (reader attachmentMemoryRadar) ListAttachmentReferenceLinkIDs(_ context.Context, _ int64) ([]int64, error) {
	if reader.state.failReaders {
		return nil, errors.New("radar read failure")
	}
	return append([]int64{}, reader.state.radarLinks...), nil
}

func (events attachmentMemoryEvents) Append(_ context.Context, event eventport.Event) (eventport.EventID, error) {
	if events.state.failEvents {
		return 0, errors.New("event append failure")
	}
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	events.state.events = append(events.state.events, event)
	return eventport.EventID(len(events.state.events)), nil
}

func TestAttachmentLifecycleIsAtomicPrivateAndIdempotent(t *testing.T) {
	service, state := newAttachmentMemoryService()
	content := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	upload := mediaport.AttachmentUploadCommand{Actor: 7, IdempotencyKey: "attachment-upload-key-0001", FileName: "guide.pdf", DeclaredType: "application/pdf", Content: content, Tags: []string{"guide", "private"}}
	created, err := service.Upload(context.Background(), upload)
	if err != nil || created.ID != 1 || created.Name != "guide.pdf" || !created.Enabled || created.Version != 1 || len(state.receipts) != 1 || len(state.events) != 1 || state.events[0].Type != "media.attachment_created" {
		t.Fatalf("created=%+v err=%v state=%+v", created, err, state)
	}
	if string(state.blobs[created.ID].Content) != string(content) || state.blobs[created.ID].Attachment.ID != created.ID {
		t.Fatalf("blob=%+v", state.blobs[created.ID])
	}

	replayed, err := service.Upload(context.Background(), upload)
	if err != nil || !equalAttachment(replayed, created) || len(state.receipts) != 1 || len(state.events) != 1 {
		t.Fatalf("replayed=%+v err=%v state=%+v", replayed, err, state)
	}
	changedUpload := upload
	changedUpload.Description = "different"
	if _, err = service.Upload(context.Background(), changedUpload); !errors.Is(err, ErrAttachmentConflict) || len(state.attachments) != 1 {
		t.Fatalf("changed upload err=%v state=%+v", err, state)
	}

	page, err := service.List(context.Background(), mediaport.AttachmentListQuery{EnabledOnly: true})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID || page.Items[0].FileName != "guide.pdf" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	metadata, err := service.Get(context.Background(), created.ID)
	if err != nil || !equalAttachment(metadata, created) {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	download, err := service.Download(context.Background(), created.ID)
	if err != nil || !equalAttachment(download.Attachment, created) || string(download.Content) != string(content) {
		t.Fatalf("download=%+v err=%v", download, err)
	}

	updated, err := service.Update(context.Background(), mediaport.AttachmentUpdateCommand{AttachmentID: created.ID, ExpectedVersion: 1, Actor: 8, IdempotencyKey: "attachment-update-key-0001", Name: "Launch guide", Description: "Read this first", Tags: []string{"guide"}, Enabled: false})
	if err != nil || updated.Version != 2 || updated.UpdatedBy != 8 || updated.Enabled || updated.Name != "Launch guide" || len(state.receipts) != 2 || len(state.events) != 2 || state.events[1].Type != "media.attachment_updated" {
		t.Fatalf("updated=%+v err=%v state=%+v", updated, err, state)
	}
	replayedUpdate, err := service.Update(context.Background(), mediaport.AttachmentUpdateCommand{AttachmentID: created.ID, ExpectedVersion: 1, Actor: 8, IdempotencyKey: "attachment-update-key-0001", Name: "Launch guide", Description: "Read this first", Tags: []string{"guide"}, Enabled: false})
	if err != nil || !equalAttachment(replayedUpdate, updated) || len(state.events) != 2 {
		t.Fatalf("update replay=%+v err=%v", replayedUpdate, err)
	}
	if _, err = service.Update(context.Background(), mediaport.AttachmentUpdateCommand{AttachmentID: created.ID, ExpectedVersion: 1, Actor: 8, IdempotencyKey: "attachment-update-key-0002", Name: "Stale", Tags: []string{}, Enabled: false}); !errors.Is(err, ErrAttachmentConflict) || len(state.receipts) != 2 {
		t.Fatalf("stale update err=%v state=%+v", err, state)
	}

	state.agents = []int64{11}
	blocked, err := service.Delete(context.Background(), mediaport.AttachmentDeleteCommand{AttachmentID: created.ID, Actor: 8, IdempotencyKey: "attachment-delete-key-0001"})
	if !errors.Is(err, ErrAttachmentHasReferences) || len(blocked.References.AutomationAgents) != 1 || blocked.References.AutomationAgents[0] != 11 || len(state.attachments) != 1 || len(state.receipts) != 2 {
		t.Fatalf("blocked=%+v err=%v state=%+v", blocked, err, state)
	}
	state.agents = []int64{}
	deleted, err := service.Delete(context.Background(), mediaport.AttachmentDeleteCommand{AttachmentID: created.ID, Actor: 8, IdempotencyKey: "attachment-delete-key-0002"})
	if err != nil || !deleted.Deleted || !deleted.HardDeleted || deleted.References.Any() || len(state.attachments) != 0 || len(state.blobs) != 0 || len(state.receipts) != 3 || len(state.events) != 3 || state.events[2].Type != "media.attachment_deleted" {
		t.Fatalf("deleted=%+v err=%v state=%+v", deleted, err, state)
	}
	replayedDelete, err := service.Delete(context.Background(), mediaport.AttachmentDeleteCommand{AttachmentID: created.ID, Actor: 8, IdempotencyKey: "attachment-delete-key-0002"})
	if err != nil || !reflect.DeepEqual(replayedDelete, deleted) || len(state.events) != 3 {
		t.Fatalf("delete replay=%+v err=%v", replayedDelete, err)
	}
}

func TestAttachmentRejectsUnsafeDataAndFailsClosedOnReferences(t *testing.T) {
	service, state := newAttachmentMemoryService()
	for _, command := range []mediaport.AttachmentUploadCommand{
		{Actor: 7, IdempotencyKey: attachmentTestIdempotencyKey("invalid-01"), FileName: "guide.pdf", DeclaredType: "application/pdf", Content: []byte("not-a-pdf")},
		{Actor: 7, IdempotencyKey: attachmentTestIdempotencyKey("invalid-02"), FileName: "guide.pdf", DeclaredType: "application/zip", Content: []byte("%PDF-1.7\n%%EOF")},
		{Actor: 7, IdempotencyKey: attachmentTestIdempotencyKey("invalid-03"), FileName: "../guide.pdf", DeclaredType: "application/pdf", Content: []byte("%PDF-1.7\n%%EOF")},
	} {
		if _, err := service.Upload(context.Background(), command); !errors.Is(err, ErrInvalidAttachment) || len(state.attachments) != 0 || len(state.receipts) != 0 {
			t.Fatalf("command=%+v err=%v state=%+v", command, err, state)
		}
	}

	created, err := service.Upload(context.Background(), mediaport.AttachmentUploadCommand{Actor: 7, IdempotencyKey: "attachment-safe-upload-0001", FileName: "guide.pdf", DeclaredType: "application/pdf", Content: []byte("%PDF-1.7\n%%EOF")})
	if err != nil {
		t.Fatal(err)
	}
	state.radarLinks = []int64{9, 5}
	if result, err := service.Delete(context.Background(), mediaport.AttachmentDeleteCommand{AttachmentID: created.ID, Actor: 7, IdempotencyKey: attachmentTestIdempotencyKey("fail-close-01")}); !errors.Is(err, ErrAttachmentUnavailable) || result.ID != 0 || len(state.attachments) != 1 || len(state.receipts) != 1 {
		t.Fatalf("unordered refs result=%+v err=%v state=%+v", result, err, state)
	}
	state.radarLinks = []int64{}
	state.failReaders = true
	if result, err := service.Delete(context.Background(), mediaport.AttachmentDeleteCommand{AttachmentID: created.ID, Actor: 7, IdempotencyKey: attachmentTestIdempotencyKey("fail-close-02")}); !errors.Is(err, ErrAttachmentUnavailable) || result.ID != 0 || len(state.attachments) != 1 || len(state.receipts) != 1 {
		t.Fatalf("reader failure result=%+v err=%v state=%+v", result, err, state)
	}
	state.failReaders = false
	state.blobs[created.ID] = AttachmentBlob{Attachment: cloneAttachment(created), Content: []byte("%PDF-1.7\n%%EOF"), Checksum: sha256.Sum256([]byte("different"))}
	if _, err := service.Download(context.Background(), created.ID); !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("checksum err=%v", err)
	}
}

func newAttachmentMemoryService() (*AttachmentService, *attachmentMemory) {
	state := &attachmentMemory{attachments: map[int64]mediaport.Attachment{}, blobs: map[int64]AttachmentBlob{}, receipts: map[string]AttachmentMutationReceipt{}, events: []eventport.Event{}, nextID: 1, agents: []int64{}, channels: []int64{}, radarLinks: []int64{}}
	service := NewAttachmentServiceWithReferences(attachmentMemoryUOW{state}, attachmentMemoryStore{state}, attachmentMemoryAutomation{state}, attachmentMemoryContact{state}, attachmentMemoryRadar{state}, attachmentMemoryEvents{state})
	base := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	var steps int64
	service.now = func() time.Time {
		steps++
		return base.Add(time.Duration(steps) * time.Second)
	}
	return service, state
}

func attachmentReceiptKey(reservation AttachmentMutationReservation) string {
	return reservation.Operation + "\x00" + reservation.ActorScope + "\x00" + reservation.BusinessKey + "\x00" + string(reservation.KeyDigest[:])
}

func attachmentTestIdempotencyKey(suffix string) string {
	return "attachment-" + suffix + "-key-" + "0001"
}

func cloneAttachmentReceipt(receipt AttachmentMutationReceipt) AttachmentMutationReceipt {
	receipt.ResultSnapshot = append(json.RawMessage(nil), receipt.ResultSnapshot...)
	return receipt
}

func cloneAttachmentMemory(source attachmentMemory) attachmentMemory {
	cloned := attachmentMemory{attachments: make(map[int64]mediaport.Attachment, len(source.attachments)), blobs: make(map[int64]AttachmentBlob, len(source.blobs)), receipts: make(map[string]AttachmentMutationReceipt, len(source.receipts)), events: make([]eventport.Event, len(source.events)), nextID: source.nextID, agents: append([]int64{}, source.agents...), channels: append([]int64{}, source.channels...), radarLinks: append([]int64{}, source.radarLinks...), failReaders: source.failReaders, failEvents: source.failEvents}
	for id, attachment := range source.attachments {
		cloned.attachments[id] = cloneAttachment(attachment)
	}
	for id, blob := range source.blobs {
		blob.Attachment = cloneAttachment(blob.Attachment)
		blob.Content = append([]byte(nil), blob.Content...)
		cloned.blobs[id] = blob
	}
	for key, receipt := range source.receipts {
		cloned.receipts[key] = cloneAttachmentReceipt(receipt)
	}
	for index, event := range source.events {
		event.Payload = append(json.RawMessage(nil), event.Payload...)
		cloned.events[index] = event
	}
	return cloned
}
