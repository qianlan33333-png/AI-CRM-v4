// Package port is the only supported cross-domain Order contract.
package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var (
	ErrNotFound    = errors.New("order not found")
	ErrConflict    = errors.New("order conflict")
	ErrUnavailable = errors.New("order unavailable")
)

type CreateCommand struct {
	Input          domain.NewOrderInput
	Actor          int64
	IdempotencyKey string
}

type ListQuery struct {
	Cursor      string
	Limit       int32
	Offset      int32
	Provider    domain.Provider
	Status      domain.Status
	OrderRef    string
	CustomerID  int64
	Product     string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	// NoCustomerMatch is set only after a read-only Customer/Identity Port
	// could not resolve a requested identity.  It keeps the resulting empty
	// page inside Order's Count/List predicate instead of dropping the filter
	// and accidentally returning every order.
	NoCustomerMatch bool
}

// CustomerFilter is the small composition seam used by the admin order list.
// Exactly one declared value is accepted.  It neither provisions a Customer
// nor attaches or merges an identity.
type CustomerFilter struct {
	Phone          string
	ExternalUserID string
}

type CustomerFilterStatus string

const (
	CustomerFilterFound       CustomerFilterStatus = "found"
	CustomerFilterNotFound    CustomerFilterStatus = "not_found"
	CustomerFilterConflict    CustomerFilterStatus = "conflict"
	CustomerFilterInvalid     CustomerFilterStatus = "invalid"
	CustomerFilterUnavailable CustomerFilterStatus = "unavailable"
)

type CustomerFilterResolution struct {
	Status     CustomerFilterStatus
	CustomerID customerdomain.CustomerID
}

// CustomerFilterResolver resolves only an existing canonical Customer for an
// order-list predicate.  The implementation belongs at composition and must
// use a trusted Identity Port; it must not create or mutate identity state.
type CustomerFilterResolver interface {
	ResolveOrderCustomerFilter(context.Context, CustomerFilter) (CustomerFilterResolution, error)
}

type Page struct {
	Items      []domain.Snapshot `json:"items"`
	NextCursor string            `json:"next_cursor"`
	Total      int64             `json:"total"`
}

type SettlementCommand struct {
	OrderID         int64
	ExpectedVersion int64
	Status          domain.Status
	RefundedMinor   int64
	OccurredAt      time.Time
	ActorScope      string
	IdempotencyKey  string
}

type HistoricalImportCommand struct {
	RunID        string
	SourceDigest [32]byte
	Order        domain.Snapshot
}

type CommandService interface {
	Create(context.Context, CreateCommand) (domain.Snapshot, error)
}

type Query interface {
	Get(context.Context, int64) (domain.Snapshot, error)
	GetByReference(context.Context, string) (domain.Snapshot, error)
	List(context.Context, ListQuery) (Page, error)
}

// ProviderScopedQuery adds an exact payment-provider constraint to a legacy
// merchant reference. It is intentionally separate from Query so existing
// consumers keep the historical ambiguity-safe lookup, while an Order list
// row can carry its server-owned provider into a detail read.
type ProviderScopedQuery interface {
	GetByReferenceForProvider(context.Context, domain.Provider, string) (domain.Snapshot, error)
}

// ExternalReadQuery is the deliberately narrow, public-read projection.  It
// accepts only canonical customer constraints supplied by Access; it never
// resolves identities or accepts an untrusted owner predicate.
type ExternalReadQuery struct {
	Provider                                            domain.Provider
	ProductCode, MerchantOrderNo, ProviderTransactionNo string
	SourceSystem, SourceRecordID                        string
	CustomerIDs                                         []int64
	CreatedFrom, CreatedTo, PaidFrom, PaidTo            *time.Time
	IsPaid                                              *bool
	// RefundedOrderIDs is supplied by Payment's public-safe read Port. Order
	// applies the resulting IDs to its own query and never joins Payment tables.
	IsRefunded          *bool
	RefundedOrderIDs    []int64
	RefundKnownOrderIDs []int64
	AfterCreatedAt      time.Time
	AfterID             int64
	Limit               int32
}

