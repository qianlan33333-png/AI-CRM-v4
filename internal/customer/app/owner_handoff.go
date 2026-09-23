package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var (
	ErrOwnerHandoffInvalid   = errors.New("owner handoff command invalid")
	ErrOwnerHandoffExpired   = errors.New("owner handoff preview expired")
	ErrOwnerHandoffForbidden = errors.New("owner handoff target unavailable")
	ErrOwnerHandoffDrift     = errors.New("owner handoff preview changed")
)

type ownerHandoffTransferStore interface {
	LoadOwnerHandoffTransferRead(context.Context, int64, string) (customerport.OwnerHandoffTransferRead, error)
	RecordOwnerHandoffTransferResult(context.Context, customerport.OwnerHandoffTransferRead, string, []customerport.OwnerHandoffTransferObservation) (customerport.OwnerHandoffBatch, int, error)
}

type ownerHandoffTransferReader interface {
	TransferResult(context.Context, string, string, string) (wecomport.CustomerTransferResult, error)
}

type OwnerHandoffStore interface {
	CreateOwnerHandoffPreview(context.Context, customerport.OwnerHandoffPreviewRecord) (customerport.OwnerHandoffPreview, error)
	LoadOwnerHandoffPreview(context.Context, string, bool) (customerport.OwnerHandoffPreviewRecord, error)
	CreateLocalOnlyOwnerHandoffBatch(context.Context, customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error)
	CreateWeComOwnerHandoffBatch(context.Context, customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error)
	BindOwnerHandoffEffect(context.Context, customerport.OwnerHandoffEffectBinding) error
	OwnerHandoffBatchByIdempotency(context.Context, int64, string) (customerport.OwnerHandoffBatch, [32]byte, bool, error)
	LoadOwnerHandoffBatchSegment(context.Context, string, int64, int) (customerport.OwnerHandoffBatchSegment, error)
	SetOwnerHandoffLineState(context.Context, string, int64, string) error
	RecomputeOwnerHandoffBatchState(context.Context, string) error
	// LockOwnerHandoffCustomersAndRejectActiveWeCom serializes every pending
	// transfer_customer request for each canonical customer. It is called inside
	// the confirmation UoW before accepting any external effect.
	LockOwnerHandoffCustomersAndRejectActiveWeCom(context.Context, []customerdomain.CustomerID) error
	LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error)
	AssignLocalOwner(context.Context, customerdomain.CustomerID, int64, int64, string, time.Time) (customerport.LocalOwner, error)
}

type ownerHandoffStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}

type ownerHandoffBatchEnqueuer interface {
	EnqueueOwnerHandoffBatchWithin(context.Context, string, int64) error
}

type ownerHandoffCandidateDiscoverer interface {
	DiscoverOwnerHandoffCustomerIDs(context.Context, customerport.OwnerHandoffMode, int64, string, int) ([]customerdomain.CustomerID, error)
}

const ownerHandoffBatchSegmentSize = 100

type OwnerHandoffService struct {
	uow      platformport.UnitOfWork
	store    OwnerHandoffStore
	staff    ownerHandoffStaffReader
	resolver customerport.OwnerHandoffCandidateResolver
	audit    interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	outbox               platformoutbox.Appender
	effects              effectport.TransactionalAccepter
	batchJobs            ownerHandoffBatchEnqueuer
	transferResults      ownerHandoffTransferReader
	wecomProviderEnabled bool
	now                  func() time.Time
	newID                func() (string, error)
}

func NewOwnerHandoffService(uow platformport.UnitOfWork, store OwnerHandoffStore, staff ownerHandoffStaffReader, resolver customerport.OwnerHandoffCandidateResolver, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}, outbox platformoutbox.Appender) (*OwnerHandoffService, error) {
	if uow == nil || store == nil || staff == nil || resolver == nil || audit == nil || outbox == nil {
		return nil, errors.New("owner handoff dependencies are required")
	}
	return &OwnerHandoffService{uow: uow, store: store, staff: staff, resolver: resolver, audit: audit, outbox: outbox, now: time.Now, newID: ownerHandoffID}, nil
}

