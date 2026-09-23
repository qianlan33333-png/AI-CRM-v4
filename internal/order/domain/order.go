// Package domain owns the canonical Order aggregate. It has no Provider,
// database, Identity, queue, or HTTP dependencies.
package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidOrder      = errors.New("invalid order")
	ErrInvalidSettlement = errors.New("invalid order settlement")
	ErrInvalidTransition = errors.New("invalid order status transition")
	ErrVersionConflict   = errors.New("order version conflict")
)

type Provider string

const (
	ProviderWeChatPay  Provider = "wechat_pay"
	ProviderWeChatShop Provider = "wechat_shop"
	ProviderAlipay     Provider = "alipay"
)

type Status string

const (
	StatusPendingPayment    Status = "pending_payment"
	StatusPaid              Status = "paid"
	StatusPartiallyRefunded Status = "partially_refunded"
	StatusRefunded          Status = "refunded"
	StatusCancelled         Status = "cancelled"
	StatusPaymentFailed     Status = "payment_failed"
	StatusClosed            Status = "closed"
)

type RecordOrigin string

const (
	RecordOriginNative  RecordOrigin = "native"
	RecordOriginHistory RecordOrigin = "history"
)

type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

func NewMoney(amountMinor int64, currency string) (Money, error) {
	if amountMinor < 1 || !validCurrency(currency) {
		return Money{}, ErrInvalidOrder
	}
	return Money{AmountMinor: amountMinor, Currency: currency}, nil
}

type ItemSnapshot struct {
	LineNo          int32  `json:"line_no"`
	ProductID       *int64 `json:"product_id,omitempty"`
	ProductVersion  *int64 `json:"product_version,omitempty"`
	ProductCode     string `json:"product_code"`
	ProductName     string `json:"product_name"`
	UnitAmountMinor int64  `json:"unit_amount_minor"`
	Quantity        int32  `json:"quantity"`
	LineAmountMinor int64  `json:"line_amount_minor"`
}

type NewOrderInput struct {
	Provider              Provider
	SourceSystem          string
	SourceKey             string
	MerchantOrderNo       string
	ProviderTransactionNo string
	PayerCustomerID       *int64
	BeneficiaryCustomerID *int64
	Amount                Money
	Items                 []ItemSnapshot
	RecordOrigin          RecordOrigin
	EffectEligible        bool
	CreatedAt             time.Time
}

