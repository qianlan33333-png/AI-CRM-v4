package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type AdminDistributor struct {
	ID                                      int64
	CustomerID                              customerdomain.CustomerID
	PublicNo, DisplayName, AgreementVersion string
	Enabled, ReceiverReady                  bool
	ReceiverReason                          string
	RegisteredAt                            time.Time
	Version                                 int64
}
type AdminOrder struct {
	AttributionID                                                                                                             int64
	DistributorCustomerID                                                                                                     customerdomain.CustomerID
	OrderReference                                                                                                            string
	ItemLine                                                                                                                  int32
	ProductID                                                                                                                 int64
	ProductType, ProductName, DistributorPublicNo, DistributorDisplayName, QualificationState, QualificationEvidenceReference string
	PolicyVersion                                                                                                             int64
	RateBasisPoints, WaitDays                                                                                                 int32
	PaidMinor                                                                                                                 int64
	Currency                                                                                                                  string
	AttributedAt                                                                                                              time.Time
}

// AdminDistributorDetail is a staff-only projection. Distribution retains the
// canonical CustomerID only for a post-transaction, presentation-safe Customer
// directory lookup; it never resolves identities for this page.
type AdminDistributorDetail struct {
	Distributor AdminDistributor
	Earnings    Earnings
}

// AdminCommissionAdjustment and AdminSettlement are frozen Distribution facts.
// They deliberately omit Payment provider payloads and receiver identifiers.
type AdminCommissionAdjustment struct {
	ID, DeltaMinor, ResultingPayableMinor int64
	Kind, Reason, SourceReference         string
	OccurredAt                            time.Time
}
type AdminSettlement struct {
	ID, AmountMinor                                                 int64
	Reference, Currency, State                                      string
	ProviderDeadlineAt, SettlementConfirmedAt, CreatedAt, UpdatedAt *time.Time
}

// AdminCommissionDetail is the full, frozen money fact for one attributed
// order. PaidConfirmedAt is the order-paid confirmation time; the domain does
// not persist a separate payout timestamp, so settlement state is presented
// alongside it instead of inventing one.
type AdminCommissionDetail struct {
	CommissionID                                                  string
	OrderReference, ProductName, Status, HoldReason, CancelReason string
	ExceptionReason, Currency                                     string
	OriginalItemPaidMinor, SuccessfulRefundMinor                  int64
	InitialMinor, CurrentPayableMinor, PaidMinor                  int64
	PaidConfirmedAt, DueAt, CreatedAt                             time.Time
}

// AdminExceptionAuditFact exposes only the human-reviewable fields from an
// append-only Distribution audit record. It intentionally omits arbitrary
// JSON payloads and payment/provider identifiers.
type AdminExceptionAuditFact struct {
	EventType, ActorScope, Reason, EvidenceReference string
	AmountMinor                                      int64
	OccurredAt                                       time.Time
}
type AdminOrderDetail struct {
	Order       AdminOrder
	Commission  *AdminCommissionDetail
	Adjustments []AdminCommissionAdjustment
	Settlements []AdminSettlement
	Exceptions  []AdminException
}
type AdminReconcileTarget string

const (
	AdminReconcileTargetNone     AdminReconcileTarget = ""
	AdminReconcileTargetSplit    AdminReconcileTarget = "split"
	AdminReconcileTargetUnfreeze AdminReconcileTarget = "unfreeze"
)

