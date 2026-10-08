package invoice

import (
	"context"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	order "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func (s *Service) InvoicePreflight(ctx context.Context, ref string) (bool, time.Duration, error) {
	var id string
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		return t.QueryRow(tx, `SELECT id FROM invoice_applications WHERE issue_effect=$1 OR reverse_effect=$1`, ref).Scan(&id)
	})
	if e != nil {
		return false, time.Minute, e
	}
	ready := true
	e = s.withOrder(ctx, id, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if a.State != "queued" || a.RefundConfirmed || o.Order.RefundedMinor > 0 {
			return nil
		}
		r, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{a.OrderID})
		if e != nil {
			return e
		}
		_, pending := r[a.OrderID]
		ready = !pending
		return nil
	})
	return ready, time.Minute, e
}

type providerWithPreflight struct {
	s       *Service
	adapter effects.ProviderAdapter
}

func (p providerWithPreflight) Execute(ctx context.Context, e effects.Envelope, a effects.Attempt) (effects.AdapterResult, error) {
	return p.adapter.Execute(ctx, e, a)
}
func (p providerWithPreflight) Preflight(ctx context.Context, e effects.Envelope, ref string) (bool, time.Duration, error) {
	if e.Kind != effects.KindInvoiceIssue {
		return true, 0, nil
	}
	return p.s.InvoicePreflight(ctx, ref)
}
