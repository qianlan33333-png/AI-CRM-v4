package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type contentDeliveryTestUOW struct{}

func (contentDeliveryTestUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type contentDeliveryTestStore struct {
	receipts map[string]mediaport.ContentDeliveryMutationReceipt
	eligible map[string]bool
	creates  int
}

func (s *contentDeliveryTestStore) key(operation string, actor int64, digest [32]byte) string {
	return fmt.Sprintf("%s:%d:%x", operation, actor, digest)
}
func (s *contentDeliveryTestStore) ReserveMutation(_ context.Context, x mediaport.ContentDeliveryMutationReservation) (mediaport.ContentDeliveryMutationReceipt, bool, error) {
	key := s.key(x.Operation, x.Actor, x.KeyDigest)
	if receipt, ok := s.receipts[key]; ok {
		return receipt, false, nil
	}
	receipt := mediaport.ContentDeliveryMutationReceipt{ID: int64(len(s.receipts) + 1), Operation: x.Operation, Actor: x.Actor, KeyDigest: x.KeyDigest, PayloadDigest: x.PayloadDigest}
	s.receipts[key] = receipt
	return receipt, true, nil
}
func (s *contentDeliveryTestStore) CompleteMutation(_ context.Context, id int64, snapshot json.RawMessage) (mediaport.ContentDeliveryMutationReceipt, error) {
	for key, receipt := range s.receipts {
		if receipt.ID == id {
			receipt.ResultSnapshot = append(json.RawMessage(nil), snapshot...)
			s.receipts[key] = receipt
			return receipt, nil
		}
	}
	return mediaport.ContentDeliveryMutationReceipt{}, errors.New("missing receipt")
}
func (s *contentDeliveryTestStore) Eligible(_ context.Context, kind string, id int64) (bool, error) {
	return s.eligible[fmt.Sprintf("%s:%d", kind, id)], nil
}
func (s *contentDeliveryTestStore) Create(_ context.Context, c mediaport.ContentPackageCommand, _ time.Time) (mediaport.ContentPackage, error) {
	s.creates++
	return mediaport.ContentPackage{ID: 1, Name: c.Name, ContentText: c.ContentText, Enabled: c.Enabled, Version: 1, Refs: append([]mediaport.ContentRef(nil), c.Refs...)}, nil
}
func (*contentDeliveryTestStore) Update(context.Context, mediaport.ContentPackageUpdateCommand, time.Time) (mediaport.ContentPackage, error) {
	return mediaport.ContentPackage{}, errors.New("not used")
}
func (*contentDeliveryTestStore) Bind(context.Context, mediaport.DeliveryBindingCommand, time.Time) (mediaport.DeliveryBinding, error) {
	return mediaport.DeliveryBinding{}, errors.New("not used")
}
func (*contentDeliveryTestStore) GetBinding(context.Context, string, string) (mediaport.DeliveryBinding, error) {
	return mediaport.DeliveryBinding{}, errors.New("not used")
}
func (*contentDeliveryTestStore) Initiate(context.Context, mediaport.AttachmentUploadInitiateCommand, [32]byte, time.Time) (int64, error) {
	return 0, errors.New("not used")
}
func (*contentDeliveryTestStore) PutPart(context.Context, mediaport.AttachmentUploadPartCommand, [32]byte, time.Time) (bool, error) {
	return false, errors.New("not used")
}
func (*contentDeliveryTestStore) Complete(context.Context, mediaport.AttachmentUploadCompleteCommand, time.Time) (int64, error) {
	return 0, errors.New("not used")
}

func TestContentDeliveryCreateReplaysAndRejectsInvalidReferences(t *testing.T) {
	store := &contentDeliveryTestStore{receipts: map[string]mediaport.ContentDeliveryMutationReceipt{}, eligible: map[string]bool{"image:7": true}}
	service := NewContentDeliveryService(contentDeliveryTestUOW{}, store)
	service.now = func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	command := mediaport.ContentPackageCommand{Name: "内容包", ContentText: "正文", Enabled: true, Refs: []mediaport.ContentRef{{Kind: "image", ID: 7}}, Actor: 9, IdempotencyKey: "content-delivery-create-0001"}
	created, err := service.Create(context.Background(), command)
	if err != nil || created.ID != 1 || store.creates != 1 {
		t.Fatalf("created=%+v err=%v creates=%d", created, err, store.creates)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != created.ID || store.creates != 1 {
		t.Fatalf("replay=%+v err=%v creates=%d", replay, err, store.creates)
	}
	drift := command
	drift.ContentText = "changed"
	if _, err = service.Create(context.Background(), drift); !errors.Is(err, ErrContentDeliveryConflict) {
		t.Fatalf("payload drift=%v", err)
	}
	invalid := command
	invalid.IdempotencyKey = "content-delivery-create-0002"
	invalid.Refs = []mediaport.ContentRef{{Kind: "image", ID: 7}, {Kind: "image", ID: 7}}
	if _, err = service.Preview(context.Background(), invalid); !errors.Is(err, ErrContentDeliveryInvalid) {
		t.Fatalf("duplicate refs=%v", err)
	}
	disabled := command
	disabled.IdempotencyKey = "content-delivery-create-0003"
	disabled.Refs = []mediaport.ContentRef{{Kind: "image", ID: 8}}
	if _, err = service.Preview(context.Background(), disabled); !errors.Is(err, ErrContentDeliveryInvalid) {
		t.Fatalf("disabled reference=%v", err)
	}
}
