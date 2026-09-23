package app

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const profitSharingWindow = 30 * 24 * time.Hour

// DistributionSettlementStore is Payment's private persistence seam. It is
// intentionally not exported from payment/port: Distribution receives only the
// opaque projection types above and cannot couple to Payment tables.
type DistributionSettlementStore interface {
	FindProfitSharingReceiver(context.Context, int64, string, bool) (domain.ProfitSharingReceiver, bool, error)
	GetProfitSharingReceiver(context.Context, int64, bool) (domain.ProfitSharingReceiver, error)
	GetProfitSharingReceiverByEffect(context.Context, string, bool) (domain.ProfitSharingReceiver, error)
	UpdateProfitSharingReceiver(context.Context, domain.ProfitSharingReceiver, string) (domain.ProfitSharingReceiver, error)
	CreateProfitSharingReceiver(context.Context, domain.ProfitSharingReceiver) (domain.ProfitSharingReceiver, bool, error)
	BindProfitSharingReceiverEffect(context.Context, domain.ProfitSharingReceiver, effectport.PaymentV1Intent) (domain.ProfitSharingReceiver, error)
	FindProfitSharingReceiverRecoveryWithin(context.Context, string, [32]byte, [32]byte) (domain.ProfitSharingReceiver, bool, error)
	RecoverProfitSharingReceiverEffectWithin(context.Context, domain.ProfitSharingReceiver, string, effectport.PaymentV1Intent, string, string, [32]byte, [32]byte) (domain.ProfitSharingReceiver, bool, error)
	PaymentForDistributionOrder(context.Context, int64, bool) (domain.Payment, error)
	ProfitSharingFunding(context.Context, int64) (domain.ProfitSharingFunding, error)
	FindProfitSharingBySettlement(context.Context, string, bool) (domain.ProfitSharingInstruction, bool, error)
	GetProfitSharingInstruction(context.Context, string, bool) (domain.ProfitSharingInstruction, error)
	GetProfitSharingInstructionByEffect(context.Context, string, bool) (domain.ProfitSharingInstruction, error)
	CreateProfitSharingInstruction(context.Context, domain.ProfitSharingInstruction) (domain.ProfitSharingInstruction, bool, error)
	BindProfitSharingInstructionEffect(context.Context, domain.ProfitSharingInstruction, effectport.PaymentV1Intent) (domain.ProfitSharingInstruction, error)
	UpdateProfitSharingInstruction(context.Context, domain.ProfitSharingInstruction, string) (domain.ProfitSharingInstruction, error)
	ReleaseProfitSharingReserve(context.Context, int64, string, time.Time) error
	FindProfitSharingUnfreeze(context.Context, int64, bool) (domain.ProfitSharingUnfreeze, bool, error)
	GetProfitSharingUnfreezeByReference(context.Context, string, bool) (domain.ProfitSharingUnfreeze, error)
	GetProfitSharingUnfreezeByEffect(context.Context, string, bool) (domain.ProfitSharingUnfreeze, error)
	CreateProfitSharingUnfreeze(context.Context, domain.ProfitSharingUnfreeze) (domain.ProfitSharingUnfreeze, bool, error)
	BindProfitSharingUnfreezeEffect(context.Context, domain.ProfitSharingUnfreeze, effectport.PaymentV1Intent) (domain.ProfitSharingUnfreeze, error)
	UpdateProfitSharingUnfreeze(context.Context, domain.ProfitSharingUnfreeze, string) (domain.ProfitSharingUnfreeze, error)
	FindProfitSharingProviderIntent(context.Context, effectport.Kind, effectport.Digest) (domain.ProfitSharingProviderIntent, error)
}

func (s *Service) distributionStore() (DistributionSettlementStore, error) {
	if s == nil || s.store == nil {
		return nil, paymentport.ErrUnavailable
	}
	store, ok := s.store.(DistributionSettlementStore)
	if !ok {
		return nil, paymentport.ErrUnavailable
	}
	return store, nil
}

// SettlementCapability gives Distribution a safe explanation for the
// merchant-level split gate. It intentionally carries no configuration,
// receiver account, or Provider detail, so a public distributor page cannot
// turn it into a financial configuration surface.
func (s *Service) SettlementCapability(context.Context) (paymentport.SettlementCapability, error) {
	if s == nil {
		return paymentport.SettlementCapability{}, paymentport.ErrUnavailable
	}
	if !s.profitSharingEnabled {
		return paymentport.SettlementCapability{Reason: "merchant_settlement_disabled"}, nil
	}
	return paymentport.SettlementCapability{Enabled: true}, nil
}

