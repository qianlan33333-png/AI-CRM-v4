package invoice

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"strings"
)

func (s *Service) Card(ctx context.Context, token, id string) (p.CardEntry, error) {
	var a p.Application
	var recipient string
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		actor, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		a, e = s.Store.get(tx, id, ids, false)
		if e != nil {
			return e
		}
		v, ok, e := s.Identities.VerifiedPaymentIdentity(tx, actor.PayerIdentityID, identitydomain.KindOAOpenID, a.Snapshot.AppScope)
		if e != nil {
			return e
		}
		if !ok || v.Value == "" {
			return p.ErrNotFound
		}
		recipient = v.Value
		return nil
	})
	if e != nil {
		return p.CardEntry{}, e
	}
	if a.State != "issued" || a.RefundConfirmed {
		return p.CardEntry{}, p.ErrIneligible
	}
	if s.Provider == nil {
		return p.CardEntry{}, p.ErrDisabled
	}
	// No network call holds the authorization transaction.
	r, e := s.Provider.Query(ctx, a)
	if e != nil {
		return p.CardEntry{}, e
	}
	entry, e := verifiedCard(a, r, recipient)
	if e != nil {
		return p.CardEntry{}, e
	}
	current, e := s.Read(ctx, token, id)
	if e != nil {
		return p.CardEntry{}, e
	}
	if current.State != "issued" || current.RefundConfirmed || current.InvoiceID != a.InvoiceID || current.BlueNumber != a.BlueNumber {
		return p.CardEntry{}, p.ErrIneligible
	}
	return entry, nil
}

func verifiedCard(a p.Application, r p.Result, recipient string) (p.CardEntry, error) {
	if r.Status != "ISSUED" || r.InvoiceID != a.InvoiceID || r.TotalAmount != a.Snapshot.Amount || r.Seller.TaxpayerID != a.Snapshot.SellerTaxID || r.Blue.Number == "" || r.Blue.Number != a.BlueNumber || recipient == "" || r.Card.OpenID != recipient || r.Card.AppID == "" || r.Card.AppID != a.Snapshot.AppID {
		return p.CardEntry{}, p.ErrConflict
	}
	if r.Card.Status == "DISCARDED" || r.Card.Status == "DISCARD_ACCEPTED" {
		return p.CardEntry{}, p.ErrIneligible
	}
	if r.Card.Status != "INSERTED" {
		return p.CardEntry{}, p.ErrPending
	}
	if strings.TrimSpace(r.Card.ID) != r.Card.ID || len(r.Card.ID) < 1 || len(r.Card.ID) > 32 || strings.TrimSpace(r.Card.Code) != r.Card.Code || len(r.Card.Code) < 1 || len(r.Card.Code) > 32 {
		return p.CardEntry{}, p.ErrUnavailable
	}
	return p.CardEntry{ID: r.Card.ID, Code: r.Card.Code}, nil
}
