// Package app coordinates Radar-owned use cases.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

const (
	minimumIdempotencyKeyBytes = 16
	maximumIdempotencyKeyBytes = 128
)

type Service struct {
	uow        platformport.UnitOfWork
	repository radarport.Repository
	journal    radarport.MutationJournal
	now        func() time.Time
	code       func() (radar.PublicCode, error)
	media      radarport.MediaReferenceValidator
}

var _ radarport.Manager = (*Service)(nil)

func NewService(uow platformport.UnitOfWork, repository radarport.Repository, journal radarport.MutationJournal) (*Service, error) {
	if uow == nil || repository == nil || journal == nil {
		return nil, radarport.ErrUnavailable
	}
	return &Service{uow: uow, repository: repository, journal: journal, now: time.Now, code: randomPublicCode}, nil
}

func (service *Service) BindMediaValidator(validator radarport.MediaReferenceValidator) error {
	if service == nil || validator == nil {
		return radarport.ErrUnavailable
	}
	service.media = validator
	return nil
}

func (service *Service) validateMedia(ctx context.Context, content radar.Content) error {
	if content.Type == radar.ContentTypeLink {
		return nil
	}
	if service.media == nil {
		return radarport.ErrUnavailable
	}
	return service.media.ValidateRadarMedia(ctx, content.Type, content.MediaID)
}

func (service *Service) List(ctx context.Context, query radarport.ListQuery) (radarport.LinkPage, error) {
	if service == nil || service.uow == nil || service.repository == nil {
		return radarport.LinkPage{}, radarport.ErrUnavailable
	}
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > 200 || query.ContentType != "" && !query.ContentType.Valid() || query.Status != "" && !query.Status.Valid() || query.AuthPolicy != "" && !query.AuthPolicy.Valid() {
		return radarport.LinkPage{}, radar.ErrInvalidArgument
	}
	if query.Limit == 0 {
		query.Limit = radarport.DefaultLimit
	}
	if query.Limit < 1 || query.Limit > radarport.MaximumLimit || query.Offset < 0 || query.Offset > 1_000_000 || query.CreatedAfter != nil && query.CreatedBefore != nil && !query.CreatedAfter.Before(*query.CreatedBefore) {
		return radarport.LinkPage{}, radar.ErrInvalidArgument
	}
	var page radarport.LinkPage
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = service.repository.List(tx, query)
		return readErr
	})
	if err != nil {
		return radarport.LinkPage{}, classify(err)
	}
	return page, nil
}

func (service *Service) Get(ctx context.Context, id radar.RadarID) (radarport.LinkDetail, error) {
	if !id.Valid() || service == nil || service.uow == nil || service.repository == nil {
		return radarport.LinkDetail{}, radar.ErrInvalidArgument
	}
	var link radar.Link
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		link, readErr = service.repository.Get(tx, id)
		return readErr
	})
	if err != nil {
		return radarport.LinkDetail{}, classify(err)
	}
	return detail(link)
}

func (service *Service) Create(ctx context.Context, command radarport.CreateCommand) (radarport.LinkDetail, error) {
	if service == nil || service.uow == nil || service.repository == nil || service.journal == nil || command.ActorID < 1 || !validIdempotencyKey(command.IdempotencyKey) {
		return radarport.LinkDetail{}, radar.ErrInvalidArgument
	}
	now := service.nowUTC()
	code, err := service.code()
	if err != nil {
		return radarport.LinkDetail{}, radarport.ErrUnavailable
	}
	if _, err = radar.NewDraft(1, code, command.Name, command.Title, command.Description, command.Content, command.AuthPolicy, now); err != nil {
		return radarport.LinkDetail{}, err
	}
	payloadDigest, err := digest(struct {
		Name        string           `json:"name"`
		Title       string           `json:"title"`
		Description string           `json:"description"`
		Content     radar.Content    `json:"content"`
		AuthPolicy  radar.AuthPolicy `json:"auth_policy"`
	}{command.Name, command.Title, command.Description, command.Content, command.AuthPolicy})
	if err != nil {
		return radarport.LinkDetail{}, radarport.ErrUnavailable
	}
	var result radarport.LinkDetail
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, reserveErr := service.reserve(tx, "create", command.ActorID, command.IdempotencyKey, payloadDigest, now)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			return service.replay(tx, receipt, payloadDigest, &result)
		}
		if validateErr := service.validateMedia(tx, command.Content); validateErr != nil {
			return validateErr
		}
		created, createErr := service.repository.Create(tx, radarport.CreateRecord{PublicCode: code, Name: command.Name, Title: command.Title, Description: command.Description, Content: command.Content, AuthPolicy: command.AuthPolicy}, command.ActorID, now)
		if createErr != nil {
			return createErr
		}
		result, createErr = detail(created)
		if createErr != nil {
			return createErr
		}
		return service.finish(tx, receipt.ID, "created", created, command.ActorID, command.IdempotencyKey, payloadDigest, now)
	})
	if err != nil {
		return radarport.LinkDetail{}, classify(err)
	}
	return result, nil
}