type ExternalOrder struct {
	ID                    int64
	Provider              domain.Provider
	SourceSystem          string
	SourceKey             string
	MerchantOrderNo       string
	ProviderTransactionNo string
	PayerCustomerID       *int64
	BeneficiaryCustomerID *int64
	Amount                domain.Money
	Status                domain.Status
	CreatedAt             time.Time
	// PaidAt is present only for the immutable, verified Order paid event.
	// Imported/history transition timestamps are intentionally not payment time.
	PaidAt       *time.Time
	IsPaid       bool
	ProductCodes []string
	Items        []ExternalOrderItem
}

type ExternalOrderItem struct {
	LineNo          int32
	ProductCode     string
	ProductName     string
	UnitAmountMinor int64
	Quantity        int32
	LineAmountMinor int64
}

type ExternalOrderTimelineEvent struct {
	Status        domain.Status
	RefundedMinor int64
	OccurredAt    time.Time
}

type ExternalReadPage struct{ Items []ExternalOrder }

type ExternalReadQueryService interface {
	ListExternalRead(context.Context, ExternalReadQuery) (ExternalReadPage, error)
	GetExternalRead(context.Context, int64, []int64) (ExternalOrder, error)
}

type ExternalOrderTimelineReader interface {
	ExternalOrderTimeline(context.Context, int64) ([]ExternalOrderTimelineEvent, error)
}

// CustomerScopedQuery reads one order reference through Order's own customer
// predicate. Callers that hold a customer-bounded credential must use this
// seam instead of fetching an unbounded order and filtering it afterwards.
type CustomerScopedQuery interface {
	GetByReferenceForCustomer(context.Context, string, int64) (domain.Snapshot, error)
}

type ProductSalesKey struct {
	ProductID   int64
	ProductCode string
}

type ProductOrderFact struct {
	OrderID       int64
	ProductID     *int64
	ProductCode   string
	OrderRefunded bool
}

// ProductSalesReader returns distinct orders that have authoritative paid
// history and match the requested products. It requires the caller's UoW.
type ProductSalesReader interface {
	ReadPaidProductOrdersWithin(context.Context, []ProductSalesKey) ([]ProductOrderFact, error)
}

type CustomerOrderSummary struct {
	Total    int64             `json:"total"`
	Paid     int64             `json:"paid"`
	Failed   int64             `json:"failed"`
	Refunded int64             `json:"refunded"`
	Recent   []domain.Snapshot `json:"recent"`
}

type CustomerOrderSummaryReader interface {
	CustomerOrderSummary(context.Context, int64, int32) (CustomerOrderSummary, error)
}

// CustomerActivityQuery is the Order-owned, canonical-customer query used by
// the V1 customer activity stream. The Host supplies an aggregate cursor;
// Order owns the per-type descending created_at/id keyset and never exposes an
// unbounded order read to a customer-scoped caller.
type CustomerActivityQuery struct {
	CustomerID int64
	Limit      int32
	Watermark  time.Time
	AfterAt    time.Time
	AfterID    int64
}

type CustomerActivity struct {
	ProductNames  []string            `json:"product_names,omitempty"`
	OrderID       int64               `json:"order_id"`
	Relationship  string              `json:"relationship"`
	Provider      domain.Provider     `json:"provider"`
	Status        domain.Status       `json:"status"`
	Amount        domain.Money        `json:"amount"`
	RefundedMinor int64               `json:"refunded_minor"`
	RecordOrigin  domain.RecordOrigin `json:"record_origin"`
	OccurredAt    time.Time           `json:"occurred_at"`
}

type CustomerActivityPage struct {
	Items []CustomerActivity `json:"items"`
}

// CustomerActivityReader publishes the narrow Order projection required by an
// authorized customer activity feed. It does not return payer/beneficiary IDs
// or merchant/provider reference strings that belong to another party.
type CustomerActivityReader interface {
	CustomerActivities(context.Context, CustomerActivityQuery) (CustomerActivityPage, error)
}

type ExportPreview struct {
	Rows      int  `json:"total"`
	Truncated bool `json:"truncated"`
}

type ExportResult struct {
	ReceiptID     int64
	Rows          int
	Bytes         int
	Content       []byte
	ContentDigest [32]byte
}

type Exporter interface {
	PreviewExport(context.Context, ListQuery) (ExportPreview, error)
	ExportCSV(context.Context, ListQuery, int64, string) (ExportResult, error)
}

