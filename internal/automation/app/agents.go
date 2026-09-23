// Package app contains Automation's local configuration lifecycle only.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	maxAgentName      = 120
	maxAgentCode      = 120
	maxPrompt         = 20_000
	maxContentText    = 4_000
	maxLegacyConfig   = 100_000
	maxIdempotencyKey = 128
)

var (
	ErrInvalidAgent           = errors.New("invalid automation agent")
	ErrAgentNotFound          = errors.New("automation agent not found")
	ErrAgentConflict          = errors.New("automation agent command conflict")
	ErrAgentUnavailable       = errors.New("automation agent service unavailable")
	ErrAgentExecutionDisabled = errors.New("automation agent execution disabled")
)

type Receipt struct {
	ID                           int64
	Operation, ActorScope, State string
	KeyDigest, PayloadDigest     [32]byte
	ResultSnapshot               json.RawMessage
}

type Reservation struct {
	Operation, ActorScope    string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

// Store is Automation-owned. The event append remains behind the local
// configuration event seam; Terra adapts it to the v3 versioned event/outbox
// port at the composition boundary.
type Store interface {
	List(context.Context, automationport.AutomationType) ([]automationport.Agent, error)
	Get(context.Context, automationport.AgentID) (automationport.Agent, error)
	Lock(context.Context, automationport.AgentID) (automationport.Agent, error)
	Create(context.Context, automationport.Agent, time.Time) (automationport.Agent, error)
	Update(context.Context, automationport.Agent, time.Time) (automationport.Agent, error)
	NextCopyCode(context.Context, string) (string, error)
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}

type Service struct {
	uow          platformport.UnitOfWork
	store        Store
	images       automationport.ImageMetadataReader
	attachments  automationport.AttachmentMetadataReader
	miniPrograms automationport.MiniProgramMetadataReader
	groupInvites automationport.GroupInviteMetadataReader
	events       automationport.EventAppender
	now          func() time.Time
}

var _ automationport.AgentService = (*Service)(nil)

func NewAgentService(uow platformport.UnitOfWork, store Store, events automationport.EventAppender) *Service {
	return &Service{uow: uow, store: store, events: events, now: time.Now}
}

// NewAgentServiceWithImageReferences wires Media's transaction-bound image
// reader for the only Automation image-reference field.
func NewAgentServiceWithImageReferences(uow platformport.UnitOfWork, store Store, images automationport.ImageMetadataReader, events automationport.EventAppender) *Service {
	return NewAgentServiceWithMediaReferences(uow, store, images, nil, nil, nil, events)
}

// NewAgentServiceWithMediaReferences validates every local Media reference
// under the caller's UoW. Metadata readers acquire FOR KEY SHARE before JSON
// references are persisted, closing the delete/write race.
func NewAgentServiceWithMediaReferences(uow platformport.UnitOfWork, store Store, images automationport.ImageMetadataReader, attachments automationport.AttachmentMetadataReader, miniPrograms automationport.MiniProgramMetadataReader, groupInvites automationport.GroupInviteMetadataReader, events automationport.EventAppender) *Service {
	return &Service{uow: uow, store: store, images: images, attachments: attachments, miniPrograms: miniPrograms, groupInvites: groupInvites, events: events, now: time.Now}
}

func (s *Service) List(ctx context.Context, kind automationport.AutomationType) (automationport.Page, error) {
	if !ready(s) || !validTypeOrEmpty(kind) {
		return automationport.Page{}, ErrInvalidAgent
	}
	var items []automationport.Agent
	if err := s.uow.Within(ctx, func(tx context.Context) error { var err error; items, err = s.store.List(tx, kind); return err }); err != nil {
		return automationport.Page{}, classify(err)
	}
	for _, item := range items {
		if !validVisible(item) {
			return automationport.Page{}, ErrAgentUnavailable
		}
	}
	return automationport.Page{Items: items, Total: int64(len(items))}, nil
}

func (s *Service) Get(ctx context.Context, id automationport.AgentID) (automationport.Agent, error) {
	if !ready(s) || id < 1 {
		return automationport.Agent{}, ErrAgentNotFound
	}
	var item automationport.Agent
	if err := s.uow.Within(ctx, func(tx context.Context) error { var err error; item, err = s.store.Get(tx, id); return err }); err != nil {
		return automationport.Agent{}, classify(err)
	}
	if !validVisible(item) {
		return automationport.Agent{}, ErrAgentUnavailable
	}
	return item, nil
}

func (s *Service) Create(ctx context.Context, input automationport.CreateCommand) (automationport.Agent, error) {
	if !validMutation(input.Actor, input.IdempotencyKey) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	item, err := normalizeCreate(input.Agent, input.Actor)
	if err != nil {
		return automationport.Agent{}, err
	}
	return s.mutate(ctx, "create", input.Actor, input.IdempotencyKey, item, func(tx context.Context, now time.Time) (automationport.Agent, bool, error) {
		if err := s.validateMediaReferences(tx, item.FixedContentPackage); err != nil {
			return automationport.Agent{}, false, err
		}
		item, err := s.store.Create(tx, item, now)
		return item, err == nil, err
	})
}

func (s *Service) Update(ctx context.Context, input automationport.UpdateCommand) (automationport.Agent, error) {
	if input.ID < 1 || !validMutation(input.Actor, input.IdempotencyKey) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	return s.mutate(ctx, "update", input.Actor, input.IdempotencyKey, input, func(tx context.Context, now time.Time) (automationport.Agent, bool, error) {
		current, err := s.store.Lock(tx, input.ID)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		next, err := applyUpdate(current, input)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		if sameConfig(current, next) {
			return current, false, nil
		}
		if err := s.validateMediaReferences(tx, next.FixedContentPackage); err != nil {
			return automationport.Agent{}, false, err
		}
		next.UpdatedBy = input.Actor
		item, err := s.store.Update(tx, next, now)
		return item, err == nil, err
	})
}

func (s *Service) Copy(ctx context.Context, input automationport.MutationCommand) (automationport.Agent, error) {
	if input.ID < 1 || !validMutation(input.Actor, input.IdempotencyKey) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	return s.mutate(ctx, "copy", input.Actor, input.IdempotencyKey, input, func(tx context.Context, now time.Time) (automationport.Agent, bool, error) {
		source, err := s.store.Lock(tx, input.ID)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		if source.Status == automationport.AgentStatusArchived {
			return automationport.Agent{}, false, ErrAgentNotFound
		}
		code, err := s.store.NextCopyCode(tx, source.AgentCode)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		source.ID, source.AgentCode = 0, code
		source.AgentName, source.CreatedBy, source.UpdatedBy = copiedName(source.AgentName), input.Actor, input.Actor
		if err := s.validateMediaReferences(tx, source.FixedContentPackage); err != nil {
			return automationport.Agent{}, false, err
		}
		item, err := s.store.Create(tx, source, now)
		return item, err == nil, err
	})
}

func (s *Service) Publish(ctx context.Context, input automationport.MutationCommand) (automationport.Agent, error) {
	return s.transition(ctx, "publish", input, "", func(item *automationport.Agent) {
		item.PublishedRolePrompt, item.PublishedTaskPrompt, item.PublishedVersion = item.DraftRolePrompt, item.DraftTaskPrompt, item.DraftVersion
	})
}

func (s *Service) SetStatus(ctx context.Context, input automationport.MutationCommand, status automationport.AgentStatus) (automationport.Agent, error) {
	if !validStatus(status) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	return s.transition(ctx, "set_status", input, string(status), func(item *automationport.Agent) {
		if status == automationport.AgentStatusActive && !activationReady(*item) {
			return
		}
		item.Status = status
		item.ExecutionEnabled = status == automationport.AgentStatusActive
	})
}

func (s *Service) SaveFixedContent(ctx context.Context, input automationport.FixedContentCommand) (automationport.Agent, error) {
	if input.ID < 1 || !validMutation(input.Actor, input.IdempotencyKey) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	return s.mutate(ctx, "fixed_content", input.Actor, input.IdempotencyKey, input, func(tx context.Context, now time.Time) (automationport.Agent, bool, error) {
		item, err := s.store.Lock(tx, input.ID)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		if item.Status == automationport.AgentStatusArchived {
			return automationport.Agent{}, false, ErrAgentNotFound
		}
		if item.Status == automationport.AgentStatusActive {
			return automationport.Agent{}, false, ErrAgentConflict
		}
		content, err := normalizeContent(input.ContentPackage, item.AutomationType)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		if reflect.DeepEqual(item.FixedContentPackage, content) {
			return item, false, nil
		}
		if err := s.validateMediaReferences(tx, content); err != nil {
			return automationport.Agent{}, false, err
		}
		item.FixedContentPackage, item.UpdatedBy = content, input.Actor
		// Fixed content participates in the same local draft/publish contract as
		// prompts. A changed package cannot be enabled until it is republished.
		item.DraftVersion++
		updated, err := s.store.Update(tx, item, now)
		return updated, err == nil, err
	})
}

func (s *Service) transition(ctx context.Context, operation string, input automationport.MutationCommand, desired string, update func(*automationport.Agent)) (automationport.Agent, error) {
	if input.ID < 1 || !validMutation(input.Actor, input.IdempotencyKey) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	payload := struct {
		ID      automationport.AgentID
		Desired string
	}{input.ID, desired}
	return s.mutate(ctx, operation, input.Actor, input.IdempotencyKey, payload, func(tx context.Context, now time.Time) (automationport.Agent, bool, error) {
		item, err := s.store.Lock(tx, input.ID)
		if err != nil {
			return automationport.Agent{}, false, err
		}
		if item.Status == automationport.AgentStatusArchived {
			if operation == "set_status" && desired == string(automationport.AgentStatusArchived) {
				return item, false, nil
			}
			return automationport.Agent{}, false, ErrAgentNotFound
		}
		before := item
		update(&item)
		if operation == "set_status" && desired == string(automationport.AgentStatusActive) && sameConfig(before, item) {
			if before.Status == automationport.AgentStatusActive {
				return before, false, nil
			}
			return automationport.Agent{}, false, ErrAgentConflict
		}
		if sameConfig(before, item) {
			return before, false, nil
		}
		item.UpdatedBy = input.Actor
		updated, err := s.store.Update(tx, item, now)
		return updated, err == nil, err
	})
}

func (s *Service) mutate(ctx context.Context, operation string, actor int64, key string, payload any, apply func(context.Context, time.Time) (automationport.Agent, bool, error)) (automationport.Agent, error) {
	if !ready(s) || !validMutation(actor, key) || apply == nil {
		return automationport.Agent{}, ErrInvalidAgent
	}
	now := s.now().UTC()
	if now.IsZero() {
		return automationport.Agent{}, ErrAgentUnavailable
	}
	raw, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{operation, payload})
	if err != nil {
		return automationport.Agent{}, ErrInvalidAgent
	}
	reservation := Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(raw), CreatedAt: now}
	var result automationport.Agent
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation)
		if err != nil {
			return err
		}
		if !sameReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrAgentConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil || !validPersisted(result) {
				return ErrAgentUnavailable
			}
			return nil
		}
		var changed bool
		result, changed, err = apply(tx, now)
		if err != nil {
			return err
		}
		if !validPersisted(result) {
			return ErrAgentUnavailable
		}
		if changed {
			if err = s.appendEvent(tx, operation, result, actor, key, now); err != nil {
				return err
			}
		}
		snapshot, err := json.Marshal(result)
		if err != nil {
			return err
		}
		completed, err := s.store.Complete(tx, receipt.ID, snapshot, now)
		if err != nil || completed.State != "completed" || !jsonEqual(completed.ResultSnapshot, snapshot) {
			return ErrAgentUnavailable
		}
		return nil
	})
	if err != nil {
		return automationport.Agent{}, classify(err)
	}
	return result, nil
}