func (service *Service) Update(ctx context.Context, command radarport.UpdateCommand) (radarport.LinkDetail, error) {
	if service == nil || !command.RadarID.Valid() || !command.Expected.Valid() || command.ActorID < 1 || !validIdempotencyKey(command.IdempotencyKey) {
		return radarport.LinkDetail{}, radar.ErrInvalidArgument
	}
	payloadDigest, err := digest(struct {
		RadarID  radar.RadarID     `json:"radar_id"`
		Expected radar.LinkVersion `json:"expected_version"`
		Revision radar.Revision    `json:"revision"`
	}{command.RadarID, command.Expected, command.Revision})
	if err != nil {
		return radarport.LinkDetail{}, radarport.ErrUnavailable
	}
	now := service.nowUTC()
	var result radarport.LinkDetail
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, reserveErr := service.reserve(tx, "update", command.ActorID, command.IdempotencyKey, payloadDigest, now)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			return service.replay(tx, receipt, payloadDigest, &result)
		}
		if validateErr := service.validateMedia(tx, command.Revision.Content); validateErr != nil {
			return validateErr
		}
		current, readErr := service.repository.Get(tx, command.RadarID)
		if readErr != nil {
			return readErr
		}
		updated, reviseErr := current.Revise(command.Expected, command.Revision, now)
		if reviseErr != nil {
			return reviseErr
		}
		updated, saveErr := service.repository.Save(tx, updated, command.Expected, command.ActorID, now)
		if saveErr != nil {
			return saveErr
		}
		result, saveErr = detail(updated)
		if saveErr != nil {
			return saveErr
		}
		return service.finish(tx, receipt.ID, "updated", updated, command.ActorID, command.IdempotencyKey, payloadDigest, now)
	})
	if err != nil {
		return radarport.LinkDetail{}, classify(err)
	}
	return result, nil
}

func (service *Service) SetStatus(ctx context.Context, command radarport.SetStatusCommand) (radarport.LinkDetail, error) {
	if service == nil || !command.RadarID.Valid() || !command.Expected.Valid() || command.ActorID < 1 || !validIdempotencyKey(command.IdempotencyKey) || !command.Target.Valid() || command.Target == radar.StatusDraft {
		return radarport.LinkDetail{}, radar.ErrInvalidArgument
	}
	operation := "enable"
	if command.Target == radar.StatusDisabled {
		operation = "disable"
	}
	payloadDigest, err := digest(struct {
		RadarID  radar.RadarID     `json:"radar_id"`
		Expected radar.LinkVersion `json:"expected_version"`
		Target   radar.Status      `json:"target"`
	}{command.RadarID, command.Expected, command.Target})
	if err != nil {
		return radarport.LinkDetail{}, radarport.ErrUnavailable
	}
	now := service.nowUTC()
	var result radarport.LinkDetail
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, replay, reserveErr := service.reserve(tx, operation, command.ActorID, command.IdempotencyKey, payloadDigest, now)
		if reserveErr != nil {
			return reserveErr
		}
		if replay {
			return service.replay(tx, receipt, payloadDigest, &result)
		}
		current, readErr := service.repository.Get(tx, command.RadarID)
		if readErr != nil {
			return readErr
		}
		updated, changed, transitionErr := current.Transition(command.Expected, command.Target, now)
		if transitionErr != nil {
			return transitionErr
		}
		if changed {
			updated, transitionErr = service.repository.Save(tx, updated, command.Expected, command.ActorID, now)
			if transitionErr != nil {
				return transitionErr
			}
		}
		result, transitionErr = detail(updated)
		if transitionErr != nil {
			return transitionErr
		}
		return service.finish(tx, receipt.ID, operation+"d", updated, command.ActorID, command.IdempotencyKey, payloadDigest, now)
	})
	if err != nil {
		return radarport.LinkDetail{}, classify(err)
	}
	return result, nil
}

