package app

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestCreateIsIdempotentAndRejectsPayloadDrift(t *testing.T) {
	memory := newMemoryPersistence()
	service := newTestService(t, memory)
	command := radarport.CreateCommand{Name: "guide", Title: "Guide", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com/guide"}, AuthPolicy: radar.AuthPolicyUnionIDRequired, ActorID: 7, IdempotencyKey: "radar-create-key-0001"}

	first, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Link.ID != replay.Link.ID || len(memory.links) != 1 || len(memory.receipts) != 1 || len(memory.audits) != 1 || len(memory.outbox) != 1 {
		t.Fatalf("first/replay=%#v/%#v links/receipts/audit/outbox=%d/%d/%d/%d", first, replay, len(memory.links), len(memory.receipts), len(memory.audits), len(memory.outbox))
	}

	command.Title = "Payload changed"
	if _, err = service.Create(context.Background(), command); !errors.Is(err, radarport.ErrIdempotencyConflict) {
		t.Fatalf("payload drift error=%v", err)
	}
}

func TestMutationRollbackIncludesReceiptAuditAndOutbox(t *testing.T) {
	memory := newMemoryPersistence()
	memory.failAudit = true
	service := newTestService(t, memory)
	_, err := service.Create(context.Background(), radarport.CreateCommand{Name: "guide", Title: "Guide", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com/guide"}, AuthPolicy: radar.AuthPolicyAnonymous, ActorID: 7, IdempotencyKey: "radar-create-key-0002"})
	if !errors.Is(err, radarport.ErrUnavailable) {
		t.Fatalf("create error=%v", err)
	}
	if len(memory.links) != 0 || len(memory.receipts) != 0 || len(memory.audits) != 0 || len(memory.outbox) != 0 {
		t.Fatalf("rollback leaked links/receipts/audit/outbox=%d/%d/%d/%d", len(memory.links), len(memory.receipts), len(memory.audits), len(memory.outbox))
	}
}

func TestUpdateAndStatusUseExpectedVersion(t *testing.T) {
	memory := newMemoryPersistence()
	service := newTestService(t, memory)
	created, err := service.Create(context.Background(), radarport.CreateCommand{Name: "guide", Title: "Guide", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com/guide"}, AuthPolicy: radar.AuthPolicyAnonymous, ActorID: 7, IdempotencyKey: "radar-create-key-0003"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), radarport.UpdateCommand{RadarID: created.Link.ID, Expected: 1, Revision: radar.Revision{Name: "guide", Title: "Updated", Content: radar.Content{Type: radar.ContentTypeImage, MediaID: 9}, AuthPolicy: radar.AuthPolicyUnionIDRequired}, ActorID: 7, IdempotencyKey: "radar-update-key-0001"})
	if err != nil || updated.Link.Version != 2 || updated.Link.PublicCode != created.Link.PublicCode {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err = service.Update(context.Background(), radarport.UpdateCommand{RadarID: created.Link.ID, Expected: 1, Revision: radar.Revision{Name: "guide", Title: "Stale", Content: radar.Content{Type: radar.ContentTypeImage, MediaID: 9}, AuthPolicy: radar.AuthPolicyUnionIDRequired}, ActorID: 7, IdempotencyKey: "radar-update-key-0002"}); !errors.Is(err, radar.ErrVersionConflict) {
		t.Fatalf("stale update error=%v", err)
	}
	enabled, err := service.SetStatus(context.Background(), radarport.SetStatusCommand{RadarID: created.Link.ID, Expected: 2, Target: radar.StatusEnabled, ActorID: 7, IdempotencyKey: "radar-enable-key-0001"})
	if err != nil || enabled.Link.Status != radar.StatusEnabled || enabled.Link.Version != 3 {
		t.Fatalf("enabled=%#v err=%v", enabled, err)
	}
}

func TestListTrimsSearchAndRejectsOver200UTF8Bytes(t *testing.T) {
	memory := newMemoryPersistence()
	service := newTestService(t, memory)
	if _, err := service.List(context.Background(), radarport.ListQuery{Search: "  name needle  ", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if memory.listQuery.Search != "name needle" || memory.listQuery.Limit != 20 {
		t.Fatalf("trimmed query=%+v", memory.listQuery)
	}
	if _, err := service.List(context.Background(), radarport.ListQuery{Search: strings.Repeat("界", 67), Limit: 20}); !errors.Is(err, radar.ErrInvalidArgument) {
		t.Fatalf("over-200-byte UTF-8 query error=%v", err)
	}
}

func newTestService(t *testing.T, memory *memoryPersistence) *Service {
	t.Helper()
	service, err := NewService(memory, memory, memory)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC) }
	service.code = func() (radar.PublicCode, error) { return "rd_0123456789abcdefghijAB", nil }
	service.media = allowMedia{}
	return service
}

type allowMedia struct{}

func (allowMedia) ValidateRadarMedia(context.Context, radar.ContentType, radar.MediaID) error {
	return nil
}

type memoryPersistence struct {
	links         map[radar.RadarID]radar.Link
	receipts      map[string]radarport.OperationReceipt
	audits        []radarport.AuditRecord
	outbox        []radarport.OutboxRecord
	nextID        radar.RadarID
	nextRecID     int64
	failAudit     bool
	externalPage  radarport.ExternalLinkMappingPage
	externalQuery radarport.ExternalLinkMappingQuery
	externalErr   error
	listQuery     radarport.ListQuery
}

func newMemoryPersistence() *memoryPersistence {
	return &memoryPersistence{links: map[radar.RadarID]radar.Link{}, receipts: map[string]radarport.OperationReceipt{}, nextID: 1, nextRecID: 1}
}

func (memory *memoryPersistence) Within(ctx context.Context, callback func(context.Context) error) error {
	snapshot := memory.clone()
	if err := callback(ctx); err != nil {
		*memory = *snapshot
		return err
	}
	return nil
}

func (memory *memoryPersistence) Get(_ context.Context, id radar.RadarID) (radar.Link, error) {
	link, ok := memory.links[id]
	if !ok {
		return radar.Link{}, radarport.ErrNotFound
	}
	return link, nil
}

func (memory *memoryPersistence) GetByPublicCode(_ context.Context, code radar.PublicCode) (radar.Link, error) {
	for _, link := range memory.links {
		if link.PublicCode == code {
			return link, nil
		}
	}
	return radar.Link{}, radarport.ErrNotFound
}

func (memory *memoryPersistence) List(_ context.Context, query radarport.ListQuery) (radarport.LinkPage, error) {
	memory.listQuery = query
	items := make([]radarport.LinkSummary, 0, len(memory.links))
	for _, link := range memory.links {
		items = append(items, radarport.LinkSummary{Link: link})
	}
	return radarport.LinkPage{Items: items, Total: int64(len(items)), Limit: query.Limit, Offset: query.Offset}, nil
}

func (memory *memoryPersistence) Create(_ context.Context, record radarport.CreateRecord, _ int64, now time.Time) (radar.Link, error) {
	link, err := radar.NewDraft(memory.nextID, record.PublicCode, record.Name, record.Title, record.Description, record.Content, record.AuthPolicy, now)
	if err != nil {
		return radar.Link{}, err
	}
	memory.links[link.ID] = link
	memory.nextID++
	return link, nil
}

func (memory *memoryPersistence) ExternalLinkMappings(_ context.Context, query radarport.ExternalLinkMappingQuery) (radarport.ExternalLinkMappingPage, error) {
	memory.externalQuery = query
	return memory.externalPage, memory.externalErr
}

func (memory *memoryPersistence) Save(_ context.Context, link radar.Link, expected radar.LinkVersion, _ int64, _ time.Time) (radar.Link, error) {
	current, ok := memory.links[link.ID]
	if !ok {
		return radar.Link{}, radarport.ErrNotFound
	}
	if current.Version != expected {
		return radar.Link{}, radarport.ErrConflict
	}
	memory.links[link.ID] = link
	return link, nil
}

func (memory *memoryPersistence) ReserveOperation(_ context.Context, receipt radarport.OperationReceipt, _ time.Time) (radarport.OperationReceipt, bool, error) {
	key := receipt.Operation + ":" + string(rune(receipt.ActorID)) + ":" + hex.EncodeToString(receipt.KeyDigest[:])
	if stored, ok := memory.receipts[key]; ok {
		return stored, true, nil
	}
	receipt.ID = memory.nextRecID
	memory.nextRecID++
	memory.receipts[key] = receipt
	return receipt, false, nil
}

func (memory *memoryPersistence) CompleteOperation(_ context.Context, receiptID int64, radarID radar.RadarID, version radar.LinkVersion, now time.Time) error {
	for key, receipt := range memory.receipts {
		if receipt.ID == receiptID {
			receipt.State, receipt.RadarID, receipt.Version, receipt.CompletedAt = radarport.OperationCompleted, radarID, version, &now
			memory.receipts[key] = receipt
			return nil
		}
	}
	return radarport.ErrNotFound
}

func (memory *memoryPersistence) AppendAudit(_ context.Context, record radarport.AuditRecord) error {
	if memory.failAudit {
		return errors.New("injected audit failure")
	}
	memory.audits = append(memory.audits, record)
	return nil
}

func (memory *memoryPersistence) AppendOutbox(_ context.Context, record radarport.OutboxRecord) error {
	memory.outbox = append(memory.outbox, record)
	return nil
}

func (memory *memoryPersistence) clone() *memoryPersistence {
	copy := *memory
	copy.links = make(map[radar.RadarID]radar.Link, len(memory.links))
	for key, value := range memory.links {
		copy.links[key] = value
	}
	copy.receipts = make(map[string]radarport.OperationReceipt, len(memory.receipts))
	for key, value := range memory.receipts {
		copy.receipts[key] = value
	}
	copy.audits = append([]radarport.AuditRecord(nil), memory.audits...)
	copy.outbox = append([]radarport.OutboxRecord(nil), memory.outbox...)
	return &copy
}

func TestExternalLinkMappingsUseRadarOwnedKeysetProjection(t *testing.T) {
	memory := newMemoryPersistence()
	memory.externalPage = radarport.ExternalLinkMappingPage{Items: []radarport.ExternalLinkMapping{{RadarID: 9, RadarCode: "rd_1234567890abcdef", Title: "Disabled historical mapping"}}, Total: 3, HasMore: true}
	service := newTestService(t, memory)
	page, err := service.ExternalLinkMappings(context.Background(), radarport.ExternalLinkMappingQuery{RadarCode: "  rd_1234567890abcdef  ", BeforeRadarID: 10})
	if err != nil {
		t.Fatal(err)
	}
	if memory.externalQuery.RadarCode != "rd_1234567890abcdef" || memory.externalQuery.BeforeRadarID != 10 || memory.externalQuery.Limit != 100 || page.Total != 3 || !page.HasMore || len(page.Items) != 1 || page.Items[0].RadarID != 9 {
		t.Fatalf("query=%+v page=%+v", memory.externalQuery, page)
	}
	if _, err = service.ExternalLinkMappings(context.Background(), radarport.ExternalLinkMappingQuery{Limit: 501}); !errors.Is(err, radar.ErrInvalidArgument) {
		t.Fatalf("limit error=%v", err)
	}
	if _, err = service.ExternalLinkMappings(context.Background(), radarport.ExternalLinkMappingQuery{BeforeRadarID: -1}); !errors.Is(err, radar.ErrInvalidArgument) {
		t.Fatalf("cursor error=%v", err)
	}
}