func (s *Service) appendEvent(ctx context.Context, operation string, item automationport.Agent, actor int64, key string, now time.Time) error {
	eventType := map[string]string{
		"create":        automationport.EventAgentCreated,
		"update":        automationport.EventAgentUpdated,
		"copy":          automationport.EventAgentCopied,
		"publish":       automationport.EventAgentPublished,
		"set_status":    automationport.EventAgentStatusChanged,
		"fixed_content": automationport.EventFixedContentUpdated,
	}[operation]
	if eventType == "" {
		return ErrAgentUnavailable
	}
	payload, err := json.Marshal(struct {
		AgentID automationport.AgentID     `json:"agent_id"`
		Actor   int64                      `json:"actor"`
		Status  automationport.AgentStatus `json:"status"`
	}{item.ID, actor, item.Status})
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte("automation.agent." + operation + "\x00" + key))
	_, err = s.events.Append(ctx, automationport.Event{Type: eventType, Payload: payload, OccurredAt: now, IdempotencyKey: "automation.agent." + operation + ":" + hex.EncodeToString(digest[:])})
	return err
}

func normalizeCreate(item automationport.Agent, actor int64) (automationport.Agent, error) {
	item.ID = 0
	item.AgentName, item.AgentCode = strings.TrimSpace(item.AgentName), strings.TrimSpace(item.AgentCode)
	if !validText(item.AgentName, maxAgentName) || item.AgentName == "" || !validCode(item.AgentCode) || !validType(item.AutomationType) || item.Status != automationport.AgentStatusPaused || item.ExecutionEnabled {
		return automationport.Agent{}, ErrInvalidAgent
	}
	if !validText(item.DraftRolePrompt, maxPrompt) || !validText(item.DraftTaskPrompt, maxPrompt) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	content, err := normalizeContent(item.FixedContentPackage, item.AutomationType)
	if err != nil {
		return automationport.Agent{}, err
	}
	item.FixedContentPackage = content
	legacy, err := normalizeLegacyConfiguration(item.LegacyConfiguration)
	if err != nil {
		return automationport.Agent{}, err
	}
	item.LegacyConfiguration = legacy
	item.DraftVersion, item.PublishedVersion = 1, 1
	item.PublishedRolePrompt, item.PublishedTaskPrompt = item.DraftRolePrompt, item.DraftTaskPrompt
	item.CreatedBy, item.UpdatedBy = actor, actor
	return item, nil
}