type AdminException struct {
	ExceptionID, CommissionID int64
	DistributorCustomerID     customerdomain.CustomerID
	// Currency is the Distribution-owned currency for every exception amount.
	// Audit facts inherit it; callers must not select a display fallback.
	DistributorPublicNo, DistributorDisplayName, OrderReference, Kind, Status, Currency string
	UnpaidDueMinor, AlreadyPaidMinor, AmountMinor                                       int64
	Reason, PaymentInstructionReference, EvidenceReference                              string
	ActorScope                                                                          string
	ReconcileTarget                                                                     AdminReconcileTarget
	CreatedAt, UpdatedAt                                                                time.Time
	Version                                                                             int64
	CanReconcile, CanRecordRecovery, CanRecordMerchantLiability                         bool
	Audit                                                                               []AdminExceptionAuditFact
}
type AdminPage[T any] struct {
	Items      []T
	NextCursor string
}
type AdminExceptionCommand struct {
	ExceptionID, ExpectedVersion, AmountMinor             int64
	ActorScope, Reason, EvidenceReference, IdempotencyKey string
}
type AdminDistributionReadModel interface {
	ListAdminDistributors(context.Context, string, int32) (AdminPage[AdminDistributor], error)
	ListAdminOrders(context.Context, string, int32) (AdminPage[AdminOrder], error)
	ListAdminExceptions(context.Context, string, int32) (AdminPage[AdminException], error)
}

// AdminDistributionDetailReadModel is intentionally an additive staff-read
// capability. Existing list-only callers do not gain new data access merely
// because this richer admin projection is composed.
type AdminDistributionDetailReadModel interface {
	AdminDistributionReadModel
	ReadAdminDistributorDetail(context.Context, int64) (AdminDistributorDetail, error)
	ListAdminOrdersByDistributor(context.Context, int64, string, int32) (AdminPage[AdminOrder], error)
	ReadAdminOrderDetail(context.Context, int64) (AdminOrderDetail, error)
	ReadAdminExceptionDetail(context.Context, int64) (AdminException, error)
}

// OrderDistributionAdjustment and OrderDistributionSettlement are the safe
// evidence Order may present. They deliberately omit Provider payloads and
// receiver identifiers.
type OrderDistributionAdjustment struct {
	Kind, Reason                      string
	DeltaMinor, ResultingPayableMinor int64
	OccurredAt                        time.Time
}
type OrderDistributionSettlement struct {
	Reference, Currency, State                                      string
	AmountMinor                                                     int64
	ProviderDeadlineAt, SettlementConfirmedAt, CreatedAt, UpdatedAt *time.Time
}
type OrderDistributionException struct {
	Kind, Status, Reason, EvidenceReference string
	AmountMinor                             int64
	CreatedAt, UpdatedAt                    time.Time
}

// OrderDistributionLine is the immutable distribution snapshot associated
// with one Order-owned item. It is a read model only: Order authorizes access
// to its own records, while Distribution owns the facts and tables behind it.
type OrderDistributionLine struct {
	OrderID, AttributionID, CommissionID                        int64
	ItemLine                                                    int32
	ProductName                                                 string
	DistributorCustomerID                                       customerdomain.CustomerID
	DistributorDisplayName                                      string
	RateBasisPoints, WaitDays                                   int32
	PolicyVersion                                               int64
	InitialMinor, CurrentPayableMinor, PaidMinor                int64
	Currency, Status, HoldReason, CancelReason, ExceptionReason string
	HasCommission                                               bool
	DueAt, SettlementConfirmedAt                                *time.Time
	Adjustments                                                 []OrderDistributionAdjustment
	Settlements                                                 []OrderDistributionSettlement
	Exceptions                                                  []OrderDistributionException
}

// OrderDistributionReader batches by canonical Order ID. In particular it is
// not the attribution-detail reader: callers must not guess attribution IDs
// from an Order ID or scan the administrator distribution list.
type OrderDistributionReader interface {
	ReadOrderDistribution(context.Context, []int64) (map[int64][]OrderDistributionLine, error)
}
type AdminDistributionService interface {
	AdminDistributionReadModel
	DisableDistributor(context.Context, int64, int64, string, string) error
	EnableDistributor(context.Context, int64, string, string) error
	ReconcileException(context.Context, AdminExceptionCommand) error
	RecordRecovery(context.Context, AdminExceptionCommand) error
	RecordMerchantLiability(context.Context, AdminExceptionCommand) error
}