// SetExternalEffectAccepter installs the established EER transactional port.
// Provider mode fails closed until composition supplies this dependency; it
// never silently degrades to local_only.
func (service *OwnerHandoffService) SetExternalEffectAccepter(accepter effectport.TransactionalAccepter) error {
	if service == nil || accepter == nil {
		return errors.New("owner handoff external-effects accepter is required")
	}
	service.effects = accepter
	return nil
}

func (service *OwnerHandoffService) SetWeComProviderEnabled(enabled bool) {
	if service != nil {
		service.wecomProviderEnabled = enabled
	}
}

// SetBatchEnqueuer supplies the existing River-backed internal-job port. Both
// modes fail closed without it so a 20k confirmation can never commit an
// unbounded synchronous mutation.
func (service *OwnerHandoffService) SetBatchEnqueuer(enqueuer ownerHandoffBatchEnqueuer) error {
	if service == nil || enqueuer == nil {
		return errors.New("owner handoff batch enqueuer is required")
	}
	service.batchJobs = enqueuer
	return nil
}

// SetTransferResultReader installs the existing WeCom-owned, read-only
// protocol leaf. Composition calls this during startup once the provider
// adapter exists; the endpoint fails closed until then.
func (service *OwnerHandoffService) SetTransferResultReader(reader ownerHandoffTransferReader) error {
	if service == nil || reader == nil {
		return errors.New("owner handoff transfer-result reader is required")
	}
	service.transferResults = reader
	return nil
}

// PreviewAllOwnerHandoff derives the all-range from Customer and WeCom owned
// read ports, then freezes it through the same preview path as Excel selection.
func (service *OwnerHandoffService) PreviewAllOwnerHandoff(ctx context.Context, command customerport.OwnerHandoffPreviewCommand) (customerport.OwnerHandoffPreview, error) {
	discoverer, ok := service.resolver.(ownerHandoffCandidateDiscoverer)
	if !ok {
		return customerport.OwnerHandoffPreview{}, ErrOwnerHandoffForbidden
	}
	var ids []customerdomain.CustomerID
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		ids, err = discoverer.DiscoverOwnerHandoffCustomerIDs(tx, command.Mode, command.SourceStaffID, command.CorpScope, 20000)
		return err
	})
	if err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	command.CustomerIDs = ids
	return service.PreviewOwnerHandoff(ctx, command)
}

func (service *OwnerHandoffService) PreviewOwnerHandoff(ctx context.Context, command customerport.OwnerHandoffPreviewCommand) (customerport.OwnerHandoffPreview, error) {
	if err := validOwnerHandoffPreview(command); err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	var out customerport.OwnerHandoffPreview
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		source, err := service.staff.UserByID(txctx, command.SourceStaffID, false)
		if err != nil {
			return ErrOwnerHandoffForbidden
		}
		// Legacy local_only permits a source employee which has since been
		// disabled. The target is the only party that must remain active.
		if source.ID != command.SourceStaffID {
			return ErrOwnerHandoffForbidden
		}
		target, err := service.staff.UserByID(txctx, command.TargetStaffID, false)
		if err != nil || !target.Active {
			return ErrOwnerHandoffForbidden
		}
		candidates, err := service.resolver.ResolveOwnerHandoffCandidates(txctx, command.Mode, command.SourceStaffID, command.TargetStaffID, command.CorpScope, command.CustomerIDs)
		if err != nil {
			return err
		}
		if len(candidates) != len(command.CustomerIDs) {
			return ErrOwnerHandoffDrift
		}
		ready := false
		for _, candidate := range candidates {
			if candidate.State == "ready" {
				ready = true
				break
			}
		}
		if !ready {
			return ErrOwnerHandoffForbidden
		}
		for index, candidate := range candidates {
			if candidate.CustomerID != command.CustomerIDs[index] {
				return ErrOwnerHandoffDrift
			}
		}
		id, err := service.newID()
		if err != nil {
			return err
		}
		requestDigest := ownerHandoffPreviewDigest(command, candidates)
		draft := customerport.OwnerHandoffPreviewRecord{ActorAdminUserID: command.ActorAdminUserID, Preview: customerport.OwnerHandoffPreview{ID: id, Mode: command.Mode, SourceStaffID: command.SourceStaffID, TargetStaffID: command.TargetStaffID, CorpScope: command.CorpScope, Hash: hex.EncodeToString(requestDigest[:]), ConfirmationPhrase: command.ConfirmationPhrase, ExpiresAt: service.now().UTC().Add(30 * time.Minute)}, WelcomeMessage: command.WelcomeMessage, Candidates: candidates, RequestDigest: requestDigest}
		out, err = service.store.CreateOwnerHandoffPreview(txctx, draft)
		return err
	})
	return out, err
}