func applyUpdate(item automationport.Agent, input automationport.UpdateCommand) (automationport.Agent, error) {
	if item.Status == automationport.AgentStatusArchived {
		return automationport.Agent{}, ErrAgentNotFound
	}
	before := item
	if input.AgentName != nil {
		item.AgentName = strings.TrimSpace(*input.AgentName)
	}
	if input.AutomationType != nil {
		item.AutomationType = *input.AutomationType
	}
	if input.RolePrompt != nil {
		item.DraftRolePrompt = *input.RolePrompt
	}
	if input.TaskPrompt != nil {
		item.DraftTaskPrompt = *input.TaskPrompt
	}
	if input.Status != nil && *input.Status != automationport.AgentStatusPaused {
		return automationport.Agent{}, ErrInvalidAgent
	}
	if input.Status != nil {
		item.Status, item.ExecutionEnabled = *input.Status, false
	}
	if !validText(item.AgentName, maxAgentName) || item.AgentName == "" || !validType(item.AutomationType) || !validStatus(item.Status) || item.Status == automationport.AgentStatusArchived || item.ExecutionEnabled || !validText(item.DraftRolePrompt, maxPrompt) || !validText(item.DraftTaskPrompt, maxPrompt) {
		return automationport.Agent{}, ErrInvalidAgent
	}
	if input.FixedContentPackage != nil {
		var err error
		item.FixedContentPackage, err = normalizeContent(*input.FixedContentPackage, item.AutomationType)
		if err != nil {
			return automationport.Agent{}, err
		}
	} else if item.AutomationType != before.AutomationType {
		var err error
		item.FixedContentPackage, err = normalizeContent(item.FixedContentPackage, item.AutomationType)
		if err != nil {
			return automationport.Agent{}, err
		}
	}
	if input.LegacyConfiguration != nil {
		legacy, err := normalizeLegacyConfiguration(*input.LegacyConfiguration)
		if err != nil {
			return automationport.Agent{}, err
		}
		item.LegacyConfiguration = legacy
	}
	if item.DraftRolePrompt != before.DraftRolePrompt || item.DraftTaskPrompt != before.DraftTaskPrompt {
		item.DraftVersion++
	}
	return item, nil
}

