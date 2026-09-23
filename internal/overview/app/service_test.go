package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type overviewCustomerStub struct {
	facts   customerport.NewCustomerOverview
	err     error
	release <-chan struct{}
}

func (stub overviewCustomerStub) ReadNewCustomerOverview(context.Context, customerport.OverviewWindow) (customerport.NewCustomerOverview, error) {
	if stub.release != nil {
		<-stub.release
	}
	return stub.facts, stub.err
}

type overviewPaymentStub struct {
	paid            paymentport.PaidOverview
	paidErr         error
	paidRecords     paymentport.PaidOverviewRecordPage
	paidRecordsErr  error
	paidRecordsCall *overviewPaidRecordsCall
	refunds         paymentport.RefundOverview
	refundErr       error
	blockPaid       bool
	refundReady     chan<- struct{}
	refundRelease   <-chan struct{}
}

type overviewPaidRecordsCall struct {
	window paymentport.OverviewWindow
	cursor *paymentport.PaidOverviewRecordCursor
	limit  int
}

func (stub overviewPaymentStub) ReadPaidOverview(ctx context.Context, _ paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	if stub.blockPaid {
		<-ctx.Done()
		return paymentport.PaidOverview{}, ctx.Err()
	}
	return stub.paid, stub.paidErr
}

func (stub overviewPaymentStub) ReadPaidOverviewRecords(_ context.Context, window paymentport.OverviewWindow, cursor *paymentport.PaidOverviewRecordCursor, limit int) (paymentport.PaidOverviewRecordPage, error) {
	if stub.paidRecordsCall != nil {
		stub.paidRecordsCall.window = window
		stub.paidRecordsCall.limit = limit
		if cursor != nil {
			copy := *cursor
			stub.paidRecordsCall.cursor = &copy
		}
	}
	return stub.paidRecords, stub.paidRecordsErr
}

func (stub overviewPaymentStub) ReadRefundOverview(context.Context, paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	if stub.refundReady != nil {
		stub.refundReady <- struct{}{}
	}
	if stub.refundRelease != nil {
		<-stub.refundRelease
	}
	return stub.refunds, stub.refundErr
}

type overviewDistributionStub struct {
	facts   distributionport.Overview
	err     error
	release <-chan struct{}
}

func (stub overviewDistributionStub) ReadOverview(context.Context, distributionport.OverviewWindow) (distributionport.Overview, error) {
	if stub.release != nil {
		<-stub.release
	}
	return stub.facts, stub.err
}

func overviewQuery() Query {
	return Query{Range: Range{Period: "today", Timezone: "Asia/Shanghai", Start: time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)}}
}

func TestReadReturnsOtherSectionsWhenOneOwnerTimesOut(t *testing.T) {
	service, err := NewServiceWithTimeout(
		overviewCustomerStub{facts: customerport.NewCustomerOverview{KnownNewCanonicalCustomers: 2}},
		overviewPaymentStub{blockPaid: true, refunds: paymentport.RefundOverview{Completed: []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 30}}, CompletedCount: 1}},
		overviewDistributionStub{facts: distributionport.Overview{PeriodPaidSalesMinor: 100, PeriodCommissionCount: 1, Currency: "CNY"}},
		40*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := service.Read(context.Background(), overviewQuery())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("overview waited beyond its section deadline: %s", elapsed)
	}
	if response.Paid.Status != StatusFailed || response.Paid.ReasonCode != "payment_aggregate_timeout" || response.Paid.DistinctCanonicalPayers != nil {
		t.Fatalf("paid timeout section=%+v", response.Paid.Section)
	}
	if response.Customers.Status != StatusReady || response.Customers.NewCanonicalCustomers != 2 {
		t.Fatalf("customer section was not retained: %+v", response.Customers)
	}
	if response.Distribution.Status != StatusReady || response.Distribution.PeriodPaidSalesMinor != 100 {
		t.Fatalf("distribution section was not retained: %+v", response.Distribution)
	}
	if response.Refunds.Status != StatusDataMissing || response.Refunds.ReasonCode != "net_paid_aggregate_unavailable" || response.Refunds.CompletedCount != 1 {
		t.Fatalf("refund known result was not retained beside timed-out gross: %+v", response.Refunds)
	}
	if response.Paid.AsOf.IsZero() || response.Customers.AsOf.IsZero() || response.Refunds.AsOf.IsZero() || response.Distribution.AsOf.IsZero() {
		t.Fatalf("each section must report its observed completion time: %+v", response)
	}
}

func TestPaidCanonicalPayerUnavailableOmitsOnlyThatUnknownMetric(t *testing.T) {
	service, err := NewServiceWithTimeout(
		overviewCustomerStub{},
		overviewPaymentStub{paid: paymentport.PaidOverview{Gross: []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 120}}, OrderCount: 1}},
		overviewDistributionStub{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.payments = overviewPaymentStub{paid: paymentport.PaidOverview{Gross: []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 120}}, OrderCount: 1}, paidErr: paymentport.ErrCanonicalPayerUnavailable}
	response, err := service.Read(context.Background(), overviewQuery())
	if err != nil {
		t.Fatal(err)
	}
	if response.Paid.Status != StatusDataMissing || response.Paid.ReasonCode != "canonical_payer_unavailable" || response.Paid.DistinctCanonicalPayers != nil || response.Paid.OrderCount != 1 || len(response.Paid.Gross) != 1 || response.Paid.Gross[0].AmountMinor != 120 {
		t.Fatalf("canonical payer unavailable response=%+v", response.Paid)
	}
}

