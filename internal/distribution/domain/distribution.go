// Package domain contains the closed first-level Distribution business facts.
// It deliberately has no provider, database, HTTP, identity-resolution, or
// order-table dependency.
package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid distribution fact")
	ErrTransition = errors.New("invalid distribution transition")
	ErrVersion    = errors.New("distribution version conflict")
)

type ProductType string

const (
	ProductTypeStandard      ProductType = "standard_product"
	ProductTypeServicePeriod ProductType = "service_period"
)

func (p ProductType) Valid() bool { return p == ProductTypeStandard || p == ProductTypeServicePeriod }

const (
	MaximumCommissionRateBasisPoints int32 = 3000
	MaximumWaitDays                  int32 = 29
)

// Policy is a Distribution-owned, versioned product policy. Product only owns
// the referenced product and saves this fact through the policy Port.
type Policy struct {
	ProductID                 int64
	ProductType               ProductType
	Enabled                   bool
	CommissionRateBasisPoints int32
	WaitDays                  int32
	Version                   int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

func NewPolicy(productID int64, productType ProductType, enabled bool, rateBasisPoints, waitDays int32, now time.Time) (Policy, error) {
	policy := Policy{ProductID: productID, ProductType: productType, Enabled: enabled, CommissionRateBasisPoints: rateBasisPoints, WaitDays: waitDays, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if !policy.Valid() {
		return Policy{}, ErrInvalid
	}
	return policy, nil
}

func (p Policy) Update(expectedVersion int64, enabled bool, rateBasisPoints, waitDays int32, now time.Time) (Policy, error) {
	if expectedVersion != p.Version {
		return Policy{}, ErrVersion
	}
	if now.IsZero() || now.Before(p.UpdatedAt) {
		return Policy{}, ErrInvalid
	}
	next := p
	next.Enabled, next.CommissionRateBasisPoints, next.WaitDays = enabled, rateBasisPoints, waitDays
	next.Version++
	next.UpdatedAt = now.UTC()
	if !next.Valid() {
		return Policy{}, ErrInvalid
	}
	return next, nil
}

func (p Policy) Valid() bool {
	return p.ProductID > 0 && p.ProductType.Valid() && p.CommissionRateBasisPoints >= 0 && p.CommissionRateBasisPoints <= MaximumCommissionRateBasisPoints && p.WaitDays >= 0 && p.WaitDays <= MaximumWaitDays && p.Version > 0 && !p.CreatedAt.IsZero() && !p.UpdatedAt.Before(p.CreatedAt)
}

type Distributor struct {
	ID               int64
	CustomerID       int64
	PublicNo         string
	AgreementVersion string
	Enabled          bool
	RegisteredAt     time.Time
	Version          int64
}

func (d Distributor) Valid() bool {
	return d.ID > 0 && d.CustomerID > 0 && d.PublicNo == strings.TrimSpace(d.PublicNo) && len(d.PublicNo) >= 6 && len(d.PublicNo) <= 64 && d.AgreementVersion == strings.TrimSpace(d.AgreementVersion) && len(d.AgreementVersion) >= 1 && len(d.AgreementVersion) <= 100 && !d.RegisteredAt.IsZero() && d.Version > 0
}

type QualificationState string

const (
	QualificationEligible    QualificationState = "eligible"
	QualificationIneligible  QualificationState = "ineligible"
	QualificationSuspended   QualificationState = "suspended"
	QualificationConflict    QualificationState = "identity_conflict"
	QualificationUnavailable QualificationState = "evidence_unavailable"
)

func (s QualificationState) Valid() bool {
	switch s {
	case QualificationEligible, QualificationIneligible, QualificationSuspended, QualificationConflict, QualificationUnavailable:
		return true
	default:
		return false
	}
}

type Qualification struct {
	State       QualificationState
	Reason      string
	EvidenceRef string
	CheckedAt   time.Time
}

func (q Qualification) AllowsPromotion() bool { return q.State == QualificationEligible }

func (q Qualification) Valid() bool {
	return q.State.Valid() && q.Reason == strings.TrimSpace(q.Reason) && len(q.Reason) <= 200 && q.EvidenceRef == strings.TrimSpace(q.EvidenceRef) && len(q.EvidenceRef) <= 200 && !q.CheckedAt.IsZero() && (q.State != QualificationEligible || q.EvidenceRef != "")
}

type CredentialStatus string

const (
	CredentialActive  CredentialStatus = "active"
	CredentialRevoked CredentialStatus = "revoked"
	CredentialExpired CredentialStatus = "expired"
)

type PromotionCredential struct {
	ID            int64
	DistributorID int64
	ProductID     int64
	ProductType   ProductType
	TokenDigest   [32]byte
	Status        CredentialStatus
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

func (c PromotionCredential) Valid() bool {
	return c.ID > 0 && c.DistributorID > 0 && c.ProductID > 0 && c.ProductType.Valid() && (c.Status == CredentialActive || c.Status == CredentialRevoked || c.Status == CredentialExpired) && !c.CreatedAt.IsZero() && c.ExpiresAt.After(c.CreatedAt) && (c.Status != CredentialRevoked || c.RevokedAt != nil)
}

// ValidForInsert checks the active, not-yet-persisted form of a promotion
// credential. IDs are assigned by PostgreSQL, so applying Valid before an
// INSERT would make every new credential invalid.
func (c PromotionCredential) ValidForInsert() bool {
	return c.ID == 0 && c.DistributorID > 0 && c.ProductID > 0 && c.ProductType.Valid() && c.Status == CredentialActive && !c.CreatedAt.IsZero() && c.ExpiresAt.After(c.CreatedAt) && c.RevokedAt == nil
}

type Attribution struct {
	ID                        int64
	OrderID                   int64
	OrderItemLine             int32
	ProductCode, ProductName  string
	DistributorID             int64
	PromotionCredentialID     int64
	QualificationEvidenceRef  string
	QualificationState        QualificationState
	PolicyVersion             int64
	CommissionRateBasisPoints int32
	WaitDays                  int32
	AttributedAt              time.Time
}

func (a Attribution) Valid() bool {
	return a.ID > 0 && a.OrderID > 0 && a.OrderItemLine > 0 && a.ProductCode == strings.TrimSpace(a.ProductCode) && len(a.ProductCode) >= 1 && len(a.ProductCode) <= 200 && a.ProductName == strings.TrimSpace(a.ProductName) && len(a.ProductName) >= 1 && len(a.ProductName) <= 500 && a.DistributorID > 0 && a.PromotionCredentialID > 0 && a.QualificationEvidenceRef == strings.TrimSpace(a.QualificationEvidenceRef) && a.QualificationEvidenceRef != "" && len(a.QualificationEvidenceRef) <= 200 && a.QualificationState == QualificationEligible && a.PolicyVersion > 0 && a.CommissionRateBasisPoints >= 0 && a.CommissionRateBasisPoints <= MaximumCommissionRateBasisPoints && a.WaitDays >= 0 && a.WaitDays <= MaximumWaitDays && !a.AttributedAt.IsZero()
}

// ValidForInsert checks an attribution before PostgreSQL assigns its identity.
// Checkout attribution is written inside Order's transaction, so requiring an
// ID here would reject every legitimate first write before the order can commit.
func (a Attribution) ValidForInsert() bool {
	return a.ID == 0 && a.OrderID > 0 && a.OrderItemLine > 0 && a.ProductCode == strings.TrimSpace(a.ProductCode) && len(a.ProductCode) >= 1 && len(a.ProductCode) <= 200 && a.ProductName == strings.TrimSpace(a.ProductName) && len(a.ProductName) >= 1 && len(a.ProductName) <= 500 && a.DistributorID > 0 && a.PromotionCredentialID > 0 && a.QualificationEvidenceRef == strings.TrimSpace(a.QualificationEvidenceRef) && a.QualificationEvidenceRef != "" && len(a.QualificationEvidenceRef) <= 200 && a.QualificationState == QualificationEligible && a.PolicyVersion > 0 && a.CommissionRateBasisPoints >= 0 && a.CommissionRateBasisPoints <= MaximumCommissionRateBasisPoints && a.WaitDays >= 0 && a.WaitDays <= MaximumWaitDays && !a.AttributedAt.IsZero()
}

type CommissionStatus string

const (
	CommissionPending   CommissionStatus = "pending"
	CommissionHeld      CommissionStatus = "held"
	CommissionSettling  CommissionStatus = "settling"
	CommissionPaid      CommissionStatus = "paid"
	CommissionCancelled CommissionStatus = "cancelled"
	CommissionException CommissionStatus = "exception"
	CommissionZero      CommissionStatus = "zero_commission"
)

func (s CommissionStatus) Valid() bool {
	switch s {
	case CommissionPending, CommissionHeld, CommissionSettling, CommissionPaid, CommissionCancelled, CommissionException, CommissionZero:
		return true
	default:
		return false
	}
}

type Commission struct {
	ID                        int64
	AttributionID             int64
	OrderID                   int64
	OrderItemLine             int32
	DistributorID             int64
	OriginalItemPaidMinor     int64
	SuccessfulRefundMinor     int64
	InitialMinor              int64
	CurrentPayableMinor       int64
	PaidMinor                 int64
	CommissionRateBasisPoints int32
	PaidConfirmedAt           time.Time
	DueAt                     time.Time
	Status                    CommissionStatus
	HoldReason                string
	CancelReason              string
	ExceptionReason           string
	Version                   int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

func NewCommission(attribution Attribution, paidMinor int64, paidAt time.Time) (Commission, error) {
	if !attribution.Valid() || paidMinor < 0 || paidAt.IsZero() {
		return Commission{}, ErrInvalid
	}
	initial, err := CalculateCommission(paidMinor, attribution.CommissionRateBasisPoints)
	if err != nil {
		return Commission{}, err
	}
	status := CommissionPending
	if initial == 0 {
		status = CommissionZero
	}
	due := paidAt.UTC().Add(time.Duration(attribution.WaitDays) * 24 * time.Hour)
	return Commission{AttributionID: attribution.ID, OrderID: attribution.OrderID, OrderItemLine: attribution.OrderItemLine, DistributorID: attribution.DistributorID, OriginalItemPaidMinor: paidMinor, SuccessfulRefundMinor: 0, InitialMinor: initial, CurrentPayableMinor: initial, CommissionRateBasisPoints: attribution.CommissionRateBasisPoints, PaidConfirmedAt: paidAt.UTC(), DueAt: due, Status: status, Version: 1, CreatedAt: paidAt.UTC(), UpdatedAt: paidAt.UTC()}, nil
}

// CalculateCommission uses integer minor units and always rounds down.
func CalculateCommission(remainingPaidMinor int64, basisPoints int32) (int64, error) {
	if remainingPaidMinor < 0 || basisPoints < 0 || basisPoints > MaximumCommissionRateBasisPoints || remainingPaidMinor > math.MaxInt64/int64(MaximumCommissionRateBasisPoints) {
		return 0, ErrInvalid
	}
	return remainingPaidMinor * int64(basisPoints) / 10000, nil
}

func (c Commission) RepriceFromPaidMinor(expectedVersion, originalPaidMinor, cumulativeSuccessfulRefundMinor int64, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || originalPaidMinor < 0 || cumulativeSuccessfulRefundMinor < 0 || cumulativeSuccessfulRefundMinor > originalPaidMinor || at.IsZero() || at.Before(c.UpdatedAt) || (c.Status != CommissionPending && c.Status != CommissionHeld) {
		return Commission{}, ErrTransition
	}
	nextPayable, err := CalculateCommission(originalPaidMinor-cumulativeSuccessfulRefundMinor, c.CommissionRateBasisPoints)
	if err != nil {
		return Commission{}, err
	}
	next := c
	next.CurrentPayableMinor, next.SuccessfulRefundMinor = nextPayable, cumulativeSuccessfulRefundMinor
	next.Version++
	next.UpdatedAt = at.UTC()
	if nextPayable == 0 {
		next.Status, next.CancelReason = CommissionCancelled, "buyer_refund"
	}
	return next, nil
}

// RecordRefundAfterSubmission records the authoritative cumulative successful
// buyer refund after a split has been accepted or paid.  It deliberately keeps
// the original settlement instruction and any paid fact intact: the revised
// current payable amount is the business obligation after the refund, while
// reconciliation still queries the immutable provider instruction.
func (c Commission) RecordRefundAfterSubmission(expectedVersion, cumulativeSuccessfulRefundMinor int64, reason string, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionSettling && c.Status != CommissionPaid && c.Status != CommissionException) || cumulativeSuccessfulRefundMinor < c.SuccessfulRefundMinor || cumulativeSuccessfulRefundMinor > c.OriginalItemPaidMinor || strings.TrimSpace(reason) == "" || len(reason) > 200 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	payable, err := CalculateCommission(c.OriginalItemPaidMinor-cumulativeSuccessfulRefundMinor, c.CommissionRateBasisPoints)
	if err != nil {
		return Commission{}, err
	}
	next := c
	next.SuccessfulRefundMinor, next.CurrentPayableMinor = cumulativeSuccessfulRefundMinor, payable
	// A refund after submission is never an implicit cancellation.  The exact
	// original instruction must be reconciled first; if it succeeds, PaidMinor
	// remains a durable payment fact and the difference is handled manually.
	if c.Status != CommissionException {
		next.Status, next.ExceptionReason = CommissionException, strings.TrimSpace(reason)
	}
	next.Version, next.UpdatedAt = c.Version+1, at.UTC()
	return next, nil
}

// RecordRefundAfterFinalization retains refund accounting for a commission
// that already has a non-payable terminal state (zero or cancelled). It never
// resurrects that obligation.
func (c Commission) RecordRefundAfterFinalization(expectedVersion, cumulativeSuccessfulRefundMinor int64, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionCancelled && c.Status != CommissionZero) || cumulativeSuccessfulRefundMinor < c.SuccessfulRefundMinor || cumulativeSuccessfulRefundMinor > c.OriginalItemPaidMinor || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.SuccessfulRefundMinor, next.Version, next.UpdatedAt = cumulativeSuccessfulRefundMinor, c.Version+1, at.UTC()
	return next, nil
}

func (c Commission) Hold(expectedVersion int64, reason string, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || c.Status != CommissionPending || strings.TrimSpace(reason) == "" || len(reason) > 200 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.HoldReason, next.Version, next.UpdatedAt = CommissionHeld, strings.TrimSpace(reason), c.Version+1, at.UTC()
	return next, nil
}

func (c Commission) Resume(expectedVersion int64, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || c.Status != CommissionHeld || c.CurrentPayableMinor < 1 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.HoldReason, next.Version, next.UpdatedAt = CommissionPending, "", c.Version+1, at.UTC()
	return next, nil
}

// CancelUnsettled records a final decision that an unpaid commission must not
// be submitted. It is intentionally unavailable once a split was submitted or
// paid: those cases require original-instruction reconciliation and an
// exception/audit trail rather than silently erasing an obligation.
func (c Commission) CancelUnsettled(expectedVersion int64, reason string, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionPending && c.Status != CommissionHeld) || strings.TrimSpace(reason) == "" || len(reason) > 200 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	// A cancelled unsettled commission no longer has an amount payable to the
	// distributor.  Keeping the old amount here would contradict the appended
	// cancellation adjustment and leak a cancelled obligation into any future
	// calculation that reads the frozen commission row.
	next.Status, next.CancelReason, next.ExceptionReason, next.CurrentPayableMinor, next.Version, next.UpdatedAt = CommissionCancelled, strings.TrimSpace(reason), "", 0, c.Version+1, at.UTC()
	return next, nil
}

// ConfirmInstructionUnpaid closes an already-submitted commission only after
// the original, immutable profit-sharing instruction has been authoritatively
// reconciled as not paid and its caller has established a separate durable
// business cancellation fact. It is deliberately narrower than
// CancelUnsettled: a Provider refusal, retry timeout, or refund in flight does
// not by itself erase the commission obligation.
func (c Commission) ConfirmInstructionUnpaid(expectedVersion int64, reason string, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionSettling && !(c.Status == CommissionException && c.PaidMinor == 0)) || c.PaidMinor != 0 || strings.TrimSpace(reason) == "" || len(reason) > 200 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.CancelReason, next.ExceptionReason, next.CurrentPayableMinor, next.Version, next.UpdatedAt = CommissionCancelled, strings.TrimSpace(reason), "", 0, c.Version+1, at.UTC()
	return next, nil
}

