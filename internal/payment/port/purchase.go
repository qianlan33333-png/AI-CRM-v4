package port

import (
	"context"
	"errors"
	"time"
)

var ErrAlreadyPurchased = errors.New("already purchased")
var ErrPurchasePending = errors.New("purchase pending")

type PurchaseState struct {
	PaidOrderID     int64  `json:"-"`
	MerchantOrderNo string `json:"-"`
	State           string `json:"purchase_state"`
	CanPurchase     bool   `json:"can_purchase"`
}
type PurchaseStatusReader interface {
	PurchaseStatus(context.Context, string, string, int64) (PurchaseState, error)
}

type CheckoutSessionReadiness interface {
	CanCreateCheckout(context.Context, string) (bool, error)
}
type SessionCheckoutReadiness interface {
	CanCreateCheckoutWithin(context.Context, string, time.Time) (bool, error)
}
