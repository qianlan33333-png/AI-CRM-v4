// Package app coordinates AI Assistant review plans inside one PostgreSQL UoW.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const MaximumPageSize = 50

var (
	ErrInvalid  = errors.New("invalid AI Assistant command")
	ErrNotFound = errors.New("AI Assistant record not found")
	ErrConflict = errors.New("AI Assistant command conflict")
	// ErrIdempotencyConflict means a caller reused a key with a different
	// business command. It remains distinct at the HTTP edge from a stale
	// version or duplicate file.
	ErrIdempotencyConflict = errors.New("AI Assistant idempotency key conflict")
	ErrUnavailable         = errors.New("AI Assistant dependency unavailable")
	ErrNoRecipients        = errors.New("AI Assistant plan has no resolvable recipients")
	ErrMaterialDrift       = errors.New("AI Assistant material changed or unavailable")
	// ErrLegacyMaterialUnmapped is a safe per-target compatibility result. It
	// never carries a legacy identifier and cannot be used to mint a Media row.
	ErrLegacyMaterialUnmapped = errors.New("AI Assistant legacy material is not mapped")
)

type Reservation struct {
	Operation     string
	ActorScope    string
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	CreatedAt     time.Time
}

type Receipt struct {
	ID             int64
	Operation      string
	ActorScope     string
	KeyDigest      [32]byte
	PayloadDigest  [32]byte
	State          string
	ResultSnapshot json.RawMessage
}

type Cursor struct {
	UpdatedAt time.Time
	ID        int64
}

type Store interface {
	CreatePlan(context.Context, aiassistantdomain.Plan, []aiassistantport.RecipientCandidate, int64, time.Time) (aiassistantport.Plan, []aiassistantport.Recipient, error)
	ListPlans(context.Context, aiassistantport.PlanListQuery, Cursor) ([]aiassistantport.Plan, error)
	GetPlan(context.Context, aiassistantport.PlanID, bool) (aiassistantport.Plan, error)
	ListRecipients(context.Context, aiassistantport.RecipientPageQuery, int64) ([]aiassistantport.Recipient, error)
	GetRecipient(context.Context, aiassistantport.PlanID, aiassistantport.RecipientID, bool) (aiassistantport.Recipient, aiassistantport.ContentVersion, error)
	UpdateContent(context.Context, aiassistantport.PlanID, aiassistantport.RecipientID, int64, []byte, effectport.Digest, int64, time.Time) (aiassistantport.Recipient, aiassistantport.ContentVersion, error)
	SaveRecipientReview(context.Context, aiassistantport.Plan, aiassistantport.Recipient, aiassistantport.ReviewState, string, int64, [32]byte, time.Time) error
	SavePlanRejection(context.Context, aiassistantport.Plan, string, int64, [32]byte, time.Time) error
	ListApprovalRecipients(context.Context, aiassistantport.PlanID, bool) ([]aiassistantport.Recipient, []aiassistantport.ContentVersion, error)
	SavePlanApproval(context.Context, aiassistantport.Plan, []aiassistantport.Recipient, []aiassistantport.ContentVersion, []outboundport.PrivateMessageIntentResult, int64, [32]byte, time.Time) error
	ListEffectBindings(context.Context, aiassistantport.PlanID) ([]aiassistantport.EffectBinding, error)
	GetRecipientByEffect(context.Context, string) (aiassistantport.Recipient, error)
	ReserveIntegrationNonce(context.Context, string, string, string, [32]byte, time.Time, time.Time) error
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	aiassistantport.EventAppender
}

type CustomerSnapshot struct {
	CanonicalID customerdomain.CustomerID
	Status      customerdomain.Status
	DisplayName string
	OneIDLabel  string
}

type CustomerReader interface {
	CustomerSnapshot(context.Context, customerdomain.CustomerID) (CustomerSnapshot, error)
}

type StaffSnapshot struct {
	ID          int64
	DisplayName string
	Active      bool
}

type StaffReader interface {
	StaffSnapshot(context.Context, int64) (StaffSnapshot, error)
	StaffByWeComUserID(context.Context, string) (StaffSnapshot, error)
}

type MaterialResolver interface {
	ResolveMaterial(context.Context, aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error)
	RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error
}

type IdentityTarget struct {
	Reference identitydomain.Reference
	StaffID   int64
	// StaffWeComUserID is accepted only by the legacy edge adapter. It is
	// resolved inside the plan UoW through the Access-owned reader.
	StaffWeComUserID string
	Content          []aiassistantport.ContentBlock
}

type IdentityPlanCommand struct {
	Actor          aiassistantport.Actor
	IdempotencyKey string
	Name           string
	SourceKind     string
	SourceDigest   effectport.Digest
	Targets        []IdentityTarget
	OccurredAt     time.Time
	IntegrationKey string
	Nonce          string
	ExpiresAt      time.Time
}

type IdentityPlanResult struct {
	Plan             aiassistantport.Plan
	Replayed         bool
	Found            int
	NotFound         int
	Conflicted       int
	Unverified       int
	Ineligible       int
	Invalid          int
	MaterialUnmapped int
	Dispositions     []IdentityTargetDisposition
}

// IdentityTargetDisposition is deliberately free of raw external identity
// values. Ordinal preserves a caller's input-to-result accounting.
type IdentityTargetDisposition struct {
	Ordinal int    `json:"ordinal"`
	Status  string `json:"status"`
}

type Service struct {
	ExcelSnapshot   func(context.Context, aiassistantport.PlanID, int64) (string, error)
	uow             platformport.UnitOfWork
	store           Store
	customers       CustomerReader
	staff           StaffReader
	materials       MaterialResolver
	identities      identityport.Resolver
	trustedValues   identityport.ExternalIdentityValueReader
	outbound        outboundport.PrivateMessageIntentWriter
	dispatchEnabled bool
	reconciler      effectport.UnknownReconciler
	excelStrategies operationport.StrategyReader
	now             func() time.Time
}

// BindExcelBatchStrategyReader supplies the only cross-domain dependency for
// Excel batches. The reader validates an opaque long-plan key inside the
// caller's PostgreSQL UoW; it exposes no OperationCycle mutation.
func (s *Service) BindExcelBatchStrategyReader(reader operationport.StrategyReader) error {
	if s == nil || reader == nil {
		return ErrUnavailable
	}
	s.excelStrategies = reader
	return nil
}

