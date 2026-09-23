package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	maximumAttachmentNameRunes        = 200
	maximumAttachmentDescriptionRunes = 10_000
	maximumAttachmentTags             = 50
	maximumAttachmentTagRunes         = 64
	minimumAttachmentIdempotencyKey   = 16
	maximumAttachmentIdempotencyKey   = 128
)

var (
	ErrInvalidAttachment       = errors.New("invalid attachment")
	ErrAttachmentNotFound      = errors.New("attachment not found")
	ErrAttachmentConflict      = errors.New("attachment command conflict")
	ErrAttachmentHasReferences = errors.New("attachment has references")
	ErrAttachmentUnavailable   = errors.New("attachment service unavailable")
)

type AttachmentMutationReceipt struct {
	ID                                        int64
	Operation, ActorScope, BusinessKey, State string
	KeyDigest, PayloadDigest                  [32]byte
	ResultSnapshot                            json.RawMessage
}

type AttachmentMutationReservation struct {
	Operation, ActorScope, BusinessKey string
	KeyDigest, PayloadDigest           [32]byte
	CreatedAt                          time.Time
}

type AttachmentCreateInput struct {
	Command   mediaport.AttachmentUploadCommand
	MediaType string
	Checksum  [32]byte
	Now       time.Time
}

type AttachmentUpdateInput struct {
	Attachment      mediaport.Attachment
	ExpectedVersion int64
}

type AttachmentBlob struct {
	Attachment mediaport.Attachment
	Content    []byte
	Checksum   [32]byte
}

type AttachmentListRead struct {
	Total int64
	Items []mediaport.Attachment
}

// AttachmentStore is Media-owned. It runs only inside the caller's UoW, so a
// blob, its metadata, mutation receipt, and event can commit or roll back as
// one local transaction.
type AttachmentStore interface {
	ReserveAttachmentMutation(context.Context, AttachmentMutationReservation) (AttachmentMutationReceipt, bool, error)
	CreateAttachment(context.Context, AttachmentCreateInput) (mediaport.Attachment, error)
	GetAttachment(context.Context, int64) (mediaport.Attachment, error)
	GetAttachmentForUpdate(context.Context, int64) (mediaport.Attachment, error)
	ListAttachments(context.Context, mediaport.AttachmentListQuery) (AttachmentListRead, error)
	ReadAttachment(context.Context, int64) (AttachmentBlob, error)
	UpdateAttachment(context.Context, AttachmentUpdateInput) (mediaport.Attachment, error)
	DeleteAttachment(context.Context, int64) (int64, error)
	CompleteAttachmentMutation(context.Context, int64, json.RawMessage, time.Time) (AttachmentMutationReceipt, error)
}

type AttachmentDeleteReferences struct {
	AutomationAgents []int64 `json:"automation_agents"`
	Channels         []int64 `json:"channels"`
	RadarLinks       []int64 `json:"radar_links"`
}

func (references AttachmentDeleteReferences) Any() bool {
	return len(references.AutomationAgents) != 0 || len(references.Channels) != 0 || len(references.RadarLinks) != 0
}

type AttachmentDeleteResult struct {
	ID          int64                      `json:"id"`
	Deleted     bool                       `json:"deleted"`
	HardDeleted bool                       `json:"hard_deleted"`
	References  AttachmentDeleteReferences `json:"references"`
}

type AttachmentDownload struct {
	Attachment mediaport.Attachment
	Content    []byte
}

type AttachmentService struct {
	uow        platformport.UnitOfWork
	store      AttachmentStore
	automation mediaport.AutomationAttachmentReferenceReader
	contact    mediaport.ChannelAttachmentDeletionReferenceReader
	radar      mediaport.RadarAttachmentReferenceReader
	events     mediaport.EventAppender
	now        func() time.Time
}

func NewAttachmentService(uow platformport.UnitOfWork, store AttachmentStore, events mediaport.EventAppender) *AttachmentService {
	return &AttachmentService{uow: uow, store: store, events: events, now: time.Now}
}

