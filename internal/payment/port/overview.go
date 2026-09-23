package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// OverviewWindow is a half-open, UTC-normalized business reporting window.
type OverviewWindow struct {
	Start time.Time
	End   time.Time
}

func (w OverviewWindow) Valid() bool {
	return !w.Start.IsZero() && !w.End.IsZero() && w.End.After(w.Start)
}

type OverviewMoney struct {
	AmountMinor int64
	Currency    string
}

// PaidOverviewTrend is grouped by the Asia/Shanghai calendar date of Payment's
// persisted original paid-confirmation fact.
type PaidOverviewTrend struct {
	Date       string
	Gross      []OverviewMoney
	OrderCount int64
}

// PaidOverview is the one financial denominator for gross amount, business
// order count and canonical payer count. payments.order_id is unique, and
// implementations count distinct order_id so a payment attempt is never
// presented as a business order. Trusted history carries its original
// paid_confirmed_at in the same owner table and is included here.
//
// MissingConfirmationEvidence is intentionally not range-filtered: without a
// confirmation instant, an imported paid payment cannot be assigned to any
// requested reporting window. It therefore makes the selected-window result
// incomplete rather than silently dropping legacy money or calling it zero.
type PaidOverview struct {
	Gross                             []OverviewMoney
	OrderCount                        int64
	DistinctCanonicalPayers           int64
	MissingPayerCount                 int64
	MissingConfirmationEvidenceCount  int64
	MissingConfirmationEvidenceAmount []OverviewMoney
	Trend                             []PaidOverviewTrend
}

// PaidOverviewPayerPage is an internal Payment-owner keyset page. It is kept
// out of the HTTP response so historical payer IDs never leave the composed
// overview read path.
type PaidOverviewPayerPage struct {
	CustomerIDs []customerdomain.CustomerID
}

// PaidOverviewRecordCursor is the Payment-owner position for an exact
// paid-confirmation keyset. It is intentionally typed rather than exposing a
// database ID through the overview HTTP response.
type PaidOverviewRecordCursor struct {
	PaidConfirmedAt time.Time
	PaymentID       int64
}

func (c PaidOverviewRecordCursor) Valid() bool {
	return !c.PaidConfirmedAt.IsZero() && c.PaymentID > 0
}

// PaidOverviewRecord is one Payment row behind the paid gross and business
// order count in an OverviewWindow. payments.order_id is unique, so one row
// is also one business order. PayerCustomerID remains the historical Payment
// fact; callers must not present it as a current canonical Customer root.
//
// Provider is limited by Payment's persisted provider contract. It is present
// with OrderReference because a merchant reference can collide across
// providers. Provider transaction references and external identity values are
// deliberately excluded.
type PaidOverviewRecord struct {
	Provider        string
	OrderReference  string
	PayerCustomerID *int64
	AmountMinor     int64
	Currency        string
	PaidConfirmedAt time.Time
}

// PaidOverviewRecordPage is a bounded Payment-owner page. NextCursor is not
// serialized directly; the composing HTTP layer binds it to its reporting
// window before emitting an opaque cursor.
type PaidOverviewRecordPage struct {
	Items      []PaidOverviewRecord
	NextCursor *PaidOverviewRecordCursor
}

// RefundOverview is backed only by the latest committed
// payment.refund_settled audit fact for a refund that is currently completed.
// MissingCompletionEvidence counts completed local rows for which no such
// fact exists and therefore no trustworthy completed-at instant is available.
type RefundOverview struct {
	Completed                 []OverviewMoney
	CompletedCount            int64
	MissingCompletionEvidence int64
}

// OverviewReader is the stable Payment read seam for the admin overview. Its
// implementation may query only Payment-owned tables.
type OverviewReader interface {
	ReadPaidOverview(context.Context, OverviewWindow) (PaidOverview, error)
	ReadPaidOverviewRecords(context.Context, OverviewWindow, *PaidOverviewRecordCursor, int) (PaidOverviewRecordPage, error)
	ReadRefundOverview(context.Context, OverviewWindow) (RefundOverview, error)
}
