package app

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (s *Service) standardPurchaseWithin(ctx context.Context, customerID, productID int64, code string, currentOrderID int64, lock bool) (paymentport.PurchaseState, error) {
	r, ok := s.orders.(orderport.StandardPurchaseReader)
	if !ok {
		return paymentport.PurchaseState{}, paymentport.ErrUnavailable
	}
	ids := []int64{customerID}
	if s.lineage != nil {
		var roots []customerdomain.CustomerID
		var err error
		if locked, ok := s.lineage.(identityport.LockedCanonicalLineageReader); lock && ok {
			roots, err = locked.LockedCanonicalLineage(ctx, customerdomain.CustomerID(customerID))
		} else {
			roots, err = s.lineage.CanonicalLineage(ctx, customerdomain.CustomerID(customerID))
		}
		if err != nil {
			return paymentport.PurchaseState{}, err
		}
		ids = nil
		for _, root := range roots {
			ids = append(ids, int64(root))
		}
	}
	excluded := []int64{}
	// Restart permissions are only relevant when explicitly recovering an
	// existing order. Fresh product checkouts ignore pending orders entirely,
	// so they must not depend on the restart-review read path.
	if currentOrderID > 0 {
		if reviews, ok := s.store.(interface {
			RestartAllowedOrderIDs(context.Context) ([]int64, error)
		}); ok {
			var err error
			excluded, err = reviews.RestartAllowedOrderIDs(ctx)
			if err != nil {
				return paymentport.PurchaseState{}, err
			}
		}
	}
	state, err := r.ReadStandardPurchaseWithin(ctx, orderport.StandardPurchaseQuery{CustomerIDs: ids, ProductID: productID, ProductCode: code, CurrentOrderID: currentOrderID, ExcludedPendingOrderIDs: excluded, Lock: lock})
	if err != nil {
		return paymentport.PurchaseState{}, err
	}
	// A pending payment is an order fact, not a purchase eligibility block. The
	// caller may create a new checkout with its own idempotency key while the
	// earlier order remains available for provider reconciliation and admin
	// support. Paid ownership remains the only standard-product gate.
	result := paymentport.PurchaseState{State: "available", CanPurchase: true}
	if state.Owned {
		result.State = "owned"
		result.PaidOrderID = state.PaidOrderID
		result.MerchantOrderNo = state.MerchantOrderNo
		result.CanPurchase = false
	}
	return result, nil
}
func (s *Service) PurchaseStatus(ctx context.Context, token, kind string, id int64) (paymentport.PurchaseState, error) {
	if s == nil || s.products == nil || len(token) < 20 || id < 1 || (kind != "standard" && kind != "service_period") {
		return paymentport.PurchaseState{}, paymentport.ErrInvalid
	}
	var result paymentport.PurchaseState
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.LookupWithin(tx, token, s.now().UTC())
		if err != nil {
			return err
		}
		product, err := s.products.ReadCheckoutProductWithin(tx, productport.ProductOptionType(kind), productport.ID(id))
		if err != nil {
			return err
		}
		if kind == "service_period" {
			result = paymentport.PurchaseState{State: "available", CanPurchase: true}
			return nil
		}
		customer := actor.BeneficiaryCustomerID
		if customer < 1 {
			customer = actor.PayerCustomerID
		}
		result, err = s.standardPurchaseWithin(tx, customer, id, product.Code, 0, false)
		if err == nil && result.State == "owned" && (result.PaidOrderID < 1 || result.MerchantOrderNo == "") {
			return paymentport.ErrUnavailable
		}
		return err
	})
	return result, classify(err)
}

func purchaseBlocked(state paymentport.PurchaseState) error {
	if state.State == "owned" {
		return paymentport.ErrAlreadyPurchased
	}
	return paymentport.ErrPurchasePending
}

func (s *Service) CanCreateCheckout(ctx context.Context, token string) (bool, error) {
	reader, ok := s.sessions.(paymentport.SessionCheckoutReadiness)
	if !ok {
		return false, paymentport.ErrUnavailable
	}
	var ready bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		ready, e = reader.CanCreateCheckoutWithin(tx, token, s.now().UTC())
		return e
	})
	return ready, classify(err)
}
