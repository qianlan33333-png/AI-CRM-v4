package invoice

import (
	"context"
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"strings"
)

type officialTitle struct {
	Buyer  p.Buyer
	Source p.Snapshot
}

func sameUnit(a, b p.Buyer) bool {
	return a.Type == b.Type && strings.TrimSpace(a.Name) == strings.TrimSpace(b.Name) && strings.TrimSpace(a.TaxpayerID) == strings.TrimSpace(b.TaxpayerID)
}
func sameTitleSource(a, b p.Snapshot) bool {
	return a.TransactionID == b.TransactionID && a.OrderNo == b.OrderNo && a.Amount == b.Amount && a.MerchantID == b.MerchantID && a.AppID == b.AppID && a.AppScope == b.AppScope && a.SellerTaxID == b.SellerTaxID
}

func (s *Service) resolveOfficialTitle(ctx context.Context, token string, orderID int64, key string, title p.TitleSubmission) (*officialTitle, error) {
	if orderID < 1 || len(key) < 16 || len(key) > 120 {
		return nil, p.ErrInvalid
	}
	if !s.Config.Enabled || s.Provider == nil {
		return nil, p.ErrDisabled
	}
	reader, ok := s.Provider.(p.OptionalTitleReader)
	if !ok {
		return nil, nil
	}
	var target p.Application
	needed := true
	err := s.UoW.Within(ctx, func(tx context.Context) error {
		_, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		o, e := s.Orders.GetSelfWithin(tx, orderID, ids, false)
		if e != nil {
			return p.ErrNotFound
		}
		refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{orderID})
		if e != nil {
			return e
		}
		_, refund := refunds[orderID]
		if s.reason(o, ids, refund, nil) != "" {
			return p.ErrIneligible
		}
		target.Snapshot, e = s.freeze(o.Order)
		if e != nil {
			return e
		}
		target.ID = target.Snapshot.TransactionID
		if title.ExpectedAmount != target.Snapshot.Amount {
			return p.ErrConflict
		}
		a, e := s.Store.byOrder(tx, orderID)
		if e == nil {
			needed = a.State == "awaiting_title" && a.IssueEffect == ""
			return nil
		}
		if !errors.Is(e, p.ErrNotFound) {
			return e
		}
		return nil
	})
	if err != nil || !needed {
		return nil, err
	}
	// Provider network reads are outside both the preliminary read and acceptance UoW.
	buyer, present, err := reader.ReadOptionalTitle(ctx, target)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	expected := p.Buyer{Type: title.Buyer.Type, Name: title.Buyer.Name, TaxpayerID: title.Buyer.TaxpayerID}
	if !sameUnit(buyer, expected) || !officialBuyerValid(buyer, target.Snapshot) {
		return nil, p.ErrConflict
	}
	return &officialTitle{Buyer: buyer, Source: target.Snapshot}, nil
}