func (service *Service) reserve(ctx context.Context, operation string, actor int64, key string, payload [32]byte, now time.Time) (radarport.OperationReceipt, bool, error) {
	keyDigest := sha256.Sum256([]byte(key))
	return service.journal.ReserveOperation(ctx, radarport.OperationReceipt{Operation: operation, ActorID: actor, KeyDigest: keyDigest, PayloadDigest: payload, State: radarport.OperationInProgress}, now)
}

func (service *Service) replay(ctx context.Context, receipt radarport.OperationReceipt, payload [32]byte, result *radarport.LinkDetail) error {
	if receipt.PayloadDigest != payload {
		return radarport.ErrIdempotencyConflict
	}
	if receipt.State != radarport.OperationCompleted || !receipt.RadarID.Valid() {
		return radarport.ErrConflict
	}
	link, err := service.repository.Get(ctx, receipt.RadarID)
	if err != nil {
		return err
	}
	*result, err = detail(link)
	return err
}

func (service *Service) finish(ctx context.Context, receiptID int64, eventSuffix string, link radar.Link, actor int64, key string, payload [32]byte, now time.Time) error {
	keyDigest := sha256.Sum256([]byte(key))
	if err := service.journal.AppendAudit(ctx, radarport.AuditRecord{Operation: eventSuffix, RadarID: link.ID, Version: link.Version, ActorID: actor, PayloadDigest: payload, OccurredAt: now}); err != nil {
		return err
	}
	outboxPayload, err := json.Marshal(map[string]any{"radar_id": link.ID, "version": link.Version, "status": link.Status, "content_type": link.Content.Type})
	if err != nil {
		return radarport.ErrUnavailable
	}
	if err = service.journal.AppendOutbox(ctx, radarport.OutboxRecord{EventID: "radar:" + eventSuffix + ":" + hex.EncodeToString(keyDigest[:16]), EventType: "radar.link_" + eventSuffix, AggregateID: link.ID, AggregateVer: link.Version, Payload: outboxPayload, IdempotencyDigest: keyDigest, OccurredAt: now}); err != nil {
		return err
	}
	return service.journal.CompleteOperation(ctx, receiptID, link.ID, link.Version, now)
}

func detail(link radar.Link) (radarport.LinkDetail, error) {
	if err := link.Validate(); err != nil {
		return radarport.LinkDetail{}, radarport.ErrUnavailable
	}
	return radarport.LinkDetail{Link: link}, nil
}

func digest(value any) ([32]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func validIdempotencyKey(value string) bool {
	if len(value) < minimumIdempotencyKeyBytes || len(value) > maximumIdempotencyKeyBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e || character == ',' {
			return false
		}
	}
	return true
}

func randomPublicCode() (radar.PublicCode, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return radar.PublicCode("rd_" + base64.RawURLEncoding.EncodeToString(raw)), nil
}

func (service *Service) nowUTC() time.Time {
	if service == nil || service.now == nil {
		return time.Time{}
	}
	return service.now().UTC()
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, radar.ErrInvalidArgument), errors.Is(err, radar.ErrInvalidStatus), errors.Is(err, radar.ErrInvalidTransition), errors.Is(err, radar.ErrVersionConflict), errors.Is(err, radarport.ErrNotFound), errors.Is(err, radarport.ErrGone), errors.Is(err, radarport.ErrConflict), errors.Is(err, radarport.ErrIdempotencyConflict), errors.Is(err, radarport.ErrVisitorSearchTooWide):
		return err
	default:
		return radarport.ErrUnavailable
	}
}
