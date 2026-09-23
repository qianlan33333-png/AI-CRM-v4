package domain

import (
	"errors"
	"strings"
	"time"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var ErrInvalid = errors.New("invalid payment")
var ErrTransition = errors.New("invalid payment transition")
var ErrVersion = errors.New("payment version conflict")

type Provider string
type Channel string

const (
	ProviderWeChatPay  Provider = "wechat_pay"
	ProviderWeChatShop Provider = "wechat_shop"
	ProviderAlipay     Provider = "alipay"
	ChannelMiniProgram Channel  = "mini_program"
	ChannelH5Official  Channel  = "h5_official_account"
	ChannelAlipayWap   Channel  = "alipay_wap"
	ChannelAlipayPage  Channel  = "alipay_page"
)

type Payment struct {
	Historical                                              bool
	SourceStatus, HistoryReason                             string
	ID, OrderID                                             int64
	Provider                                                Provider
	Channel                                                 Channel
	MerchantOrderNo                                         string
	PayerIdentityID, PayerCustomerID, BeneficiaryCustomerID int64
	AmountMinor                                             int64
	ProfitSharingMarked                                     bool
	Currency                                                string
	Status                                                  Status
	EffectID                                                string
	ProviderTransactionReference                            string
	ProviderTransactionDigest                               string
	Version                                                 int64
	PaidConfirmedAt                                         *time.Time
	CreatedAt, UpdatedAt                                    time.Time
}
type Refund struct {
	ID, PaymentID           int64
	Provider                Provider
	RefundNo, Reason        string
	AmountMinor             int64
	Status                  RefundStatus
	EffectID                string
	ProviderRefundReference string
	ProviderRefundDigest    string
	Version                 int64
	CreatedAt, UpdatedAt    time.Time
}

func NewPayment(order orderdomain.Snapshot, payerIdentityID int64, now time.Time, requestedChannel ...Channel) (Payment, error) {
	return newPayment(order, payerIdentityID, now, false, requestedChannel...)
}

// NewPaymentWithProfitSharing is called only by the server-side checkout
// coordinator after it has frozen a valid first-level attribution. The bool is
// never derived from public request JSON.
func NewPaymentWithProfitSharing(order orderdomain.Snapshot, payerIdentityID int64, marked bool, now time.Time, requestedChannel ...Channel) (Payment, error) {
	return newPayment(order, payerIdentityID, now, marked, requestedChannel...)
}

func newPayment(order orderdomain.Snapshot, payerIdentityID int64, now time.Time, profitSharingMarked bool, requestedChannel ...Channel) (Payment, error) {
	if order.ID < 1 || order.RecordOrigin != orderdomain.RecordOriginNative || !order.EffectEligible || order.PayerCustomerID == nil || order.BeneficiaryCustomerID == nil || payerIdentityID < 1 || now.IsZero() || order.Amount.Currency != "CNY" {
		return Payment{}, ErrInvalid
	}
	provider := Provider(order.Provider)
	if provider != ProviderWeChatPay && provider != ProviderWeChatShop && provider != ProviderAlipay {
		return Payment{}, ErrInvalid
	}
	channel := ChannelMiniProgram
	if len(requestedChannel) > 0 {
		channel = requestedChannel[0]
	}
	if channel != ChannelMiniProgram && channel != ChannelH5Official && channel != ChannelAlipayWap && channel != ChannelAlipayPage {
		return Payment{}, ErrInvalid
	}
	if profitSharingMarked && provider != ProviderWeChatPay {
		return Payment{}, ErrInvalid
	}
	return Payment{OrderID: order.ID, Provider: provider, Channel: channel, MerchantOrderNo: order.MerchantOrderNo, PayerIdentityID: payerIdentityID, PayerCustomerID: *order.PayerCustomerID, BeneficiaryCustomerID: *order.BeneficiaryCustomerID, AmountMinor: order.Amount.AmountMinor, ProfitSharingMarked: profitSharingMarked, Currency: order.Amount.Currency, Status: StatusAwaitingPrepay, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}
func (p Payment) BindEffect(expected int64, effectID string, now time.Time) (Payment, error) {
	if expected != p.Version {
		return Payment{}, ErrVersion
	}
	if p.Status != StatusAwaitingPrepay || !validEffectID(effectID) || now.Before(p.UpdatedAt) {
		return Payment{}, ErrTransition
	}
	p.EffectID = effectID
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}
func (p Payment) Settle(expected int64, status Status, now time.Time) (Payment, error) {
	if expected != p.Version {
		return Payment{}, ErrVersion
	}
	if now.Before(p.UpdatedAt) || (p.Status != StatusAwaitingPrepay && p.Status != StatusAwaitingPayment) || (status != StatusAwaitingPayment && status != StatusPaid && status != StatusFailed && status != StatusCancelled) {
		return Payment{}, ErrTransition
	}
	p.Status = status
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

// RestorePaidConfirmation records a Provider-verified original success time
// for a legacy native payment already marked paid but missing that immutable
// fact. It cannot settle a new payment, change an existing confirmation, or
// substitute bookkeeping UpdatedAt for Provider time.
func (p Payment) RestorePaidConfirmation(expected int64, confirmedAt, reconciledAt time.Time) (Payment, error) {
	if expected != p.Version || p.Status != StatusPaid || p.PaidConfirmedAt != nil || confirmedAt.IsZero() || reconciledAt.IsZero() || confirmedAt.Before(p.CreatedAt) || reconciledAt.Before(p.UpdatedAt) || reconciledAt.Before(confirmedAt) {
		return Payment{}, ErrTransition
	}
	confirmed := confirmedAt.UTC()
	p.PaidConfirmedAt = &confirmed
	p.Version++
	p.UpdatedAt = reconciledAt.UTC()
	return p, nil
}
func NewRefund(payment Payment, refundNo string, amount int64, reason string, now time.Time) (Refund, error) {
	refundNo = strings.TrimSpace(refundNo)
	reason = strings.TrimSpace(reason)
	if payment.Historical || payment.ID < 1 || payment.Status != StatusPaid || refundNo == "" || len(refundNo) > 200 || amount < 1 || amount > payment.AmountMinor || reason == "" || len(reason) > 500 || now.IsZero() {
		return Refund{}, ErrInvalid
	}
	return Refund{PaymentID: payment.ID, Provider: payment.Provider, RefundNo: refundNo, Reason: reason, AmountMinor: amount, Status: RefundRequested, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}
func (r Refund) BindEffect(expected int64, effectID string, now time.Time) (Refund, error) {
	if expected != r.Version {
		return Refund{}, ErrVersion
	}
	if r.Status != RefundRequested || !validEffectID(effectID) || now.Before(r.UpdatedAt) {
		return Refund{}, ErrTransition
	}
	r.Status = RefundEffectAccepted
	r.EffectID = effectID
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, nil
}
func (r Refund) Complete(expected int64, status RefundStatus, now time.Time) (Refund, error) {
	if expected != r.Version {
		return Refund{}, ErrVersion
	}
	if now.Before(r.UpdatedAt) || (r.Status != RefundEffectAccepted && r.Status != RefundOutcomeUnknown) || (status != RefundOutcomeUnknown && status != RefundCompleted && status != RefundFinalFailed) {
		return Refund{}, ErrTransition
	}
	r.Status = status
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, nil
}
func validEffectID(value string) bool {
	if !strings.HasPrefix(value, "eer_") || len(value) < 5 {
		return false
	}
	for _, r := range value[4:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value[4] != '0'
}