func (s *Service) PrepareProfitSharingReceiverWithin(ctx context.Context, request paymentport.ReceiverPreparation) (paymentport.ReceiverReadiness, error) {
	if !request.Valid() || s == nil {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	if !s.profitSharingEnabled {
		return paymentport.ReceiverReadiness{}, paymentport.ErrSettlementCapabilityDisabled
	}
	if s.receiverIDs == nil || s.lineage == nil || s.effects == nil {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	expectedAppID := s.miniAppID
	if request.Channel == domain.ChannelH5Official {
		expectedAppID = s.h5AppID
	}
	// AppID is configuration, never a browser or Distribution choice. The
	// caller still supplies AppScope from its trusted Payment-session actor so
	// Identity can perform the exact scoped lookup below.
	if expectedAppID == "" || request.AppID != expectedAppID {
		return paymentport.ReceiverReadiness{}, paymentport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	kind := identitydomain.KindMPOpenID
	if request.Channel == domain.ChannelH5Official {
		kind = identitydomain.KindOAOpenID
	}
	identity, found, err := s.receiverIDs.VerifiedPaymentIdentity(ctx, request.IdentityID, kind, request.AppScope)
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	if !found || identity.IdentityID != request.IdentityID || identity.Kind != kind || identity.Scope != request.AppScope || identity.Value == "" {
		return paymentport.ReceiverReadiness{}, paymentport.ErrUnavailable
	}
	requestedRoots, err := s.lineage.CanonicalLineage(ctx, customerdomain.CustomerID(request.CustomerID))
	if err != nil || len(requestedRoots) == 0 || int64(requestedRoots[0]) != request.CustomerID {
		return paymentport.ReceiverReadiness{}, paymentport.ErrUnavailable
	}
	identityRoots, err := s.lineage.CanonicalLineage(ctx, identity.CustomerID)
	if err != nil || len(identityRoots) == 0 || requestedRoots[0] != identityRoots[0] {
		return paymentport.ReceiverReadiness{}, paymentport.ErrConflict
	}
	accountDigest := effectport.Hash("payment.profit-sharing.receiver.account.v1", request.AppID, request.AppScope, identity.Value)
	existing, exists, err := store.FindProfitSharingReceiver(ctx, request.CustomerID, request.AppID, true)
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	if exists {
		if existing.IdentityID != request.IdentityID || existing.AppScope != request.AppScope || existing.AccountDigest != string(accountDigest) {
			return paymentport.ReceiverReadiness{}, paymentport.ErrConflict
		}
		return receiverReadiness(existing), nil
	}
	now := s.now().UTC()
	receiver := domain.ProfitSharingReceiver{CustomerID: request.CustomerID, IdentityID: request.IdentityID, AppID: request.AppID, AppScope: request.AppScope, Channel: request.Channel, AccountDigest: string(accountDigest), State: domain.ProfitSharingReceiverAccepted, Version: 1, CreatedAt: now, UpdatedAt: now}
	receiver, _, err = store.CreateProfitSharingReceiver(ctx, receiver)
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	intent := effectport.PaymentV1Intent{
		Kind:              effectport.KindWeChatPayReceiverAdd,
		ReceiptKey:        effectport.Hash("payment.profit-sharing.receiver.accept.v1", request.IdempotencyKey),
		SourceRefDigest:   request.SourceDigest,
		TargetRefDigest:   effectport.Hash("payment.profit-sharing.receiver.target.v1", strconv.FormatInt(receiver.ID, 10)),
		PayloadDigest:     request.PayloadDigest,
		PolicyVersionHash: effectport.Hash("payment.profit-sharing.receiver.policy.v1", request.AppID, request.AppScope, string(request.Channel)),
	}
	accept, ok := intent.AcceptCommand()
	if !ok {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	projection, _, err := s.effects.AcceptAndQueueWithin(ctx, accept)
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	receiver.EffectID = projection.ID
	receiver.Version++
	receiver.UpdatedAt = now
	receiver, err = store.BindProfitSharingReceiverEffect(ctx, receiver, intent)
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	return receiverReadiness(receiver), nil
}

// RecoverProfitSharingReceiver accepts one reviewed replacement AddReceiver
// effect for a receiver whose complete old effect history proves no Provider
// call was made.  It never retries, rewrites, or explains the old effect.
// The administrator must deliberately invoke this path after current merchant
// capability and the receiver's trusted identity can both be checked again.
func (s *Service) RecoverProfitSharingReceiver(ctx context.Context, command paymentport.ProfitSharingReceiverRecoveryCommand) (paymentport.ReceiverReadiness, error) {
	if !command.Valid() || s == nil || s.uow == nil || s.effects == nil || s.effectReader == nil || s.receiverIDs == nil || s.lineage == nil || !s.profitSharingEnabled || s.profitSharingReconciler == nil {
		return paymentport.ReceiverReadiness{}, paymentport.ErrUnavailable
	}
	receiverID, ok := profitSharingReceiverID(command.ReceiverReference)
	if !ok {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	prover, ok := s.effectReader.(effectport.FinalFailureWithoutExternalCallReader)
	if !ok {
		return paymentport.ReceiverReadiness{}, paymentport.ErrUnavailable
	}
	actorScope := "admin:" + strconv.FormatInt(command.ActorAdminUserID, 10)
	key := sha256.Sum256([]byte("payment.profit-sharing.receiver.recovery.key.v1\x00" + actorScope + "\x00" + command.IdempotencyKey))
	payload := sha256.Sum256([]byte("payment.profit-sharing.receiver.recovery.payload.v1\x00" + command.ReceiverReference + "\x00" + command.EvidenceReference))
	var recovered domain.ProfitSharingReceiver
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var (
			inner error
			found bool
		)
		recovered, found, inner = store.FindProfitSharingReceiverRecoveryWithin(tx, actorScope, key, payload)
		if inner != nil {
			return inner
		}
		if found {
			if recovered.ID != receiverID {
				return paymentport.ErrConflict
			}
			return nil
		}
		receiver, inner := store.GetProfitSharingReceiver(tx, receiverID, true)
		if inner != nil {
			return inner
		}
		if receiver.State != domain.ProfitSharingReceiverFinalFailed || receiver.EffectID == "" {
			return paymentport.ErrConflict
		}
		oldEffect := receiver.EffectID
		materialDigest := effectport.Hash("payment.profit-sharing.receiver.recovery.material.v1", command.ReceiverReference, command.EvidenceReference)
		if _, inner = s.profitSharingReceiverMaterial(tx, receiver, materialDigest); inner != nil {
			return inner
		}
		if _, inner = prover.FinalFailureWithoutExternalCallWithin(tx, oldEffect, effectport.OwnerPayment, effectport.KindWeChatPayReceiverAdd); inner != nil {
			return inner
		}
		// EER uses the same actor/key receipt across all receivers.  It serializes
		// a mistaken same-key command for another receiver before either command
		// can leave a separate queued effect behind.
		intent := effectport.PaymentV1Intent{
			Kind:              effectport.KindWeChatPayReceiverAdd,
			ReceiptKey:        effectport.Hash("payment.profit-sharing.receiver.recovery.accept.v1", actorScope, command.IdempotencyKey),
			SourceRefDigest:   effectport.Hash("payment.profit-sharing.receiver.recovery.source.v1", oldEffect, actorScope, command.EvidenceReference),
			TargetRefDigest:   effectport.Hash("payment.profit-sharing.receiver.recovery.target.v1", command.ReceiverReference),
			PayloadDigest:     materialDigest,
			PolicyVersionHash: effectport.Hash("payment.profit-sharing.receiver.recovery.policy.v1", receiver.AppID, receiver.AppScope, string(receiver.Channel)),
		}
		accept, valid := intent.AcceptCommand()
		if !valid {
			return paymentport.ErrInvalid
		}
		projection, _, inner := s.effects.AcceptAndQueueWithin(tx, accept)
		if inner != nil {
			return inner
		}
		now := s.now().UTC()
		receiver.State = domain.ProfitSharingReceiverAccepted
		receiver.EffectID = projection.ID
		receiver.Version++
		receiver.UpdatedAt = now
		var changed bool
		recovered, changed, inner = store.RecoverProfitSharingReceiverEffectWithin(tx, receiver, oldEffect, intent, actorScope, command.EvidenceReference, key, payload)
		if inner != nil || !changed || s.receiverStatusObserver == nil {
			return inner
		}
		return s.receiverStatusObserver.SyncProfitSharingReceiverStatusWithin(tx, receiverReadiness(recovered))
	})
	if err != nil {
		return paymentport.ReceiverReadiness{}, classify(err)
	}
	return receiverReadiness(recovered), nil
}

func (s *Service) ReceiverReadiness(ctx context.Context, customerID int64, appID string) (paymentport.ReceiverReadiness, error) {
	if s == nil || s.uow == nil || customerID < 1 || !paymentPortText(appID, 1, 160) {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	var receiver domain.ProfitSharingReceiver
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		receiver, inner = s.receiverReadinessWithin(tx, store, customerID, appID, false)
		return inner
	})
	if err != nil {
		return paymentport.ReceiverReadiness{}, classify(err)
	}
	return receiverReadiness(receiver), nil
}

// ReceiverReadinessWithin is the transaction-preserving counterpart used by
// Distribution just before it accepts an immutable split instruction. The
// locked Payment receiver row makes the readiness check and reserve/instruction
// acceptance one PostgreSQL decision rather than a stale preflight read.
func (s *Service) ReceiverReadinessWithin(ctx context.Context, customerID int64, appID string) (paymentport.ReceiverReadiness, error) {
	if s == nil || customerID < 1 || !paymentPortText(appID, 1, 160) {
		return paymentport.ReceiverReadiness{}, paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ReceiverReadiness{}, err
	}
	receiver, err := s.receiverReadinessWithin(ctx, store, customerID, appID, true)
	if err != nil {
		return paymentport.ReceiverReadiness{}, classify(err)
	}
	return receiverReadiness(receiver), nil
}

func (s *Service) receiverReadinessWithin(ctx context.Context, store DistributionSettlementStore, customerID int64, appID string, lock bool) (domain.ProfitSharingReceiver, error) {
	receiver, found, err := store.FindProfitSharingReceiver(ctx, customerID, appID, lock)
	if err != nil {
		return domain.ProfitSharingReceiver{}, err
	}
	if !found {
		return domain.ProfitSharingReceiver{}, paymentport.ErrNotFound
	}
	return receiver, nil
}

// CompleteEffect is registered with EER by Composition's payment completion
// router. It runs inside the EER completion transaction and only marks a
// receiver ready after the official AddReceiver call was executed. An HTTP
// response alone cannot make this projection ready outside that transaction.
func (s *Service) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, _ effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || envelope.Owner != effectport.OwnerPayment || effectRef == "" {
		return paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	store, err := s.distributionStore()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	switch result.Completion {
	case effectport.StateExecuted, effectport.StateUnknown, effectport.StateFinalFailed:
	default:
		return nil
	}
	switch envelope.Kind {
	case effectport.KindWeChatPayReceiverAdd:
		receiver, err := store.GetProfitSharingReceiverByEffect(ctx, effectRef, true)
		if err != nil {
			return err
		}
		switch result.Completion {
		case effectport.StateExecuted:
			receiver.State, receiver.FailureClass = domain.ProfitSharingReceiverReady, ""
		case effectport.StateUnknown:
			receiver.State, receiver.FailureClass = domain.ProfitSharingReceiverOutcomeUnknown, ""
		case effectport.StateFinalFailed:
			receiver.State = domain.ProfitSharingReceiverFinalFailed
			receiver.FailureClass = receiverFailureClass(result.FailureCode)
		}
		receiver.Version++
		receiver.UpdatedAt = now
		receiver, err = store.UpdateProfitSharingReceiver(ctx, receiver, "effect:"+string(result.Completion))
		if err != nil || s.receiverStatusObserver == nil {
			return err
		}
		return s.receiverStatusObserver.SyncProfitSharingReceiverStatusWithin(ctx, receiverReadiness(receiver))
	case effectport.KindWeChatPayProfitSharing:
		instruction, err := store.GetProfitSharingInstructionByEffect(ctx, effectRef, true)
		if err != nil {
			return err
		}
		switch result.Completion {
		case effectport.StateExecuted:
			instruction.State = domain.ProfitSharingSettling
		case effectport.StateUnknown:
			instruction.State = domain.ProfitSharingOutcomeUnknown
		case effectport.StateFinalFailed:
			instruction.State, instruction.OutcomeKnown = domain.ProfitSharingException, true
		}
		instruction.Version++
		instruction.UpdatedAt = now
		_, err = store.UpdateProfitSharingInstruction(ctx, instruction, "effect:"+string(result.Completion))
		return err
	case effectport.KindWeChatPayProfitUnfreeze:
		unfreeze, err := store.GetProfitSharingUnfreezeByEffect(ctx, effectRef, true)
		if err != nil {
			return err
		}
		switch result.Completion {
		case effectport.StateExecuted: /* result is async: retain accepted until query */
		case effectport.StateUnknown:
			unfreeze.State = "outcome_unknown"
		case effectport.StateFinalFailed:
			unfreeze.State, unfreeze.OutcomeKnown = "exception", true
		}
		unfreeze.Version++
		unfreeze.UpdatedAt = now
		_, err = store.UpdateProfitSharingUnfreeze(ctx, unfreeze, "effect:"+string(result.Completion))
		return err
	default:
		return nil
	}
}

func (s *Service) LoadProfitSharingEffectMaterial(ctx context.Context, kind effectport.Kind, source effectport.Digest) (paymentport.ProfitSharingProviderMaterial, error) {
	if s == nil || s.uow == nil || s.receiverIDs == nil || !effectport.ValidDigest(source) {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrUnavailable
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingProviderMaterial{}, err
	}
	var material paymentport.ProfitSharingProviderMaterial
	err = s.uow.Within(ctx, func(tx context.Context) error {
		intent, err := store.FindProfitSharingProviderIntent(tx, kind, source)
		if err != nil {
			return err
		}
		if intent.ReceiverID > 0 {
			receiver, err := store.GetProfitSharingReceiver(tx, intent.ReceiverID, false)
			if err != nil {
				return err
			}
			material, err = s.profitSharingReceiverMaterial(tx, receiver, intent.PayloadDigest)
			return err
		}
		if intent.InstructionID > 0 {
			instruction, err := store.GetProfitSharingInstruction(tx, "psinst_"+strconv.FormatInt(intent.InstructionID, 10), false)
			if err != nil {
				return err
			}
			material, err = s.profitSharingInstructionMaterial(tx, store, instruction, intent.PayloadDigest)
			return err
		}
		if intent.UnfreezeID > 0 {
			unfreeze, err := store.GetProfitSharingUnfreezeByReference(tx, "psunfreeze_"+strconv.FormatInt(intent.UnfreezeID, 10), false)
			if err != nil {
				return err
			}
			payment, err := s.store.GetPayment(tx, unfreeze.PaymentID, false)
			if err != nil {
				return err
			}
			material = paymentport.ProfitSharingProviderMaterial{PayloadDigest: intent.PayloadDigest, AppID: s.profitSharingAppID(payment), TransactionReference: payment.ProviderTransactionReference, ProviderOrderNo: unfreeze.ProviderOrderNo, Reason: unfreeze.Reason}
			return s.validateProfitSharingMaterial(material, kind)
		}
		return paymentport.ErrConflict
	})
	return material, classify(err)
}

func (s *Service) LoadProfitSharingReferenceMaterial(ctx context.Context, reference string) (effectport.Kind, paymentport.ProfitSharingProviderMaterial, error) {
	if s == nil || s.uow == nil || s.receiverIDs == nil {
		return "", paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrUnavailable
	}
	store, err := s.distributionStore()
	if err != nil {
		return "", paymentport.ProfitSharingProviderMaterial{}, err
	}
	var kind effectport.Kind
	var material paymentport.ProfitSharingProviderMaterial
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if validInstructionReference(reference) {
			instruction, err := store.GetProfitSharingInstruction(tx, reference, false)
			if err != nil {
				return err
			}
			material, err = s.profitSharingInstructionMaterial(tx, store, instruction, effectport.Digest(instruction.PayloadDigest))
			kind = effectport.KindWeChatPayProfitSharing
			return err
		}
		unfreeze, err := store.GetProfitSharingUnfreezeByReference(tx, reference, false)
		if err != nil {
			return err
		}
		payment, err := s.store.GetPayment(tx, unfreeze.PaymentID, false)
		if err != nil {
			return err
		}
		kind, material = effectport.KindWeChatPayProfitUnfreeze, paymentport.ProfitSharingProviderMaterial{PayloadDigest: effectport.Hash("payment.profit-sharing.unfreeze.query", reference), AppID: s.profitSharingAppID(payment), TransactionReference: payment.ProviderTransactionReference, ProviderOrderNo: unfreeze.ProviderOrderNo, Reason: unfreeze.Reason}
		return s.validateProfitSharingMaterial(material, kind)
	})
	return kind, material, classify(err)
}

