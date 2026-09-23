// Package app coordinates the stateless admin operating overview. It reads
// only stable Customer, Payment and Distribution ports; it owns no
// database table, Provider call, queue, or background task.
package app

import (
	"context"
	"errors"
	"sort"
	"time"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type Status string

const (
	StatusReady       Status = "ready"
	StatusZero        Status = "zero"
	StatusDataMissing Status = "data_missing"
	StatusFailed      Status = "failed"
)

const authorizedGlobalScope = "admin_authorized_global"

// DefaultSectionReadTimeout bounds each independent owner read. PostgreSQL
// honors request-context cancellation, so one saturated owner connection does
// not consume the whole overview request or hide the sections that did
// return. This follows the repository's request-context timeout pattern and
// deliberately does not start a background job.
const DefaultSectionReadTimeout = 2 * time.Second

// PaidRecordsPageSize is deliberately fixed for the admin drawer. The API
// never lets a browser turn a detail click into an unbounded Payment read.
const PaidRecordsPageSize = 25

type Section struct {
	Status     Status    `json:"status"`
	AsOf       time.Time `json:"as_of"`
	Scope      string    `json:"scope"`
	ReasonCode string    `json:"reason_code,omitempty"`
}

type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type Range struct {
	Period   string    `json:"period"`
	Timezone string    `json:"timezone"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// Contract makes each displayed number's owner and denominator explicit for
// the Host. Owner read ports run in separate short PostgreSQL transactions to
// preserve partial results, so their observations are not a cross-domain
// serializable snapshot. The Host must use a section's status/reason rather
// than deriving consistency from a coincidental equality of counts.
type Contract struct {
	Scope            string `json:"scope"`
	PaidGross        string `json:"paid_gross"`
	PaidPayers       string `json:"paid_payers"`
	NewCustomers     string `json:"new_customers"`
	Refunds          string `json:"refunds"`
	Distribution     string `json:"distribution"`
	CrossDomainReads string `json:"cross_domain_reads"`
}

type TrendPoint struct {
	Date       string  `json:"date"`
	Gross      []Money `json:"gross"`
	OrderCount int64   `json:"order_count"`
}

type Paid struct {
	Section
	Gross                             []Money      `json:"gross"`
	OrderCount                        int64        `json:"order_count"`
	DistinctCanonicalPayers           *int64       `json:"distinct_canonical_payers,omitempty"`
	MissingPayerCount                 int64        `json:"missing_payer_count,omitempty"`
	MissingConfirmationEvidenceCount  int64        `json:"missing_confirmation_evidence_count,omitempty"`
	MissingConfirmationEvidenceAmount []Money      `json:"missing_confirmation_evidence_amount,omitempty"`
	Trend                             []TrendPoint `json:"trend"`
}

type Customers struct {
	Section
	NewCanonicalCustomers int64  `json:"new_canonical_customers"`
	HistoricalExcluded    int64  `json:"historical_excluded"`
	UnknownSource         int64  `json:"unknown_source_count,omitempty"`
	Evidence              string `json:"evidence,omitempty"`
}

type Refunds struct {
	Section
	CompletedAmount           []Money `json:"completed_amount"`
	CompletedCount            int64   `json:"completed_count"`
	MissingCompletionEvidence int64   `json:"missing_completion_evidence_count,omitempty"`
	NetAmount                 []Money `json:"net_amount"`
	Evidence                  string  `json:"evidence,omitempty"`
}

type Distribution struct {
	Section
	PeriodPaidSalesMinor       int64  `json:"period_paid_sales_minor"`
	PeriodInitialCommission    int64  `json:"period_initial_commission_minor"`
	PeriodCommissionCount      int64  `json:"period_commission_count"`
	CurrentUnsettledMinor      int64  `json:"current_unsettled_minor"`
	CurrentSettledMinor        int64  `json:"current_settled_minor"`
	CurrentExceptionOrderCount int64  `json:"current_exception_order_count"`
	Currency                   string `json:"currency"`
}

type Todo struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
	Href  string `json:"href"`
}

type Todos struct {
	Section
	Items []Todo `json:"items"`
}

type Response struct {
	Range        Range        `json:"range"`
	Contract     Contract     `json:"contract"`
	Paid         Paid         `json:"paid"`
	Customers    Customers    `json:"customers"`
	Refunds      Refunds      `json:"refunds"`
	Distribution Distribution `json:"distribution"`
	Todos        Todos        `json:"todos"`
}

type Query struct {
	Range Range
}

func (q Query) Valid() bool {
	return q.Range.Period != "" && q.Range.Timezone == "Asia/Shanghai" && !q.Range.Start.IsZero() && !q.Range.End.IsZero() && q.Range.End.After(q.Range.Start)
}

// PaidRecordsQuery reuses the exact reporting range of the overview's paid
// denominator. The cursor is Payment's typed keyset position; HTTP owns any
// opaque serialization and binds it to this Range before calling the app.
type PaidRecordsQuery struct {
	Range  Range
	Cursor *paymentport.PaidOverviewRecordCursor
}

func (q PaidRecordsQuery) Valid() bool {
	return Query{Range: q.Range}.Valid() && (q.Cursor == nil || q.Cursor.Valid())
}

// PaidRecordsResponse is intentionally limited to Payment-owned payment
// facts. It does not resolve a historical payer to a present-day Customer.
type PaidRecordsResponse struct {
	Range      Range
	Items      []paymentport.PaidOverviewRecord
	NextCursor *paymentport.PaidOverviewRecordCursor
}

type Service struct {
	customers    customerport.OverviewReader
	payments     paymentport.OverviewReader
	distribution distributionport.OverviewReader
	now          func() time.Time
	readTimeout  time.Duration
}

func NewService(customers customerport.OverviewReader, payments paymentport.OverviewReader, distribution distributionport.OverviewReader) (*Service, error) {
	return NewServiceWithTimeout(customers, payments, distribution, DefaultSectionReadTimeout)
}

// NewServiceWithTimeout exists for Composition and focused failure tests. A
// caller cannot disable the deadline: the overview must retain independent
// partial availability under a slow owner read.
func NewServiceWithTimeout(customers customerport.OverviewReader, payments paymentport.OverviewReader, distribution distributionport.OverviewReader, readTimeout time.Duration) (*Service, error) {
	if customers == nil || payments == nil {
		return nil, paymentport.ErrUnavailable
	}
	if readTimeout <= 0 {
		return nil, paymentport.ErrInvalid
	}
	return &Service{customers: customers, payments: payments, distribution: distribution, now: time.Now, readTimeout: readTimeout}, nil
}

func (service *Service) Read(ctx context.Context, query Query) (Response, error) {
	if service == nil || service.customers == nil || service.payments == nil || service.now == nil || service.readTimeout <= 0 || !query.Valid() {
		return Response{}, paymentport.ErrInvalid
	}
	response := Response{Range: query.Range, Contract: overviewContract()}
	paymentWindow := paymentport.OverviewWindow{Start: query.Range.Start, End: query.Range.End}
	customerWindow := customerport.OverviewWindow{Start: query.Range.Start, End: query.Range.End}
	distributionWindow := distributionport.OverviewWindow{Start: query.Range.Start, End: query.Range.End}

	paidDone := make(chan paidReadResult, 1)
	customersDone := make(chan customerReadResult, 1)
	refundsDone := make(chan refundReadResult, 1)
	go service.readPaid(ctx, paymentWindow, paidDone)
	go service.readCustomers(ctx, customerWindow, customersDone)
	go service.readRefunds(ctx, paymentWindow, refundsDone)
	var distributionDone chan distributionReadResult
	if service.distribution != nil {
		distributionDone = make(chan distributionReadResult, 1)
		go service.readDistribution(ctx, distributionWindow, distributionDone)
	}

	paidResult := <-paidDone
	customersResult := <-customersDone
	refundsResult := <-refundsDone

	response.Paid = paidResponse(paidResult.asOf, paidResult.facts, paidResult.err)
	response.Customers = customerResponse(customersResult.asOf, customersResult.facts, customersResult.err)
	// Refunds may display a net amount that is derived from the independent Paid
	// read, but its section observation time belongs to the refund owner read.
	// Reporting the later of two observations would falsely imply a single
	// snapshot and hide the documented cross-domain timing boundary.
	response.Refunds = refundResponse(refundsResult.asOf, refundsResult.facts, refundsResult.err, paidResult.facts, paidResult.err)

	if distributionDone == nil {
		asOf := service.now().UTC()
		response.Distribution = Distribution{Section: missingSection(asOf, "distribution_not_configured"), Currency: "CNY"}
		response.Todos = Todos{Section: missingSection(asOf, "distribution_not_configured"), Items: []Todo{}}
		return response, nil
	}
	distributionResult := <-distributionDone
	response.Distribution = distributionResponse(distributionResult.asOf, distributionResult.facts, distributionResult.err)
	response.Todos = todosResponse(distributionResult.asOf, distributionResult.facts, distributionResult.err)
	return response, nil
}

// ReadPaidRecords reads a bounded, same-denominator Payment page for the
// overview drawer. Unlike Read, it is one explicit owner read: an error never
// returns a partial page dressed as a successful overview response.
func (service *Service) ReadPaidRecords(ctx context.Context, query PaidRecordsQuery) (PaidRecordsResponse, error) {
	if service == nil || service.payments == nil || service.readTimeout <= 0 || !query.Valid() {
		return PaidRecordsResponse{}, paymentport.ErrInvalid
	}
	child, cancel := context.WithTimeout(ctx, service.readTimeout)
	page, err := service.payments.ReadPaidOverviewRecords(child, paymentport.OverviewWindow{Start: query.Range.Start, End: query.Range.End}, query.Cursor, PaidRecordsPageSize)
	cancel()
	if err != nil {
		return PaidRecordsResponse{}, err
	}
	return PaidRecordsResponse{Range: query.Range, Items: page.Items, NextCursor: page.NextCursor}, nil
}

type paidReadResult struct {
	facts paymentport.PaidOverview
	err   error
	asOf  time.Time
}

type customerReadResult struct {
	facts customerport.NewCustomerOverview
	err   error
	asOf  time.Time
}

type refundReadResult struct {
	facts paymentport.RefundOverview
	err   error
	asOf  time.Time
}

type distributionReadResult struct {
	facts distributionport.Overview
	err   error
	asOf  time.Time
}

func (service *Service) readPaid(ctx context.Context, window paymentport.OverviewWindow, done chan<- paidReadResult) {
	child, cancel := context.WithTimeout(ctx, service.readTimeout)
	facts, err := service.payments.ReadPaidOverview(child, window)
	cancel()
	done <- paidReadResult{facts: facts, err: err, asOf: service.now().UTC()}
}

func (service *Service) readCustomers(ctx context.Context, window customerport.OverviewWindow, done chan<- customerReadResult) {
	child, cancel := context.WithTimeout(ctx, service.readTimeout)
	facts, err := service.customers.ReadNewCustomerOverview(child, window)
	cancel()
	done <- customerReadResult{facts: facts, err: err, asOf: service.now().UTC()}
}

func (service *Service) readRefunds(ctx context.Context, window paymentport.OverviewWindow, done chan<- refundReadResult) {
	child, cancel := context.WithTimeout(ctx, service.readTimeout)
	facts, err := service.payments.ReadRefundOverview(child, window)
	cancel()
	done <- refundReadResult{facts: facts, err: err, asOf: service.now().UTC()}
}

func (service *Service) readDistribution(ctx context.Context, window distributionport.OverviewWindow, done chan<- distributionReadResult) {
	child, cancel := context.WithTimeout(ctx, service.readTimeout)
	facts, err := service.distribution.ReadOverview(child, window)
	cancel()
	done <- distributionReadResult{facts: facts, err: err, asOf: service.now().UTC()}
}

func baseSection(asOf time.Time) Section {
	return Section{Status: StatusReady, AsOf: asOf, Scope: authorizedGlobalScope}
}

func failedSection(asOf time.Time, reason string) Section {
	section := baseSection(asOf)
	section.Status, section.ReasonCode = StatusFailed, reason
	return section
}

func failedReadSection(asOf time.Time, prefix string, err error) Section {
	if errors.Is(err, context.DeadlineExceeded) {
		return failedSection(asOf, prefix+"_timeout")
	}
	if errors.Is(err, context.Canceled) {
		return failedSection(asOf, prefix+"_cancelled")
	}
	return failedSection(asOf, prefix+"_failed")
}

func missingSection(asOf time.Time, reason string) Section {
	section := baseSection(asOf)
	section.Status, section.ReasonCode = StatusDataMissing, reason
	return section
}

func zeroSection(asOf time.Time) Section {
	section := baseSection(asOf)
	section.Status = StatusZero
	return section
}

func paidResponse(asOf time.Time, facts paymentport.PaidOverview, err error) Paid {
	canonicalPayers := facts.DistinctCanonicalPayers
	result := Paid{Section: baseSection(asOf), Gross: paymentMoney(facts.Gross), OrderCount: facts.OrderCount, DistinctCanonicalPayers: &canonicalPayers, MissingPayerCount: facts.MissingPayerCount, MissingConfirmationEvidenceCount: facts.MissingConfirmationEvidenceCount, MissingConfirmationEvidenceAmount: paymentMoney(facts.MissingConfirmationEvidenceAmount), Trend: paymentTrendPoints(facts.Trend)}
	switch {
	case errors.Is(err, paymentport.ErrCanonicalPayerUnavailable):
		result.DistinctCanonicalPayers = nil
		result.Section = missingSection(asOf, "canonical_payer_unavailable")
	case err != nil:
		result.DistinctCanonicalPayers = nil
		result.Section = failedReadSection(asOf, "payment_aggregate", err)
	case facts.MissingConfirmationEvidenceCount > 0:
		result.Section = missingSection(asOf, "paid_confirmation_time_missing")
	case facts.MissingPayerCount > 0:
		result.Section = missingSection(asOf, "payer_customer_missing")
	case facts.OrderCount == 0:
		result.Section = zeroSection(asOf)
	}
	return result
}

func overviewContract() Contract {
	return Contract{
		Scope:            authorizedGlobalScope,
		PaidGross:        "payment.payments: status=paid, one unique business order_id, and trustworthy original paid_confirmed_at; native and history payments share this denominator, while no-time history amounts are returned as missing evidence",
		PaidPayers:       "payment.payments: same known paid-confirmed-at denominator, historic payer_customer_id resolved read-only to current canonical Customer roots; unavailable roots omit the count rather than returning a partial or zero value",
		NewCustomers:     "identity.customers: root created_at with explicit interactive identity source; history receipts excluded; unknown sources are data_missing",
		Refunds:          "payment refund evidence: current completed refund uses latest payment.refund_settled audit append by id; completed history uses payment.refund_history_imported at its source occurred_at",
		Distribution:     "distribution.commissions: period paid_confirmed_at; current settlement balances and open/querying exceptions are not period-filtered",
		CrossDomainReads: "each owner port is read independently with its own bounded request context and observation time; do not infer failure solely from count differences",
	}
}

func customerResponse(asOf time.Time, facts customerport.NewCustomerOverview, err error) Customers {
	result := Customers{Section: baseSection(asOf), NewCanonicalCustomers: facts.KnownNewCanonicalCustomers, HistoricalExcluded: facts.HistoricalExcluded, UnknownSource: facts.UnknownSource, Evidence: facts.Evidence}
	switch {
	case err != nil:
		result.Section = failedReadSection(asOf, "customer_provenance_aggregate", err)
	case facts.UnknownSource > 0:
		result.Section = missingSection(asOf, "customer_creation_source_unknown")
	case facts.KnownNewCanonicalCustomers == 0:
		result.Section = zeroSection(asOf)
	}
	return result
}

func refundResponse(asOf time.Time, facts paymentport.RefundOverview, refundErr error, paid paymentport.PaidOverview, paidErr error) Refunds {
	result := Refunds{Section: baseSection(asOf), CompletedAmount: paymentMoney(facts.Completed), CompletedCount: facts.CompletedCount, MissingCompletionEvidence: facts.MissingCompletionEvidence, NetAmount: nil, Evidence: "payment.refund_completion_evidence.v1"}
	if refundErr != nil {
		result.Section = failedReadSection(asOf, "refund_aggregate", refundErr)
		return result
	}
	if paidErr != nil {
		result.Section = missingSection(asOf, "net_paid_aggregate_unavailable")
		return result
	}
	result.NetAmount = subtractMoney(paymentMoney(paid.Gross), result.CompletedAmount)
	if facts.MissingCompletionEvidence > 0 {
		result.Section = missingSection(asOf, "refund_completed_at_missing")
		return result
	}
	if paid.MissingConfirmationEvidenceCount > 0 {
		result.Section = missingSection(asOf, "net_paid_confirmation_time_missing")
		return result
	}
	if facts.CompletedCount == 0 && moneyIsZero(result.NetAmount) {
		result.Section = zeroSection(asOf)
	}
	return result
}

func distributionResponse(asOf time.Time, facts distributionport.Overview, err error) Distribution {
	result := Distribution{Section: baseSection(asOf), PeriodPaidSalesMinor: facts.PeriodPaidSalesMinor, PeriodInitialCommission: facts.PeriodInitialCommission, PeriodCommissionCount: facts.PeriodCommissionCount, CurrentUnsettledMinor: facts.CurrentUnsettledMinor, CurrentSettledMinor: facts.CurrentSettledMinor, CurrentExceptionOrderCount: facts.CurrentExceptionOrderCount, Currency: facts.Currency}
	if result.Currency == "" {
		result.Currency = "CNY"
	}
	if err != nil {
		result.Section = failedReadSection(asOf, "distribution_aggregate", err)
		return result
	}
	if facts.PeriodPaidSalesMinor == 0 && facts.PeriodInitialCommission == 0 && facts.PeriodCommissionCount == 0 && facts.CurrentUnsettledMinor == 0 && facts.CurrentSettledMinor == 0 && facts.CurrentExceptionOrderCount == 0 {
		result.Section = zeroSection(asOf)
	}
	return result
}

func todosResponse(asOf time.Time, facts distributionport.Overview, err error) Todos {
	result := Todos{Section: baseSection(asOf), Items: []Todo{{Code: "distribution_exceptions", Count: facts.OpenExceptionCount, Href: "/admin/distribution"}}}
	if err != nil {
		result.Section = failedReadSection(asOf, "distribution_todo_aggregate", err)
		return result
	}
	if facts.OpenExceptionCount == 0 {
		result.Section = zeroSection(asOf)
	}
	return result
}

func paymentMoney(items []paymentport.OverviewMoney) []Money {
	result := make([]Money, 0, len(items))
	for _, item := range items {
		result = append(result, Money{AmountMinor: item.AmountMinor, Currency: item.Currency})
	}
	return result
}

func paymentTrendPoints(items []paymentport.PaidOverviewTrend) []TrendPoint {
	result := make([]TrendPoint, 0, len(items))
	for _, item := range items {
		result = append(result, TrendPoint{Date: item.Date, Gross: paymentMoney(item.Gross), OrderCount: item.OrderCount})
	}
	return result
}

func subtractMoney(gross, refunds []Money) []Money {
	byCurrency := map[string]int64{}
	for _, item := range gross {
		byCurrency[item.Currency] += item.AmountMinor
	}
	for _, item := range refunds {
		byCurrency[item.Currency] -= item.AmountMinor
	}
	keys := make([]string, 0, len(byCurrency))
	for currency := range byCurrency {
		keys = append(keys, currency)
	}
	sort.Strings(keys)
	result := make([]Money, 0, len(keys))
	for _, currency := range keys {
		result = append(result, Money{Currency: currency, AmountMinor: byCurrency[currency]})
	}
	return result
}

func moneyIsZero(items []Money) bool {
	for _, item := range items {
		if item.AmountMinor != 0 {
			return false
		}
	}
	return true
}
