package port

import (
	"context"
	"time"
)

type ID int64

type ValidityMode string

const (
	ValidityFixedRange   ValidityMode = "fixed_range"
	ValidityRelativeDays ValidityMode = "relative_days"
)

type Coupon struct {
	ID                   ID           `json:"id"`
	Name                 string       `json:"name"`
	DiscountAmountTotal  int64        `json:"discount_amount_total"`
	Currency             string       `json:"currency"`
	Status               string       `json:"status"`
	AvailabilityStatus   string       `json:"availability_status"`
	TotalIssueLimit      int64        `json:"total_issue_limit"`
	PerUserIssueLimit    int64        `json:"per_user_issue_limit"`
	IssuedCount          int64        `json:"issued_count"`
	ClaimStartsAt        time.Time    `json:"claim_starts_at"`
	ClaimEndsAt          time.Time    `json:"claim_ends_at"`
	ValidityMode         ValidityMode `json:"validity_mode"`
	UseStartsAt          *time.Time   `json:"use_starts_at"`
	UseEndsAt            *time.Time   `json:"use_ends_at"`
	RelativeValidityDays *int32       `json:"relative_validity_days"`
	Instructions         string       `json:"instructions"`
	TargetRefs           []string     `json:"target_refs"`
	CreatedBy            int64        `json:"created_by"`
	UpdatedBy            int64        `json:"updated_by"`
	Version              int64        `json:"version"`
	HistoryOnly          bool         `json:"history_only,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

// AvailabilityStatusAt returns Coupon's shared public availability state.
// It intentionally excludes holder-specific limits: callers that know a
// canonical customer may separately expose the durable claim-count fact, while
// every actual claim remains transactionally revalidated by Coupon.
func AvailabilityStatusAt(c Coupon, at time.Time) string {
	switch {
	case c.Status == "published" && c.IssuedCount >= c.TotalIssueLimit:
		return "sold_out"
	case c.Status == "published" && at.Before(c.ClaimStartsAt):
		return "scheduled"
	case c.Status == "published" && !at.Before(c.ClaimEndsAt):
		return "ended"
	case c.Status == "published":
		return "active"
	default:
		return c.Status
	}
}

type UpsertCommand struct {
	Coupon
	Actor          int64
	IdempotencyKey string
}

type Page struct {
	Items  []Coupon `json:"items"`
	Total  int64    `json:"total"`
	Limit  int32    `json:"limit"`
	Offset int32    `json:"offset"`
}

// RuleStats is derived only from the Coupon rule row. It contains no claim,
// redemption, customer, order, payment, or entitlement information.
type RuleStats struct {
	CouponID            ID        `json:"coupon_id"`
	TotalIssueLimit     int64     `json:"total_issue_limit"`
	IssuedCount         int64     `json:"issued_count"`
	RemainingIssueCount int64     `json:"remaining_issue_count"`
	Status              string    `json:"status"`
	AvailabilityStatus  string    `json:"availability_status"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// RuleApplication is the local, transport-neutral PR05 contract. It manages
// definitions and derived rule counters only; it exposes no claim, holder,
// redemption, order, payment, entitlement, or identity operation.
type RuleApplication interface {
	List(context.Context, int32, int32, string, string) (Page, error)
	Get(context.Context, ID) (Coupon, error)
	Stats(context.Context, ID) (RuleStats, error)
	Create(context.Context, UpsertCommand) (Coupon, error)
	Update(context.Context, UpsertCommand) (Coupon, error)
	UpdateDraft(context.Context, UpsertCommand) (Coupon, error)
	Publish(context.Context, ID, int64, string) (Coupon, error)
	Stop(context.Context, ID, int64, string) (Coupon, error)
	// Archive is a versioned terminal lifecycle command. The caller must bind
	// the exact row version displayed in its confirmation; replaying the same
	// idempotency key/body returns the original archived snapshot.
	Archive(context.Context, ID, int64, int64, string) (Coupon, error)
	Delete(context.Context, ID, int64, string) (Coupon, error)
	Copy(context.Context, ID, int64, string) (Coupon, error)
}