func (s *Service) profitSharingInstructionMaterial(ctx context.Context, store DistributionSettlementStore, instruction domain.ProfitSharingInstruction, digest effectport.Digest) (paymentport.ProfitSharingProviderMaterial, error) {
	receiver, err := store.GetProfitSharingReceiver(ctx, instruction.ReceiverID, false)
	if err != nil {
		return paymentport.ProfitSharingProviderMaterial{}, err
	}
	base, err := s.profitSharingReceiverMaterial(ctx, receiver, digest)
	if err != nil {
		return paymentport.ProfitSharingProviderMaterial{}, err
	}
	payment, err := s.store.GetPayment(ctx, instruction.PaymentID, false)
	if err != nil {
		return paymentport.ProfitSharingProviderMaterial{}, err
	}
	base.TransactionReference, base.ProviderOrderNo, base.AmountMinor, base.Reason = payment.ProviderTransactionReference, instruction.ProviderOrderNo, instruction.AmountMinor, "distribution commission"
	if base.AppID != s.profitSharingAppID(payment) {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrConflict
	}
	return base, s.validateProfitSharingMaterial(base, effectport.KindWeChatPayProfitSharing)
}

func (s *Service) profitSharingReceiverMaterial(ctx context.Context, receiver domain.ProfitSharingReceiver, digest effectport.Digest) (paymentport.ProfitSharingProviderMaterial, error) {
	expectedAppID := s.miniAppID
	if receiver.Channel == domain.ChannelH5Official {
		expectedAppID = s.h5AppID
	}
	// A receiver is scoped to the configured payment application.  A merchant
	// changing that application must not let an old OpenID/account digest be
	// reused for a new AddReceiver intent.
	if expectedAppID == "" || receiver.AppID != expectedAppID {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrConflict
	}
	kind := identitydomain.KindMPOpenID
	if receiver.Channel == domain.ChannelH5Official {
		kind = identitydomain.KindOAOpenID
	}
	identity, found, err := s.receiverIDs.VerifiedPaymentIdentity(ctx, receiver.IdentityID, kind, receiver.AppScope)
	if err != nil || !found || identity.IdentityID != receiver.IdentityID || identity.Kind != kind || identity.Scope != receiver.AppScope || identity.Value == "" || string(effectport.Hash("payment.profit-sharing.receiver.account.v1", receiver.AppID, receiver.AppScope, identity.Value)) != receiver.AccountDigest {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrUnavailable
	}
	if s.lineage == nil {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrUnavailable
	}
	receiverRoots, err := s.lineage.CanonicalLineage(ctx, customerdomain.CustomerID(receiver.CustomerID))
	if err != nil || len(receiverRoots) == 0 || int64(receiverRoots[0]) != receiver.CustomerID {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrUnavailable
	}
	identityRoots, err := s.lineage.CanonicalLineage(ctx, identity.CustomerID)
	if err != nil || len(identityRoots) == 0 || identityRoots[0] != receiverRoots[0] {
		return paymentport.ProfitSharingProviderMaterial{}, paymentport.ErrConflict
	}
	return paymentport.ProfitSharingProviderMaterial{PayloadDigest: digest, AppID: receiver.AppID, ReceiverAccount: identity.Value}, s.validateProfitSharingMaterial(paymentport.ProfitSharingProviderMaterial{PayloadDigest: digest, AppID: receiver.AppID, ReceiverAccount: identity.Value}, effectport.KindWeChatPayReceiverAdd)
}