func (service *OwnerHandoffService) ConfirmOwnerHandoff(ctx context.Context, command customerport.OwnerHandoffConfirmCommand) (customerport.OwnerHandoffBatch, error) {
	if command.ActorAdminUserID < 1 || command.PreviewID == "" || command.PreviewHash == "" || strings.TrimSpace(command.ConfirmationPhrase) == "" || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey || len(command.IdempotencyKey) < 8 {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffInvalid
	}
	var out customerport.OwnerHandoffBatch
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		confirmDigest := sha256.Sum256([]byte(command.PreviewID + "\x00" + command.PreviewHash + "\x00" + command.ConfirmationPhrase))
		if prior, priorDigest, found, findErr := service.store.OwnerHandoffBatchByIdempotency(txctx, command.ActorAdminUserID, command.IdempotencyKey); findErr != nil {
			return findErr
		} else if found {
			if priorDigest != confirmDigest {
				return ErrOwnerHandoffDrift
			}
			out = prior
			return nil
		}
		draft, err := service.store.LoadOwnerHandoffPreview(txctx, command.PreviewID, true)
		if err != nil {
			return err
		}
		if draft.ActorAdminUserID != command.ActorAdminUserID || draft.Preview.Hash != command.PreviewHash || draft.Preview.ConfirmationPhrase != command.ConfirmationPhrase {
			return ErrOwnerHandoffDrift
		}
		if draft.ExecutedBatchID != "" {
			// The original key was handled above. A second key for this frozen
			// preview must fail before batch/EER creation rather than rely on a
			// database unique violation as its only duplicate-write protection.
			return ErrOwnerHandoffDrift
		}
		if !service.now().UTC().Before(draft.Preview.ExpiresAt) {
			return ErrOwnerHandoffExpired
		}
		target, err := service.staff.UserByID(txctx, draft.Preview.TargetStaffID, false)
		if err != nil || !target.Active {
			return ErrOwnerHandoffForbidden
		}
		customerIDs := make([]customerdomain.CustomerID, 0, len(draft.Candidates))
		for _, candidate := range draft.Candidates {
			customerIDs = append(customerIDs, candidate.CustomerID)
		}
		current, resolveErr := service.resolver.ResolveOwnerHandoffCandidates(txctx, draft.Preview.Mode, draft.Preview.SourceStaffID, draft.Preview.TargetStaffID, draft.Preview.CorpScope, customerIDs)
		if resolveErr != nil || !sameOwnerHandoffCandidates(draft.Candidates, current) {
			return ErrOwnerHandoffDrift
		}
		if service.batchJobs == nil {
			return ErrOwnerHandoffForbidden
		}
		if draft.Preview.Mode == customerport.OwnerHandoffWeComThenCRM {
			// Take Customer-owned row locks before reading pending lines. A different
			// preview may still contain the same frozen relation while its first
			// transfer is queued or outcome_unknown; accepting a second effect would
			// issue a duplicate, potentially conflicting WeCom transfer.
			if lockErr := service.store.LockOwnerHandoffCustomersAndRejectActiveWeCom(txctx, customerIDs); lockErr != nil {
				return lockErr
			}
			if service.effects == nil || !service.wecomProviderEnabled {
				return ErrOwnerHandoffForbidden
			}
			lines := make([]customerport.OwnerHandoffLine, 0, len(draft.Candidates))
			for _, candidate := range draft.Candidates {
				state := candidate.State
				if state == "ready" {
					state = "queued"
				}
				lines = append(lines, customerport.OwnerHandoffLine{Line: int64(len(lines) + 1), CustomerID: candidate.CustomerID, State: state})
			}
			batch, createErr := service.store.CreateWeComOwnerHandoffBatch(txctx, customerport.OwnerHandoffBatchRecord{Preview: draft, ActorID: command.ActorAdminUserID, Idempotency: command.IdempotencyKey, RequestDigest: confirmDigest, Lines: lines})
			if createErr != nil {
				return createErr
			}
			if enqueueErr := service.batchJobs.EnqueueOwnerHandoffBatchWithin(txctx, batch.ID, 0); enqueueErr != nil {
				return enqueueErr
			}
			out = batch
			return nil
		}
		if draft.Preview.Mode != customerport.OwnerHandoffLocalOnly {
			return ErrOwnerHandoffDrift
		}
		lines := make([]customerport.OwnerHandoffLine, 0, len(draft.Candidates))
		for _, candidate := range draft.Candidates {
			state := candidate.State
			if state == "ready" {
				state = "queued"
			}
			lines = append(lines, customerport.OwnerHandoffLine{Line: int64(len(lines) + 1), CustomerID: candidate.CustomerID, State: state})
		}
		batch, createErr := service.store.CreateLocalOnlyOwnerHandoffBatch(txctx, customerport.OwnerHandoffBatchRecord{Preview: draft, ActorID: command.ActorAdminUserID, Idempotency: command.IdempotencyKey, RequestDigest: confirmDigest, Lines: lines})
		if createErr != nil {
			return createErr
		}
		if enqueueErr := service.batchJobs.EnqueueOwnerHandoffBatchWithin(txctx, batch.ID, 0); enqueueErr != nil {
			return enqueueErr
		}
		out = batch
		return nil

	})
	return out, err
}

