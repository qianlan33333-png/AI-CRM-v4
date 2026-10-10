package app

import (
	"context"
	"strconv"
	"strings"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// The caller holds the payment lock. Cancellation, reserve release, refund
// receipt/effect acceptance and refund exposure all commit in its one UoW.
// Dispatch can win the effect lock; that preserves the split and never makes
// querying it a prerequisite for the customer's independent refund request.
func (s *Service) cancelRefundQueuedProfitSharingWithin(ctx context.Context, payment domain.Payment, command paymentport.RefundCommand) error {
	if payment.Provider != domain.ProviderWeChatPay || payment.Historical {
		return nil
	}
	// HTTP freezes this scope from the authenticated principal, never from a
	// request field. Root operations retain their distinct actor in the refund
	// receipt; the existing due worker owns their subsequent queued cancellation.
	// Both paths are protected by refund exposure at split material loading.
	adminID, err := strconv.ParseInt(strings.TrimPrefix(command.ActorScope, "admin:"), 10, 64)
	if !strings.HasPrefix(command.ActorScope, "admin:") || err != nil || adminID < 1 {
		return nil
	}
	refs, ok := s.store.(interface {
		ReservedProfitSharingReferencesWithin(context.Context, int64) ([]string, error)
	})
	if !ok {
		return nil
	}
	reserved, err := refs.ReservedProfitSharingReferencesWithin(ctx, payment.ID)
	if err != nil || len(reserved) == 0 {
		return err
	}
	store, err := s.distributionStore()
	if err != nil {
		return err
	}
	for _, reference := range reserved {
		instruction, err := store.GetProfitSharingInstruction(ctx, reference, true)
		if err != nil {
			return err
		}
		if instruction.State != domain.ProfitSharingAccepted || instruction.EffectID == "" {
			continue
		}
		canceller, ok := s.effects.(effectport.TransactionalCanceller)
		if !ok {
			return paymentport.ErrUnavailable
		}
		effect, err := canceller.CancelQueuedEffectWithin(ctx, effectport.CancelCommand{
			EffectID: instruction.EffectID, ReceiptKey: effectport.Hash("payment.profit-sharing.refund.cancel.v1", reference, command.ActorScope, command.IdempotencyKey),
			ReasonCode: "refund_requested", Actor: effectport.ControlActor{AdminUserID: adminID}, OnlyIfUnattempted: true,
		})
		if err != nil {
			return err
		}
		if effect.State != effectport.StateCancelled || effect.AttemptCount != 0 {
			continue
		}
		now := s.now().UTC()
		instruction, err = instruction.Cancel(instruction.Version, "refund_requested", now)
		if err != nil {
			return paymentport.ErrConflict
		}
		if _, err = store.UpdateProfitSharingInstruction(ctx, instruction, "refund_requested"); err != nil {
			return err
		}
		if err = store.ReleaseProfitSharingReserve(ctx, instruction.ID, "refund_requested", now); err != nil {
			return err
		}
	}
	return nil
}