func (s *Service) validateProfitSharingMaterial(value paymentport.ProfitSharingProviderMaterial, kind effectport.Kind) error {
	if !effectport.ValidDigest(value.PayloadDigest) || value.AppID == "" || strings.TrimSpace(value.AppID) != value.AppID {
		return paymentport.ErrInvalid
	}
	if kind == effectport.KindWeChatPayReceiverAdd {
		if value.ReceiverAccount == "" {
			return paymentport.ErrUnavailable
		}
		return nil
	}
	if kind == effectport.KindWeChatPayProfitSharing {
		if value.ReceiverAccount == "" || value.TransactionReference == "" || value.AmountMinor < 1 {
			return paymentport.ErrUnavailable
		}
		return nil
	}
	if kind == effectport.KindWeChatPayProfitUnfreeze {
		if value.TransactionReference == "" || value.Reason == "" {
			return paymentport.ErrUnavailable
		}
		return nil
	}
	return paymentport.ErrInvalid
}

func (s *Service) DistributionPaymentState(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	if s == nil || s.uow == nil || orderID < 1 {
		return paymentport.DistributionPaymentState{}, paymentport.ErrInvalid
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.DistributionPaymentState{}, err
	}
	var payment domain.Payment
	var funding domain.ProfitSharingFunding
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		payment, funding, inner = s.distributionPaymentStateFactsWithin(tx, store, orderID, false)
		return inner
	})
	if err != nil {
		return paymentport.DistributionPaymentState{}, classify(err)
	}
	return distributionPaymentState(payment, funding, s.now().UTC()), nil
}