// Snapshot is the persistence-safe, immutable projection of an aggregate.
type Snapshot struct {
	ID                    int64          `json:"id"`
	Provider              Provider       `json:"provider"`
	SourceSystem          string         `json:"source_system"`
	SourceKey             string         `json:"source_key"`
	MerchantOrderNo       string         `json:"merchant_order_no"`
	ProviderTransactionNo string         `json:"provider_transaction_no,omitempty"`
	PayerCustomerID       *int64         `json:"payer_customer_id,omitempty"`
	BeneficiaryCustomerID *int64         `json:"beneficiary_customer_id,omitempty"`
	Amount                Money          `json:"amount"`
	RefundedMinor         int64          `json:"refunded_minor"`
	Status                Status         `json:"status"`
	Items                 []ItemSnapshot `json:"items"`
	RecordOrigin          RecordOrigin   `json:"record_origin"`
	EffectEligible        bool           `json:"effect_eligible"`
	// ProfitSharingRequired is frozen only after Distribution accepted a
	// positive attribution in the active Order/Payment UoW. Browser payloads
	// never bind this server-derived payment instruction fact.
	ProfitSharingRequired bool      `json:"profit_sharing_required"`
	Version               int64     `json:"version"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Order struct {
	ID                    int64
	Provider              Provider
	SourceSystem          string
	SourceKey             string
	MerchantOrderNo       string
	ProviderTransactionNo string
	PayerCustomerID       *int64
	BeneficiaryCustomerID *int64
	Amount                Money
	RefundedMinor         int64
	Status                Status
	RecordOrigin          RecordOrigin
	EffectEligible        bool
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	items                 []ItemSnapshot
}

type StatusEvent struct {
	From          Status
	To            Status
	RefundedMinor int64
	Version       int64
	OccurredAt    time.Time
}

// AttributeHistoricalPayer links a previously floating historical order to a
// canonical Customer. It never changes the beneficiary or makes the order
// eligible for provider effects. Replaying the same Customer is a no-op;
// attempting to replace an existing payer fails closed.
func (o Order) AttributeHistoricalPayer(expectedVersion, customerID int64, at time.Time) (Order, bool, error) {
	if o.RecordOrigin != RecordOriginHistory || o.EffectEligible || expectedVersion != o.Version || customerID < 1 || at.IsZero() || at.Before(o.UpdatedAt) {
		return Order{}, false, ErrInvalidOrder
	}
	if o.PayerCustomerID != nil {
		if *o.PayerCustomerID == customerID {
			return o, false, nil
		}
		return Order{}, false, ErrInvalidOrder
	}
	updated := o
	updated.PayerCustomerID = &customerID
	updated.Version++
	updated.UpdatedAt = at.UTC()
	if err := validate(updated); err != nil {
		return Order{}, false, err
	}
	return updated, true, nil
}

func NewOrder(input NewOrderInput) (Order, error) {
	origin := input.RecordOrigin
	if origin == "" {
		origin = RecordOriginNative
	}
	order := Order{
		Provider: input.Provider, SourceSystem: strings.TrimSpace(input.SourceSystem), SourceKey: strings.TrimSpace(input.SourceKey),
		MerchantOrderNo: strings.TrimSpace(input.MerchantOrderNo), ProviderTransactionNo: strings.TrimSpace(input.ProviderTransactionNo),
		PayerCustomerID: cloneID(input.PayerCustomerID), BeneficiaryCustomerID: cloneID(input.BeneficiaryCustomerID),
		Amount: input.Amount, Status: StatusPendingPayment, RecordOrigin: origin, EffectEligible: origin == RecordOriginNative,
		Version: 1, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC(), items: cloneItems(input.Items),
	}
	if err := validate(order); err != nil {
		return Order{}, err
	}
	return order, nil
}

func Restore(snapshot Snapshot) (Order, error) {
	order := Order{
		ID: snapshot.ID, Provider: snapshot.Provider, SourceSystem: snapshot.SourceSystem, SourceKey: snapshot.SourceKey,
		MerchantOrderNo: snapshot.MerchantOrderNo, ProviderTransactionNo: snapshot.ProviderTransactionNo,
		PayerCustomerID: cloneID(snapshot.PayerCustomerID), BeneficiaryCustomerID: cloneID(snapshot.BeneficiaryCustomerID),
		Amount: snapshot.Amount, RefundedMinor: snapshot.RefundedMinor, Status: snapshot.Status,
		RecordOrigin: snapshot.RecordOrigin, EffectEligible: snapshot.EffectEligible, Version: snapshot.Version,
		CreatedAt: snapshot.CreatedAt.UTC(), UpdatedAt: snapshot.UpdatedAt.UTC(), items: cloneItems(snapshot.Items),
	}
	if err := validate(order); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (o Order) Items() []ItemSnapshot { return cloneItems(o.items) }

func (o Order) Snapshot() Snapshot {
	return Snapshot{
		ID: o.ID, Provider: o.Provider, SourceSystem: o.SourceSystem, SourceKey: o.SourceKey,
		MerchantOrderNo: o.MerchantOrderNo, ProviderTransactionNo: o.ProviderTransactionNo,
		PayerCustomerID: cloneID(o.PayerCustomerID), BeneficiaryCustomerID: cloneID(o.BeneficiaryCustomerID),
		Amount: o.Amount, RefundedMinor: o.RefundedMinor, Status: o.Status, Items: o.Items(),
		RecordOrigin: o.RecordOrigin, EffectEligible: o.EffectEligible, Version: o.Version,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

func (o Order) RefundableMinor() int64 {
	if o.RefundedMinor >= o.Amount.AmountMinor {
		return 0
	}
	return o.Amount.AmountMinor - o.RefundedMinor
}

// WithVerifiedProviderTransaction records the Payment-verified provider
// transaction reference as part of the first paid Order settlement. It never
// replaces a prior reference and does not create another status transition.
func (o Order) WithVerifiedProviderTransaction(reference string) (Order, error) {
	if o.Status != StatusPaid || reference == "" || len(reference) > 200 || reference != strings.TrimSpace(reference) {
		return Order{}, ErrInvalidSettlement
	}
	if o.ProviderTransactionNo != "" {
		if o.ProviderTransactionNo == reference {
			return o, nil
		}
		return Order{}, ErrInvalidSettlement
	}
	updated := o
	updated.ProviderTransactionNo = reference
	if err := validate(updated); err != nil {
		return Order{}, err
	}
	return updated, nil
}

func (o Order) ApplySettlement(expectedVersion int64, next Status, refundedMinor int64, at time.Time) (Order, StatusEvent, error) {
	if expectedVersion != o.Version {
		return Order{}, StatusEvent{}, ErrVersionConflict
	}
	if at.IsZero() || at.Before(o.UpdatedAt) || !validSettlement(next, refundedMinor, o.Amount.AmountMinor) {
		return Order{}, StatusEvent{}, ErrInvalidSettlement
	}
	if next == o.Status && refundedMinor == o.RefundedMinor {
		return o, StatusEvent{From: o.Status, To: o.Status, RefundedMinor: refundedMinor, Version: o.Version, OccurredAt: at.UTC()}, nil
	}
	if !mayTransition(o.Status, next) {
		return Order{}, StatusEvent{}, ErrInvalidTransition
	}
	updated := o
	updated.Status = next
	updated.RefundedMinor = refundedMinor
	updated.Version++
	updated.UpdatedAt = at.UTC()
	event := StatusEvent{From: o.Status, To: next, RefundedMinor: refundedMinor, Version: updated.Version, OccurredAt: updated.UpdatedAt}
	return updated, event, nil
}

func validate(o Order) error {
	if !validProvider(o.Provider) || !validText(o.SourceSystem, 100) || !validText(o.SourceKey, 200) || !validText(o.MerchantOrderNo, 200) || len(o.ProviderTransactionNo) > 200 || o.ProviderTransactionNo != strings.TrimSpace(o.ProviderTransactionNo) ||
		o.Amount.AmountMinor < 1 || !validCurrency(o.Amount.Currency) || o.Version < 1 || o.CreatedAt.IsZero() || o.UpdatedAt.Before(o.CreatedAt) ||
		(o.RecordOrigin != RecordOriginNative && o.RecordOrigin != RecordOriginHistory) || o.EffectEligible != (o.RecordOrigin == RecordOriginNative) ||
		!validSettlement(o.Status, o.RefundedMinor, o.Amount.AmountMinor) {
		return ErrInvalidOrder
	}
	if o.RecordOrigin == RecordOriginNative && (!validID(o.PayerCustomerID) || !validID(o.BeneficiaryCustomerID)) {
		return ErrInvalidOrder
	}
	if o.PayerCustomerID != nil && *o.PayerCustomerID < 1 || o.BeneficiaryCustomerID != nil && *o.BeneficiaryCustomerID < 1 {
		return ErrInvalidOrder
	}
	if len(o.items) < 1 || len(o.items) > 100 {
		return ErrInvalidOrder
	}
	var total int64
	seen := make(map[int32]struct{}, len(o.items))
	for _, item := range o.items {
		if item.LineNo < 1 || item.ProductID != nil && *item.ProductID < 1 || item.ProductVersion != nil && *item.ProductVersion < 1 || !validText(item.ProductCode, 200) || !validText(item.ProductName, 500) ||
			item.UnitAmountMinor < 1 || item.Quantity < 1 || item.UnitAmountMinor > math.MaxInt64/int64(item.Quantity) ||
			item.LineAmountMinor != item.UnitAmountMinor*int64(item.Quantity) || total > math.MaxInt64-item.LineAmountMinor {
			return ErrInvalidOrder
		}
		if _, duplicate := seen[item.LineNo]; duplicate {
			return ErrInvalidOrder
		}
		seen[item.LineNo] = struct{}{}
		total += item.LineAmountMinor
	}
	if total != o.Amount.AmountMinor {
		return ErrInvalidOrder
	}
	return nil
}

func validSettlement(status Status, refunded, amount int64) bool {
	if refunded < 0 || refunded > amount {
		return false
	}
	switch status {
	case StatusPartiallyRefunded:
		return refunded > 0 && refunded < amount
	case StatusRefunded:
		return refunded == amount
	case StatusPendingPayment, StatusPaid, StatusCancelled, StatusPaymentFailed, StatusClosed:
		return refunded == 0
	default:
		return false
	}
}

func mayTransition(from, to Status) bool {
	switch from {
	case StatusPendingPayment:
		return to == StatusPaid || to == StatusCancelled || to == StatusPaymentFailed
	case StatusPaid:
		return to == StatusPartiallyRefunded || to == StatusRefunded || to == StatusClosed
	case StatusPartiallyRefunded:
		return to == StatusPartiallyRefunded || to == StatusRefunded
	default:
		return false
	}
}

func validProvider(provider Provider) bool {
	return provider == ProviderWeChatPay || provider == ProviderWeChatShop || provider == ProviderAlipay
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validText(value string, max int) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= max
}

func validID(value *int64) bool { return value != nil && *value > 0 }

func cloneID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneItems(items []ItemSnapshot) []ItemSnapshot {
	result := make([]ItemSnapshot, len(items))
	copy(result, items)
	for index := range result {
		result[index].ProductID = cloneID(result[index].ProductID)
	}
	return result
}
