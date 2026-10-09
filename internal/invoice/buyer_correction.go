package invoice

import (
	"context"
	"encoding/json"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	order "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"time"
	"unicode/utf8"
)

const buyerMismatchMessage = "商户提交的购买方信息与用户填写的发票抬头不一致，请检查参数中的购买方信息与【获取用户填写的抬头】接口返回的发票抬头信息是否一致"

func officialBuyerValid(b p.Buyer, s p.Snapshot) bool {
	if b.Type != "ORGANIZATION" || !b.Valid(s) || b.BillType != "" && b.BillType != "COMM_FAPIAO" {
		return false
	}
	for _, field := range []struct {
		value string
		limit int
	}{{b.Name, 256}, {b.TaxpayerID, 32}, {b.Address, 128}, {b.Telephone, 32}, {b.BankName, 128}, {b.BankAccount, 32}, {b.Phone, 32}, {b.Email, 190}, {b.UserApplyMessage, 100}} {
		if !utf8.ValidString(field.value) || len(field.value) > field.limit {
			return false
		}
	}
	return true
}

// Correct only a diagnosed rejection, never replay an arbitrary unknown write.
// The persistent correction audit closes this path even if official extras later change.
func (s *Service) correctRejectedBuyer(ctx context.Context, prior p.Application, rejection effects.Digest) (p.Receipt, error) {
	check, ok := s.Provider.(p.UnsubmittedVerifier)
	if !ok || s.Finality == nil || s.EffectStates == nil || prior.ID != prior.Snapshot.TransactionID {
		return p.MarshalReceipt(prior), p.ErrUnavailable
	}
	if err := check.VerifyUnsubmitted(ctx, prior); err != nil {
		return p.MarshalReceipt(prior), err
	}
	buyer, err := s.Provider.ReadTitle(ctx, prior)
	if err != nil {
		return p.MarshalReceipt(prior), err
	}
	if !officialBuyerValid(buyer, prior.Snapshot) || !sameUnit(buyer, prior.Buyer) || buyer.Amount != prior.Snapshot.Amount || buyer.OrderNo != prior.Snapshot.OrderNo {
		return p.MarshalReceipt(prior), p.ErrConflict
	}
	oldJSON, _ := json.Marshal(prior.Buyer)
	newJSON, _ := json.Marshal(buyer)
	if string(oldJSON) == string(newJSON) {
		return p.MarshalReceipt(prior), p.ErrPending
	}
	var receipt p.Receipt
	err = s.withOrder(ctx, prior.ID, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if a.State != "issuing" || a.InvoiceID != prior.InvoiceID || a.IssueEffect != prior.IssueEffect || a.DispatchAttempt != prior.DispatchAttempt {
			receipt = p.MarshalReceipt(*a)
			return nil
		}
		used, e := s.Store.buyerCorrectionUsed(tx, *a)
		if e != nil {
			return e
		}
		if used {
			return p.ErrIneligible
		}
		proof, matched, e := s.Store.buyerMismatch(tx, *a)
		if e != nil {
			return e
		}
		if !matched || proof != rejection {
			return p.ErrPending
		}
		if a.BlueNumber != "" || a.RefundConfirmed {
			return p.ErrIneligible
		}
		ids, e := s.customerIDs(tx, a.CustomerID)
		if e != nil {
			return e
		}
		refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{a.OrderID})
		if e != nil {
			return e
		}
		_, refund := refunds[a.OrderID]
		if s.reason(o, ids, refund, nil) != "" {
			return p.ErrIneligible
		}
		fresh, e := s.freeze(o.Order)
		if e != nil {
			return e
		}
		fresh.Lines = a.Snapshot.Lines
		fresh.BillingPersonID = a.Snapshot.BillingPersonID
		frozen, _ := json.Marshal(a.Snapshot)
		current, _ := json.Marshal(fresh)
		if string(frozen) != string(current) {
			return p.ErrConflict
		}
		effect, e := s.EffectStates.GetWithin(tx, a.IssueEffect)
		if e != nil {
			return e
		}
		if effect.State != effects.StateUnknown {
			return p.ErrPending
		}
		evidence := effects.Hash("invoice.verified.buyer-correction", a.ID, a.InvoiceID, a.IssueEffect, string(rejection), string(newJSON))
		if e = s.Finality.ReconcileInvoiceWithin(tx, a.IssueEffect, evidence); e != nil {
			return e
		}
		effect, e = s.EffectStates.GetWithin(tx, a.IssueEffect)
		if e != nil {
			return e
		}
		if effect.State != effects.StateReconciled {
			return p.ErrPending
		}
		if e = s.Store.correctBuyer(tx, a, buyer, evidence, rejection); e != nil {
			return e
		}
		// A diagnosed material correction is a distinct payload-bound intent. Neither
		// its invoice identifier nor the one-replay dispatch budget changes.
		if e = s.queueRevision(tx, a, false, time.Time{}, string(evidence)); e != nil {
			return e
		}
		if e = s.Jobs.EnqueueWithin(tx, a.ID, time.Time{}); e != nil {
			return e
		}
		receipt = p.MarshalReceipt(*a)
		return nil
	})
	return receipt, err
}