// DistributionPaymentStateWithin is the UoW-preserving variant used by
// trusted cross-domain import/qualification workflows. It performs no network
// work and never starts a nested transaction.
func (s *Service) DistributionPaymentStateWithin(ctx context.Context, orderID int64) (paymentport.DistributionPaymentState, error) {
	if s == nil || orderID < 1 {
		return paymentport.DistributionPaymentState{}, paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.DistributionPaymentState{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.DistributionPaymentState{}, err
	}
	payment, funding, err := s.distributionPaymentStateFactsWithin(ctx, store, orderID, true)
	if err != nil {
		return paymentport.DistributionPaymentState{}, classify(err)
	}
	return distributionPaymentState(payment, funding, s.now().UTC()), nil
}

func (s *Service) distributionPaymentStateFactsWithin(ctx context.Context, store DistributionSettlementStore, orderID int64, lock bool) (domain.Payment, domain.ProfitSharingFunding, error) {
	payment, err := store.PaymentForDistributionOrder(ctx, orderID, lock)
	if err != nil {
		return domain.Payment{}, domain.ProfitSharingFunding{}, err
	}
	funding, err := store.ProfitSharingFunding(ctx, payment.ID)
	if err != nil {
		return domain.Payment{}, domain.ProfitSharingFunding{}, err
	}
	return payment, funding, nil
}

func (s *Service) AcceptProfitSharingWithin(ctx context.Context, request paymentport.ProfitSharingRequest) (paymentport.ProfitSharingInstruction, error) {
	if !request.Valid() || s == nil || s.effects == nil {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	// Stable settlement replay precedes the live funding gates. A response-loss
	// retry must return the one immutable instruction even if a refund arrived
	// after the first command committed.
	if existing, found, findErr := store.FindProfitSharingBySettlement(ctx, request.SettlementRef, true); findErr != nil {
		return paymentport.ProfitSharingInstruction{}, findErr
	} else if found {
		receiver, receiverErr := store.GetProfitSharingReceiver(ctx, existing.ReceiverID, true)
		if receiverErr != nil {
			return paymentport.ProfitSharingInstruction{}, receiverErr
		}
		if !profitSharingReplayMatches(existing, receiver, request) {
			return paymentport.ProfitSharingInstruction{}, paymentport.ErrConflict
		}
		return instructionProjection(existing), nil
	}
	paymentID, ok := parsePaymentReference(request.OriginalPaymentRef)
	if !ok {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	payment, err := s.store.GetPayment(ctx, paymentID, true)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	funding, err := store.ProfitSharingFunding(ctx, payment.ID)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	now := s.now().UTC()
	state := distributionPaymentState(payment, funding, now)
	if !state.ConfirmedPaid || !state.SplitCapable || state.RefundExposure || state.AvailableMinor < request.AmountMinor {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	receiver, found, err := store.FindProfitSharingReceiver(ctx, request.RecipientCustomerID, s.profitSharingAppID(payment), true)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	if !found || receiver.State != domain.ProfitSharingReceiverReady {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	instruction := domain.ProfitSharingInstruction{
		PaymentID: payment.ID, ReceiverID: receiver.ID, SettlementRef: request.SettlementRef,
		ProviderOrderNo:      profitSharingProviderOrderNo("v3ps_", request.IdempotencyKey),
		IdempotencyKeyDigest: string(effectport.Hash("payment.profit-sharing.idempotency.v1", request.IdempotencyKey)),
		SourceRefDigest:      string(request.SourceDigest), PayloadDigest: string(request.PayloadDigest), PolicyVersionHash: string(request.PolicyDigest),
		AmountMinor: request.AmountMinor, Currency: request.Currency, State: domain.ProfitSharingAccepted,
		DeadlineAt: state.DeadlineAt, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	instruction, _, err = store.CreateProfitSharingInstruction(ctx, instruction)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	intent := effectport.PaymentV1Intent{Kind: effectport.KindWeChatPayProfitSharing, ReceiptKey: effectport.Hash("payment.profit-sharing.accept.v1", request.IdempotencyKey), SourceRefDigest: request.SourceDigest, TargetRefDigest: effectport.Hash("payment.profit-sharing.instruction.target.v1", strconv.FormatInt(instruction.ID, 10)), PayloadDigest: request.PayloadDigest, PolicyVersionHash: request.PolicyDigest}
	accept, ok := intent.AcceptCommand()
	if !ok {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	projection, _, err := s.effects.AcceptAndQueueWithin(ctx, accept)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	instruction.EffectID = projection.ID
	instruction.Version++
	instruction.UpdatedAt = now
	instruction, err = store.BindProfitSharingInstructionEffect(ctx, instruction, intent)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	return instructionProjection(instruction), nil
}

func (s *Service) GetProfitSharing(ctx context.Context, reference string) (paymentport.ProfitSharingInstruction, error) {
	if s == nil || s.uow == nil || !validInstructionReference(reference) {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	var instruction domain.ProfitSharingInstruction
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		instruction, inner = store.GetProfitSharingInstruction(tx, reference, false)
		return inner
	})
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, classify(err)
	}
	return instructionProjection(instruction), nil
}

func (s *Service) CancelUnsubmittedProfitSharingWithin(ctx context.Context, reference, reason string, actor paymentport.ProfitSharingCancellationActor) (paymentport.ProfitSharingInstruction, error) {
	if s == nil || !validInstructionReference(reference) || !paymentPortText(reason, 1, 500) || !actor.Valid() {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	instruction, err := store.GetProfitSharingInstruction(ctx, reference, true)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	if instruction.State != domain.ProfitSharingAccepted || instruction.EffectID == "" {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	canceller, ok := s.effects.(effectport.TransactionalCanceller)
	if !ok {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	if _, err = canceller.CancelQueuedEffectWithin(ctx, effectport.CancelCommand{EffectID: instruction.EffectID, ReceiptKey: effectport.Hash("payment.profit-sharing.cancel.v1", reference), ReasonCode: "profit_sharing_cancelled", Actor: actor.EffectControlActor()}); err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	now := s.now().UTC()
	instruction, err = instruction.Cancel(instruction.Version, reason, now)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrConflict
	}
	instruction, err = store.UpdateProfitSharingInstruction(ctx, instruction, reason)
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	if err = store.ReleaseProfitSharingReserve(ctx, instruction.ID, reason, now); err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	return instructionProjection(instruction), nil
}

func (s *Service) UnfreezeProfitSharingRemainingWithin(ctx context.Context, request paymentport.ProfitSharingUnfreezeRequest) (paymentport.ProfitSharingUnfreeze, error) {
	if s == nil || s.effects == nil || !request.Valid() {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	paymentID, ok := parsePaymentReference(request.OriginalPaymentRef)
	if !ok {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	payment, err := s.store.GetPayment(ctx, paymentID, true)
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	if existing, found, findErr := store.FindProfitSharingUnfreeze(ctx, payment.ID, true); findErr != nil {
		return paymentport.ProfitSharingUnfreeze{}, findErr
	} else if found {
		if !profitSharingUnfreezeReplayMatches(existing, request) {
			return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrConflict
		}
		return unfreezeProjection(existing), nil
	}
	funding, err := store.ProfitSharingFunding(ctx, payment.ID)
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	state := distributionPaymentState(payment, funding, s.now().UTC())
	if !payment.ProfitSharingMarked {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrNothingToUnfreeze
	}
	if !state.ConfirmedPaid || funding.RefundExposure() || funding.ReservedSplitMinor != 0 {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrConflict
	}
	now := s.now().UTC()
	unfreeze := domain.ProfitSharingUnfreeze{PaymentID: payment.ID, ProviderOrderNo: profitSharingProviderOrderNo("v3psu_", request.IdempotencyKey), Reason: request.Reason, IdempotencyKeyDigest: string(effectport.Hash("payment.profit-sharing.unfreeze.idempotency.v1", request.IdempotencyKey)), SourceRefDigest: string(request.SourceDigest), PayloadDigest: string(request.PayloadDigest), PolicyVersionHash: string(request.PolicyDigest), State: "accepted", Version: 1, CreatedAt: now, UpdatedAt: now}
	unfreeze, _, err = store.CreateProfitSharingUnfreeze(ctx, unfreeze)
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	intent := effectport.PaymentV1Intent{Kind: effectport.KindWeChatPayProfitUnfreeze, ReceiptKey: effectport.Hash("payment.profit-sharing.unfreeze.v1", request.IdempotencyKey), SourceRefDigest: request.SourceDigest, TargetRefDigest: effectport.Hash("payment.profit-sharing.unfreeze.target.v1", strconv.FormatInt(unfreeze.ID, 10)), PayloadDigest: request.PayloadDigest, PolicyVersionHash: request.PolicyDigest}
	accept, ok := intent.AcceptCommand()
	if !ok {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrInvalid
	}
	projection, _, err := s.effects.AcceptAndQueueWithin(ctx, accept)
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	unfreeze.EffectID = projection.ID
	unfreeze.Version++
	unfreeze.UpdatedAt = now
	unfreeze, err = store.BindProfitSharingUnfreezeEffect(ctx, unfreeze, intent)
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	return unfreezeProjection(unfreeze), nil
}

func (s *Service) ReconcileProfitSharing(ctx context.Context, reference string) (paymentport.ProfitSharingInstruction, error) {
	if s == nil || s.uow == nil || s.profitSharingReconciler == nil || !validInstructionReference(reference) {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, err
	}
	var current domain.ProfitSharingInstruction
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = store.GetProfitSharingInstruction(tx, reference, false)
		return inner
	})
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, classify(err)
	}
	if current.State == domain.ProfitSharingPaid || current.State == domain.ProfitSharingCancelled {
		return instructionProjection(current), nil
	}
	query, err := s.profitSharingReconciler.QueryProfitSharing(ctx, reference)
	if err != nil || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := store.GetProfitSharingInstruction(tx, reference, true)
		if inner != nil {
			return inner
		}
		if locked.State == domain.ProfitSharingPaid || locked.State == domain.ProfitSharingCancelled {
			current = locked
			return nil
		}
		if query.ReceiverConfirmedSuccess {
			locked.State, locked.FailureClass, locked.ReceiverConfirmedSuccess, locked.OutcomeKnown = domain.ProfitSharingPaid, "", true, true
			if inner = store.ReleaseProfitSharingReserve(tx, locked.ID, "receiver_success", query.OccurredAt.UTC()); inner != nil {
				return inner
			}
		} else if query.ReceiverConfirmedFailure {
			locked.State, locked.FailureClass, locked.OutcomeKnown = domain.ProfitSharingException, instructionFailureClass(query.FailureClass), true
			// Only an exact receiver/amount terminal failure proves this
			// commission was not paid. An aggregate FINISHED mismatch is held
			// below for manual/provider reconciliation.
			if inner = store.ReleaseProfitSharingReserve(tx, locked.ID, "receiver_final_failed", query.OccurredAt.UTC()); inner != nil {
				return inner
			}
		} else if query.OutcomeKnown {
			// The provider may know its aggregate order is finished while the
			// instructed receiver/amount is absent or mismatched. Do not mark
			// this as an exact non-payment and do not release retained funds.
			locked.State, locked.OutcomeKnown = domain.ProfitSharingException, false
		} else if strings.EqualFold(query.State, "OUTCOME_UNKNOWN") {
			locked.State = domain.ProfitSharingOutcomeUnknown
		} else {
			locked.State = domain.ProfitSharingSettling
		}
		locked.Version++
		locked.UpdatedAt = query.OccurredAt.UTC()
		current, inner = store.UpdateProfitSharingInstruction(tx, locked, "provider_query:"+string(query.EvidenceDigest))
		return inner
	})
	if err != nil {
		return paymentport.ProfitSharingInstruction{}, classify(err)
	}
	return instructionProjection(current), nil
}

func (s *Service) ReconcileProfitSharingUnfreeze(ctx context.Context, reference string) (paymentport.ProfitSharingUnfreeze, error) {
	if s == nil || s.uow == nil || s.profitSharingReconciler == nil || !strings.HasPrefix(reference, "psunfreeze_") {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrUnavailable
	}
	store, err := s.distributionStore()
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, err
	}
	var current domain.ProfitSharingUnfreeze
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = store.GetProfitSharingUnfreezeByReference(tx, reference, false)
		return inner
	})
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, classify(err)
	}
	if current.OutcomeKnown {
		return unfreezeProjection(current), nil
	}
	query, err := s.profitSharingReconciler.QueryProfitSharingUnfreeze(ctx, reference)
	if err != nil || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return paymentport.ProfitSharingUnfreeze{}, paymentport.ErrUnavailable
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := store.GetProfitSharingUnfreezeByReference(tx, reference, true)
		if inner != nil {
			return inner
		}
		if locked.OutcomeKnown {
			current = locked
			return nil
		}
		if query.OutcomeKnown && strings.EqualFold(query.State, "FINISHED") {
			locked.State, locked.OutcomeKnown = "succeeded", true
		} else if query.OutcomeKnown {
			locked.State, locked.OutcomeKnown = "exception", true
		} else if strings.EqualFold(query.State, "OUTCOME_UNKNOWN") {
			locked.State = "outcome_unknown"
		}
		locked.Version++
		locked.UpdatedAt = query.OccurredAt.UTC()
		current, inner = store.UpdateProfitSharingUnfreeze(tx, locked, "provider_query:"+string(query.EvidenceDigest))
		return inner
	})
	if err != nil {
		return paymentport.ProfitSharingUnfreeze{}, classify(err)
	}
	return unfreezeProjection(current), nil
}