func NewAttachmentServiceWithReferences(uow platformport.UnitOfWork, store AttachmentStore, automation mediaport.AutomationAttachmentReferenceReader, contact mediaport.ChannelAttachmentDeletionReferenceReader, radar mediaport.RadarAttachmentReferenceReader, events mediaport.EventAppender) *AttachmentService {
	return &AttachmentService{uow: uow, store: store, automation: automation, contact: contact, radar: radar, events: events, now: time.Now}
}

func (service *AttachmentService) Upload(ctx context.Context, command mediaport.AttachmentUploadCommand) (mediaport.Attachment, error) {
	normalized, inspection, err := normalizeAttachmentUpload(command)
	if err != nil {
		return mediaport.Attachment{}, err
	}
	if !attachmentReady(service) || ctx == nil {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	checksum := sha256.Sum256(normalized.Content)
	payload, err := json.Marshal(struct {
		FileName, MediaType, Name, Description string
		Tags                                   []string
		Size                                   int64
		Checksum                               string
		Enabled                                bool
	}{normalized.FileName, inspection.MediaType, normalized.Name, normalized.Description, normalized.Tags, int64(len(normalized.Content)), hex.EncodeToString(checksum[:]), attachmentEnabled(normalized.Enabled)})
	if err != nil {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	reservation := AttachmentMutationReservation{
		Operation: "upload", ActorScope: attachmentActorScope(normalized.Actor), BusinessKey: "upload",
		KeyDigest: sha256.Sum256([]byte(normalized.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result mediaport.Attachment
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveAttachmentMutation(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validAttachmentReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrAttachmentConflict
		}
		if !owned {
			return replayAttachmentReceipt(receipt, reservation, &result)
		}
		result, reserveErr = service.store.CreateAttachment(tx, AttachmentCreateInput{Command: normalized, MediaType: inspection.MediaType, Checksum: checksum, Now: now})
		if reserveErr != nil || !validAttachment(result) || result.Enabled != attachmentEnabled(normalized.Enabled) || result.CreatedBy != normalized.Actor || result.UpdatedBy != normalized.Actor {
			return ErrAttachmentUnavailable
		}
		readBack, readErr := service.store.GetAttachment(tx, result.ID)
		if readErr != nil || !equalAttachment(readBack, result) {
			return ErrAttachmentUnavailable
		}
		if appendErr := service.appendAttachmentEvent(tx, "created", result, normalized.Actor, normalized.IdempotencyKey, now); appendErr != nil {
			return appendErr
		}
		return service.completeAttachmentReceipt(tx, receipt.ID, result, reservation, now)
	})
	if err != nil {
		return mediaport.Attachment{}, classifyAttachmentError(err)
	}
	return cloneAttachment(result), nil
}

func (service *AttachmentService) List(ctx context.Context, query mediaport.AttachmentListQuery) (mediaport.AttachmentListPage, error) {
	query = normalizeAttachmentListQuery(query)
	empty := mediaport.AttachmentListPage{Items: []mediaport.Attachment{}, Limit: query.Limit, Offset: query.Offset}
	if !attachmentReady(service) || ctx == nil {
		return empty, ErrAttachmentUnavailable
	}
	var read AttachmentListRead
	err := service.uow.Within(ctx, func(tx context.Context) error {
		value, err := service.store.ListAttachments(tx, query)
		if err != nil || !validAttachmentListRead(value, query) {
			return ErrAttachmentUnavailable
		}
		read = value
		return nil
	})
	if err != nil {
		return empty, ErrAttachmentUnavailable
	}
	items := make([]mediaport.Attachment, 0, len(read.Items))
	for _, attachment := range read.Items {
		items = append(items, cloneAttachment(attachment))
	}
	return mediaport.AttachmentListPage{Items: items, Total: read.Total, Limit: query.Limit, Offset: query.Offset}, nil
}

func (service *AttachmentService) Get(ctx context.Context, attachmentID int64) (mediaport.Attachment, error) {
	if attachmentID < 1 {
		return mediaport.Attachment{}, ErrInvalidAttachment
	}
	if !attachmentReady(service) || ctx == nil {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	var result mediaport.Attachment
	err := service.uow.Within(ctx, func(tx context.Context) error {
		value, err := service.store.GetAttachment(tx, attachmentID)
		if err != nil {
			return err
		}
		if !validAttachment(value) {
			return ErrAttachmentUnavailable
		}
		result = value
		return nil
	})
	if err != nil {
		return mediaport.Attachment{}, classifyAttachmentError(err)
	}
	return cloneAttachment(result), nil
}

func (service *AttachmentService) Download(ctx context.Context, attachmentID int64) (AttachmentDownload, error) {
	if attachmentID < 1 {
		return AttachmentDownload{}, ErrInvalidAttachment
	}
	if !attachmentReady(service) || ctx == nil {
		return AttachmentDownload{}, ErrAttachmentUnavailable
	}
	var blob AttachmentBlob
	err := service.uow.Within(ctx, func(tx context.Context) error {
		value, err := service.store.ReadAttachment(tx, attachmentID)
		if err != nil {
			return err
		}
		if !validAttachment(value.Attachment) || len(value.Content) != int(value.Attachment.FileSize) || len(value.Content) == 0 {
			return ErrAttachmentUnavailable
		}
		checksum := sha256.Sum256(value.Content)
		if subtle.ConstantTimeCompare(checksum[:], value.Checksum[:]) != 1 {
			return ErrAttachmentUnavailable
		}
		inspection, inspectErr := domain.InspectAttachment(value.Attachment.FileName, value.Attachment.MimeType, value.Content)
		if inspectErr != nil || inspection.MediaType != value.Attachment.MimeType {
			return ErrAttachmentUnavailable
		}
		blob = AttachmentBlob{Attachment: cloneAttachment(value.Attachment), Content: append([]byte(nil), value.Content...), Checksum: value.Checksum}
		return nil
	})
	if err != nil {
		return AttachmentDownload{}, classifyAttachmentError(err)
	}
	return AttachmentDownload{Attachment: cloneAttachment(blob.Attachment), Content: append([]byte(nil), blob.Content...)}, nil
}

func (service *AttachmentService) Update(ctx context.Context, command mediaport.AttachmentUpdateCommand) (mediaport.Attachment, error) {
	normalized, err := normalizeAttachmentUpdate(command)
	if err != nil {
		return mediaport.Attachment{}, err
	}
	if !attachmentReady(service) || ctx == nil {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	payload, err := json.Marshal(struct {
		AttachmentID, ExpectedVersion int64
		Name                          string
		Description                   string
		Tags                          []string
		Enabled                       bool
	}{normalized.AttachmentID, normalized.ExpectedVersion, normalized.Name, normalized.Description, normalized.Tags, normalized.Enabled})
	if err != nil {
		return mediaport.Attachment{}, ErrAttachmentUnavailable
	}
	reservation := AttachmentMutationReservation{
		Operation: "update", ActorScope: attachmentActorScope(normalized.Actor), BusinessKey: strconv.FormatInt(normalized.AttachmentID, 10),
		KeyDigest: sha256.Sum256([]byte(normalized.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result mediaport.Attachment
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveAttachmentMutation(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validAttachmentReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrAttachmentConflict
		}
		if !owned {
			return replayAttachmentReceipt(receipt, reservation, &result)
		}
		current, loadErr := service.store.GetAttachmentForUpdate(tx, normalized.AttachmentID)
		if loadErr != nil {
			return loadErr
		}
		if !validAttachment(current) {
			return ErrAttachmentUnavailable
		}
		if current.Version != normalized.ExpectedVersion {
			return ErrAttachmentConflict
		}
		candidate := cloneAttachment(current)
		candidate.Name, candidate.Description, candidate.Tags, candidate.Enabled = normalized.Name, normalized.Description, cloneAttachmentTags(normalized.Tags), normalized.Enabled
		changed := !equalAttachmentMutable(current, candidate)
		if !changed {
			result = current
			return service.completeAttachmentReceipt(tx, receipt.ID, result, reservation, now)
		}
		candidate.Version = current.Version + 1
		candidate.UpdatedBy = normalized.Actor
		candidate.UpdatedAt = nextAttachmentUpdateTime(now, current.UpdatedAt)
		result, loadErr = service.store.UpdateAttachment(tx, AttachmentUpdateInput{Attachment: candidate, ExpectedVersion: current.Version})
		if loadErr != nil {
			return loadErr
		}
		if !equalAttachment(result, candidate) {
			return ErrAttachmentUnavailable
		}
		readBack, readErr := service.store.GetAttachment(tx, result.ID)
		if readErr != nil || !equalAttachment(readBack, result) {
			return ErrAttachmentUnavailable
		}
		if appendErr := service.appendAttachmentEvent(tx, "updated", result, normalized.Actor, normalized.IdempotencyKey, now); appendErr != nil {
			return appendErr
		}
		return service.completeAttachmentReceipt(tx, receipt.ID, result, reservation, now)
	})
	if err != nil {
		return mediaport.Attachment{}, classifyAttachmentError(err)
	}
	return cloneAttachment(result), nil
}

func (service *AttachmentService) Delete(ctx context.Context, command mediaport.AttachmentDeleteCommand) (AttachmentDeleteResult, error) {
	if command.AttachmentID < 1 || command.Actor < 1 || !validAttachmentIdempotencyKey(command.IdempotencyKey) {
		return AttachmentDeleteResult{}, ErrInvalidAttachment
	}
	if !attachmentDeleteReady(service) || ctx == nil {
		return AttachmentDeleteResult{}, ErrAttachmentUnavailable
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return AttachmentDeleteResult{}, ErrAttachmentUnavailable
	}
	payload, err := json.Marshal(struct {
		AttachmentID int64 `json:"attachment_id"`
	}{command.AttachmentID})
	if err != nil {
		return AttachmentDeleteResult{}, ErrAttachmentUnavailable
	}
	reservation := AttachmentMutationReservation{
		Operation: "delete", ActorScope: attachmentActorScope(command.Actor), BusinessKey: strconv.FormatInt(command.AttachmentID, 10),
		KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result AttachmentDeleteResult
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveAttachmentMutation(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validAttachmentReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrAttachmentConflict
		}
		if !owned {
			return replayAttachmentDeleteReceipt(receipt, reservation, &result)
		}
		current, loadErr := service.store.GetAttachmentForUpdate(tx, command.AttachmentID)
		if loadErr != nil {
			return loadErr
		}
		if !validAttachment(current) {
			return ErrAttachmentUnavailable
		}
		references, referenceErr := service.attachmentReferences(tx, command.AttachmentID)
		if referenceErr != nil {
			return ErrAttachmentUnavailable
		}
		if references.Any() {
			result = AttachmentDeleteResult{ID: command.AttachmentID, References: references}
			return ErrAttachmentHasReferences
		}
		deleted, deleteErr := service.store.DeleteAttachment(tx, command.AttachmentID)
		if deleteErr != nil || deleted != 1 {
			return ErrAttachmentUnavailable
		}
		result = AttachmentDeleteResult{ID: command.AttachmentID, Deleted: true, HardDeleted: true, References: emptyAttachmentDeleteReferences()}
		if appendErr := service.appendAttachmentDeleteEvent(tx, command.Actor, command.IdempotencyKey, now, result.ID); appendErr != nil {
			return appendErr
		}
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ErrAttachmentUnavailable
		}
		completed, completeErr := service.store.CompleteAttachmentMutation(tx, receipt.ID, snapshot, now)
		if completeErr != nil || !validAttachmentReceipt(completed, reservation) || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrAttachmentUnavailable
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) || errors.Is(err, ErrAttachmentConflict) || errors.Is(err, ErrAttachmentHasReferences) {
			return result, err
		}
		return AttachmentDeleteResult{}, ErrAttachmentUnavailable
	}
	return result, nil
}

func (service *AttachmentService) completeAttachmentReceipt(ctx context.Context, receiptID int64, result mediaport.Attachment, reservation AttachmentMutationReservation, now time.Time) error {
	snapshot, err := json.Marshal(result)
	if err != nil {
		return ErrAttachmentUnavailable
	}
	completed, err := service.store.CompleteAttachmentMutation(ctx, receiptID, snapshot, now)
	if err != nil || !validAttachmentReceipt(completed, reservation) || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
		return ErrAttachmentUnavailable
	}
	return nil
}

func (service *AttachmentService) attachmentReferences(ctx context.Context, attachmentID int64) (AttachmentDeleteReferences, error) {
	if service == nil || service.automation == nil || service.contact == nil || service.radar == nil {
		return AttachmentDeleteReferences{}, ErrAttachmentUnavailable
	}
	var references AttachmentDeleteReferences
	var err error
	references.AutomationAgents, err = service.automation.ListAttachmentReferenceAgentIDs(ctx, attachmentID)
	if err != nil {
		return AttachmentDeleteReferences{}, err
	}
	references.Channels, err = service.contact.ListAttachmentReferenceChannelIDs(ctx, attachmentID)
	if err != nil {
		return AttachmentDeleteReferences{}, err
	}
	references.RadarLinks, err = service.radar.ListAttachmentReferenceLinkIDs(ctx, attachmentID)
	if err != nil || !validAttachmentDeleteReferences(references) {
		return AttachmentDeleteReferences{}, ErrAttachmentUnavailable
	}
	return references, nil
}

func (service *AttachmentService) appendAttachmentEvent(ctx context.Context, action string, attachment mediaport.Attachment, actor int64, key string, now time.Time) error {
	eventType := map[string]string{"created": "media.attachment_created", "updated": "media.attachment_updated"}[action]
	if eventType == "" {
		return ErrAttachmentUnavailable
	}
	payload, err := json.Marshal(struct {
		AttachmentID int64 `json:"attachment_id"`
		Actor        int64 `json:"actor"`
		Version      int64 `json:"version"`
	}{attachment.ID, actor, attachment.Version})
	if err != nil {
		return ErrAttachmentUnavailable
	}
	digest := sha256.Sum256([]byte(attachmentActorScope(actor) + "\x00" + key + "\x00" + action))
	_, err = service.events.Append(ctx, mediaport.Event{Type: eventType, Payload: payload, OccurredAt: now, IdempotencyKey: "media.attachment_" + action + ":" + hex.EncodeToString(digest[:])})
	return err
}

func (service *AttachmentService) appendAttachmentDeleteEvent(ctx context.Context, actor int64, key string, now time.Time, attachmentID int64) error {
	payload, err := json.Marshal(struct {
		AttachmentID int64 `json:"attachment_id"`
		Actor        int64 `json:"actor"`
	}{attachmentID, actor})
	if err != nil {
		return ErrAttachmentUnavailable
	}
	digest := sha256.Sum256([]byte(attachmentActorScope(actor) + "\x00" + key + "\x00delete"))
	_, err = service.events.Append(ctx, mediaport.Event{Type: "media.attachment_deleted", Payload: payload, OccurredAt: now, IdempotencyKey: "media.attachment_deleted:" + hex.EncodeToString(digest[:])})
	return err
}

func normalizeAttachmentUpload(command mediaport.AttachmentUploadCommand) (mediaport.AttachmentUploadCommand, domain.AttachmentInspection, error) {
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	inspection, err := domain.InspectAttachment(command.FileName, command.DeclaredType, command.Content)
	if err != nil || command.Actor < 1 || !validAttachmentIdempotencyKey(command.IdempotencyKey) {
		return mediaport.AttachmentUploadCommand{}, domain.AttachmentInspection{}, ErrInvalidAttachment
	}
	if command.Name == "" {
		command.Name = command.FileName
	}
	if !validAttachmentText(command.Name, maximumAttachmentNameRunes, false) || !validAttachmentText(command.Description, maximumAttachmentDescriptionRunes, true) {
		return mediaport.AttachmentUploadCommand{}, domain.AttachmentInspection{}, ErrInvalidAttachment
	}
	command.Tags, err = normalizeAttachmentTags(command.Tags)
	if err != nil {
		return mediaport.AttachmentUploadCommand{}, domain.AttachmentInspection{}, err
	}
	return command, inspection, nil
}

func normalizeAttachmentUpdate(command mediaport.AttachmentUpdateCommand) (mediaport.AttachmentUpdateCommand, error) {
	command.Name, command.Description = strings.TrimSpace(command.Name), strings.TrimSpace(command.Description)
	if command.AttachmentID < 1 || command.ExpectedVersion < 1 || command.Actor < 1 || !validAttachmentIdempotencyKey(command.IdempotencyKey) ||
		!validAttachmentText(command.Name, maximumAttachmentNameRunes, false) || !validAttachmentText(command.Description, maximumAttachmentDescriptionRunes, true) {
		return mediaport.AttachmentUpdateCommand{}, ErrInvalidAttachment
	}
	tags, err := normalizeAttachmentTags(command.Tags)
	if err != nil {
		return mediaport.AttachmentUpdateCommand{}, err
	}
	command.Tags = tags
	return command, nil
}

func normalizeAttachmentListQuery(query mediaport.AttachmentListQuery) mediaport.AttachmentListQuery {
	if query.Limit == 0 {
		query.Limit = mediaport.DefaultAttachmentListLimit
	} else if query.Limit < 0 {
		query.Limit = 1
	} else if query.Limit > mediaport.MaximumAttachmentListLimit {
		query.Limit = mediaport.MaximumAttachmentListLimit
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	query.Search = strings.TrimSpace(query.Search)
	return query
}

func normalizeAttachmentTags(values []string) ([]string, error) {
	if len(values) > maximumAttachmentTags {
		return nil, ErrInvalidAttachment
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if !validAttachmentText(value, maximumAttachmentTagRunes, false) || strings.Contains(value, ",") {
			return nil, ErrInvalidAttachment
		}
		if _, exists := seen[value]; exists {
			return nil, ErrInvalidAttachment
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func validAttachmentReceipt(receipt AttachmentMutationReceipt, reservation AttachmentMutationReservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope && receipt.BusinessKey == reservation.BusinessKey &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func replayAttachmentReceipt(receipt AttachmentMutationReceipt, reservation AttachmentMutationReservation, result *mediaport.Attachment) error {
	if !validAttachmentReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		return ErrAttachmentConflict
	}
	if receipt.State != "completed" || result == nil || json.Unmarshal(receipt.ResultSnapshot, result) != nil || !validAttachment(*result) {
		return ErrAttachmentUnavailable
	}
	canonical, err := json.Marshal(*result)
	if err != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
		return ErrAttachmentUnavailable
	}
	return nil
}

func replayAttachmentDeleteReceipt(receipt AttachmentMutationReceipt, reservation AttachmentMutationReservation, result *AttachmentDeleteResult) error {
	if !validAttachmentReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
		return ErrAttachmentConflict
	}
	if receipt.State != "completed" || result == nil || json.Unmarshal(receipt.ResultSnapshot, result) != nil || !validAttachmentDeleteResult(*result) {
		return ErrAttachmentUnavailable
	}
	canonical, err := json.Marshal(*result)
	if err != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
		return ErrAttachmentUnavailable
	}
	return nil
}

func validAttachmentListRead(read AttachmentListRead, query mediaport.AttachmentListQuery) bool {
	count := int64(len(read.Items))
	if read.Total < 0 || count > query.Limit || count > read.Total || count == 0 && query.Offset < read.Total || count > 0 && query.Offset > read.Total-count {
		return false
	}
	for _, attachment := range read.Items {
		if !validAttachment(attachment) {
			return false
		}
	}
	return true
}

func validAttachment(attachment mediaport.Attachment) bool {
	if attachment.ID < 1 || !validAttachmentText(attachment.Name, maximumAttachmentNameRunes, false) || !validAttachmentFileName(attachment.FileName) ||
		attachment.MimeType != "application/pdf" || attachment.FileSize < 1 || attachment.FileSize > domain.MaxAttachmentBytes ||
		!validAttachmentText(attachment.Description, maximumAttachmentDescriptionRunes, true) || attachment.Version < 1 || attachment.CreatedBy < 1 || attachment.UpdatedBy < 1 ||
		attachment.CreatedAt.IsZero() || attachment.UpdatedAt.IsZero() || attachment.UpdatedAt.Before(attachment.CreatedAt) {
		return false
	}
	_, err := normalizeAttachmentTags(attachment.Tags)
	return err == nil
}

func validAttachmentText(value string, maximum int, allowEmpty bool) bool {
	return utf8.ValidString(value) && (allowEmpty || value != "") && value == strings.TrimSpace(value) && utf8.RuneCountInString(value) <= maximum
}

func validAttachmentFileName(value string) bool {
	if !validAttachmentText(value, 255, false) || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validAttachmentDeleteReferences(references AttachmentDeleteReferences) bool {
	return sortedPositiveAttachmentIDs(references.AutomationAgents) && sortedPositiveAttachmentIDs(references.Channels) && sortedPositiveAttachmentIDs(references.RadarLinks)
}

func sortedPositiveAttachmentIDs(values []int64) bool {
	return values != nil && sort.SliceIsSorted(values, func(left, right int) bool { return values[left] < values[right] }) && allPositiveAttachmentIDs(values)
}

func allPositiveAttachmentIDs(values []int64) bool {
	for index, value := range values {
		if value < 1 || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func validAttachmentDeleteResult(result AttachmentDeleteResult) bool {
	return result.ID > 0 && result.Deleted && result.HardDeleted && !result.References.Any() && validAttachmentDeleteReferences(result.References)
}

func emptyAttachmentDeleteReferences() AttachmentDeleteReferences {
	return AttachmentDeleteReferences{AutomationAgents: []int64{}, Channels: []int64{}, RadarLinks: []int64{}}
}

func equalAttachment(left, right mediaport.Attachment) bool {
	return left.ID == right.ID && left.Name == right.Name && left.FileName == right.FileName && left.MimeType == right.MimeType &&
		left.FileSize == right.FileSize && left.Description == right.Description && equalAttachmentTags(left.Tags, right.Tags) && left.Enabled == right.Enabled &&
		left.Version == right.Version && left.CreatedBy == right.CreatedBy && left.UpdatedBy == right.UpdatedBy && left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

func equalAttachmentMutable(left, right mediaport.Attachment) bool {
	return left.Name == right.Name && left.Description == right.Description && equalAttachmentTags(left.Tags, right.Tags) && left.Enabled == right.Enabled
}

func equalAttachmentTags(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneAttachment(attachment mediaport.Attachment) mediaport.Attachment {
	attachment.Tags = cloneAttachmentTags(attachment.Tags)
	return attachment
}

func cloneAttachmentTags(values []string) []string {
	return append([]string{}, values...)
}

func attachmentEnabled(value *bool) bool {
	return value == nil || *value
}

func attachmentActorScope(actor int64) string {
	return fmt.Sprintf("admin:%d", actor)
}

func validAttachmentIdempotencyKey(value string) bool {
	return utf8.ValidString(value) && len(value) >= minimumAttachmentIdempotencyKey && len(value) <= maximumAttachmentIdempotencyKey && value == strings.TrimSpace(value)
}

func nextAttachmentUpdateTime(now, current time.Time) time.Time {
	if !now.After(current) {
		return current.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return now
}

func attachmentReady(service *AttachmentService) bool {
	return service != nil && service.uow != nil && service.store != nil && service.events != nil && service.now != nil
}

func attachmentDeleteReady(service *AttachmentService) bool {
	return attachmentReady(service) && service.automation != nil && service.contact != nil && service.radar != nil
}

func classifyAttachmentError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrInvalidAttachment, ErrAttachmentNotFound, ErrAttachmentConflict, ErrAttachmentHasReferences} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrAttachmentUnavailable
}