func (s *Service) BindReconciler(value effectport.UnknownReconciler) error {
	if s == nil || value == nil {
		return ErrUnavailable
	}
	s.reconciler = value
	return nil
}

func (s *Service) BindOutbound(writer outboundport.PrivateMessageIntentWriter, enabled bool) error {
	if s == nil || writer == nil {
		return ErrUnavailable
	}
	s.outbound, s.dispatchEnabled = writer, enabled
	return nil
}

func NewService(uow platformport.UnitOfWork, store Store, customers CustomerReader, staff StaffReader, materials MaterialResolver, identities identityport.Resolver, trustedValues identityport.ExternalIdentityValueReader) (*Service, error) {
	if uow == nil || store == nil || customers == nil || staff == nil || materials == nil || identities == nil || trustedValues == nil {
		return nil, ErrUnavailable
	}
	return &Service{uow: uow, store: store, customers: customers, staff: staff, materials: materials, identities: identities, trustedValues: trustedValues, now: time.Now}, nil
}

func (s *Service) CreatePlan(ctx context.Context, command aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	if s == nil || !command.Valid() {
		return aiassistantport.CreatePlanResult{}, ErrInvalid
	}
	var result aiassistantport.CreatePlanResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		recipients, err := s.validateCanonicalRecipients(tx, command.Recipients)
		if err != nil {
			return err
		}
		return s.createWithin(tx, command, recipients, &result)
	})
	return result, classify(err)
}

// CreatePlanWithin shares the same validation and Owner writes as CreatePlan,
// but deliberately does not open a Unit of Work. A caller that needs its own
// durable fact to commit with this plan must pass the transaction-bound
// context it already owns.
func (s *Service) CreatePlanWithin(ctx context.Context, command aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error) {
	if s == nil || !command.Valid() {
		return aiassistantport.CreatePlanResult{}, ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return aiassistantport.CreatePlanResult{}, ErrUnavailable
	}
	recipients, err := s.validateCanonicalRecipients(ctx, command.Recipients)
	if err != nil {
		return aiassistantport.CreatePlanResult{}, classify(err)
	}
	var result aiassistantport.CreatePlanResult
	if err = s.createWithin(ctx, command, recipients, &result); err != nil {
		return aiassistantport.CreatePlanResult{}, classify(err)
	}
	return result, nil
}

func (s *Service) CreatePlanFromIdentities(ctx context.Context, command IdentityPlanCommand) (IdentityPlanResult, error) {
	if s == nil || !validIdentityPlan(command) {
		return IdentityPlanResult{}, ErrInvalid
	}
	result := IdentityPlanResult{}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		requestDigest := digestJSON(command)
		if err := s.store.ReserveIntegrationNonce(tx, command.IntegrationKey, command.Nonce, command.IdempotencyKey, requestDigest, command.OccurredAt, command.ExpiresAt); err != nil {
			return err
		}
		businessDigest := identityPlanBusinessDigest(command)
		receipt, owned, err := s.store.Reserve(tx, reservation("integration_plan_create", command.Actor, command.IdempotencyKey, businessDigest, command.OccurredAt))
		if err != nil {
			return err
		}
		if !owned {
			var snapshot identityPlanSnapshot
			if len(receipt.ResultSnapshot) == 0 || json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil {
				return ErrConflict
			}
			if snapshot.PlanID > 0 {
				result.Plan, err = s.store.GetPlan(tx, aiassistantport.PlanID(snapshot.PlanID), false)
				if err != nil {
					return err
				}
			}
			result.Replayed = true
			result.Found = snapshot.Found
			result.NotFound = snapshot.NotFound
			result.Conflicted = snapshot.Conflicted
			result.Unverified = snapshot.Unverified
			result.Ineligible = snapshot.Ineligible
			result.Invalid = snapshot.Invalid
			result.MaterialUnmapped = snapshot.MaterialUnmapped
			result.Dispositions = snapshot.Dispositions
			return nil
		}
		resolved := make([]aiassistantport.RecipientCandidate, 0, len(command.Targets))
		seenCustomers := map[customerdomain.CustomerID]struct{}{}
		for ordinal, target := range command.Targets {
			disposition := IdentityTargetDisposition{Ordinal: ordinal, Status: "invalid"}
			normalized, normalizeErr := identitydomain.Normalize(target.Reference)
			if normalizeErr != nil || target.Reference.Assurance != identitydomain.AssuranceDeclared {
				result.Invalid++
				result.Dispositions = append(result.Dispositions, disposition)
				continue
			}
			staffID := target.StaffID
			if staffID == 0 && target.StaffWeComUserID != "" {
				staff, staffErr := s.staff.StaffByWeComUserID(tx, target.StaffWeComUserID)
				if staffErr != nil {
					return staffErr
				}
				if !staff.Active || staff.ID < 1 {
					result.Ineligible++
					disposition.Status = "ineligible"
					result.Dispositions = append(result.Dispositions, disposition)
					continue
				}
				staffID = staff.ID
			}
			candidate := aiassistantport.RecipientCandidate{CustomerID: 1, StaffID: staffID, Content: target.Content}
			if !candidate.Valid() {
				result.Invalid++
				result.Dispositions = append(result.Dispositions, disposition)
				continue
			}
			resolution, resolveErr := s.identities.Resolve(tx, identitydomain.Reference{Kind: normalized.Kind, Scope: normalized.Scope, Value: normalized.NormalizedValue, Assurance: normalized.Assurance, Source: normalized.Source})
			if resolveErr != nil {
				return resolveErr
			}
			switch resolution.Status {
			case identityport.ResolveFound:
				value, found, trustedErr := s.trustedValues.VerifiedExternalIdentityValue(tx, resolution.CustomerID, normalized.Kind, normalized.Scope)
				if trustedErr != nil {
					return trustedErr
				}
				if !found || value != normalized.NormalizedValue {
					result.Unverified++
					disposition.Status = "unverified"
					result.Dispositions = append(result.Dispositions, disposition)
					continue
				}
				if _, duplicate := seenCustomers[resolution.CustomerID]; duplicate {
					result.Invalid++
					disposition.Status = "duplicate"
					result.Dispositions = append(result.Dispositions, disposition)
					continue
				}
				customer, customerErr := s.customers.CustomerSnapshot(tx, resolution.CustomerID)
				if customerErr != nil {
					return customerErr
				}
				staff, staffErr := s.staff.StaffSnapshot(tx, staffID)
				if staffErr != nil {
					return staffErr
				}
				if customer.CanonicalID != resolution.CustomerID || customer.Status != customerdomain.StatusActive || !staff.Active {
					result.Ineligible++
					disposition.Status = "ineligible"
					result.Dispositions = append(result.Dispositions, disposition)
					continue
				}
				content, materialErr := s.resolveBlocks(tx, target.Content)
				if errors.Is(materialErr, ErrLegacyMaterialUnmapped) {
					result.MaterialUnmapped++
					disposition.Status = "material_unmapped"
					result.Dispositions = append(result.Dispositions, disposition)
					continue
				}
				if materialErr != nil {
					return materialErr
				}
				seenCustomers[resolution.CustomerID] = struct{}{}
				result.Found++
				disposition.Status = "found"
				result.Dispositions = append(result.Dispositions, disposition)
				resolved = append(resolved, aiassistantport.RecipientCandidate{CustomerID: resolution.CustomerID, StaffID: staffID, Content: content})
			case identityport.ResolveConflict:
				result.Conflicted++
				disposition.Status = "conflict"
				result.Dispositions = append(result.Dispositions, disposition)
			default:
				result.NotFound++
				disposition.Status = "not_found"
				result.Dispositions = append(result.Dispositions, disposition)
			}
		}
		if len(resolved) == 0 {
			snapshot, _ := json.Marshal(identityPlanSnapshot{Found: result.Found, NotFound: result.NotFound, Conflicted: result.Conflicted, Unverified: result.Unverified, Ineligible: result.Ineligible, Invalid: result.Invalid, MaterialUnmapped: result.MaterialUnmapped, Dispositions: result.Dispositions})
			_, err = s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt)
			return err
		}
		recipients, err := s.validateCanonicalRecipients(tx, resolved)
		if err != nil {
			return err
		}
		var created aiassistantport.CreatePlanResult
		canonicalKey := "integration-canonical-" + string(effectport.Hash("aiassistant.integration.canonical", command.IntegrationKey, command.IdempotencyKey))
		err = s.createWithin(tx, aiassistantport.CreatePlanCommand{Actor: command.Actor, IdempotencyKey: canonicalKey, Name: command.Name, SourceKind: command.SourceKind, SourceDigest: command.SourceDigest, Recipients: recipients, OccurredAt: command.OccurredAt}, recipients, &created)
		result.Plan, result.Replayed = created.Plan, created.Replayed
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]int{"found": result.Found, "not_found": result.NotFound, "conflict": result.Conflicted, "unverified": result.Unverified, "ineligible": result.Ineligible, "invalid": result.Invalid, "material_unmapped": result.MaterialUnmapped})
		if err = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventIntegrationResolved, AggregateID: result.Plan.ID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey + ":resolution", Payload: payload, OccurredAt: command.OccurredAt}); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(identityPlanSnapshot{PlanID: int64(result.Plan.ID), Found: result.Found, NotFound: result.NotFound, Conflicted: result.Conflicted, Unverified: result.Unverified, Ineligible: result.Ineligible, Invalid: result.Invalid, MaterialUnmapped: result.MaterialUnmapped, Dispositions: result.Dispositions})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt)
		return err
	})
	return result, classify(err)
}