// ProcessOwnerHandoffBatch accepts exactly one durable 100-line segment. It
// never performs provider I/O: provider work remains EER-owned and occurs only
// after this Customer UoW commits. River retries the same segment and its
// receipt keys, so a restart cannot create a second transfer_customer request.
func (service *OwnerHandoffService) ProcessOwnerHandoffBatch(ctx context.Context, batchID string, segment int64) error {
	if service == nil || batchID == "" || segment < 0 || service.batchJobs == nil {
		return ErrOwnerHandoffInvalid
	}
	return service.uow.Within(ctx, func(txctx context.Context) error {
		work, err := service.store.LoadOwnerHandoffBatchSegment(txctx, batchID, segment, ownerHandoffBatchSegmentSize)
		if err != nil {
			return err
		}
		if work.BatchID == "" {
			return ErrOwnerHandoffDrift
		}
		if work.Mode == customerport.OwnerHandoffWeComThenCRM {
			if service.effects == nil || !service.wecomProviderEnabled {
				return ErrOwnerHandoffForbidden
			}
			if len(work.Lines) > 0 {
				ordinal := segment + 1
				envelope, envelopeErr := ownerHandoffSubBatchEnvelope(work.BatchID, ordinal, work.Lines)
				if envelopeErr != nil {
					return envelopeErr
				}
				accept := effectport.AcceptCommand{ReceiptKey: effectport.Hash("customer-owner-handoff.accept.v2", work.BatchID, strconv.FormatInt(ordinal, 10)), Envelope: envelope}
				projection, receipt, acceptErr := service.effects.AcceptAndQueueWithin(txctx, accept)
				if acceptErr != nil {
					return acceptErr
				}
				lineNumbers := make([]int64, 0, len(work.Lines))
				for _, item := range work.Lines {
					lineNumbers = append(lineNumbers, item.Line)
				}
				if bindErr := service.store.BindOwnerHandoffEffect(txctx, customerport.OwnerHandoffEffectBinding{BatchID: work.BatchID, SubBatchOrdinal: ordinal, Lines: lineNumbers, EffectID: projection.ID, ReceiptID: receipt.ID, SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadRefDigest: string(envelope.PayloadDigest), PolicyRefDigest: string(envelope.PolicyVersionHash)}); bindErr != nil {
					return bindErr
				}
				for _, item := range work.Lines {
					if factErr := service.appendProviderQueuedFacts(txctx, work.ActorID, work.PreviewID, item.OwnerHandoffLine, service.now().UTC()); factErr != nil {
						return factErr
					}
				}
			}
		} else if work.Mode == customerport.OwnerHandoffLocalOnly {
			target, targetErr := service.staff.UserByID(txctx, work.TargetStaffID, false)
			if targetErr != nil || !target.Active {
				for _, item := range work.Lines {
					if stateErr := service.store.SetOwnerHandoffLineState(txctx, work.BatchID, item.Line, "cas_conflict"); stateErr != nil {
						return stateErr
					}
				}
			} else {
				for _, item := range work.Lines {
					line := item.OwnerHandoffLine
					owner, found, readErr := service.store.LocalOwner(txctx, line.CustomerID, true)
					state := "local_updated"
					if readErr != nil || (found && owner.Version != item.ExpectedLocalVersion) || (!found && item.ExpectedLocalVersion != 0) {
						state = "cas_conflict"
					} else if _, assignErr := service.store.AssignLocalOwner(txctx, line.CustomerID, work.TargetStaffID, item.ExpectedLocalVersion, "owner_handoff_local_only", service.now().UTC()); assignErr != nil {
						if !errors.Is(assignErr, customerport.ErrOwnerHandoffConflict) {
							return assignErr
						}
						state = "cas_conflict"
					}
					if stateErr := service.store.SetOwnerHandoffLineState(txctx, work.BatchID, line.Line, state); stateErr != nil {
						return stateErr
					}
					if state == "local_updated" {
						line.State = state
						if factErr := service.appendLocalOnlyFacts(txctx, work.ActorID, work.PreviewID, line, service.now().UTC()); factErr != nil {
							return factErr
						}
					}
				}
			}
		} else {
			return ErrOwnerHandoffDrift
		}
		if err = service.store.RecomputeOwnerHandoffBatchState(txctx, work.BatchID); err != nil {
			return err
		}
		if work.HasNext {
			return service.batchJobs.EnqueueOwnerHandoffBatchWithin(txctx, work.BatchID, segment+1)
		}
		return nil
	})
}