type SettlementWriter interface {
	ApplySettlement(context.Context, SettlementCommand) (domain.Snapshot, error)
}

type HistoricalImporter interface {
	ImportHistorical(context.Context, HistoricalImportCommand) (domain.Snapshot, error)
}

// PaymentReservationReader locks and validates a native effect-eligible order
// inside the caller's existing PostgreSQL Unit of Work.
type PaymentReservationReader interface {
	ReservePaymentWithin(context.Context, int64) (domain.Snapshot, error)
}

type PaymentSettlementCommand struct {
	OrderID               int64
	RefundedDelta         int64
	Failed                bool
	ProviderTransactionNo string // verified Payment callback/query fact; only needed for first paid settlement
	OccurredAt            time.Time
	ReceiptKey            string
}

// PaymentConfirmationEvidence is supplied only after Payment has verified an
// authenticated Provider query. It lets Order prove that its immutable native
// paid event is the same fact before Payment repairs a missing confirmation
// timestamp. It never creates a paid event or changes an order state.
type PaymentConfirmationEvidence struct {
	OrderID               int64
	ProviderTransactionNo string
	OccurredAt            time.Time
}

// PaymentConfirmationEvidenceVerifier is an optional narrow extension of the
// Payment-to-Order write seam. It validates an existing immutable paid event;
// it does not mint one for an already-paid order.
type PaymentConfirmationEvidenceVerifier interface {
	VerifyPaymentConfirmationEvidenceWithin(context.Context, PaymentConfirmationEvidence) (domain.Snapshot, error)
}

type PaymentOrderCommand struct {
	Provider                        domain.Provider
	MerchantOrderNo                 string
	PayerCustomerID                 int64
	BeneficiaryCustomerID           int64
	ProductID, CouponClaimID        int64
	ProductCode, ProductName        string
	ProductVersion, UnitAmountMinor int64
	ProductType                     string
	ServicePeriodDurationDays       int32
	// PostPurchaseAction is an opaque, Product-validated buyer presentation
	// snapshot. Order persists it atomically with the checkout and never
	// interprets it.
	PostPurchaseAction     json.RawMessage
	Currency               string
	MobileE164             string
	ContactCollectionLevel string
	ShippingAddress        ShippingAddress
	// PromotionContext is an opaque, server-carried promotion credential.  It
	// has no customer, amount, policy or receiver semantics. Order freezes any
	// accepted attribution through its injected coordinator in this same UoW.
	PromotionContext string
	// ReferralActivityContext is an opaque, server-issued product activity
	// context. It is never interpreted by Order or accepted from a public
	// amount/identity field; Order hashes and freezes it with the checkout so
	// Referral can later resolve the exact activity without reading Order data.
	ReferralActivityContext    string
	ActorScope, IdempotencyKey string
}

// ShippingAddress is an immutable buyer-supplied contact fact. Region codes
// and names are both retained so historical orders do not drift with catalog
// data updates.
type ShippingAddress struct {
	RecipientName string `json:"recipient_name,omitempty"`
	ProvinceCode  string `json:"province_code,omitempty"`
	ProvinceName  string `json:"province_name,omitempty"`
	CityCode      string `json:"city_code,omitempty"`
	CityName      string `json:"city_name,omitempty"`
	DistrictCode  string `json:"district_code,omitempty"`
	DistrictName  string `json:"district_name,omitempty"`
	DetailAddress string `json:"detail_address,omitempty"`
}

// CheckoutContact is the frozen contact fact projected into transaction detail.
// Presence flags distinguish historical orders from collected empty values.
type CheckoutContact struct {
	MobileE164       string
	MobileCollected  bool
	ShippingAddress  ShippingAddress
	AddressCollected bool
}

// CheckoutAttributionCommand carries only trusted checkout facts from Order
// to an injected cross-domain coordinator. It is never constructed from a
// public identity/amount/receiver field. A coordinator treats an invalid or
// ineligible credential as ordinary purchase (no attribution); unavailable
// persistence remains an error so the checkout UoW cannot half-commit.
type CheckoutAttributionCommand struct {
	OrderID, ProductID                     int64
	OrderItemLine                          int32
	ProductType, ProductCode, ProductName  string
	PayerCustomerID, BeneficiaryCustomerID int64
	ItemPaidMinor                          int64
	PromotionContext                       string
	ReferralActivityContext                string
	OccurredAt                             time.Time
}