func receiverReadiness(receiver domain.ProfitSharingReceiver) paymentport.ReceiverReadiness {
	return paymentport.ReceiverReadiness{Reference: "psrecv_" + strconv.FormatInt(receiver.ID, 10), CustomerID: receiver.CustomerID, AppID: receiver.AppID, State: string(receiver.State), EffectRef: receiver.EffectID, FailureClass: receiver.FailureClass, Ready: receiver.State == domain.ProfitSharingReceiverReady, OutcomeKnown: receiver.State == domain.ProfitSharingReceiverReady || receiver.State == domain.ProfitSharingReceiverFinalFailed, Version: receiver.Version, UpdatedAt: receiver.UpdatedAt.UTC()}
}

func receiverFailureClass(value string) string {
	if value == domain.ProfitSharingReceiverFailureProviderPermissionDenied {
		return value
	}
	return ""
}

func profitSharingReceiverID(reference string) (int64, bool) {
	if !(paymentport.ProfitSharingReceiverRecoveryCommand{ReceiverReference: reference, ActorAdminUserID: 1, IdempotencyKey: "123456789012", EvidenceReference: "evidence"}.Valid()) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(reference, "psrecv_"), 10, 64)
	return id, err == nil && id > 0
}

func distributionPaymentState(payment domain.Payment, funding domain.ProfitSharingFunding, now time.Time) paymentport.DistributionPaymentState {
	// The channel deadline begins at the immutable provider-confirmed payment
	// time. Refund reconciliation and other later bookkeeping move UpdatedAt,
	// but must never extend the period in which a split can be submitted.
	paidAt := confirmedPaymentTime(payment)
	deadline := time.Time{}
	if !paidAt.IsZero() {
		deadline = paidAt.Add(profitSharingWindow)
	}
	available := payment.AmountMinor - funding.SuccessfulRefundMinor - funding.ReservedSplitMinor
	if available < 0 {
		available = 0
	}
	// Qualification needs the confirmed-payment fact for mapped historical
	// imports as well.  Provider splitting has a narrower gate: only a native
	// controlled WeChat transaction with its private transaction reference may
	// be sent to the SDK. A digest-only import is never split-capable.
	confirmed := payment.Status == domain.StatusPaid && payment.AmountMinor > 0
	splitCapable := !payment.Historical && payment.Provider == domain.ProviderWeChatPay && confirmed && !deadline.IsZero() && payment.ProviderTransactionDigest != "" && payment.ProviderTransactionReference != "" && payment.ProfitSharingMarked && now.Before(deadline)
	return paymentport.DistributionPaymentState{OriginalPaymentRef: paymentport.PaymentReference(payment.ID), ConfirmedPaid: confirmed, SplitCapable: splitCapable, RefundExposure: funding.RefundExposure(), DeadlineAt: deadline, OriginalMinor: payment.AmountMinor, AvailableMinor: available, PayerCustomerID: payment.PayerCustomerID, BeneficiaryCustomerID: payment.BeneficiaryCustomerID, SuccessfulRefundMinor: funding.SuccessfulRefundMinor, RequestedRefundMinor: funding.RequestedRefundMinor, ProcessingRefundMinor: funding.ProcessingRefundMinor, OutcomeUnknownRefundMinor: funding.OutcomeUnknownRefundMinor, ConfirmedPaidAt: paidAt, UpdatedAt: payment.UpdatedAt.UTC()}
}