func normalizeContent(content automationport.FixedContentPackage, kind automationport.AutomationType) (automationport.FixedContentPackage, error) {
	content.ContentText = strings.TrimSpace(content.ContentText)
	if !validText(content.ContentText, maxContentText) || (kind != automationport.AutomationTypeFixedScript && content.ContentText != "") {
		return automationport.FixedContentPackage{}, ErrInvalidAgent
	}
	if len(content.DynamicMiniprogramCard) != 0 {
		return automationport.FixedContentPackage{}, ErrInvalidAgent
	}
	var err error
	if content.ImageLibraryIDs, err = normalIDs(content.ImageLibraryIDs, 3); err != nil {
		return automationport.FixedContentPackage{}, err
	}
	if content.MiniprogramLibraryIDs, err = normalIDs(content.MiniprogramLibraryIDs, 1); err != nil {
		return automationport.FixedContentPackage{}, err
	}
	if content.AttachmentLibraryIDs, err = normalIDs(content.AttachmentLibraryIDs, 9); err != nil {
		return automationport.FixedContentPackage{}, err
	}
	if content.GroupInviteLibraryIDs, err = normalIDs(content.GroupInviteLibraryIDs, 1); err != nil {
		return automationport.FixedContentPackage{}, err
	}
	if len(content.ImageLibraryIDs)+len(content.MiniprogramLibraryIDs)+len(content.AttachmentLibraryIDs)+len(content.GroupInviteLibraryIDs) > 9 {
		return automationport.FixedContentPackage{}, ErrInvalidAgent
	}
	if len(content.DynamicMiniprogramCard) != 0 {
		var card map[string]json.RawMessage
		if !json.Valid(content.DynamicMiniprogramCard) || json.Unmarshal(content.DynamicMiniprogramCard, &card) != nil || card == nil {
			return automationport.FixedContentPackage{}, ErrInvalidAgent
		}
		allowed := map[string]bool{"schema_version": true, "appid": true, "title": true, "pagepath": true, "card_id": true, "cid": true, "cover_image_id": true}
		for key := range card {
			if !allowed[key] {
				return automationport.FixedContentPackage{}, ErrInvalidAgent
			}
		}
		canonical, err := json.Marshal(card)
		if err != nil {
			return automationport.FixedContentPackage{}, ErrInvalidAgent
		}
		content.DynamicMiniprogramCard = canonical
	}
	return content, nil
}

func normalizeLegacyConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maxLegacyConfig || !json.Valid(raw) {
		return nil, ErrInvalidAgent
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, ErrInvalidAgent
	}
	if containsForbiddenLegacyConfigurationKey(value) {
		return nil, ErrInvalidAgent
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidAgent
	}
	return canonical, nil
}

var forbiddenLegacyConfigurationKeyFragments = []string{
	"secret", "token", "password", "privatekey", "cookie", "apikey", "accesstoken", "refreshtoken",
	"authorization", "credential", "clientsecret", "webhookurl", "oauthcode", "openid", "unionid",
	"externaluserid", "phone", "mobile", "email",
}

func containsForbiddenLegacyConfigurationKey(value any) bool {
	switch item := value.(type) {
	case map[string]json.RawMessage:
		for key, raw := range item {
			if forbiddenLegacyConfigurationKey(key) || containsForbiddenLegacyConfigurationRaw(raw) {
				return true
			}
		}
	case map[string]any:
		for key, nested := range item {
			if forbiddenLegacyConfigurationKey(key) || containsForbiddenLegacyConfigurationKey(nested) {
				return true
			}
		}
	case []json.RawMessage:
		for _, raw := range item {
			if containsForbiddenLegacyConfigurationRaw(raw) {
				return true
			}
		}
	case []any:
		for _, nested := range item {
			if containsForbiddenLegacyConfigurationKey(nested) {
				return true
			}
		}
	}
	return false
}

