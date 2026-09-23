package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"time"
)

// The official JSAPI prepay credential lives two hours. An extra ten minutes
// bounds clock/processing skew. No legacy credential was ever handed out.
const legacyPrepayRecoveryDelay = 130 * time.Minute

type checkoutRestartStore interface {
	CheckoutRestartAllowed(context.Context, int64) (bool, error)
	RecordCheckoutRestart(context.Context, paymentport.AbandonCheckoutCommand, time.Time, time.Time) error
}

func (s *Service) AllowCheckoutRestart(ctx context.Context, c paymentport.AbandonCheckoutCommand) error {
	if s == nil || s.uow == nil || s.store == nil || c.PaymentID < 1 || !validScope(c.ActorScope) || !c.ConfirmedNoDebit {
		return paymentport.ErrInvalid
	}
	local, ok := s.store.(checkoutRestartStore)
	if !ok {
		return paymentport.ErrUnavailable
	}
	reader, ok := s.effectReader.(effectport.StoppedAttemptReader)
	if !ok {
		return paymentport.ErrUnavailable
	}
	current, err := s.GetPayment(ctx, c.PaymentID)
	if err != nil {
		return err
	}
	if !abandonableCheckout(current) {
		return paymentport.ErrConflict
	}
	// Lock Effect before Payment, matching the completion transaction lock order.
	return classify(s.uow.Within(ctx, func(tx context.Context) error {
		proof, err := reader.StoppedAttemptWithin(tx, current.EffectID)
		if err != nil {
			return paymentport.ErrConflict
		}
		now := s.now().UTC()
		if proof.ID != current.EffectID || proof.Owner != effectport.OwnerPayment || proof.Kind != effectport.KindWeChatPayPrepay || proof.State != effectport.StateUnknown || proof.AttemptCount != 1 || proof.CompletedAt.IsZero() || now.Before(proof.CompletedAt.Add(legacyPrepayRecoveryDelay)) {
			return paymentport.ErrConflict
		}
		locked, err := s.store.GetPayment(tx, c.PaymentID, true)
		if err != nil {
			return err
		}
		if !abandonableCheckout(locked) || locked.Version != current.Version || locked.EffectID != current.EffectID {
			return paymentport.ErrConflict
		}
		_, err = s.store.GetHandoff(tx, locked.ID)
		if !errors.Is(err, paymentport.ErrNotFound) {
			if err != nil {
				return err
			}
			return paymentport.ErrConflict
		}
		return local.RecordCheckoutRestart(tx, c, proof.CompletedAt, now)
	}))
}