type identityPlanSnapshot struct {
	PlanID           int64                       `json:"plan_id"`
	Found            int                         `json:"found"`
	NotFound         int                         `json:"not_found"`
	Conflicted       int                         `json:"conflicted"`
	Unverified       int                         `json:"unverified"`
	Ineligible       int                         `json:"ineligible"`
	Invalid          int                         `json:"invalid"`
	MaterialUnmapped int                         `json:"material_unmapped"`
	Dispositions     []IdentityTargetDisposition `json:"dispositions"`
}

func (s *Service) createWithin(ctx context.Context, command aiassistantport.CreatePlanCommand, recipients []aiassistantport.RecipientCandidate, result *aiassistantport.CreatePlanResult) error {
	payloadDigest := digestJSON(command)
	receipt, owned, err := s.store.Reserve(ctx, reservation("plan_create", command.Actor, command.IdempotencyKey, payloadDigest, command.OccurredAt))
	if err != nil {
		return err
	}
	if !owned {
		planID := receiptPlanID(receipt.ResultSnapshot)
		if planID < 1 {
			return ErrConflict
		}
		result.Plan, err = s.store.GetPlan(ctx, planID, false)
		result.Replayed = err == nil
		return err
	}
	aggregate, err := aiassistantdomain.NewPlan(command.Name, command.SourceKind, command.SourceDigest, len(recipients), command.Actor.ID, command.OccurredAt)
	if err != nil {
		return ErrInvalid
	}
	var created []aiassistantport.Recipient
	result.Plan, created, err = s.store.CreatePlan(ctx, aggregate, recipients, command.Actor.ID, command.OccurredAt)
	if err != nil {
		return err
	}
	for i := range created {
		if err = s.registerContentReferences(ctx, created[i].ContentVersionID, recipients[i].Content); err != nil {
			return err
		}
	}
	eventPayload, _ := json.Marshal(map[string]any{"plan_id": result.Plan.ID, "target_count": result.Plan.TargetCount})
	if err = s.store.AppendEvent(ctx, aiassistantport.Event{Type: aiassistantport.EventPlanCreated, AggregateID: result.Plan.ID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: eventPayload, OccurredAt: command.OccurredAt}); err != nil {
		return err
	}
	snapshot, _ := json.Marshal(map[string]any{"plan_id": result.Plan.ID})
	_, err = s.store.Complete(ctx, receipt.ID, snapshot, command.OccurredAt)
	return err
}

func (s *Service) ListPlans(ctx context.Context, query aiassistantport.PlanListQuery) (aiassistantport.PlanPage, error) {
	if s == nil || query.Limit < 0 || query.Limit > MaximumPageSize || len(strings.TrimSpace(query.Keyword)) > 200 || !validPlanState(query.State) {
		return aiassistantport.PlanPage{}, ErrInvalid
	}
	if query.Limit == 0 {
		query.Limit = MaximumPageSize
	}
	cursor, err := decodePlanCursor(query.Cursor)
	if err != nil {
		return aiassistantport.PlanPage{}, ErrInvalid
	}
	var items []aiassistantport.Plan
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		items, readErr = s.store.ListPlans(tx, query, cursor)
		return readErr
	})
	if err != nil {
		return aiassistantport.PlanPage{}, classify(err)
	}
	page := aiassistantport.PlanPage{Items: items}
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		page.Items = items[:query.Limit]
		page.NextCursor = encodePlanCursor(Cursor{UpdatedAt: last.UpdatedAt, ID: int64(last.ID)})
	}
	return page, nil
}

