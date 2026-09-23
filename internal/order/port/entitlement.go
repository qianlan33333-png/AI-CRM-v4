package port

import (
	"context"
	"time"
)

type Entitlement struct {
	ID               int64     `json:"id"`
	CustomerID       int64     `json:"customer_id,omitempty"`
	ServiceProductID int64     `json:"service_product_id"`
	ProductName      string    `json:"title"`
	LastOrderID      *int64    `json:"last_order_id,omitempty"`
	Status           string    `json:"status"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	Remark           string    `json:"remark"`
	// Alliance is nil when historical source data did not collect or could not
	// verify admin_alliance. It deliberately differs from an explicit "", which
	// is the frozen editor's clear operation.
	Alliance     *string   `json:"alliance"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updated_at"`
	SourceSystem string    `json:"source_system,omitempty"`
	// RenewalCount is the number of effective paid service-period source
	// orders after the first authoritative enrollment. It is available only
	// when Order has a complete native or mapped-historical source chain; a
	// legacy aggregate without that source evidence stays explicitly unknown.
	RenewalCount          int64 `json:"renewal_count,omitempty"`
	RenewalCountAvailable bool  `json:"renewal_count_available"`
	// MemberGridGroupCount is populated only for the requested, Order-owned
	// grouping. It is a window count over the complete filtered result, rather
	// than a count of whichever page reached the Host. One value is present for
	// every requested group level, in order.
	MemberGridGroupCount  int64   `json:"-"`
	MemberGridGroupCounts []int64 `json:"-"`
	// MemberGridOrderValues are the evaluated, stable keyset values for the
	// row. They are private to the bounded member-grid read contract and are
	// never exposed by Product.
	MemberGridOrderValues []any `json:"-"`
}

type EntitlementPage struct {
	Items []Entitlement `json:"items"`
	Total int64         `json:"total"`
}

// ServicePeriodMemberQuery is the bounded Order-owned projection for the
// Product member workspace. Product never reads entitlement tables directly.
// Source describes provenance only: native payment facts are paid_order and
// imported/manual facts stay manual.
type ServicePeriodMemberQuery struct {
	ServiceProductID int64
	State            string
	Source           string
	Cursor           string
	Limit            int32
	// Sort preserves the legacy list endpoint choices. The frozen member-grid
	// uses GridSorts and GridGroups below instead.
	Sort string
	// The Product member-grid host only admits filters that Order can evaluate
	// from its own entitlement facts. Customer names deliberately stay outside
	// this port.
	RemainingDays *MemberGridNumberFilter
	Remark        *MemberGridTextFilter
	FilterLogic   string
	// SnapshotAt freezes remaining-day filters and row values for one grid
	// request. A zero value lets ordinary list callers retain database-now
	// behavior; the Product grid always supplies one explicit instant.
	SnapshotAt time.Time
	// GroupByRemainingDays preserves the legacy one-level request shape. New
	// Product grid requests use GridGroups.
	GroupByRemainingDays bool
	// GridFilters, GridSorts and GridGroups express only fields owned by Order.
	// They deliberately contain neither identities nor copied customer data.
	// The repository evaluates the whole filtered relation before applying a
	// config-bound keyset cursor.
	GridFilters []MemberGridFilter
	GridSorts   []MemberGridOrder
	GridGroups  []MemberGridOrder
}

type MemberGridNumberFilter struct {
	Operator string
	Values   []int64
}

type MemberGridTextFilter struct {
	Operator string
	Value    string
}

// MemberGridFilter is one validated member-grid predicate. Number values keep
// the donor's numeric semantics (including decimal thresholds) for
// remaining_days and renewal_count; Text is used for remark.
type MemberGridFilter struct {
	Field    string
	Operator string
	Numbers  []float64
	Text     string
}

// MemberGridOrder follows the frozen dd8 field-first ordering contract.
// Product supplies at most eight sorts and two groups; Order accepts only its
// own entitlement facts.
type MemberGridOrder struct {
	Field     string
	Direction string
}

