package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type canonicalCouponLineage interface {
	identityport.CanonicalLineageReader
	identityport.LockedCanonicalLineageReader
	identityport.WriteLockedCanonicalLineageReader
}

// BindCanonicalLineage bridges immutable Coupon owner facts to Identity's
// current roots. Production composition binds it before serving requests.
func (r *Repository) BindCanonicalLineage(reader canonicalCouponLineage) error {
	if r == nil || reader == nil || r.lineage != nil {
		return couponapp.ErrUnavailable
	}
	r.lineage = reader
	return nil
}

func (r *Repository) couponCustomerIDs(ctx context.Context, id int64, locked bool) ([]int64, error) {
	if id < 1 {
		return nil, couponapp.ErrInvalidCoupon
	}
	// Old isolated owner-store tests have no Identity dependency. Native
	// composition always binds the existing Identity Port above.
	if r.lineage == nil {
		return []int64{id}, nil
	}
	var lineage []customerdomain.CustomerID
	var err error
	if locked {
		lineage, err = r.lineage.WriteLockedCanonicalLineage(ctx, customerdomain.CustomerID(id))
	} else {
		// Keep the owner set valid through the following Coupon SQL read. A
		// concurrent merge/revert must not split an already selected family.
		lineage, err = r.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(id))
	}
	if err != nil || len(lineage) == 0 {
		return nil, couponapp.ErrUnavailable
	}
	ids := make([]int64, 0, len(lineage))
	seen := make(map[int64]struct{}, len(lineage))
	for _, member := range lineage {
		if _, duplicate := seen[int64(member)]; member < 1 || duplicate {
			return nil, couponapp.ErrUnavailable
		}
		ids = append(ids, int64(member))
		seen[int64(member)] = struct{}{}
	}
	if _, found := seen[id]; !found {
		return nil, couponapp.ErrUnavailable
	}
	return ids, nil
}

// Preserve Checkout's original payload encoding for historical receipts.
// Only Holder may change within the same locked Identity lineage; all other
// business fields and the receipt's actor scope remain identical.
func claimPayloadMatchesLineage(c couponport.ClaimCommand, ids []int64, prior []byte) bool {
	if len(prior) != sha256.Size {
		return false
	}
	for _, holder := range ids {
		raw, err := json.Marshal(struct {
			CouponID couponport.ID
			Holder   int64
			Scope    string
		}{c.CouponID, holder, c.ActorScope})
		if err != nil {
			return false
		}
		digest := sha256.Sum256(raw)
		if string(prior) == string(digest[:]) {
			return true
		}
	}
	return false
}

func reservePayloadMatchesLineage(c couponport.ReserveCommand, ids []int64, prior []byte) bool {
	if len(prior) != sha256.Size {
		return false
	}
	for _, holder := range ids {
		raw, err := json.Marshal(struct {
			Holder, Claim, Product             int64
			Code, Type, Currency, Order, Scope string
			Gross                              int64
		}{holder, c.ClaimID, c.ProductID, c.ProductCode, c.ProductType, c.Currency, c.OrderReference, c.ActorScope, c.GrossAmountMinor})
		if err != nil {
			return false
		}
		digest := sha256.Sum256(raw)
		if string(prior) == string(digest[:]) {
			return true
		}
	}
	return false
}