func (s *Service) GetPlan(ctx context.Context, id aiassistantport.PlanID) (aiassistantport.Plan, error) {
	if s == nil || id < 1 {
		return aiassistantport.Plan{}, ErrInvalid
	}
	var plan aiassistantport.Plan
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		plan, readErr = s.store.GetPlan(tx, id, false)
		return readErr
	})
	return plan, classify(err)
}

func (s *Service) ListRecipients(ctx context.Context, query aiassistantport.RecipientPageQuery) (aiassistantport.RecipientPage, error) {
	if s == nil || query.PlanID < 1 || query.Limit < 0 || query.Limit > MaximumPageSize || !validReviewState(query.State) {
		return aiassistantport.RecipientPage{}, ErrInvalid
	}
	if query.Limit == 0 {
		query.Limit = MaximumPageSize
	}
	afterID, err := decodeIDCursor(query.Cursor)
	if err != nil {
		return aiassistantport.RecipientPage{}, ErrInvalid
	}
	var items []aiassistantport.Recipient
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if _, readErr := s.store.GetPlan(tx, query.PlanID, false); readErr != nil {
			return readErr
		}
		var readErr error
		items, readErr = s.store.ListRecipients(tx, query, afterID)
		if readErr != nil {
			return readErr
		}
		return s.enrich(tx, items)
	})
	if err != nil {
		return aiassistantport.RecipientPage{}, classify(err)
	}
	page := aiassistantport.RecipientPage{Items: items}
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		page.Items = items[:query.Limit]
		page.NextCursor = encodeIDCursor(int64(last.ID))
	}
	return page, nil
}

func (s *Service) GetRecipient(ctx context.Context, planID aiassistantport.PlanID, recipientID aiassistantport.RecipientID) (aiassistantport.Recipient, aiassistantport.ContentVersion, error) {
	if s == nil || planID < 1 || recipientID < 1 {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrInvalid
	}
	var recipient aiassistantport.Recipient
	var content aiassistantport.ContentVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		recipient, content, readErr = s.store.GetRecipient(tx, planID, recipientID, false)
		if readErr != nil {
			return readErr
		}
		items := []aiassistantport.Recipient{recipient}
		if readErr = s.enrich(tx, items); readErr != nil {
			return readErr
		}
		recipient = items[0]
		return nil
	})
	return recipient, content, classify(err)
}

func (s *Service) UpdateContent(ctx context.Context, command aiassistantport.UpdateContentCommand) (aiassistantport.ContentVersion, error) {
	if s == nil || !command.Actor.Valid() || command.PlanID < 1 || command.RecipientID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	var content aiassistantport.ContentVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		linked, linkedErr := s.linkedOperationExcelBatch(tx, command.PlanID)
		if linkedErr != nil {
			return linkedErr
		}
		if linked {
			return ErrConflict
		}
		current, priorContent, readErr := s.store.GetRecipient(tx, command.PlanID, command.RecipientID, false)
		if readErr != nil {
			return readErr
		}
		if current.DeferredTarget != nil {
			if len(command.Blocks) != 2 || command.Blocks[0].Kind != aiassistantport.ContentText || command.Blocks[1].ExcelCard == nil || len(priorContent.Blocks) != 2 || priorContent.Blocks[1].ExcelCard == nil || command.Blocks[1].ExcelCard.AppID != priorContent.Blocks[1].ExcelCard.AppID || command.Blocks[1].ExcelCard.CoverDigest != priorContent.Blocks[1].ExcelCard.CoverDigest {
				return ErrInvalid
			}
		} else {
			for _, block := range command.Blocks {
				if block.ExcelCard != nil {
					return ErrInvalid
				}
			}
		}
		resolved, materialErr := s.resolveBlocks(tx, command.Blocks)
		if materialErr != nil {
			return materialErr
		}
		payload, digest, freezeErr := aiassistantdomain.FreezeContent(resolved)
		if freezeErr != nil {
			return ErrInvalid
		}
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation("content_update", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			_, contentID := receiptIDs(receipt.ResultSnapshot)
			if contentID < 1 {
				return ErrConflict
			}
			_, content, reserveErr = s.store.GetRecipient(tx, command.PlanID, command.RecipientID, false)
			if reserveErr == nil && content.ID != aiassistantport.ContentVersionID(contentID) {
				return ErrConflict
			}
			return reserveErr
		}
		_, content, reserveErr = s.store.UpdateContent(tx, command.PlanID, command.RecipientID, command.ExpectedVersion, payload, digest, command.Actor.ID, s.nowUTC())
		if reserveErr != nil {
			return reserveErr
		}
		if reserveErr = s.registerContentReferences(tx, content.ID, resolved); reserveErr != nil {
			return reserveErr
		}
		eventPayload, _ := json.Marshal(map[string]any{"plan_id": command.PlanID, "recipient_id": command.RecipientID, "content_version_id": content.ID, "content_digest": content.Digest})
		if reserveErr = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventContentUpdated, AggregateID: command.PlanID, RecipientID: command.RecipientID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: eventPayload, OccurredAt: s.nowUTC()}); reserveErr != nil {
			return reserveErr
		}
		snapshot, _ := json.Marshal(map[string]any{"recipient_id": command.RecipientID, "content_version_id": content.ID})
		_, reserveErr = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return reserveErr
	})
	return content, classify(err)
}