func instructionProjection(value domain.ProfitSharingInstruction) paymentport.ProfitSharingInstruction {
	return paymentport.ProfitSharingInstruction{Reference: "psinst_" + strconv.FormatInt(value.ID, 10), SettlementRef: value.SettlementRef, OriginalPaymentRef: paymentport.PaymentReference(value.PaymentID), State: string(value.State), EffectRef: value.EffectID, FailureClass: value.FailureClass, AmountMinor: value.AmountMinor, Currency: value.Currency, ReceiverConfirmedSuccess: value.ReceiverConfirmedSuccess, OutcomeKnown: value.OutcomeKnown, DeadlineAt: value.DeadlineAt.UTC(), Version: value.Version, UpdatedAt: value.UpdatedAt.UTC()}
}

func instructionFailureClass(value string) string {
	if domain.ValidProfitSharingInstructionFailureClass(value) {
		return value
	}
	return ""
}

func unfreezeProjection(value domain.ProfitSharingUnfreeze) paymentport.ProfitSharingUnfreeze {
	return paymentport.ProfitSharingUnfreeze{Reference: "psunfreeze_" + strconv.FormatInt(value.ID, 10), OriginalPaymentRef: paymentport.PaymentReference(value.PaymentID), State: value.State, EffectRef: value.EffectID, OutcomeKnown: value.OutcomeKnown, Version: value.Version, UpdatedAt: value.UpdatedAt.UTC()}
}