func (c Commission) BeginSettlement(expectedVersion int64, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || c.Status != CommissionPending || c.CurrentPayableMinor < 1 || at.Before(c.DueAt) || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.Version, next.UpdatedAt = CommissionSettling, c.Version+1, at.UTC()
	return next, nil
}

// ConfirmReceiverPaid records only a receiver-specific Provider success. It
// can recover a pre-payment unknown result, but never overwrites a paid
// after-sales exception.
func (c Commission) ConfirmReceiverPaid(expectedVersion, actualPaidMinor int64, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionSettling && !(c.Status == CommissionException && c.PaidMinor == 0)) || actualPaidMinor < 1 || (c.Status == CommissionSettling && actualPaidMinor != c.CurrentPayableMinor) || (c.Status == CommissionException && actualPaidMinor < c.CurrentPayableMinor) || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.PaidMinor, next.Version, next.UpdatedAt = CommissionPaid, actualPaidMinor, c.Version+1, at.UTC()
	return next, nil
}

func (c Commission) MarkException(expectedVersion int64, reason string, at time.Time) (Commission, error) {
	if expectedVersion != c.Version || (c.Status != CommissionPending && c.Status != CommissionHeld && c.Status != CommissionSettling && c.Status != CommissionPaid) || strings.TrimSpace(reason) == "" || len(reason) > 200 || at.IsZero() || at.Before(c.UpdatedAt) {
		return Commission{}, ErrTransition
	}
	next := c
	next.Status, next.ExceptionReason, next.Version, next.UpdatedAt = CommissionException, strings.TrimSpace(reason), c.Version+1, at.UTC()
	return next, nil
}
