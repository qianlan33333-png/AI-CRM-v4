package app

import (
	"context"
	"strconv"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type profitSharingAbsenceStore interface {
	RecordProfitSharingAbsenceWithin(context.Context, domain.ProfitSharingInstruction, effectport.Projection, effectport.Digest, time.Time) (bool, error)
	ResetProfitSharingAbsenceWithin(context.Context, int64, time.Time) error
	ReservedProfitSharingReferencesWithin(context.Context, int64) ([]string, error)
}

func (s *Service) reconcileAbsentProfitSharing(ctx context.Context, original domain.ProfitSharingInstruction, query paymentport.ProfitSharingProviderResult) (paymentport.ProfitSharingInstruction, error) {
	store, err := s.distributionStore()
	proof, ok := s.store.(profitSharingAbsenceStore)
	if err != nil || !ok || s.effectReader == nil || query.State != "NOT_FOUND" || query.OutcomeKnown || query.ReceiverConfirmedSuccess || query.ReceiverConfirmedFailure {
		if resetErr := s.resetProfitSharingAbsence(ctx, original.ID); resetErr != nil {
			return paymentport.ProfitSharingInstruction{}, classify(resetErr)
		}
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	effect, err := s.effectReader.Get(ctx, original.EffectID)
	if err != nil || effect.ID != original.EffectID || effect.Owner != effectport.OwnerPayment || effect.Kind != effectport.KindWeChatPayProfitSharing || effect.State != effectport.StateUnknown || effect.AttemptCount < 1 || effect.Generation < 1 || effect.UpdatedAt.IsZero() {
		if resetErr := s.resetProfitSharingAbsence(ctx, original.ID); resetErr != nil {
			return paymentport.ProfitSharingInstruction{}, classify(resetErr)
		}
		return paymentport.ProfitSharingInstruction{}, paymentport.ErrUnavailable
	}
	var current domain.ProfitSharingInstruction
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := store.GetProfitSharingInstruction(tx, "psinst_"+strconv.FormatInt(original.ID, 10), true)
		if inner != nil {
			return inner
		}
		if locked.State == domain.ProfitSharingPaid || locked.State == domain.ProfitSharingCancelled || (locked.State == domain.ProfitSharingException && locked.OutcomeKnown) {
			current = locked
			return nil
		}
		if locked.Version != original.Version || locked.EffectID != original.EffectID {
			return paymentport.ErrConflict
		}
		confirmed, inner := proof.RecordProfitSharingAbsenceWithin(tx, locked, effect, query.EvidenceDigest, s.now().UTC())
		if inner != nil {
			return inner
		}
		current = locked
		if !confirmed {
			return nil
		}
		// Cancelling this unexecuted instruction does not cancel the separate
		// commission obligation; Distribution still requires a refund fact.
		locked.State, locked.OutcomeKnown, locked.ReceiverConfirmedSuccess = domain.ProfitSharingCancelled, true, false
		// Absence is not a receiver CLOSED reason; preserve that column's
		// finite contract and keep the proof in the existing audit instead.
		locked.FailureClass = ""
		locked.Version++
		locked.UpdatedAt = s.now().UTC()
		if inner = store.ReleaseProfitSharingReserve(tx, locked.ID, "original_order_absent", locked.UpdatedAt); inner != nil {
			return inner
		}
		current, inner = store.UpdateProfitSharingInstruction(tx, locked, "verified_absence:"+string(query.EvidenceDigest))
		return inner
	})
	return instructionProjection(current), classify(err)
}

func (s *Service) resetProfitSharingAbsence(ctx context.Context, id int64) error {
	proof, ok := s.store.(profitSharingAbsenceStore)
	if !ok {
		return nil
	}
	store, err := s.distributionStore()
	if err != nil {
		return err
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := store.GetProfitSharingInstruction(tx, "psinst_"+strconv.FormatInt(id, 10), true)
		if inner != nil {
			return inner
		}
		if locked.State == domain.ProfitSharingPaid || locked.State == domain.ProfitSharingCancelled || (locked.State == domain.ProfitSharingException && locked.OutcomeKnown) {
			return nil
		}
		return proof.ResetProfitSharingAbsenceWithin(tx, id, s.now().UTC())
	})
}
