package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	ErrContentDeliveryInvalid     = errors.New("invalid media content delivery command")
	ErrContentDeliveryConflict    = errors.New("media content delivery conflict")
	ErrContentDeliveryUnavailable = errors.New("media content delivery unavailable")
)

type ContentDeliveryStore interface {
	ReserveMutation(context.Context, mediaport.ContentDeliveryMutationReservation) (mediaport.ContentDeliveryMutationReceipt, bool, error)
	CompleteMutation(context.Context, int64, json.RawMessage) (mediaport.ContentDeliveryMutationReceipt, error)
	Eligible(context.Context, string, int64) (bool, error)
	Create(context.Context, mediaport.ContentPackageCommand, time.Time) (mediaport.ContentPackage, error)
	Update(context.Context, mediaport.ContentPackageUpdateCommand, time.Time) (mediaport.ContentPackage, error)
	Bind(context.Context, mediaport.DeliveryBindingCommand, time.Time) (mediaport.DeliveryBinding, error)
	GetBinding(context.Context, string, string) (mediaport.DeliveryBinding, error)
	Initiate(context.Context, mediaport.AttachmentUploadInitiateCommand, [32]byte, time.Time) (int64, error)
	PutPart(context.Context, mediaport.AttachmentUploadPartCommand, [32]byte, time.Time) (bool, error)
	Complete(context.Context, mediaport.AttachmentUploadCompleteCommand, time.Time) (int64, error)
}
type ContentDeliveryMutationReceipt = mediaport.ContentDeliveryMutationReceipt
type ContentDeliveryMutationReservation = mediaport.ContentDeliveryMutationReservation
type ContentDelivery struct {
	uow   platformport.UnitOfWork
	store ContentDeliveryStore
	now   func() time.Time
}

var _ mediaport.ContentDeliveryService = (*ContentDelivery)(nil)

