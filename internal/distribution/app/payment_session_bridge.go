package app

import (
	"context"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// PaymentSessionBridge reads a short-lived verified Payment/WeChat session
// only to bootstrap the separate Distribution cookie. It deliberately does
// not expose the Payment session to Distribution HTTP, nor renew/mutate it.
type PaymentSessionBridge struct {
	uow                     platformport.UnitOfWork
	reader                  paymentport.SessionReader
	distribution            *BrowserSessionService
	miniAppID, miniAppScope string
	h5AppID, h5AppScope     string
	now                     func() time.Time
}

func NewPaymentSessionBridge(uow platformport.UnitOfWork, reader paymentport.SessionReader, distribution *BrowserSessionService, miniAppID, miniAppScope, h5AppID, h5AppScope string) (*PaymentSessionBridge, error) {
	if uow == nil || reader == nil || distribution == nil || miniAppID == "" || miniAppScope == "" || (h5AppID == "") != (h5AppScope == "") {
		return nil, distributionport.ErrUnavailable
	}
	return &PaymentSessionBridge{uow: uow, reader: reader, distribution: distribution, miniAppID: miniAppID, miniAppScope: miniAppScope, h5AppID: h5AppID, h5AppScope: h5AppScope, now: time.Now}, nil
}

func (b *PaymentSessionBridge) BridgePaymentSession(ctx context.Context, token string) (string, time.Time, error) {
	if b == nil || b.uow == nil || b.reader == nil || b.distribution == nil || len(token) < 20 || len(token) > 100 {
		return "", time.Time{}, distributionport.ErrUnauthorized
	}
	var source paymentport.SessionActor
	err := b.uow.Within(ctx, func(tx context.Context) error {
		var err error
		source, err = b.reader.LookupWithin(tx, token, b.now().UTC())
		return err
	})
	if err != nil {
		return "", time.Time{}, distributionport.ErrUnauthorized
	}
	actor := distributionport.TrustedSessionActor{CustomerID: source.PayerCustomerID, IdentityID: source.PayerIdentityID, OccurredAt: b.now().UTC()}
	switch source.Channel {
	case paymentdomain.ChannelMiniProgram:
		actor.Channel, actor.AppID, actor.AppScope = "mini_program", b.miniAppID, b.miniAppScope
	case paymentdomain.ChannelH5Official:
		actor.Channel, actor.AppID, actor.AppScope = "h5_official_account", b.h5AppID, b.h5AppScope
	default:
		return "", time.Time{}, distributionport.ErrUnauthorized
	}
	if !actor.Valid() {
		return "", time.Time{}, distributionport.ErrUnauthorized
	}
	return b.distribution.Issue(ctx, actor)
}
