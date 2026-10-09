package invoice

import (
	"context"
	"encoding/json"
	"time"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
)

// Only the explicitly selected operator recovery reaches this path. A saved
// official title and affirmative canonical no-issuance proof replace a missing
// callback, not its signature or receipt. Network reads never hold a transaction.
func (s *Service) recoverMissingNative(ctx context.Context, id string) (p.Receipt, error) {
	if !p.ValidApplyID(id) {
		return p.Receipt{}, p.ErrInvalid
	}
	proof, ok := s.Provider.(p.UnsubmittedVerifier)
	if !ok {
		return p.Receipt{}, p.ErrUnavailable
	}
	target := p.Application{ID: id, Snapshot: p.Snapshot{MerchantID: s.Config.MerchantID}}
	buyer, err := s.Provider.ReadTitle(ctx, target)
	if err != nil {
		return p.Receipt{}, err
	}
	if err = proof.VerifyUnsubmitted(ctx, target); err != nil {
		return p.Receipt{}, err
	}
	var receipt p.Receipt
	err = s.UoW.Within(ctx, func(tx context.Context) error {
		// This shared path pins the original payment, verified OneID owner,
		// Order and refund exposure before creating or reusing the application.
		a, e := s.billApplicationWithin(tx, p.Notice{ApplicationID: id})
		if e != nil {
			return e
		}
		if a.State != "awaiting_title" || a.IssueEffect != "" {
			receipt = p.MarshalReceipt(a)
			return nil
		}
		if buyer.Amount != a.Snapshot.Amount || buyer.OrderNo != a.Snapshot.OrderNo || !officialBuyerValid(buyer, a.Snapshot) {
			return p.ErrConflict
		}
		a.Buyer = buyer
		raw, e := json.Marshal(buyer)
		if e != nil {
			return e
		}
		if e = s.Store.update(tx, a, "native_title_recovered", string(effects.Hash("invoice.title", string(raw)))); e != nil {
			return e
		}
		if e = s.queue(tx, &a, false, time.Time{}); e != nil {
			return e
		}
		if e = s.Jobs.EnqueueWithin(tx, a.ID, time.Time{}); e != nil {
			return e
		}
		receipt = p.MarshalReceipt(a)
		return nil
	})
	return receipt, err
}
