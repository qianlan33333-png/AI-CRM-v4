package app

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type checkoutAbandonmentStore interface {
	CheckoutAbandoned(context.Context, int64) (bool, error)
	RecordCheckoutAbandonment(context.Context, paymentport.AbandonCheckoutCommand, time.Time) error
}

func (s *Service) SetCanonicalLineageReader(reader identityport.CanonicalLineageReader) {
	s.lineage = reader
}

// AbandonCheckout preserves the original payment and unknown effect. A human
// observation of no debit is not substituted for a signed Provider receipt.
// This command neither retries prepay nor authorizes a replacement payment.
func (s *Service) AbandonCheckout(ctx context.Context, c paymentport.AbandonCheckoutCommand) error {
	if s == nil || s.uow == nil || s.store == nil || s.effectReader == nil || c.PaymentID < 1 || !validScope(c.ActorScope) || !c.ConfirmedNoDebit {
		return paymentport.ErrInvalid
	}
	evidence, err := hex.DecodeString(c.EvidenceDigest)
	if err != nil || len(evidence) != 32 || c.EvidenceDigest == "0000000000000000000000000000000000000000000000000000000000000000" {
		return paymentport.ErrInvalid
	}
	local, ok := s.store.(checkoutAbandonmentStore)
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
	projection, err := s.effectReader.Get(ctx, current.EffectID)
	if err != nil {
		return paymentport.ErrUnavailable
	}
	if projection.ID != current.EffectID || projection.Owner != effectport.OwnerPayment || projection.Kind != effectport.KindWeChatPayPrepay || projection.State != "outcome_unknown" {
		return paymentport.ErrConflict
	}
	return classify(s.uow.Within(ctx, func(tx context.Context) error {
		locked, err := s.store.GetPayment(tx, c.PaymentID, true)
		if err != nil {
			return err
		}
		if !abandonableCheckout(locked) || locked.Version != current.Version || locked.EffectID != current.EffectID {
			return paymentport.ErrConflict
		}
		_, err = s.store.GetHandoff(tx, locked.ID)
		// Even an expired handoff disqualifies this path: it might have been used.
		if !errors.Is(err, paymentport.ErrNotFound) {
			if err != nil {
				return err
			}
			return paymentport.ErrConflict
		}
		return local.RecordCheckoutAbandonment(tx, c, s.now().UTC())
	}))
}
func abandonableCheckout(p domain.Payment) bool {
	legacy, err := hex.DecodeString(strings.TrimPrefix(p.MerchantOrderNo, "v3pay_"))
	return err == nil && len(legacy) == 16 && strings.HasPrefix(p.MerchantOrderNo, "v3pay_") && !p.Historical && p.Provider == domain.ProviderWeChatPay && p.Channel == domain.ChannelH5Official && p.Status == domain.StatusAwaitingPrepay && len(p.MerchantOrderNo) == 38 && p.EffectID != ""
}