func (s *Service) ReviewRecipient(ctx context.Context, command aiassistantport.ReviewRecipientCommand) (aiassistantport.Recipient, error) {
	if s == nil || !command.Actor.Valid() || command.PlanID < 1 || command.RecipientID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || (command.Decision != aiassistantport.ReviewApproved && command.Decision != aiassistantport.ReviewRejected) || len(command.Reason) > 500 {
		return aiassistantport.Recipient{}, ErrInvalid
	}
	var recipient aiassistantport.Recipient
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation("recipient_review", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			recipientID, _ := receiptIDs(receipt.ResultSnapshot)
			if recipientID != int64(command.RecipientID) {
				return ErrConflict
			}
			var content aiassistantport.ContentVersion
			recipient, content, reserveErr = s.store.GetRecipient(tx, command.PlanID, command.RecipientID, false)
			_ = content
			return reserveErr
		}
		planProjection, loadErr := s.store.GetPlan(tx, command.PlanID, true)
		if loadErr != nil {
			return loadErr
		}
		linked, linkedErr := s.linkedOperationExcelBatch(tx, command.PlanID)
		if linkedErr != nil {
			return linkedErr
		}
		if linked {
			return ErrConflict
		}
		var content aiassistantport.ContentVersion
		recipient, content, loadErr = s.store.GetRecipient(tx, command.PlanID, command.RecipientID, true)
		_ = content
		if loadErr != nil {
			return loadErr
		}
		if recipient.Version != command.ExpectedVersion {
			return ErrConflict
		}
		aggregate, loadErr := aiassistantdomain.Restore(planProjection)
		if loadErr != nil || aggregate.ApplyRecipientDecision(recipient.ReviewState, command.Decision, planProjection.Version, s.nowUTC()) != nil {
			return ErrConflict
		}
		previous := recipient.ReviewState
		recipient.ReviewState = command.Decision
		recipient.Version++
		recipient.UpdatedAt = s.nowUTC()
		decisionDigest := sha256.Sum256([]byte(command.IdempotencyKey))
		if loadErr = s.store.SaveRecipientReview(tx, aggregate.Projection, recipient, previous, command.Reason, command.Actor.ID, decisionDigest, s.nowUTC()); loadErr != nil {
			return loadErr
		}
		eventPayload, _ := json.Marshal(map[string]any{"plan_id": command.PlanID, "recipient_id": command.RecipientID, "decision": command.Decision})
		if loadErr = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventRecipientReviewed, AggregateID: command.PlanID, RecipientID: command.RecipientID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: eventPayload, OccurredAt: s.nowUTC()}); loadErr != nil {
			return loadErr
		}
		snapshot, _ := json.Marshal(map[string]any{"recipient_id": command.RecipientID})
		_, loadErr = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return loadErr
	})
	return recipient, classify(err)
}

func (s *Service) RejectPlan(ctx context.Context, command aiassistantport.RejectPlanCommand) (aiassistantport.Plan, error) {
	if s == nil || !command.Actor.Valid() || command.PlanID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 500 {
		return aiassistantport.Plan{}, ErrInvalid
	}
	var plan aiassistantport.Plan
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation("plan_reject", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if reserveErr != nil {
			return reserveErr
		}
		if !owned {
			id := receiptPlanID(receipt.ResultSnapshot)
			if id != command.PlanID {
				return ErrConflict
			}
			plan, reserveErr = s.store.GetPlan(tx, command.PlanID, false)
			return reserveErr
		}
		projection, loadErr := s.store.GetPlan(tx, command.PlanID, true)
		if loadErr != nil {
			return loadErr
		}
		linked, linkedErr := s.linkedOperationExcelBatch(tx, command.PlanID)
		if linkedErr != nil {
			return linkedErr
		}
		if linked {
			return ErrConflict
		}
		aggregate, loadErr := aiassistantdomain.Restore(projection)
		if loadErr != nil || aggregate.MarkRejected(command.ExpectedVersion, s.nowUTC()) != nil {
			return ErrConflict
		}
		decisionDigest := sha256.Sum256([]byte(command.IdempotencyKey))
		if loadErr = s.store.SavePlanRejection(tx, aggregate.Projection, command.Reason, command.Actor.ID, decisionDigest, s.nowUTC()); loadErr != nil {
			return loadErr
		}
		plan = aggregate.Projection
		eventPayload, _ := json.Marshal(map[string]any{"plan_id": command.PlanID, "reason_present": true})
		if loadErr = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventPlanRejected, AggregateID: command.PlanID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: eventPayload, OccurredAt: s.nowUTC()}); loadErr != nil {
			return loadErr
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": command.PlanID})
		_, loadErr = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return loadErr
	})
	return plan, classify(err)
}

func (s *Service) PreviewApproval(ctx context.Context, command aiassistantport.PreviewApprovalCommand) (aiassistantport.ApprovalPreview, error) {
	return s.previewApproval(ctx, command, false)
}

func (s *Service) PreviewOperationExcelBatch(ctx context.Context, command aiassistantport.PreviewApprovalCommand) (aiassistantport.ApprovalPreview, error) {
	return s.previewApproval(ctx, command, true)
}

func (s *Service) previewApproval(ctx context.Context, command aiassistantport.PreviewApprovalCommand, allowLinkedExcel bool) (aiassistantport.ApprovalPreview, error) {
	if s == nil || !command.Actor.Valid() || command.PlanID < 1 || command.ExpectedVersion < 1 {
		return aiassistantport.ApprovalPreview{}, ErrInvalid
	}
	var preview aiassistantport.ApprovalPreview
	err := s.uow.Within(ctx, func(tx context.Context) error {
		plan, err := s.store.GetPlan(tx, command.PlanID, false)
		if err != nil {
			return err
		}
		linked, linkedErr := s.linkedOperationExcelBatch(tx, command.PlanID)
		if linkedErr != nil {
			return linkedErr
		}
		if linked != allowLinkedExcel {
			return ErrConflict
		}
		if plan.Version != command.ExpectedVersion || (plan.State != aiassistantport.PlanPendingReview && plan.State != aiassistantport.PlanPartiallyApproved) {
			return ErrConflict
		}
		recipients, contents, err := s.store.ListApprovalRecipients(tx, command.PlanID, false)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return ErrNoRecipients
		}
		if err = s.validateApprovalFacts(tx, recipients, contents); err != nil {
			return err
		}
		preview = approvalPreview(plan, recipients, contents)
		return nil
	})
	return preview, classify(err)
}

func (s *Service) ApprovePlan(ctx context.Context, command aiassistantport.ApprovePlanCommand) (aiassistantport.Plan, error) {
	return s.approvePlan(ctx, command, false)
}

func (s *Service) ApproveOperationExcelBatch(ctx context.Context, command aiassistantport.ApprovePlanCommand) (aiassistantport.Plan, error) {
	return s.approvePlan(ctx, command, true)
}