func containsForbiddenLegacyConfigurationRaw(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsForbiddenLegacyConfigurationKey(value)
}

func forbiddenLegacyConfigurationKey(value string) bool {
	normalized := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, value)
	for _, fragment := range forbiddenLegacyConfigurationKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func normalIDs(values []int64, limit int) ([]int64, error) {
	if len(values) > limit {
		return nil, ErrInvalidAgent
	}
	result := make([]int64, 0, len(values))
	seen := map[int64]struct{}{}
	for _, value := range values {
		if value < 1 {
			return nil, ErrInvalidAgent
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result, nil
}

func (s *Service) validateImageReferences(ctx context.Context, content automationport.FixedContentPackage) error {
	if len(content.ImageLibraryIDs) == 0 {
		return nil
	}
	if s == nil || s.images == nil {
		return ErrAgentUnavailable
	}
	for _, imageID := range content.ImageLibraryIDs {
		exists, err := s.images.ImageExists(ctx, imageID)
		if err != nil {
			return ErrAgentUnavailable
		}
		if !exists {
			return ErrInvalidAgent
		}
	}
	return nil
}

func (s *Service) validateAttachmentReferences(ctx context.Context, content automationport.FixedContentPackage) error {
	if len(content.AttachmentLibraryIDs) == 0 {
		return nil
	}
	if s == nil || s.attachments == nil {
		return ErrAgentUnavailable
	}
	for _, attachmentID := range content.AttachmentLibraryIDs {
		exists, err := s.attachments.AttachmentExists(ctx, attachmentID)
		if err != nil {
			return ErrAgentUnavailable
		}
		if !exists {
			return ErrInvalidAgent
		}
	}
	return nil
}

func (s *Service) validateMiniProgramReferences(ctx context.Context, content automationport.FixedContentPackage) error {
	if len(content.MiniprogramLibraryIDs) == 0 {
		return nil
	}
	if s == nil || s.miniPrograms == nil {
		return ErrAgentUnavailable
	}
	for _, id := range content.MiniprogramLibraryIDs {
		exists, err := s.miniPrograms.MiniProgramExists(ctx, id)
		if err != nil {
			return ErrAgentUnavailable
		}
		if !exists {
			return ErrInvalidAgent
		}
	}
	return nil
}

func (s *Service) validateGroupInviteReferences(ctx context.Context, content automationport.FixedContentPackage) error {
	if len(content.GroupInviteLibraryIDs) == 0 {
		return nil
	}
	if s == nil || s.groupInvites == nil {
		return ErrAgentUnavailable
	}
	for _, id := range content.GroupInviteLibraryIDs {
		exists, err := s.groupInvites.GroupInviteExists(ctx, id)
		if err != nil {
			return ErrAgentUnavailable
		}
		if !exists {
			return ErrInvalidAgent
		}
	}
	return nil
}

func (s *Service) validateMediaReferences(ctx context.Context, content automationport.FixedContentPackage) error {
	if err := s.validateImageReferences(ctx, content); err != nil {
		return err
	}
	if err := s.validateAttachmentReferences(ctx, content); err != nil {
		return err
	}
	if err := s.validateMiniProgramReferences(ctx, content); err != nil {
		return err
	}
	return s.validateGroupInviteReferences(ctx, content)
}
func copiedName(name string) string {
	candidate := strings.TrimSpace(name) + "（副本）"
	if len([]rune(candidate)) > maxAgentName {
		candidate = string([]rune(strings.TrimSpace(name))[:maxAgentName-4]) + "（副本）"
	}
	return candidate
}
func ready(s *Service) bool {
	return s != nil && s.uow != nil && s.store != nil && s.events != nil && s.now != nil
}
func validMutation(actor int64, key string) bool {
	return actor > 0 && len(key) >= 16 && len(key) <= maxIdempotencyKey && strings.TrimSpace(key) == key
}
func validText(value string, limit int) bool {
	return len([]rune(value)) <= limit && strings.TrimSpace(value) == value
}
func validCode(value string) bool {
	if value == "" || !validText(value, maxAgentCode) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validType(value automationport.AutomationType) bool {
	return value == automationport.AutomationTypeAgent || value == automationport.AutomationTypeFixedScript
}
func validTypeOrEmpty(value automationport.AutomationType) bool {
	return value == "" || validType(value)
}
func validStatus(value automationport.AgentStatus) bool {
	return value == automationport.AgentStatusActive || value == automationport.AgentStatusPaused || value == automationport.AgentStatusArchived
}
func validPersisted(item automationport.Agent) bool {
	_, legacyErr := normalizeLegacyConfiguration(item.LegacyConfiguration)
	_, contentErr := normalizeContent(item.FixedContentPackage, item.AutomationType)
	return item.ID > 0 && item.CreatedBy > 0 && item.UpdatedBy > 0 && !item.CreatedAt.IsZero() && !item.UpdatedAt.IsZero() && item.DraftVersion >= 1 && item.PublishedVersion >= 1 && item.PublishedVersion <= item.DraftVersion && validCode(item.AgentCode) && validType(item.AutomationType) && validStatus(item.Status) && item.ExecutionEnabled == (item.Status == automationport.AgentStatusActive) && legacyErr == nil && contentErr == nil
}

// ValidPersistedForImport exposes the existing read-model invariant to the
// one-time configuration importer without granting it mutation behavior.
func ValidPersistedForImport(item automationport.Agent) bool {
	return validPersisted(item)
}

func activationReady(item automationport.Agent) bool {
	if item.Status != automationport.AgentStatusPaused || item.DraftVersion != item.PublishedVersion || strings.TrimSpace(item.PublishedRolePrompt) == "" || strings.TrimSpace(item.PublishedTaskPrompt) == "" {
		return false
	}
	return item.AutomationType != automationport.AutomationTypeFixedScript || item.FixedContentPackage.ContentText != "" || len(item.FixedContentPackage.ImageLibraryIDs) != 0 || len(item.FixedContentPackage.AttachmentLibraryIDs) != 0
}
func validVisible(item automationport.Agent) bool {
	return validPersisted(item) && item.Status != automationport.AgentStatusArchived
}
func sameConfig(a, b automationport.Agent) bool {
	return a.AgentName == b.AgentName && a.AgentCode == b.AgentCode && a.AutomationType == b.AutomationType && a.Status == b.Status && a.ExecutionEnabled == b.ExecutionEnabled && a.DraftRolePrompt == b.DraftRolePrompt && a.DraftTaskPrompt == b.DraftTaskPrompt && a.PublishedRolePrompt == b.PublishedRolePrompt && a.PublishedTaskPrompt == b.PublishedTaskPrompt && a.DraftVersion == b.DraftVersion && a.PublishedVersion == b.PublishedVersion && reflect.DeepEqual(a.FixedContentPackage, b.FixedContentPackage) && jsonEqual(a.LegacyConfiguration, b.LegacyConfiguration)
}
func sameReceipt(receipt Receipt, reservation Reservation) bool {
	return receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope && subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1
}
func jsonEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
func classify(err error) error {
	switch {
	case errors.Is(err, ErrInvalidAgent), errors.Is(err, ErrAgentNotFound), errors.Is(err, ErrAgentConflict), errors.Is(err, ErrAgentUnavailable), errors.Is(err, ErrAgentExecutionDisabled):
		return err
	default:
		if strings.Contains(strings.ToLower(err.Error()), "no rows") {
			return ErrAgentNotFound
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrAgentConflict
		}
		return ErrAgentUnavailable
	}
}
