// Package port is Distribution's only cross-domain contract. The types here
// contain canonical internal IDs and opaque references only; they are never a
// public authority for identity, payment provider, or money instructions.
package port

import (
	"context"
	"errors"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
)

var (
	ErrNotFound      = errors.New("distribution record not found")
	ErrConflict      = errors.New("distribution command conflict")
	ErrUnavailable   = errors.New("distribution unavailable")
	ErrUnauthorized  = errors.New("distribution unauthorized")
	ErrQualification = errors.New("distribution qualification unavailable")
)

// PolicyCommand is supplied only by Product's server-side application while
// it holds its product mutation UoW. ExpectedVersion=0 creates the first
// policy (including a disabled default); later writes must use a positive CAS.
type PolicyCommand struct {
	ProductID                 int64
	ProductType               domain.ProductType
	Enabled                   bool
	CommissionRateBasisPoints int32
	WaitDays                  int32
	ExpectedVersion           int64
	ActorScope                string
	IdempotencyKey            string
}

type ProductPolicyWriter interface {
	SaveProductPolicyWithin(context.Context, PolicyCommand) (domain.Policy, error)
}

type ProductPolicyReader interface {
	ReadProductPolicy(context.Context, int64, domain.ProductType) (domain.Policy, error)
	ReadProductPolicyWithin(context.Context, int64, domain.ProductType) (domain.Policy, error)
}

type ProductPolicyService interface {
	ProductPolicyWriter
	ProductPolicyReader
}

// TrustedSessionActor is created only from an authenticated payment/WeChat
// session adapter. HTTP callers cannot provide any field in it.
type TrustedSessionActor struct {
	CustomerID int64
	IdentityID int64
	AppID      string
	AppScope   string
	// Channel is a Payment-session server fact. It selects scoped MPOpenID or
	// OAOpenID verification inside Payment; it is never an HTTP assertion.
	Channel    string
	OccurredAt time.Time
}

func (a TrustedSessionActor) Valid() bool {
	return a.CustomerID > 0 && a.IdentityID > 0 && a.AppID != "" && a.AppScope != "" && (a.Channel == "mini_program" || a.Channel == "h5_official_account") && a.OccurredAt.IsZero() == false
}

type RegisterCommand struct {
	Actor            TrustedSessionActor
	AgreementVersion string
	IdempotencyKey   string
}

type ReceiverReadiness struct {
	Ready     bool
	Reason    string
	Reference string
	AppID     string
	CheckedAt time.Time
}

// SettlementCapability is a safe merchant-level projection. It is separate
// from a distributor receiver because registration remains valid while the
// merchant has not enabled money-moving settlement.
type SettlementCapability struct {
	Enabled bool
	Reason  string
}

type DistributorProfile struct {
	Distributor             domain.Distributor
	Receiver                ReceiverReadiness
	Settlement              SettlementCapability
	CurrentAgreementVersion string
	RegistrationRequired    bool
}

type Agreement struct {
	Version string
	Content string
}

type ReceiverPreparationResult struct {
	Receiver      ReceiverReadiness
	State         string
	ActionURL     string
	RetryAfterSec int32
}

type PromotionProduct struct {
	ProductID                   int64
	ProductType                 domain.ProductType
	CoverURL, Name, PurchaseURL string
	PriceMinor                  int64
	Currency                    string
	CommissionRateBasisPoints   int32
	EstimatedCommissionMinor    int64
	WaitDays                    int32
	PromotionReady              bool
	PromotionBlockReason        string
}

type PromotionPage struct {
	Items      []PromotionProduct
	NextCursor string
	// EmptyReason is populated only when this cursor has reached the end with
	// no safe, saleable, policy-enabled candidate. It never describes a
	// disabled policy or an unavailable product.
	EmptyReason string
}

// ApplicationTarget is the server-confirmed public product fact retained by a
// distributor application link. It deliberately has no customer, commission,
// receiver, agreement, or promotion-credential fields.
type ApplicationTarget struct {
	ProductID     int64
	ProductType   domain.ProductType
	PolicyEnabled bool
	ProductName   string
	PurchaseURL   string
}

type IssuePromotionCommand struct {
	Actor          TrustedSessionActor
	ProductID      int64
	ProductType    domain.ProductType
	IdempotencyKey string
}

type PromotionLink struct {
	URL       string
	ExpiresAt time.Time
}

type Earnings struct {
	GrossPaidSalesMinor        int64
	SuccessfulRefundsMinor     int64
	InitialCommissionMinor     int64
	CommissionAdjustmentsMinor int64
	UnsettledPayableMinor      int64
	PaidCommissionMinor        int64
	RecoveredMinor             int64
	Currency                   string
}

type CommissionListItem struct {
	CommissionID, OrderReference, ProductName                        string
	InitialMinor, CurrentPayableMinor, PaidMinor                     int64
	Status, HoldReason, CancelReason, ExceptionReason                string
	PaidConfirmedAt, DueAt, SettlementConfirmedAt, PaidAt, CreatedAt time.Time
	Currency                                                         string
}

type CommissionPage struct {
	Items      []CommissionListItem
	NextCursor string
}

type PublicApplication interface {
	CurrentAgreement(context.Context) (Agreement, error)
	Profile(context.Context, TrustedSessionActor) (DistributorProfile, error)
	Register(context.Context, RegisterCommand) (DistributorProfile, error)
	PrepareReceiver(context.Context, TrustedSessionActor) (ReceiverPreparationResult, error)
	ListPromotionProducts(context.Context, TrustedSessionActor, string, int32) (PromotionPage, error)
	ApplicationTarget(context.Context, int64, domain.ProductType) (ApplicationTarget, error)
	IssuePromotionLink(context.Context, IssuePromotionCommand) (PromotionLink, error)
	Earnings(context.Context, TrustedSessionActor) (Earnings, error)
	ListCommissions(context.Context, TrustedSessionActor, domain.CommissionStatus, string, int32) (CommissionPage, error)
}