func (s *Service) approvePlan(ctx context.Context, command aiassistantport.ApprovePlanCommand, allowLinkedExcel bool) (aiassistantport.Plan, error) {
	if s == nil || !s.dispatchEnabled || s.outbound == nil {
		return aiassistantport.Plan{}, ErrUnavailable
	}
	if !command.Actor.Valid() || command.PlanID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || !effectport.ValidDigest(command.PreviewDigest) {
		return aiassistantport.Plan{}, ErrInvalid
	}
	excelSnapshotKey := ""
	planBefore, readErr := s.GetPlan(ctx, command.PlanID)
	if readErr != nil {
		return aiassistantport.Plan{}, readErr
	}
	var linked bool
	readErr = s.uow.Within(ctx, func(tx context.Context) error {
		var linkedErr error
		linked, linkedErr = s.linkedOperationExcelBatch(tx, command.PlanID)
		return linkedErr
	})
	if readErr != nil {
		return aiassistantport.Plan{}, classify(readErr)
	}
	if linked != allowLinkedExcel {
		return aiassistantport.Plan{}, ErrConflict
	}
	if planBefore.SourceKind == "excel_batch" && (planBefore.State == aiassistantport.PlanPendingReview || planBefore.State == aiassistantport.PlanPartiallyApproved) {
		if planBefore.Version != command.ExpectedVersion {
			return aiassistantport.Plan{}, ErrConflict
		}
		if s.ExcelSnapshot == nil {
			return aiassistantport.Plan{}, ErrUnavailable
		}
		var snapshotErr error
		excelSnapshotKey, snapshotErr = s.ExcelSnapshot(ctx, command.PlanID, command.ExpectedVersion)
		if snapshotErr != nil {
			return aiassistantport.Plan{}, snapshotErr
		}
	}
	var result aiassistantport.Plan
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("plan_approve", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if err != nil {
			return err
		}
		if !owned {
			if receiptPlanID(receipt.ResultSnapshot) != command.PlanID {
				return ErrConflict
			}
			result, err = s.store.GetPlan(tx, command.PlanID, false)
			return err
		}
		plan, err := s.store.GetPlan(tx, command.PlanID, true)
		if err != nil {
			return err
		}
		if plan.Version != command.ExpectedVersion {
			return ErrConflict
		}
		recipients, contents, err := s.store.ListApprovalRecipients(tx, command.PlanID, true)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return ErrNoRecipients
		}
		if err = s.validateApprovalFacts(tx, recipients, contents); err != nil {
			return err
		}
		preview := approvalPreview(plan, recipients, contents)
		if preview.PreviewDigest != command.PreviewDigest {
			return ErrConflict
		}
		aggregate, err := aiassistantdomain.Restore(plan)
		if err != nil || aggregate.MarkApproved(command.ExpectedVersion, s.nowUTC()) != nil {
			return ErrConflict
		}
		aggregate.Projection.State = aiassistantport.PlanDispatching
		intents := make([]outboundport.PrivateMessageIntentResult, 0, len(recipients))
		for i, recipient := range recipients {
			var lane effectport.Lane
			if plan.SourceKind == "excel_batch" {
				lane = effectport.LaneOutboundExcel
			}
			sourceRef := "aiassistant:" + strconv.FormatInt(int64(plan.ID), 10) + ":" + strconv.FormatInt(int64(recipient.ID), 10) + ":" + strconv.FormatInt(int64(contents[i].ID), 10)
			deferredRef := ""
			targetDigest := effectport.Hash("aiassistant.target", strconv.FormatInt(int64(recipient.CustomerID), 10), strconv.FormatInt(recipient.StaffID, 10))
			if recipient.DeferredTarget != nil {
				deferredRef = sourceRef
				targetDigest = effectport.Hash("aiassistant.excel.target", recipient.DeferredTarget.Scope, recipient.DeferredTarget.UnionID, recipient.DeferredTarget.SenderUserID)
			}
			intent, writeErr := s.outbound.WritePrivateMessageIntentWithin(tx, outboundport.PrivateMessageIntentCommand{
				DeferredTargetReference: deferredRef, SourceReference: sourceRef, CustomerID: recipient.CustomerID, StaffID: recipient.StaffID, PayloadReference: sourceRef,
				SourceDigest: effectport.Hash("aiassistant.source", sourceRef), TargetDigest: targetDigest, PayloadDigest: contents[i].Digest,
				PolicyHash: effectport.Hash("aiassistant.private-message.policy", "v1"), ReceiptKey: effectport.Hash("aiassistant.approval", strconv.FormatInt(int64(plan.ID), 10), strconv.FormatInt(int64(recipient.ID), 10), strconv.FormatInt(plan.Version, 10), string(contents[i].Digest)), Lane: lane})
			if writeErr != nil {
				return writeErr
			}
			intents = append(intents, intent)
		}
		decisionDigest := sha256.Sum256([]byte(command.IdempotencyKey))
		if err = s.store.SavePlanApproval(tx, aggregate.Projection, recipients, contents, intents, command.Actor.ID, decisionDigest, s.nowUTC()); err != nil {
			return err
		}
		if excelSnapshotKey != "" {
			recorder, ok := s.store.(interface {
				SaveExcelSnapshot(context.Context, aiassistantport.PlanID, string) error
			})
			if !ok {
				return ErrUnavailable
			}
			if err = recorder.SaveExcelSnapshot(tx, plan.ID, excelSnapshotKey); err != nil {
				return err
			}
		}
		result = aggregate.Projection
		payload, _ := json.Marshal(map[string]any{"plan_id": plan.ID, "eligible_count": len(recipients), "preview_digest": preview.PreviewDigest})
		if err = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventPlanApproved, AggregateID: plan.ID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: payload, OccurredAt: s.nowUTC()}); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"plan_id": plan.ID})
		_, err = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC())
		return err
	})
	return result, classify(err)
}

func (s *Service) ListEffects(ctx context.Context, planID aiassistantport.PlanID) ([]aiassistantport.EffectBinding, error) {
	if s == nil || planID < 1 {
		return nil, ErrInvalid
	}
	var items []aiassistantport.EffectBinding
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, err = s.store.ListEffectBindings(tx, planID)
		return err
	})
	return items, classify(err)
}