// RefreshOwnerHandoffTransferResult executes one read-only documented result
// page outside PostgreSQL, then records only safe status/time projections in a
// fresh UoW. It never retries transfer_customer or changes a local owner.
func (service *OwnerHandoffService) RefreshOwnerHandoffTransferResult(ctx context.Context, command customerport.OwnerHandoffTransferResultCommand) (customerport.OwnerHandoffBatch, error) {
	if service == nil || command.ActorAdminUserID < 1 || strings.TrimSpace(command.BatchID) != command.BatchID || command.BatchID == "" || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey || len(command.IdempotencyKey) < 8 || !service.wecomProviderEnabled || service.transferResults == nil {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffForbidden
	}
	store, ok := service.store.(ownerHandoffTransferStore)
	if !ok {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffForbidden
	}
	var read customerport.OwnerHandoffTransferRead
	if err := service.uow.Within(ctx, func(txctx context.Context) error {
		var loadErr error
		read, loadErr = store.LoadOwnerHandoffTransferRead(txctx, command.ActorAdminUserID, command.BatchID)
		return loadErr
	}); err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	providerResult, err := service.transferResults.TransferResult(ctx, read.SourceUserID, read.TargetUserID, read.Cursor)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	observations := make([]customerport.OwnerHandoffTransferObservation, 0, len(providerResult.Observations))
	for _, observation := range providerResult.Observations {
		observations = append(observations, customerport.OwnerHandoffTransferObservation{ExternalUserID: observation.ExternalUserID, Status: observation.Status, TakeoverTime: observation.TakeoverTime})
	}
	var out customerport.OwnerHandoffBatch
	var observed int
	err = service.uow.Within(ctx, func(txctx context.Context) error {
		var recordErr error
		out, observed, recordErr = store.RecordOwnerHandoffTransferResult(txctx, read, providerResult.Cursor, observations)
		if recordErr != nil {
			return recordErr
		}
		return service.appendTransferResultFacts(txctx, command.ActorAdminUserID, command.BatchID, command.IdempotencyKey, observed, service.now().UTC())
	})
	return out, err
}