func TestPaidAndNetExposeKnownSubsetWhenHistoryConfirmationIsMissing(t *testing.T) {
	service, err := NewServiceWithTimeout(
		overviewCustomerStub{},
		overviewPaymentStub{paid: paymentport.PaidOverview{
			Gross:                             []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 120}},
			OrderCount:                        1,
			DistinctCanonicalPayers:           1,
			MissingConfirmationEvidenceCount:  1,
			MissingConfirmationEvidenceAmount: []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 80}},
		}, refunds: paymentport.RefundOverview{Completed: []paymentport.OverviewMoney{{Currency: "CNY", AmountMinor: 20}}, CompletedCount: 1}},
		overviewDistributionStub{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Read(context.Background(), overviewQuery())
	if err != nil {
		t.Fatal(err)
	}
	if response.Paid.Status != StatusDataMissing || response.Paid.ReasonCode != "paid_confirmation_time_missing" || response.Paid.OrderCount != 1 || len(response.Paid.MissingConfirmationEvidenceAmount) != 1 || response.Paid.MissingConfirmationEvidenceAmount[0].AmountMinor != 80 {
		t.Fatalf("paid response=%+v", response.Paid)
	}
	if response.Refunds.Status != StatusDataMissing || response.Refunds.ReasonCode != "net_paid_confirmation_time_missing" || len(response.Refunds.NetAmount) != 1 || response.Refunds.NetAmount[0].AmountMinor != 100 {
		t.Fatalf("net known subset response=%+v", response.Refunds)
	}
}

func TestReadPaidRecordsUsesPaymentWindowAndNeverReturnsPartialPage(t *testing.T) {
	call := &overviewPaidRecordsCall{}
	cursor := &paymentport.PaidOverviewRecordCursor{PaidConfirmedAt: time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC), PaymentID: 44}
	service, err := NewServiceWithTimeout(
		overviewCustomerStub{},
		overviewPaymentStub{paidRecords: paymentport.PaidOverviewRecordPage{Items: []paymentport.PaidOverviewRecord{{Provider: "wechat_pay", OrderReference: "M-paid-record", AmountMinor: 120, Currency: "CNY", PaidConfirmedAt: cursor.PaidConfirmedAt}}}, paidRecordsCall: call},
		overviewDistributionStub{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	query := PaidRecordsQuery{Range: overviewQuery().Range, Cursor: cursor}
	response, err := service.ReadPaidRecords(context.Background(), query)
	if err != nil || call.limit != PaidRecordsPageSize || call.cursor == nil || call.cursor.PaymentID != cursor.PaymentID || !call.window.Start.Equal(query.Range.Start) || !call.window.End.Equal(query.Range.End) || len(response.Items) != 1 || response.Items[0].OrderReference != "M-paid-record" {
		t.Fatalf("response=%+v call=%+v err=%v", response, call, err)
	}
	service.payments = overviewPaymentStub{paidRecords: paymentport.PaidOverviewRecordPage{Items: response.Items}, paidRecordsErr: errors.New("payment unavailable")}
	failed, readErr := service.ReadPaidRecords(context.Background(), PaidRecordsQuery{Range: overviewQuery().Range})
	if readErr == nil || len(failed.Items) != 0 || failed.NextCursor != nil {
		t.Fatalf("failed page=%+v err=%v", failed, readErr)
	}
}

func TestRefundAsOfIsTheRefundOwnerObservation(t *testing.T) {
	others := make(chan struct{})
	refundRelease := make(chan struct{})
	refundReady := make(chan struct{}, 1)
	service, err := NewServiceWithTimeout(
		overviewCustomerStub{release: others},
		overviewPaymentStub{blockPaid: true, refundReady: refundReady, refundRelease: refundRelease},
		overviewDistributionStub{release: others},
		50*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	refundObserved := make(chan struct{})
	var clockMu sync.Mutex
	call := 0
	service.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		call++
		if call == 1 {
			close(refundObserved)
		}
		return base.Add(time.Duration(call) * time.Second)
	}
	type result struct {
		response Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, readErr := service.Read(context.Background(), overviewQuery())
		done <- result{response: response, err: readErr}
	}()
	<-refundReady
	close(refundRelease)
	<-refundObserved
	close(others)
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	want := base.Add(time.Second)
	if !outcome.response.Refunds.AsOf.Equal(want) {
		t.Fatalf("refund as_of=%s want its own owner observation %s", outcome.response.Refunds.AsOf, want)
	}
}

func TestDistributionResponseRemainsReadyForOpenExceptionOrdersWithoutMoney(t *testing.T) {
	asOf := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	response := distributionResponse(asOf, distributionport.Overview{CurrentExceptionOrderCount: 1, Currency: "CNY"}, nil)
	if response.Status != StatusReady || response.CurrentExceptionOrderCount != 1 {
		t.Fatalf("distribution exception-order-only response=%+v", response)
	}
}