func (s *Service) ReconcileEffect(ctx context.Context, command aiassistantport.ReconcileEffectCommand) (aiassistantport.Recipient, error) {
	if s == nil || s.reconciler == nil || !command.Actor.Valid() || !validKey(command.IdempotencyKey) || command.EffectID == "" || command.Generation < 1 || command.Fence < 1 || !effectport.ValidDigest(command.EvidenceDigest) || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 500 {
		return aiassistantport.Recipient{}, ErrInvalid
	}
	var recipient aiassistantport.Recipient
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation("effect_reconcile", command.Actor, command.IdempotencyKey, digestJSON(command), s.nowUTC()))
		if err != nil {
			return err
		}
		if owned {
			err = s.reconciler.ReconcileUnknownWithin(tx, effectport.ReconcileCommand{EffectID: command.EffectID, ReceiptKey: effectport.Hash("aiassistant.reconcile", command.IdempotencyKey, command.Reason), EvidenceDigest: command.EvidenceDigest, ActorAdminUserID: command.Actor.ID, Generation: command.Generation, Fence: command.Fence})
			if err != nil {
				return err
			}
			recipient, err = s.store.GetRecipientByEffect(tx, command.EffectID)
			if err != nil {
				return err
			}
			eventPayload, _ := json.Marshal(map[string]any{"effect_id": command.EffectID, "generation": command.Generation, "fence": command.Fence, "evidence_digest": command.EvidenceDigest, "reason_present": true})
			if err = s.store.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventEffectReconciled, AggregateID: recipient.PlanID, RecipientID: recipient.ID, ActorID: command.Actor.ID, IdempotencyKey: command.IdempotencyKey, Payload: eventPayload, OccurredAt: s.nowUTC()}); err != nil {
				return err
			}
			snapshot, _ := json.Marshal(map[string]any{"effect_id": command.EffectID})
			if _, err = s.store.Complete(tx, receipt.ID, snapshot, s.nowUTC()); err != nil {
				return err
			}
		}
		recipient, err = s.store.GetRecipientByEffect(tx, command.EffectID)
		return err
	})
	return recipient, classify(err)
}

func (s *Service) validateApprovalFacts(ctx context.Context, recipients []aiassistantport.Recipient, contents []aiassistantport.ContentVersion) error {
	var excelCover effectport.Digest
	if len(recipients) != len(contents) {
		return ErrConflict
	}
	for i, r := range recipients {
		if r.ExecutionState != aiassistantport.ExecutionNotAccepted || r.ContentVersionID != contents[i].ID {
			return ErrConflict
		}
		if r.DeferredTarget != nil {
			if !r.DeferredTarget.Valid() {
				return ErrInvalid
			}
			for _, b := range contents[i].Blocks {
				if !b.Valid() {
					return ErrInvalid
				}
				if b.ExcelCard != nil {
					if !effectport.ValidDigest(b.ExcelCard.CoverDigest) {
						return ErrInvalid
					}
					if excelCover != "" && excelCover != b.ExcelCard.CoverDigest {
						return ErrInvalid
					}
					excelCover = b.ExcelCard.CoverDigest
				}
			}
			continue
		}
		customer, err := s.customers.CustomerSnapshot(ctx, r.CustomerID)
		if err != nil || customer.CanonicalID != r.CustomerID || customer.Status != customerdomain.StatusActive {
			return ErrConflict
		}
		staff, err := s.staff.StaffSnapshot(ctx, r.StaffID)
		if err != nil || !staff.Active {
			return ErrConflict
		}
		resolved, err := s.resolveBlocks(ctx, contents[i].Blocks)
		if err != nil {
			return err
		}
		for j := range resolved {
			if resolved[j].MaterialDigest != contents[i].Blocks[j].MaterialDigest {
				return ErrMaterialDrift
			}
		}
	}
	return nil
}

func approvalPreview(plan aiassistantport.Plan, recipients []aiassistantport.Recipient, contents []aiassistantport.ContentVersion) aiassistantport.ApprovalPreview {
	parts := []string{"aiassistant.approval-preview.v1", strconv.FormatInt(int64(plan.ID), 10), strconv.FormatInt(plan.Version, 10), string(effectport.Hash("aiassistant.private-message.policy", "v1"))}
	for i, r := range recipients {
		parts = append(parts, strconv.FormatInt(int64(r.ID), 10), strconv.FormatInt(r.Version, 10), strconv.FormatInt(int64(r.CustomerID), 10), strconv.FormatInt(r.StaffID, 10), strconv.FormatInt(int64(contents[i].ID), 10), string(contents[i].Digest))
	}
	return aiassistantport.ApprovalPreview{PlanID: plan.ID, PlanVersion: plan.Version, EligibleCount: len(recipients), PreviewDigest: effectport.Hash(parts...)}
}

func (s *Service) validateCanonicalRecipients(ctx context.Context, candidates []aiassistantport.RecipientCandidate) ([]aiassistantport.RecipientCandidate, error) {
	items := append([]aiassistantport.RecipientCandidate(nil), candidates...)
	for i := range items {
		if !items[i].Valid() || items[i].DeferredTarget != nil {
			return nil, ErrInvalid
		}
		for _, block := range items[i].Content {
			if block.ExcelCard != nil {
				return nil, ErrInvalid
			}
		}
		customer, err := s.customers.CustomerSnapshot(ctx, items[i].CustomerID)
		if err != nil || customer.CanonicalID != items[i].CustomerID || customer.Status != customerdomain.StatusActive {
			return nil, ErrConflict
		}
		staff, err := s.staff.StaffSnapshot(ctx, items[i].StaffID)
		if err != nil || !staff.Active {
			return nil, ErrConflict
		}
		resolved, err := s.resolveBlocks(ctx, items[i].Content)
		if err != nil {
			return nil, err
		}
		items[i].Content = resolved
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CustomerID < items[j].CustomerID })
	for i := 1; i < len(items); i++ {
		if items[i-1].CustomerID == items[i].CustomerID {
			return nil, ErrConflict
		}
	}
	return items, nil
}

func (s *Service) resolveBlocks(ctx context.Context, blocks []aiassistantport.ContentBlock) ([]aiassistantport.ContentBlock, error) {
	if len(blocks) == 0 || len(blocks) > aiassistantport.MaxMessagesPerTarget {
		return nil, ErrInvalid
	}
	resolved := append([]aiassistantport.ContentBlock(nil), blocks...)
	for i, block := range resolved {
		if !block.ValidInput() {
			return nil, ErrInvalid
		}
		if block.Kind == aiassistantport.ContentText || block.ExcelCard != nil {
			continue
		}
		value, err := s.materials.ResolveMaterial(ctx, block)
		if err != nil {
			return nil, err
		}
		if !value.Valid() || value.Kind != block.Kind || value.MaterialKind != block.MaterialKind {
			return nil, ErrMaterialDrift
		}
		if block.LegacySourceSystem == "" && value.MaterialID != block.MaterialID {
			return nil, ErrMaterialDrift
		}
		resolved[i] = value
	}
	return resolved, nil
}

