package invoice

import (
	"context"
	"encoding/json"
	"errors"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	order "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"slices"
	"time"
)

// RecoverFailed is an explicit action, never a read-side automatic retry. A
// caller either authenticated the owner or selected the one-shot runtime repair.
// expectedInvoiceID binds a native title job to one failed attempt. Network reads
// precede the UoW; a fresh locked comparison prevents stale proofs minting a ticket.
func (s *Service) RecoverFailed(ctx context.Context, id, expectedInvoiceID string, canonicalize bool) (p.Receipt, error) {
	if !s.Config.Enabled || s.Provider == nil {
		return p.Receipt{}, p.ErrDisabled
	}
	var prior p.Application
	err := s.UoW.Within(ctx, func(tx context.Context) error { var e error; prior, e = s.Store.get(tx, id, nil, false); return e })
	if errors.Is(err, p.ErrNotFound) && canonicalize && expectedInvoiceID == "" {
		return s.recoverMissingNative(ctx, id)
	}
	if err != nil {
		return p.Receipt{}, err
	}
	// Only the explicit one-shot operator (no expected failed ticket) may replay
	// an unaccepted unknown request. Native jobs and ordinary GET never do this.
	if canonicalize && expectedInvoiceID == "" && prior.State == "issuing" {
		var rejection effects.Digest
		var matched bool
		err = s.UoW.Within(ctx, func(tx context.Context) error {
			used, e := s.Store.buyerCorrectionUsed(tx, prior)
			if e != nil {
				return e
			}
			if used {
				return p.ErrIneligible
			}
			rejection, matched, e = s.Store.buyerMismatch(tx, prior)
			return e
		})
		if err != nil {
			return p.MarshalReceipt(prior), err
		}
		if matched {
			return s.correctRejectedBuyer(ctx, prior, rejection)
		}
		return s.recoverUnsubmitted(ctx, prior)
	}
	if prior.State != "failed" || expectedInvoiceID != "" && prior.InvoiceID != expectedInvoiceID {
		return p.MarshalReceipt(prior), nil
	}
	if !canonicalize && prior.ID != prior.Snapshot.TransactionID {
		return p.MarshalReceipt(prior), p.ErrConflict
	}
	proof, err := s.Provider.Query(ctx, prior)
	if err != nil {
		return p.MarshalReceipt(prior), err
	}
	if proof.InvoiceID != prior.InvoiceID || proof.Status != "ISSUE_FAILED" || proof.Blue.Number != "" || proof.TotalAmount != 0 && proof.TotalAmount != prior.Snapshot.Amount || proof.Seller.TaxpayerID != "" && proof.Seller.TaxpayerID != prior.Snapshot.SellerTaxID {
		return p.MarshalReceipt(prior), p.ErrPending
	}
	buyer := prior.Buyer
	targetID := prior.ID
	if canonicalize && prior.ID != prior.Snapshot.TransactionID {
		if !p.ValidApplyID(prior.Snapshot.TransactionID) {
			return p.Receipt{}, p.ErrConflict
		}
		check, ok := s.Provider.(p.UnsubmittedVerifier)
		if !ok {
			return p.Receipt{}, p.ErrUnavailable
		}
		target := prior
		target.ID = prior.Snapshot.TransactionID
		if err = check.VerifyUnsubmitted(ctx, target); err != nil {
			return p.MarshalReceipt(prior), err
		}
		targetID = target.ID
	}
	if canonicalize {
		target := prior
		target.ID = targetID
		buyer, err = s.Provider.ReadTitle(ctx, target)
		if err != nil {
			return p.MarshalReceipt(prior), err
		}
		if buyer.Amount != prior.Snapshot.Amount || buyer.OrderNo != prior.Snapshot.OrderNo {
			return p.Receipt{}, p.ErrConflict
		}
	}
	if !officialBuyerValid(buyer, prior.Snapshot) {
		return p.Receipt{}, p.ErrInvalid
	}
	// Retain the complete official title. WeChat validates optional fields too;
	// removing a native address/bank/email makes the same unit title inconsistent.
	var receipt p.Receipt
	err = s.withOrder(ctx, id, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if a.State != "failed" || a.InvoiceID != prior.InvoiceID {
			receipt = p.MarshalReceipt(*a)
			return nil
		}
		if a.BlueNumber != "" || a.RefundConfirmed || o.Order.RefundedMinor > 0 || a.IssueAttempt >= 4 {
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
		if a.IssueEffect != "" {
			if s.EffectStates == nil {
				return p.ErrUnavailable
			}
			effect, e := s.EffectStates.GetWithin(tx, a.IssueEffect)
			if e != nil {
				return e
			}
			if effect.State == effects.StateQueued || effect.State == effects.StateAttempted || effect.State == effects.StateUnknown {
				return p.ErrPending
			}
		}
		fresh, e := s.freeze(o.Order)
		if e != nil {
			return e
		}
		before := a.Snapshot
		// Only finance-controlled lines/billing person may change. Order, customer,
		// collecting merchant and sales entity stay those of the verified failure.
		stable := fresh
		stable.Lines = before.Lines
		stable.BillingPersonID = before.BillingPersonID
		oldJSON, _ := json.Marshal(before)
		stableJSON, _ := json.Marshal(stable)
		if string(oldJSON) != string(stableJSON) {
			return p.ErrConflict
		}
		if targetID != a.ID {
			if s.Payments == nil || s.Subjects == nil {
				return p.ErrUnavailable
			}
			pay, e := s.Payments.InvoicePaymentWithin(tx, targetID)
			if e != nil {
				return e
			}
			subject, ok, e := s.Subjects.VerifiedIdentitySubject(tx, a.IdentityID)
			if e != nil {
				return e
			}
			if !ok || pay.OrderID != a.OrderID || pay.IdentityID != a.IdentityID || pay.CustomerID != a.CustomerID || !slices.Contains(ids, int64(subject.CustomerID)) || pay.Amount != fresh.Amount || pay.OrderNo != fresh.OrderNo || pay.Currency != "CNY" {
				return p.ErrConflict
			}
		}
		newInvoice, e := newID()
		if e != nil {
			return e
		}
		if e = s.Store.recover(tx, a, targetID, fresh, proof); e != nil {
			return e
		}
		a.InvoiceID = newInvoice
		a.Buyer = buyer
		a.IssueAttempt++
		a.CardState = "pending"
		if e = s.queue(tx, a, false, time.Time{}); e != nil {
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

// recoverUnsubmitted preserves both application and invoice identifiers and
// the frozen payload. The full official application must affirm no acceptance,
// and the existing finality port must close the expired unknown attempt first.
func (s *Service) recoverUnsubmitted(ctx context.Context, prior p.Application) (p.Receipt, error) {
	check, ok := s.Provider.(p.UnsubmittedVerifier)
	if !ok || s.Finality == nil || s.EffectStates == nil || prior.ID != prior.Snapshot.TransactionID {
		return p.MarshalReceipt(prior), p.ErrUnavailable
	}
	if err := check.VerifyUnsubmitted(ctx, prior); err != nil {
		return p.MarshalReceipt(prior), err
	}
	var receipt p.Receipt
	err := s.withOrder(ctx, prior.ID, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if a.State != "issuing" || a.InvoiceID != prior.InvoiceID || a.IssueEffect != prior.IssueEffect || a.DispatchAttempt != prior.DispatchAttempt {
			receipt = p.MarshalReceipt(*a)
			return nil
		}
		if a.BlueNumber != "" || a.RefundConfirmed || a.DispatchAttempt >= 1 || a.Buyer.Type != "ORGANIZATION" || !a.Buyer.Valid(a.Snapshot) {
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
		// A replay retains its original finance payload, but changing payment,
		// order, app scope or seller facts invalidates the original confirmation.
		fresh.Lines = a.Snapshot.Lines
		fresh.BillingPersonID = a.Snapshot.BillingPersonID
		frozenJSON, _ := json.Marshal(a.Snapshot)
		freshJSON, _ := json.Marshal(fresh)
		if string(frozenJSON) != string(freshJSON) {
			return p.ErrConflict
		}
		effect, e := s.EffectStates.GetWithin(tx, a.IssueEffect)
		if e != nil {
			return e
		}
		if effect.State != effects.StateUnknown {
			return p.ErrPending
		}
		evidence := effects.Hash("invoice.verified.not-submitted", a.ID, a.InvoiceID, a.IssueEffect)
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
		a.DispatchAttempt++
		a.CardState = "pending"
		if e = s.Store.update(tx, *a, "unsubmitted_recovery", string(evidence)); e != nil {
			return e
		}
		if e = s.queue(tx, a, false, time.Time{}); e != nil {
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

// RecoverNativeTitle replays no write when another attempt already replaced the
// specific failed ticket. Ordinary provider queries do not use this operation.
func (s *Service) RecoverNativeTitle(ctx context.Context, id, failedInvoiceID string) error {
	_, err := s.RecoverFailed(ctx, id, failedInvoiceID, true)
	if err != nil && !errors.Is(err, p.ErrPending) {
		return err
	}
	err = s.Reconcile(ctx, id)
	if errors.Is(err, p.ErrPending) {
		return p.ErrPending
	}
	return err
}