func (service *OwnerHandoffService) appendTransferResultFacts(ctx context.Context, actorID int64, batchID, readbackKey string, observed int, at time.Time) error {
	keyDigest := sha256.Sum256([]byte(readbackKey))
	key, err := idempotency.Parse("customer-owner-handoff-result:" + batchID + ":" + hex.EncodeToString(keyDigest[:8]))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"result": "observed", "observed_count": observed})
	if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.owner_handoff.transfer_result_observed", ActorType: "admin", ActorID: int64String(actorID), ResourceType: "customer_owner_handoff_batch", ResourceID: batchID, Payload: payload, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err = service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer_owner_handoff_batch", AggregateID: batchID, Type: "customer.owner_handoff.transfer_result_observed.v1", Version: 1, IdempotencyKey: string(key), Payload: payload, OccurredAt: at})
	return err
}

func (service *OwnerHandoffService) appendProviderQueuedFacts(ctx context.Context, actorID int64, previewID string, line customerport.OwnerHandoffLine, at time.Time) error {
	key, err := idempotency.Parse("customer-owner-handoff-provider:" + previewID + ":" + int64String(line.Line))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"mode": "wecom_then_crm", "result": "queued"})
	if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.owner_handoff.wecom_queued", ActorType: "admin", ActorID: int64String(actorID), ResourceType: "customer", ResourceID: int64String(int64(line.CustomerID)), Payload: payload, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err = service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer", AggregateID: int64String(int64(line.CustomerID)), Type: "customer.owner_handoff.wecom_queued.v1", Version: 1, IdempotencyKey: string(key), Payload: payload, OccurredAt: at})
	return err
}

func (service *OwnerHandoffService) appendLocalOnlyFacts(ctx context.Context, actorID int64, previewID string, line customerport.OwnerHandoffLine, at time.Time) error {
	key, err := idempotency.Parse("customer-owner-handoff-local:" + previewID + ":" + int64String(line.Line))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"mode": "local_only", "result": line.State})
	if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.owner_handoff.local_updated", ActorType: "admin", ActorID: int64String(actorID), ResourceType: "customer", ResourceID: int64String(int64(line.CustomerID)), Payload: payload, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err = service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer", AggregateID: int64String(int64(line.CustomerID)), Type: "customer.owner_handoff.local_updated.v1", Version: 1, IdempotencyKey: string(key), Payload: payload, OccurredAt: at})
	return err
}

func sameOwnerHandoffCandidates(frozen, current []customerport.OwnerHandoffCandidate) bool {
	if len(frozen) != len(current) {
		return false
	}
	for index := range frozen {
		if frozen[index].CustomerID != current[index].CustomerID || frozen[index].ExpectedLocalOwnerID != current[index].ExpectedLocalOwnerID || frozen[index].ExpectedLocalVersion != current[index].ExpectedLocalVersion || frozen[index].State != current[index].State || frozen[index].Reason != current[index].Reason || frozen[index].RelationshipDigest != current[index].RelationshipDigest || frozen[index].SourceUserID != current[index].SourceUserID || frozen[index].TargetUserID != current[index].TargetUserID || frozen[index].ExternalUserID != current[index].ExternalUserID {
			return false
		}
	}
	return true
}

