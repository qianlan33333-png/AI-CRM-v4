package invoice

import (
	"context"
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	od "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	op "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	pd "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type paymentContinuation struct {
	OrderID       int64       `json:"order_id"`
	MerchantOrder string      `json:"merchant_order_no"`
	Provider      pd.Provider `json:"provider"`
	Channel       pd.Channel  `json:"channel"`
	Amount        int64       `json:"amount_minor"`
	Currency      string      `json:"currency"`
	ProductID     int64       `json:"product_id"`
	ProductKind   string      `json:"product_kind"`
	ProductName   string      `json:"product_name"`
	Path          string      `json:"payment_path"`
}

func (s *Service) ContinuePayment(ctx context.Context, token string, orderID int64) (paymentContinuation, error) {
	var out paymentContinuation
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		actor, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		o, e := s.Orders.GetSelfWithin(tx, orderID, ids, false)
		if e != nil {
			if errors.Is(e, op.ErrNotFound) {
				return p.ErrNotFound
			}
			return e
		}
		out, e = s.continuationWithin(tx, actor, ids, o)
		return e
	})
	return out, e
}

func (s *Service) continuationWithin(ctx context.Context, actor pp.SessionActor, ids []int64, row op.SelfOrder) (paymentContinuation, error) {
	o := row.Order
	if s.SelfPayments == nil || s.Checkouts == nil || actor.Channel != pd.ChannelH5Official || o.RecordOrigin != od.RecordOriginNative || !o.EffectEligible || o.PayerCustomerID == nil || !slices.Contains(ids, int64(*o.PayerCustomerID)) || o.RefundedMinor != 0 || (o.Status != od.StatusPendingPayment && o.Status != od.StatusPaid) {
		return paymentContinuation{}, p.ErrIneligible
	}
	v, e := s.SelfPayments.SelfCheckoutWithin(ctx, o.ID)
	if errors.Is(e, pp.ErrNotFound) {
		return paymentContinuation{}, p.ErrIneligible
	}
	if e != nil {
		return paymentContinuation{}, e
	}
	if !continuablePayment(v, o, actor, ids) {
		return paymentContinuation{}, p.ErrIneligible
	}
	refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(ctx, []int64{o.ID})
	if e != nil {
		return paymentContinuation{}, e
	}
	if _, found := refunds[o.ID]; found {
		return paymentContinuation{}, p.ErrIneligible
	}
	f, e := s.Checkouts.ReadCheckoutSnapshotWithin(ctx, o.ID)
	if errors.Is(e, op.ErrNotFound) {
		return paymentContinuation{}, p.ErrIneligible
	}
	if e != nil {
		return paymentContinuation{}, e
	}
	if f.OrderID != o.ID || f.ProductID < 1 || f.ProductCode == "" || len(o.Items) != 1 || o.Items[0].ProductID == nil || *o.Items[0].ProductID != f.ProductID || o.Items[0].ProductCode != f.ProductCode || f.PayableAmountMinor != v.AmountMinor || f.Currency != v.Currency {
		return paymentContinuation{}, p.ErrIneligible
	}
	path := "/pay/" + url.PathEscape(f.ProductCode)
	kind := f.ProductType
	if f.ProductType == "service_period" {
		path = "/s/" + url.PathEscape(f.ProductCode) + "/pay"
	} else if f.ProductType == "standard_product" {
		kind = "standard"
	} else {
		return paymentContinuation{}, p.ErrIneligible
	}
	return paymentContinuation{OrderID: o.ID, MerchantOrder: v.MerchantOrderNo, Provider: v.Provider, Channel: v.Channel, Amount: v.AmountMinor, Currency: v.Currency, ProductID: f.ProductID, ProductName: f.ProductName, ProductKind: kind, Path: path + "?resume_order=" + strconv.FormatInt(o.ID, 10)}, nil
}

func continuablePayment(v pd.Payment, o od.Snapshot, actor pp.SessionActor, ids []int64) bool {
	if o.PayerCustomerID == nil || v.Historical || v.OrderID != o.ID || string(v.Provider) != string(o.Provider) || v.PayerIdentityID != actor.PayerIdentityID || !slices.Contains(ids, v.PayerCustomerID) || v.PayerCustomerID != int64(*o.PayerCustomerID) || v.MerchantOrderNo != o.MerchantOrderNo || v.Currency != "CNY" || v.Currency != o.Amount.Currency || v.AmountMinor <= 0 || v.AmountMinor != o.Amount.AmountMinor || strings.TrimSpace(v.MerchantOrderNo) != v.MerchantOrderNo {
		return false
	}
	if v.Provider == pd.ProviderWeChatPay && v.Channel != pd.ChannelH5Official || v.Provider == pd.ProviderAlipay && v.Channel != pd.ChannelAlipayWap && v.Channel != pd.ChannelAlipayPage || v.Provider != pd.ProviderWeChatPay && v.Provider != pd.ProviderAlipay {
		return false
	}
	return v.Status == pd.StatusAwaitingPayment || v.Status == pd.StatusAwaitingPrepay || v.Status == pd.StatusPaid && o.Status == od.StatusPaid
}