func NewContentDeliveryService(uow platformport.UnitOfWork, store ContentDeliveryStore) *ContentDelivery {
	return &ContentDelivery{uow: uow, store: store, now: time.Now}
}
func (s *ContentDelivery) Preview(ctx context.Context, c mediaport.ContentPackageCommand) (mediaport.ContentPackage, error) {
	if !validContent(c) || s == nil || s.uow == nil || s.store == nil || ctx == nil {
		return mediaport.ContentPackage{}, ErrContentDeliveryInvalid
	}
	if err := s.uow.Within(ctx, func(tx context.Context) error { return s.validateRefs(tx, c.Refs) }); err != nil {
		return mediaport.ContentPackage{}, ErrContentDeliveryInvalid
	}
	return contentDeliveryPreview(c), nil
}
func (s *ContentDelivery) validateRefs(ctx context.Context, refs []mediaport.ContentRef) error {
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		if r.ID < 1 || (r.Kind != "image" && r.Kind != "attachment" && r.Kind != "miniprogram" && r.Kind != "group_invite") {
			return ErrContentDeliveryInvalid
		}
		key := r.Kind + ":" + strconv.FormatInt(r.ID, 10)
		if _, exists := seen[key]; exists {
			return ErrContentDeliveryInvalid
		}
		seen[key] = struct{}{}
		ok, e := s.store.Eligible(ctx, r.Kind, r.ID)
		if e != nil || !ok {
			return ErrContentDeliveryInvalid
		}
	}
	return nil
}
func contentDeliveryPreview(c mediaport.ContentPackageCommand) mediaport.ContentPackage {
	return mediaport.ContentPackage{Name: strings.TrimSpace(c.Name), ContentText: strings.TrimSpace(c.ContentText), Enabled: c.Enabled, Refs: append([]mediaport.ContentRef(nil), c.Refs...)}
}
func (s *ContentDelivery) Create(ctx context.Context, c mediaport.ContentPackageCommand) (out mediaport.ContentPackage, err error) {
	if !validContent(c) || s == nil || s.uow == nil || s.store == nil || ctx == nil || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return out, ErrContentDeliveryInvalid
	}
	payload := c
	payload.IdempotencyKey = ""
	reservation, err := contentDeliveryReservation("create", c.Actor, c.IdempotencyKey, payload, s.now().UTC())
	if err != nil {
		return out, ErrContentDeliveryInvalid
	}
	out, err = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (mediaport.ContentPackage, error) {
		if validateErr := s.validateRefs(tx, c.Refs); validateErr != nil {
			return mediaport.ContentPackage{}, validateErr
		}
		return s.store.Create(tx, c, reservation.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, ErrContentDeliveryInvalid) {
			return out, ErrContentDeliveryInvalid
		}
		if errors.Is(err, ErrContentDeliveryConflict) {
			return out, ErrContentDeliveryConflict
		}
		return out, ErrContentDeliveryUnavailable
	}
	return out, nil
}
func (s *ContentDelivery) Update(ctx context.Context, c mediaport.ContentPackageUpdateCommand) (out mediaport.ContentPackage, err error) {
	if c.ID < 1 || c.ExpectedVersion < 1 || !validContent(c.ContentPackageCommand) || s == nil || s.uow == nil || s.store == nil || ctx == nil || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return out, ErrContentDeliveryInvalid
	}
	payload := c
	payload.IdempotencyKey = ""
	reservation, err := contentDeliveryReservation("update", c.Actor, c.IdempotencyKey, payload, s.now().UTC())
	if err != nil {
		return out, ErrContentDeliveryInvalid
	}
	out, err = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (mediaport.ContentPackage, error) {
		if validateErr := s.validateRefs(tx, c.Refs); validateErr != nil {
			return mediaport.ContentPackage{}, validateErr
		}
		return s.store.Update(tx, c, reservation.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, ErrContentDeliveryInvalid) {
			return out, ErrContentDeliveryInvalid
		}
		return out, ErrContentDeliveryConflict
	}
	return out, nil
}
func (s *ContentDelivery) Bind(ctx context.Context, c mediaport.DeliveryBindingCommand) (out mediaport.DeliveryBinding, err error) {
	if s == nil || s.uow == nil || s.store == nil || c.Actor < 1 || c.PackageID < 1 || c.GroupInviteID < 1 || strings.TrimSpace(c.CampaignCode) != c.CampaignCode || strings.TrimSpace(c.PlanID) != c.PlanID || c.CampaignCode == "" || c.PlanID == "" || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return out, ErrContentDeliveryInvalid
	}
	payload := c
	payload.IdempotencyKey = ""
	reservation, err := contentDeliveryReservation("bind", c.Actor, c.IdempotencyKey, payload, s.now().UTC())
	if err != nil {
		return out, ErrContentDeliveryInvalid
	}
	out, err = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (mediaport.DeliveryBinding, error) {
		return s.store.Bind(tx, c, reservation.CreatedAt)
	})
	if err != nil {
		return out, ErrContentDeliveryConflict
	}
	return out, nil
}
func (s *ContentDelivery) GetBinding(ctx context.Context, campaignCode, planID string) (out mediaport.DeliveryBinding, err error) {
	if s == nil || s.uow == nil || s.store == nil || campaignCode == "" || planID == "" {
		return out, ErrContentDeliveryInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error { out, err = s.store.GetBinding(tx, campaignCode, planID); return err })
	if err != nil {
		return out, ErrContentDeliveryUnavailable
	}
	return out, nil
}
func (s *ContentDelivery) InitiatePDF(ctx context.Context, c mediaport.AttachmentUploadInitiateCommand) (id int64, err error) {
	d, e := digest(c.SHA256)
	if e != nil || c.Size < 1 || c.Size > 10<<20 || c.Actor < 1 || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return 0, ErrContentDeliveryInvalid
	}
	payload := c
	payload.IdempotencyKey = ""
	reservation, e := contentDeliveryReservation("upload_initiate", c.Actor, c.IdempotencyKey, payload, s.now().UTC())
	if e != nil {
		return 0, ErrContentDeliveryInvalid
	}
	id, err = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (int64, error) { return s.store.Initiate(tx, c, d, reservation.CreatedAt) })
	if err != nil {
		if errors.Is(err, ErrContentDeliveryConflict) {
			return 0, ErrContentDeliveryConflict
		}
		return 0, ErrContentDeliveryUnavailable
	}
	return id, nil
}
func (s *ContentDelivery) PutPDFPart(ctx context.Context, c mediaport.AttachmentUploadPartCommand) error {
	d, e := digest(c.SHA256)
	if e != nil || c.UploadID < 1 || c.PartNumber < 1 || c.Actor < 1 || len(c.Content) < 1 || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return ErrContentDeliveryInvalid
	}
	actual := sha256.Sum256(c.Content)
	if actual != d {
		return ErrContentDeliveryInvalid
	}
	reservation, e := contentDeliveryReservation("upload_part", c.Actor, c.IdempotencyKey, struct {
		UploadID   int64
		PartNumber int32
		SHA256     string
	}{c.UploadID, c.PartNumber, c.SHA256}, s.now().UTC())
	if e != nil {
		return ErrContentDeliveryInvalid
	}
	_, e = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (int64, error) {
		ok, e := s.store.PutPart(tx, c, d, reservation.CreatedAt)
		if e != nil || !ok {
			return 0, ErrContentDeliveryConflict
		}
		return c.UploadID, nil
	})
	return e
}
func (s *ContentDelivery) CompletePDF(ctx context.Context, c mediaport.AttachmentUploadCompleteCommand) (id int64, err error) {
	if c.UploadID < 1 || c.Actor < 1 || !validContentDeliveryIdempotencyKey(c.IdempotencyKey) {
		return 0, ErrContentDeliveryInvalid
	}
	payload := c
	payload.IdempotencyKey = ""
	reservation, err := contentDeliveryReservation("upload_complete", c.Actor, c.IdempotencyKey, payload, s.now().UTC())
	if err != nil {
		return 0, ErrContentDeliveryInvalid
	}
	id, err = runContentDeliveryMutation(ctx, s, reservation, func(tx context.Context) (int64, error) { return s.store.Complete(tx, c, reservation.CreatedAt) })
	if err != nil {
		if errors.Is(err, ErrContentDeliveryConflict) {
			return 0, ErrContentDeliveryConflict
		}
		return 0, ErrContentDeliveryUnavailable
	}
	return id, nil
}