func (s *Service) registerContentReferences(ctx context.Context, versionID aiassistantport.ContentVersionID, blocks []aiassistantport.ContentBlock) error {
	if versionID < 1 {
		return ErrInvalid
	}
	for index, block := range blocks {
		if block.Kind == aiassistantport.ContentText || block.ExcelCard != nil {
			continue
		}
		reference := effectport.Hash("aiassistant.content-version", strconv.FormatInt(int64(versionID), 10), strconv.Itoa(index), block.MaterialKind, strconv.FormatInt(block.MaterialID, 10), string(block.MaterialDigest))
		if err := s.materials.RegisterMaterialReference(ctx, block, reference); err != nil {
			return ErrMaterialDrift
		}
	}
	return nil
}

func (s *Service) enrich(ctx context.Context, recipients []aiassistantport.Recipient) error {
	for index := range recipients {
		if target := recipients[index].DeferredTarget; target != nil {
			recipients[index].CustomerName = target.UnionID
			recipients[index].OneIDLabel = "UnionID"
			recipients[index].StaffDisplayName = target.SenderUserID
			continue
		}
		customer, err := s.customers.CustomerSnapshot(ctx, recipients[index].CustomerID)
		if err != nil || customer.CanonicalID != recipients[index].CustomerID {
			return ErrUnavailable
		}
		staff, err := s.staff.StaffSnapshot(ctx, recipients[index].StaffID)
		if err != nil {
			return ErrUnavailable
		}
		recipients[index].CustomerName = customer.DisplayName
		recipients[index].OneIDLabel = customer.OneIDLabel
		recipients[index].StaffDisplayName = staff.DisplayName
	}
	return nil
}

func reservation(operation string, actor aiassistantport.Actor, key string, payload [32]byte, at time.Time) Reservation {
	return Reservation{Operation: operation, ActorScope: string(actor.Kind) + ":" + strconv.FormatInt(actor.ID, 10), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: payload, CreatedAt: at.UTC()}
}

func digestJSON(value any) [32]byte {
	payload, _ := json.Marshal(value)
	return sha256.Sum256(payload)
}

// identityPlanBusinessDigest deliberately excludes authentication transport
// fields. A valid retry must present a fresh nonce/timestamp but still map to
// the original business plan; content drift under the same idempotency key is
// rejected by the receipt's payload digest.
func identityPlanBusinessDigest(command IdentityPlanCommand) [32]byte {
	type target struct {
		Kind, Scope, Value, StaffWeComUserID string
		StaffID                              int64
		Content                              []aiassistantport.ContentBlock
	}
	targets := make([]target, 0, len(command.Targets))
	for _, item := range command.Targets {
		targets = append(targets, target{Kind: string(item.Reference.Kind), Scope: item.Reference.Scope, Value: item.Reference.Value, StaffID: item.StaffID, StaffWeComUserID: item.StaffWeComUserID, Content: item.Content})
	}
	return digestJSON(struct {
		Name, SourceKind string
		SourceDigest     effectport.Digest
		Targets          []target
	}{Name: command.Name, SourceKind: command.SourceKind, SourceDigest: command.SourceDigest, Targets: targets})
}

func receiptPlanID(raw json.RawMessage) aiassistantport.PlanID {
	var value struct {
		PlanID aiassistantport.PlanID `json:"plan_id"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.PlanID
}

func receiptIDs(raw json.RawMessage) (int64, int64) {
	var value struct {
		RecipientID int64 `json:"recipient_id"`
		ContentID   int64 `json:"content_version_id"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.RecipientID, value.ContentID
}

func encodePlanCursor(cursor Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursor.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(cursor.ID, 10)))
}

func decodePlanCursor(value string) (Cursor, error) {
	if value == "" {
		return Cursor{}, nil
	}
	if len(value) > 512 {
		return Cursor{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 {
		return Cursor{}, ErrInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return Cursor{}, ErrInvalid
	}
	return Cursor{UpdatedAt: at.UTC(), ID: id}, nil
}

func encodeIDCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}
func decodeIDCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	if len(value) > 512 {
		return 0, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 1 {
		return 0, ErrInvalid
	}
	return id, nil
}

func validIdentityPlan(command IdentityPlanCommand) bool {
	return command.Actor.Valid() && validKey(command.IdempotencyKey) && strings.TrimSpace(command.Name) != "" && len(command.Name) <= 200 && strings.TrimSpace(command.SourceKind) != "" && len(command.SourceKind) <= 80 && effectport.ValidDigest(command.SourceDigest) && len(command.Targets) > 0 && len(command.Targets) <= aiassistantport.MaxRecipients && !command.OccurredAt.IsZero() && strings.TrimSpace(command.IntegrationKey) == command.IntegrationKey && command.IntegrationKey != "" && len(command.IntegrationKey) <= 128 && len(command.Nonce) >= 16 && len(command.Nonce) <= 128 && strings.TrimSpace(command.Nonce) == command.Nonce && command.ExpiresAt.After(command.OccurredAt)
}
func validKey(value string) bool {
	return len(value) >= 8 && len(value) <= 200 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func validPlanState(value aiassistantport.PlanState) bool {
	switch value {
	case "", aiassistantport.PlanPendingReview, aiassistantport.PlanPartiallyApproved, aiassistantport.PlanApproved, aiassistantport.PlanRejected, aiassistantport.PlanDispatching, aiassistantport.PlanNeedsAttention, aiassistantport.PlanCompletedWithFailures, aiassistantport.PlanCompleted:
		return true
	default:
		return false
	}
}
func validReviewState(value aiassistantport.ReviewState) bool {
	return value == "" || value == aiassistantport.ReviewPending || value == aiassistantport.ReviewApproved || value == aiassistantport.ReviewRejected || value == aiassistantport.ReviewIneligible
}
func (s *Service) nowUTC() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, aiassistantdomain.ErrInvalidPlan):
		return ErrInvalid
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrIdempotencyConflict):
		return ErrIdempotencyConflict
	case errors.Is(err, ErrConflict), errors.Is(err, aiassistantdomain.ErrPlanConflict):
		return ErrConflict
	case errors.Is(err, ErrMaterialDrift):
		return ErrMaterialDrift
	default:
		return err
	}
}

var _ aiassistantport.Intake = (*Service)(nil)
var _ aiassistantport.TransactionalIntake = (*Service)(nil)
var _ aiassistantport.Reader = (*Service)(nil)
var _ aiassistantport.Reviewer = (*Service)(nil)