type ServicePeriodMemberPage struct {
	Items      []Entitlement
	NextCursor string
	// SnapshotAt is the effective Order snapshot. Cursored requests retain the
	// first page's instant, so Product renders exactly the values it filtered.
	SnapshotAt time.Time
}

type RemarkCommand struct {
	EntitlementID int64
	CustomerID    int64
	// ServiceProductID is a required scope proof supplied by the Product host.
	// Order uses it in the UPDATE predicate so a URL for one product cannot
	// mutate an entitlement belonging to another product.
	ServiceProductID int64
	EmployeeID       string
	Remark           string
	ExpectedVersion  int64
	IdempotencyKey   string
}

type AllianceCommand struct {
	EntitlementID int64
	CustomerID    int64
	// ServiceProductID is the required scope proof for the opaque member ref.
	ServiceProductID int64
	EmployeeID       string
	Alliance         string
	ExpectedVersion  int64
	IdempotencyKey   string
}

type EntitlementService interface {
	ListCustomerEntitlements(context.Context, int64, int32) (EntitlementPage, error)
	ListServicePeriodMembers(context.Context, ServicePeriodMemberQuery) (ServicePeriodMemberPage, error)
	// GetCustomerServicePeriodEntitlement is an exact, bounded public-state read.
	// It avoids inferring a product row from a capped customer entitlement list.
	GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (Entitlement, bool, error)
	UpdateEntitlementRemark(context.Context, RemarkCommand) (Entitlement, error)
	UpdateEntitlementAlliance(context.Context, AllianceCommand) (Entitlement, error)
}

type HistoricalEntitlement struct {
	SourceSystem     string
	SourceKey        string
	CustomerID       int64
	ServiceProductID int64
	ProductName      string
	LastOrderID      *int64
	Status           string
	StartAt          time.Time
	EndAt            time.Time
	Remark           string
	Alliance         *string
	SourceDigest     [32]byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// HistoricalServicePeriodSourceCommand records an already-reconciled link
// from a historical paid order to its imported entitlement. It does not grant
// access and is deliberately separate from native payment fulfillment.
type HistoricalServicePeriodSourceCommand struct {
	SourceOrderID      int64
	SourceLineNo       int32
	EntitlementID      int64
	ServiceProductID   int64
	ServiceProductCode string
	DurationDays       int32
	StartAt            time.Time
	EndAt              time.Time
	ImportedAt         time.Time
}

type HistoricalServicePeriodSourceCoordinator interface {
	RecordHistoricalServicePeriodSourceWithin(context.Context, HistoricalServicePeriodSourceCommand) error
}

type HistoricalEntitlementImporter interface {
	ImportHistoricalEntitlement(context.Context, HistoricalEntitlement) (Entitlement, bool, error)
}

// ServicePeriodGrantCommand is built from the payment-owner's authoritative
// paid order fact. BeneficiaryCustomerID must already be a canonical Customer;
// an unresolved beneficiary is deliberately not eligible for fulfillment.
type ServicePeriodGrantCommand struct {
	SourceOrderID         int64
	BeneficiaryCustomerID int64
	ServiceProductID      int64
	ProductName           string
	DurationDays          int32
	PaidAt                time.Time
	ProcessedAt           time.Time
}

// ServicePeriodRefundCommand models any first successful positive refund for
// a source order. RefundAmountMinor is kept as an immutable fact, but the
// donor's rule revokes the complete original period once, not pro rata.
type ServicePeriodRefundCommand struct {
	SourceOrderID     int64
	RefundAmountMinor int64
	ProcessedAt       time.Time
}

// ServicePeriodEntitlementCoordinator is the Order-owned fulfillment seam
// consumed after Payment has established a trusted terminal fact. Calls need a
// transaction-carrying context and are idempotent by source order and event.
type ServicePeriodEntitlementCoordinator interface {
	GrantPaidServicePeriodWithin(context.Context, ServicePeriodGrantCommand) (Entitlement, error)
	ApplyServicePeriodRefundWithin(context.Context, ServicePeriodRefundCommand) (Entitlement, error)
}
