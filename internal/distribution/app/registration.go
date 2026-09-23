package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type registrationStore interface {
	ActiveAgreementWithin(context.Context) (distributionstore.Agreement, error)
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	InsertDistributorWithin(context.Context, int64, string, string, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	UpdateReceiverReadinessWithin(context.Context, int64, int64, distributionport.ReceiverReadiness, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	LockOperationReceiptWithin(context.Context, string, string, string) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (distributionstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// RegistrationService owns a Distributor record. Receiver preparation is a
// separately retryable Payment intent: an unavailable Payment account never
// silently prevents a customer from registering or causes a duplicate public
// distributor number.
type RegistrationService struct {
	uow        platformport.UnitOfWork
	store      registrationStore
	settlement paymentport.DistributionSettlementPort
	now        func() time.Time
}

func NewRegistrationService(uow platformport.UnitOfWork, store registrationStore, settlement paymentport.DistributionSettlementPort) (*RegistrationService, error) {
	if uow == nil || store == nil || settlement == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &RegistrationService{uow: uow, store: store, settlement: settlement, now: time.Now}, nil
}

func (s *RegistrationService) CurrentAgreement(ctx context.Context) (distributionport.Agreement, error) {
	if s == nil || s.uow == nil || s.store == nil {
		return distributionport.Agreement{}, distributionport.ErrUnavailable
	}
	var agreement distributionstore.Agreement
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		agreement, err = s.store.ActiveAgreementWithin(tx)
		return err
	})
	if err != nil {
		return distributionport.Agreement{}, err
	}
	return distributionport.Agreement{Version: agreement.Version, Content: agreement.Content}, nil
}

func (s *RegistrationService) Profile(ctx context.Context, actor distributionport.TrustedSessionActor) (distributionport.DistributorProfile, error) {
	if s == nil || s.uow == nil || s.store == nil || s.settlement == nil || !actor.Valid() {
		return distributionport.DistributorProfile{}, distributionport.ErrUnauthorized
	}
	capability, err := s.settlement.SettlementCapability(ctx)
	if err != nil {
		return distributionport.DistributorProfile{}, err
	}
	settlement := distributionport.SettlementCapability{Enabled: capability.Enabled, Reason: safeSettlementCapabilityReason(capability)}
	var agreement distributionstore.Agreement
	var distributor distributiondomain.Distributor
	var readiness distributionport.ReceiverReadiness
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		agreement, err = s.store.ActiveAgreementWithin(tx)
		if err != nil {
			return err
		}
		distributor, readiness, err = s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		if errors.Is(err, distributionport.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		return distributionport.DistributorProfile{}, err
	}
	if distributor.ID == 0 {
		return distributionport.DistributorProfile{CurrentAgreementVersion: agreement.Version, RegistrationRequired: true, Settlement: settlement}, nil
	}
	return distributionport.DistributorProfile{Distributor: distributor, Receiver: readiness, Settlement: settlement, CurrentAgreementVersion: agreement.Version}, nil
}

// SyncProfitSharingReceiverStatusWithin projects a Payment-owned transition
// into Distribution's existing receiver snapshot. Payment invokes it in the
// same PostgreSQL Unit of Work as the owned receiver update, so public/admin
// Distribution reads never stay on a completed worker's stale state. The
// update is skipped when the safe projection is unchanged and therefore does
// not turn ordinary profile GETs into writes.
func (s *RegistrationService) SyncProfitSharingReceiverStatusWithin(ctx context.Context, paymentReadiness paymentport.ReceiverReadiness) error {
	if s == nil || s.store == nil || paymentReadiness.CustomerID < 1 || paymentReadiness.Reference == "" || paymentReadiness.AppID == "" || paymentReadiness.UpdatedAt.IsZero() {
		return distributionport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	distributor, current, err := s.store.ReadDistributorByCustomerWithin(ctx, paymentReadiness.CustomerID, true)
	if errors.Is(err, distributionport.ErrNotFound) {
		// Payment receivers are normally prepared from a Distributor. A missing
		// legacy projection has no Distribution row to update and must not make a
		// Payment completion retry or manufacture a distributor.
		return nil
	}
	if err != nil {
		return err
	}
	next := distributionport.ReceiverReadiness{Reference: paymentReadiness.Reference, AppID: paymentReadiness.AppID, Ready: paymentReadiness.Ready, CheckedAt: paymentReadiness.UpdatedAt.UTC()}
	if !next.Ready {
		next.Reason = safeReceiverReason(paymentReadiness)
	}
	if current.Reference == next.Reference && current.AppID == next.AppID && current.Ready == next.Ready && current.Reason == next.Reason {
		return nil
	}
	updated, persisted, err := s.store.UpdateReceiverReadinessWithin(ctx, distributor.ID, distributor.Version, next, next.CheckedAt)
	if err != nil {
		return err
	}
	return s.store.AppendAuditWithin(ctx, "distribution.receiver_status_synchronized.v1", "distributor", updated.ID, "payment:external-effect", map[string]any{"receiver_state": paymentReadiness.State, "failure_class": paymentReadiness.FailureClass, "ready": persisted.Ready, "receiver_reference": persisted.Reference}, next.CheckedAt)
}

func (s *RegistrationService) Register(ctx context.Context, command distributionport.RegisterCommand) (distributionport.DistributorProfile, error) {
	if s == nil || s.uow == nil || s.store == nil || !command.Actor.Valid() || command.AgreementVersion != strings.TrimSpace(command.AgreementVersion) || command.AgreementVersion == "" || len(command.AgreementVersion) > 100 || command.IdempotencyKey != strings.TrimSpace(command.IdempotencyKey) || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 200 {
		return distributionport.DistributorProfile{}, distributionport.ErrConflict
	}
	actorScope := registrationActorScope(command.Actor)
	payloadDigest := registrationPayloadDigest(command.AgreementVersion)
	var profile distributionport.DistributorProfile
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if err := s.store.LockOperationReceiptWithin(tx, "register", actorScope, command.IdempotencyKey); err != nil {
			return err
		}
		agreement, err := s.store.ActiveAgreementWithin(tx)
		if err != nil {
			return err
		}
		if replay, found, replayErr := s.registrationReplay(tx, command, actorScope, payloadDigest, agreement.Version); replayErr != nil {
			return replayErr
		} else if found {
			profile = replay
			return nil
		}
		if agreement.Version != command.AgreementVersion {
			return distributionport.ErrConflict
		}
		existing, readiness, err := s.store.ReadDistributorByCustomerWithin(tx, command.Actor.CustomerID, true)
		if err == nil {
			profile, err = s.registrationRecordExisting(tx, command, actorScope, payloadDigest, agreement.Version, existing, readiness)
			if errors.Is(err, distributionport.ErrConflict) {
				var found bool
				profile, found, err = s.registrationReplay(tx, command, actorScope, payloadDigest, agreement.Version)
				if err == nil && found {
					return nil
				}
				if err == nil {
					err = distributionport.ErrConflict
				}
			}
			if err != nil {
				return err
			}
			return nil
		}
		if !errors.Is(err, distributionport.ErrNotFound) {
			return err
		}
		now := s.now().UTC()
		var saved distributiondomain.Distributor
		for attempt := 0; attempt < 3; attempt++ {
			publicNo, numberErr := newPublicNumber()
			if numberErr != nil {
				return distributionport.ErrUnavailable
			}
			saved, readiness, err = s.store.InsertDistributorWithin(tx, command.Actor.CustomerID, publicNo, agreement.Version, now)
			if err == nil {
				break
			}
			if !errors.Is(err, distributionport.ErrConflict) {
				return err
			}
			if replay, found, replayErr := s.registrationReplay(tx, command, actorScope, payloadDigest, agreement.Version); replayErr != nil {
				return replayErr
			} else if found {
				profile = replay
				return nil
			}
			existing, readiness, err = s.store.ReadDistributorByCustomerWithin(tx, command.Actor.CustomerID, true)
			if err == nil {
				profile, err = s.registrationRecordExisting(tx, command, actorScope, payloadDigest, agreement.Version, existing, readiness)
				if errors.Is(err, distributionport.ErrConflict) {
					var found bool
					profile, found, err = s.registrationReplay(tx, command, actorScope, payloadDigest, agreement.Version)
					if err == nil && found {
						return nil
					}
					if err == nil {
						err = distributionport.ErrConflict
					}
				}
				return err
			}
			if !errors.Is(err, distributionport.ErrNotFound) {
				return err
			}
		}
		if saved.ID < 1 {
			return distributionport.ErrUnavailable
		}
		payload := map[string]any{"distributor_id": saved.ID, "agreement_version": agreement.Version}
		if err = s.store.AppendOperationReceiptWithin(tx, "register", actorScope, command.IdempotencyKey, payloadDigest, "distributor", saved.ID, now); err != nil {
			if errors.Is(err, distributionport.ErrConflict) {
				if replay, found, replayErr := s.registrationReplay(tx, command, actorScope, payloadDigest, agreement.Version); replayErr != nil {
					return replayErr
				} else if found {
					profile = replay
					return nil
				}
			}
			return err
		}
		if err = s.store.AppendAuditWithin(tx, "distribution.distributor_registered.v1", "distributor", saved.ID, actorScope, payload, now); err != nil {
			return err
		}
		if err = s.store.AppendOutboxWithin(tx, "distribution.distributor_registered.v1", registrationOutboxKey(actorScope, command.IdempotencyKey), saved.ID, payload, now); err != nil {
			return err
		}
		profile = distributionport.DistributorProfile{Distributor: saved, Receiver: readiness, CurrentAgreementVersion: agreement.Version}
		return nil
	})
	if err != nil {
		return distributionport.DistributorProfile{}, err
	}
	return profile, nil
}

func (s *RegistrationService) registrationReplay(ctx context.Context, command distributionport.RegisterCommand, actorScope string, digest [sha256.Size]byte, currentAgreement string) (distributionport.DistributorProfile, bool, error) {
	receipt, found, err := s.store.ReadOperationReceiptWithin(ctx, "register", actorScope, command.IdempotencyKey)
	if err != nil || !found {
		return distributionport.DistributorProfile{}, found, err
	}
	if receipt.PayloadDigest != digest {
		return distributionport.DistributorProfile{}, true, distributionport.ErrConflict
	}
	if receipt.ResultKind != "distributor" {
		return distributionport.DistributorProfile{}, true, distributionport.ErrConflict
	}
	distributor, readiness, err := s.store.ReadDistributorWithin(ctx, receipt.ResultID, false)
	if err != nil {
		return distributionport.DistributorProfile{}, true, err
	}
	if distributor.CustomerID != command.Actor.CustomerID {
		return distributionport.DistributorProfile{}, true, distributionport.ErrConflict
	}
	return distributionport.DistributorProfile{Distributor: distributor, Receiver: readiness, CurrentAgreementVersion: currentAgreement}, true, nil
}

// registrationRecordExisting provides an idempotency receipt for a valid
// registration made before receipts existed. It intentionally does not append
// a second registered audit or outbox event, because no new registration fact
// occurred in this command.
func (s *RegistrationService) registrationRecordExisting(ctx context.Context, command distributionport.RegisterCommand, actorScope string, digest [sha256.Size]byte, currentAgreement string, distributor distributiondomain.Distributor, readiness distributionport.ReceiverReadiness) (distributionport.DistributorProfile, error) {
	if distributor.CustomerID != command.Actor.CustomerID {
		return distributionport.DistributorProfile{}, distributionport.ErrConflict
	}
	if err := s.store.AppendOperationReceiptWithin(ctx, "register", actorScope, command.IdempotencyKey, digest, "distributor", distributor.ID, s.now().UTC()); err != nil {
		return distributionport.DistributorProfile{}, err
	}
	return distributionport.DistributorProfile{Distributor: distributor, Receiver: readiness, CurrentAgreementVersion: currentAgreement}, nil
}

func registrationActorScope(actor distributionport.TrustedSessionActor) string {
	return "customer:" + decimal(actor.CustomerID)
}

func registrationPayloadDigest(agreementVersion string) [sha256.Size]byte {
	return sha256.Sum256([]byte("distribution.register.v1:agreement_version=" + agreementVersion))
}

func registrationOutboxKey(actorScope, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(actorScope + "\x00" + idempotencyKey))
	return "distribution.register:" + hex.EncodeToString(digest[:])
}

