package invoice

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	payment "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"slices"
	"time"
)

func (s *Service) Notification(ctx context.Context, n p.Notice) error {
	if n.ID == "" || !p.ValidApplyID(n.ApplicationID) || !effects.ValidDigest(effects.Digest(n.BodyDigest)) {
		return p.ErrInvalid
	}
	return s.UoW.Within(ctx, func(tx context.Context) error {
		a, e := s.Store.get(tx, n.ApplicationID, nil, true)
		if errors.Is(e, p.ErrNotFound) {
			// Only the authenticated title event for this collecting merchant can
			// create a bill application. Unknown result/card/probe events are inert.
			if n.EventType != "FAPIAO.USER_APPLIED" || n.SubMerchantID != s.Config.MerchantID {
				return nil
			}
			a, e = s.billApplicationWithin(tx, n)
			if errors.Is(e, payment.ErrNotFound) {
				// The title event can precede CRM's paid confirmation. Leave it
				// retryable; never provision or guess an order from that event.
				if s.Config.Enabled {
					return p.ErrPending
				}
				return nil
			}
		}
		if e != nil {
			return e
		}
		if a.Snapshot.MerchantID != n.SubMerchantID {
			return p.ErrConflict
		}
		// Bootstrap may acknowledge an authenticated unknown application, but
		// business callbacks while disabled must remain retryable and cannot
		// create a receipt or a reconciliation job.
		if !s.Config.Enabled {
			return p.ErrDisabled
		}
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		id := effects.Hash("invoice.notify.id", n.ID)
		var prior string
		e = t.QueryRow(tx, `INSERT INTO invoice_callback_receipts(event_digest,body_digest,application_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING RETURNING body_digest`, id, n.BodyDigest, a.ID).Scan(&prior)
		if errors.Is(e, pgx.ErrNoRows) {
			e = t.QueryRow(tx, `SELECT body_digest FROM invoice_callback_receipts WHERE event_digest=$1`, id).Scan(&prior)
			if e != nil {
				return e
			}
			if prior != n.BodyDigest {
				return p.ErrConflict
			}
		} else if e != nil {
			return e
		}
		// Callbacks wake a fresh authenticated query. Their order cannot downgrade
		// a reversed invoice or confuse card acceptance with card delivery.
		return s.Jobs.EnqueueWithin(tx, a.ID, time.Time{})
	})
}

// The verifier has authenticated the transaction reference. Payment owns its
// original payer evidence; Identity verifies that exact handle without lookup
// by a name/OpenID. All locks, creation, receipt and job share this UoW.
func (s *Service) billApplicationWithin(ctx context.Context, n p.Notice) (p.Application, error) {
	if s.Payments == nil || s.Subjects == nil {
		return p.Application{}, p.ErrUnavailable
	}
	pay, e := s.Payments.InvoicePaymentWithin(ctx, n.ApplicationID)
	if e != nil {
		return p.Application{}, e
	}
	if !s.Config.Enabled || s.Provider == nil {
		return p.Application{}, p.ErrDisabled
	}
	ids, e := s.customerIDs(ctx, pay.CustomerID)
	if e != nil {
		return p.Application{}, e
	}
	subject, ok, e := s.Subjects.VerifiedIdentitySubject(ctx, pay.IdentityID)
	if e != nil {
		return p.Application{}, e
	}
	if !ok || !slices.Contains(ids, int64(subject.CustomerID)) {
		return p.Application{}, p.ErrConflict
	}
	o, e := s.Orders.GetSelfWithin(ctx, pay.OrderID, ids, true)
	if e != nil {
		return p.Application{}, e
	}
	if o.Order.PayerCustomerID == nil || !slices.Contains(ids, int64(*o.Order.PayerCustomerID)) || pay.TransactionID != n.ApplicationID || pay.TransactionID != o.Order.ProviderTransactionNo || pay.OrderNo != o.Order.MerchantOrderNo || pay.Amount != o.Order.Amount.AmountMinor || pay.Currency != o.Order.Amount.Currency {
		return p.Application{}, p.ErrConflict
	}
	a, e := s.Store.byOrder(ctx, pay.OrderID)
	if e == nil {
		if a.ID != n.ApplicationID || !slices.Contains(ids, a.CustomerID) {
			return p.Application{}, p.ErrConflict
		}
		return s.Store.get(ctx, a.ID, nil, true)
	}
	if !errors.Is(e, p.ErrNotFound) {
		return a, e
	}
	refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(ctx, []int64{pay.OrderID})
	if e != nil {
		return a, e
	}
	_, refund := refunds[pay.OrderID]
	if s.reason(o, ids, refund, nil) != "" {
		return a, p.ErrIneligible
	}
	return s.createApplicationWithin(ctx, o, pay.CustomerID, pay.IdentityID)
}