// CheckoutAttributionResult is a server-derived checkout fact. The public
// payment request never supplies it. True means an accepted first-level
// attribution has a positive frozen commission on this exact paid item; it is
// the only signal Payment may use to mark a new controlled transaction for
// profit sharing.
type CheckoutAttributionResult struct {
	Attributed            bool
	ProfitSharingRequired bool
	// The following are server-derived facts for a composed Referral product
	// activity coordinator. Zero means no accepted Distribution promoter.
	PromoterCustomerID        int64
	PromotionCredentialRef    string
	PolicyVersion             int64
	CommissionRateBasisPoints int32
	WaitDays                  int32
}

type CheckoutAttributionCoordinator interface {
	RecordCheckoutAttributionWithin(context.Context, CheckoutAttributionCommand) (CheckoutAttributionResult, error)
}

// ProductSaleCheckoutCoordinator freezes an opaque product-activity context
// beside Distribution attribution. It is a second injected seam so Order
// remains the only owner of checkout facts while Referral owns its own rows.
type ProductSaleCheckoutContextCommand struct {
	OrderID, OrderVersion, ProductID          int64
	ProductType                               string
	ProductCode, ProductName                  string
	ProductVersion                            int64
	PromotionCustomerID                       int64
	PromotionCredentialRef                    string
	PolicyVersion                             int64
	CommissionRateBasisPoints                 int32
	WaitDays                                  int32
	BuyerCustomerID, BeneficiaryCustomerID    int64
	PromotionContext, ReferralActivityContext string
	OccurredAt                                time.Time
}

type ProductSaleCheckoutCoordinator interface {
	RecordProductSaleCheckoutAttributionWithin(context.Context, ProductSaleCheckoutContextCommand) error
}

// CheckoutSnapshot is an Order-owned, immutable record of a native checkout.
// It freezes the gross price, coupon reservation and service-period term. It
// deliberately contains IDs as historical references only; Product and Coupon
// may later change without rewriting this sale fact.
type CheckoutSnapshot struct {
	OrderID                   int64
	ProductType               string
	ProductID                 int64
	ProductCode, ProductName  string
	ProductVersion            int64
	ServicePeriodDurationDays int32
	GrossAmountMinor          int64
	DiscountAmountMinor       int64
	PayableAmountMinor        int64
	Currency                  string
	CouponApplied             bool
	CouponReservationRef      string
	CouponClaimID, CouponID   int64
	CouponRuleVersion         int64
	ProfitSharingRequired     bool
	// ReferralActivityContextDigest is the SHA-256 of the opaque activity
	// context supplied at checkout. The raw context is not persisted in Order.
	ReferralActivityContextDigest [32]byte
	PromotionContextDigest        [32]byte
	PostPurchaseAction            json.RawMessage
	ReservedAt                    time.Time
}

// CheckoutShippingAddressReader is an authenticated Order-owned read seam.
// It returns no value when the checkout did not collect a shipping address.
type CheckoutShippingAddressReader interface {
	ReadCheckoutShippingAddressWithin(context.Context, int64) (ShippingAddress, bool, error)
}

// CheckoutContactReader opens its own read transaction for an HTTP detail read.
type CheckoutContactReader interface {
	ReadCheckoutContact(context.Context, int64) (CheckoutContact, error)
}

// CheckoutSnapshotReader is the narrow Order read seam for a Product paid
// consumer already inside Order's settlement transaction. Product receives an
// immutable checkout fact and never accesses Order tables directly.
type CheckoutSnapshotReader interface {
	ReadCheckoutSnapshotWithin(context.Context, int64) (CheckoutSnapshot, error)
}

// PaymentCoordinator is the only cross-domain write seam from Payment to
// Order. Both methods require the caller's existing PostgreSQL transaction.
type PaymentCoordinator interface {
	PaymentReservationReader
	CreatePaymentOrderWithin(context.Context, PaymentOrderCommand) (domain.Snapshot, error)
	SettlePaymentWithin(context.Context, PaymentSettlementCommand) (domain.Snapshot, error)
}