func (s *RegistrationService) PrepareReceiver(ctx context.Context, actor distributionport.TrustedSessionActor) (distributionport.ReceiverPreparationResult, error) {
	if s == nil || s.uow == nil || s.store == nil || s.settlement == nil || !actor.Valid() {
		return distributionport.ReceiverPreparationResult{}, distributionport.ErrUnauthorized
	}
	capability, err := s.settlement.SettlementCapability(ctx)
	if err != nil {
		return distributionport.ReceiverPreparationResult{}, err
	}
	var result distributionport.ReceiverPreparationResult
	err = s.uow.Within(ctx, func(tx context.Context) error {
		// Establish that this trusted customer is already a distributor without
		// taking Distribution's row lock. The payment receiver write below may
		// later project back into this row from the EER worker, and both paths
		// must therefore take locks in the same Payment -> Distribution order.
		distributor, current, err := s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		if err != nil {
			return err
		}
		if !capability.Enabled {
			result = distributionport.ReceiverPreparationResult{Receiver: current, State: "merchant_settlement_disabled"}
			return nil
		}
		channel := paymentdomain.ChannelMiniProgram
		if actor.Channel == "h5_official_account" {
			channel = paymentdomain.ChannelH5Official
		}
		key := "distribution.receiver:" + decimal(distributor.ID) + ":" + actor.AppID
		prepared, err := s.settlement.PrepareProfitSharingReceiverWithin(tx, paymentport.ReceiverPreparation{CustomerID: actor.CustomerID, IdentityID: actor.IdentityID, AppID: actor.AppID, AppScope: actor.AppScope, Channel: channel, IdempotencyKey: key, SourceDigest: effectport.Hash("distribution.receiver.v1", decimal(distributor.ID), actor.AppID), PayloadDigest: effectport.Hash("distribution.receiver.payload.v1", decimal(distributor.ID), actor.AppID, actor.Channel)})
		if err != nil {
			return err
		}
		// Re-read under lock only after Payment's owned receiver transition.
		// Registration rows are never implicitly created here; this is a fresh
		// version for the Distribution snapshot CAS, not a second identity
		// resolution or a separate transaction.
		distributor, current, err = s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		readiness := distributionport.ReceiverReadiness{Ready: prepared.Ready, Reference: prepared.Reference, AppID: actor.AppID, CheckedAt: now}
		if !prepared.Ready {
			readiness.Reason = safeReceiverReason(prepared)
		}
		updated, persisted, err := s.store.UpdateReceiverReadinessWithin(tx, distributor.ID, distributor.Version, readiness, now)
		if err != nil {
			return err
		}
		payload := map[string]any{"distributor_id": updated.ID, "receiver_state": prepared.State, "ready": persisted.Ready}
		if err = s.store.AppendAuditWithin(tx, "distribution.receiver_prepared.v1", "distributor", updated.ID, "customer:"+decimal(actor.CustomerID), payload, now); err != nil {
			return err
		}
		result = distributionport.ReceiverPreparationResult{Receiver: persisted, ActionURL: "/api/v1/distribution/receiver-preparation", RetryAfterSec: 15}
		if persisted.Ready {
			result.State, result.RetryAfterSec = "ready", 0
		} else if prepared.State == "accepted" || prepared.State == "queued" || prepared.State == "attempted" {
			result.State = "processing"
		} else {
			result.State = "unavailable"
		}
		return nil
	})
	if err != nil {
		return distributionport.ReceiverPreparationResult{}, err
	}
	return result, nil
}

func safeSettlementCapabilityReason(value paymentport.SettlementCapability) string {
	if value.Enabled {
		return ""
	}
	if value.Reason == "merchant_settlement_disabled" {
		return value.Reason
	}
	return "merchant_settlement_unavailable"
}

func newPublicNumber() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "D" + strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), "="), nil
}

func safeReceiverState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return "unavailable"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unavailable"
		}
	}
	return value
}

func safeReceiverReason(value paymentport.ReceiverReadiness) string {
	if value.FailureClass == paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied {
		return "receiver_provider_permission_denied"
	}
	return "receiver_" + safeReceiverState(value.State)
}

func decimal(value int64) string {
	if value < 1 {
		return "0"
	}
	const digits = "0123456789"
	var buffer [20]byte
	i := len(buffer)
	for value > 0 {
		i--
		buffer[i] = digits[value%10]
		value /= 10
	}
	return string(buffer[i:])
}