func validOwnerHandoffPreview(command customerport.OwnerHandoffPreviewCommand) error {
	if command.ActorAdminUserID < 1 || (command.Mode != customerport.OwnerHandoffLocalOnly && command.Mode != customerport.OwnerHandoffWeComThenCRM) || command.SourceStaffID < 1 || command.TargetStaffID < 1 || command.SourceStaffID == command.TargetStaffID || !strings.HasPrefix(command.CorpScope, "wecom-corp:") || len(command.CustomerIDs) == 0 || len(command.CustomerIDs) > 20000 || strings.TrimSpace(command.ConfirmationPhrase) == "" || len([]rune(command.WelcomeMessage)) > 4000 {
		return ErrOwnerHandoffInvalid
	}
	seen := make(map[customerdomain.CustomerID]struct{}, len(command.CustomerIDs))
	for _, customerID := range command.CustomerIDs {
		if customerID < 1 {
			return ErrOwnerHandoffInvalid
		}
		if _, exists := seen[customerID]; exists {
			return ErrOwnerHandoffInvalid
		}
		seen[customerID] = struct{}{}
	}
	return nil
}

func ownerHandoffPreviewDigest(command customerport.OwnerHandoffPreviewCommand, candidates []customerport.OwnerHandoffCandidate) [32]byte {
	parts := []string{"owner-handoff-preview-v1", string(command.Mode), int64String(command.SourceStaffID), int64String(command.TargetStaffID), command.CorpScope, command.WelcomeMessage, command.ConfirmationPhrase}
	for _, customerID := range command.CustomerIDs {
		parts = append(parts, "requested", int64String(int64(customerID)))
	}
	for _, candidate := range candidates {
		parts = append(parts, "frozen", int64String(int64(candidate.CustomerID)), int64String(candidate.ExpectedLocalOwnerID), int64String(candidate.ExpectedLocalVersion), candidate.State, candidate.Reason, hex.EncodeToString(candidate.RelationshipDigest[:]))
		for _, value := range []string{candidate.SourceUserID, candidate.TargetUserID, candidate.ExternalUserID} {
			if value == "" {
				parts = append(parts, "")
			} else {
				digest := sha256.Sum256([]byte(value))
				parts = append(parts, hex.EncodeToString(digest[:]))
			}
		}
	}
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func int64String(value int64) string { return strconv.FormatInt(value, 10) }

func ownerHandoffSubBatchEnvelope(batchID string, ordinal int64, lines []customerport.OwnerHandoffSegmentLine) (effectport.Envelope, error) {
	if batchID == "" || ordinal < 1 || len(lines) == 0 || len(lines) > ownerHandoffBatchSegmentSize {
		return effectport.Envelope{}, ErrOwnerHandoffDrift
	}
	sources := []string{"customer-owner-handoff.subbatch.v1", batchID, strconv.FormatInt(ordinal, 10)}
	targets := append([]string(nil), sources...)
	payloads := append([]string(nil), sources...)
	policies := append([]string(nil), sources...)
	for index, line := range lines {
		if line.Line < 1 || (line.Line-1)/ownerHandoffBatchSegmentSize+1 != ordinal || (index > 0 && lines[index-1].Line >= line.Line) {
			return effectport.Envelope{}, ErrOwnerHandoffDrift
		}
		sources = append(sources, strconv.FormatInt(line.Line, 10), hex.EncodeToString(line.SourceSnapshotDigest[:]))
		targets = append(targets, strconv.FormatInt(line.Line, 10), hex.EncodeToString(line.TargetSnapshotDigest[:]))
		payloads = append(payloads, strconv.FormatInt(line.Line, 10), hex.EncodeToString(line.PayloadSnapshotDigest[:]))
		policies = append(policies, strconv.FormatInt(line.Line, 10), hex.EncodeToString(line.PolicySnapshotDigest[:]))
	}
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerOwnerHandoff, SourceRefDigest: effectport.Hash(sources...), TargetRefDigest: effectport.Hash(targets...), PayloadDigest: effectport.Hash(payloads...), PolicyVersionHash: effectport.Hash(policies...)}, nil
}

func ownerHandoffID() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "owner_handoff_" + hex.EncodeToString(bytes), nil
}

var _ customerport.OwnerHandoffService = (*OwnerHandoffService)(nil)