func parsePaymentReference(value string) (int64, bool) {
	if !strings.HasPrefix(value, "payref_") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, "payref_"), 10, 64)
	return id, err == nil && id > 0 && paymentport.PaymentReference(id) == value
}

func validInstructionReference(value string) bool {
	if !strings.HasPrefix(value, "psinst_") {
		return false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, "psinst_"), 10, 64)
	return err == nil && id > 0 && value == "psinst_"+strconv.FormatInt(id, 10)
}

// profitSharingReplayMatches deliberately includes every immutable funding
// input. SettlementRef is a stable business id, not an authorization to reuse
// an earlier payment command with a modified recipient or policy snapshot.
func profitSharingReplayMatches(existing domain.ProfitSharingInstruction, receiver domain.ProfitSharingReceiver, request paymentport.ProfitSharingRequest) bool {
	return existing.AmountMinor == request.AmountMinor &&
		existing.Currency == request.Currency &&
		paymentport.PaymentReference(existing.PaymentID) == request.OriginalPaymentRef &&
		receiver.CustomerID == request.RecipientCustomerID &&
		existing.IdempotencyKeyDigest == string(effectport.Hash("payment.profit-sharing.idempotency.v1", request.IdempotencyKey)) &&
		existing.SourceRefDigest == string(request.SourceDigest) &&
		existing.PayloadDigest == string(request.PayloadDigest) &&
		existing.PolicyVersionHash == string(request.PolicyDigest)
}

func profitSharingUnfreezeReplayMatches(existing domain.ProfitSharingUnfreeze, request paymentport.ProfitSharingUnfreezeRequest) bool {
	return existing.Reason == request.Reason &&
		existing.IdempotencyKeyDigest == string(effectport.Hash("payment.profit-sharing.unfreeze.idempotency.v1", request.IdempotencyKey)) &&
		existing.SourceRefDigest == string(request.SourceDigest) &&
		existing.PayloadDigest == string(request.PayloadDigest) &&
		existing.PolicyVersionHash == string(request.PolicyDigest)
}

func paymentPortText(value string, min, max int) bool {
	return len(value) >= min && len(value) <= max && strings.TrimSpace(value) == value
}

func profitSharingProviderOrderNo(prefix, idempotencyKey string) string {
	digest := sha256.Sum256([]byte("payment.profit-sharing.provider-order.v1\x00" + idempotencyKey))
	return prefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:20])
}

func (s *Service) profitSharingAppID(payment domain.Payment) string {
	// Receiver readiness is keyed by the AppID that created the frozen Payment
	// identity. Composition supplies the concrete configured AppID to the
	// receiver-preparation command; this helper remains a guard for the legacy
	// single-AppID runtime until multi-AppID support is explicitly introduced.
	if payment.Channel == domain.ChannelH5Official {
		return s.h5AppID
	}
	return s.miniAppID
}

var _ paymentport.DistributionSettlementPort = (*Service)(nil)
var _ effectport.CompletionSink = (*Service)(nil)

func confirmedPaymentTime(payment domain.Payment) time.Time {
	if payment.PaidConfirmedAt == nil {
		return time.Time{}
	}
	return payment.PaidConfirmedAt.UTC()
}