func contentDeliveryReservation(operation string, actor int64, key string, payload any, createdAt time.Time) (ContentDeliveryMutationReservation, error) {
	if operation == "" || actor < 1 || !validContentDeliveryIdempotencyKey(key) || createdAt.IsZero() {
		return ContentDeliveryMutationReservation{}, ErrContentDeliveryInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ContentDeliveryMutationReservation{}, err
	}
	return ContentDeliveryMutationReservation{Operation: operation, Actor: actor, KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(raw), CreatedAt: createdAt}, nil
}

func runContentDeliveryMutation[T any](ctx context.Context, service *ContentDelivery, reservation ContentDeliveryMutationReservation, mutate func(context.Context) (T, error)) (out T, err error) {
	if service == nil || service.uow == nil || service.store == nil || mutate == nil {
		return out, ErrContentDeliveryUnavailable
	}
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveMutation(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if receipt.ID < 1 || receipt.Operation != reservation.Operation || receipt.Actor != reservation.Actor || subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) != 1 || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrContentDeliveryConflict
		}
		if !owned {
			if len(receipt.ResultSnapshot) == 0 || bytes.Equal(receipt.ResultSnapshot, []byte("null")) || json.Unmarshal(receipt.ResultSnapshot, &out) != nil {
				return ErrContentDeliveryUnavailable
			}
			return nil
		}
		out, reserveErr = mutate(tx)
		if reserveErr != nil {
			return reserveErr
		}
		snapshot, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return marshalErr
		}
		completed, completeErr := service.store.CompleteMutation(tx, receipt.ID, snapshot)
		if completeErr != nil || completed.ID != receipt.ID || completed.Operation != receipt.Operation || completed.Actor != receipt.Actor || subtle.ConstantTimeCompare(completed.KeyDigest[:], receipt.KeyDigest[:]) != 1 || subtle.ConstantTimeCompare(completed.PayloadDigest[:], receipt.PayloadDigest[:]) != 1 || !contentDeliveryJSONEqual(snapshot, completed.ResultSnapshot) {
			return ErrContentDeliveryUnavailable
		}
		return nil
	})
	return out, err
}

func contentDeliveryJSONEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func validContentDeliveryIdempotencyKey(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && strings.TrimSpace(value) == value
}
func validContent(c mediaport.ContentPackageCommand) bool {
	return c.Actor > 0 && strings.TrimSpace(c.Name) != "" && strings.TrimSpace(c.Name) == c.Name && len(c.ContentText) <= 10000 && len(c.Refs) <= 100 && (strings.TrimSpace(c.ContentText) != "" || len(c.Refs) > 0)
}
func digest(v string) ([32]byte, error) {
	var r [32]byte
	if !strings.HasPrefix(v, "sha256:") {
		return r, errors.New("digest")
	}
	b, e := hex.DecodeString(strings.TrimPrefix(v, "sha256:"))
	if e != nil || len(b) != 32 {
		return r, errors.New("digest")
	}
	copy(r[:], b)
	return r, nil
}
