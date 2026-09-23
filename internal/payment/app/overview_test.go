package app

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type overviewUoWStub struct{}

func (overviewUoWStub) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type overviewCanonicalStub struct {
	calls     [][]customerdomain.CustomerID
	roots     map[customerdomain.CustomerID]customerdomain.CustomerID
	err       error
	errOnCall int
}

var _ identityport.CanonicalCustomerRootsReader = (*overviewCanonicalStub)(nil)

func (stub *overviewCanonicalStub) CanonicalCustomerRoots(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerdomain.CustomerID, error) {
	stub.calls = append(stub.calls, append([]customerdomain.CustomerID{}, ids...))
	if stub.err != nil && (stub.errOnCall == 0 || len(stub.calls) == stub.errOnCall) {
		return nil, stub.err
	}
	result := make(map[customerdomain.CustomerID]customerdomain.CustomerID, len(ids))
	for _, id := range ids {
		root, exists := stub.roots[id]
		if !exists {
			root = id
		}
		result[id] = root
	}
	return result, nil
}

type overviewStoreStub struct {
	paid  paymentport.PaidOverview
	pages map[customerdomain.CustomerID][]customerdomain.CustomerID
}

func (stub overviewStoreStub) ReadPaidOverview(context.Context, paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	return stub.paid, nil
}

func (stub overviewStoreStub) ReadPaidOverviewPayerPage(_ context.Context, _ paymentport.OverviewWindow, after customerdomain.CustomerID, _ int) (paymentport.PaidOverviewPayerPage, error) {
	return paymentport.PaidOverviewPayerPage{CustomerIDs: append([]customerdomain.CustomerID{}, stub.pages[after]...)}, nil
}

func (overviewStoreStub) ReadPaidOverviewRecords(context.Context, paymentport.OverviewWindow, *paymentport.PaidOverviewRecordCursor, int) (paymentport.PaidOverviewRecordPage, error) {
	return paymentport.PaidOverviewRecordPage{}, nil
}

func (overviewStoreStub) ReadRefundOverview(context.Context, paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	return paymentport.RefundOverview{}, nil
}

func TestOverviewReaderCountsCanonicalPayersInBoundedKeysetPages(t *testing.T) {
	first := make([]customerdomain.CustomerID, paidOverviewPayerPageLimit)
	roots := make(map[customerdomain.CustomerID]customerdomain.CustomerID, paidOverviewPayerPageLimit+1)
	for index := range first {
		first[index] = customerdomain.CustomerID(index + 1)
		roots[first[index]] = customerdomain.CustomerID((index % 2) + 1001)
	}
	last := customerdomain.CustomerID(paidOverviewPayerPageLimit + 1)
	roots[last] = 2001
	canonical := &overviewCanonicalStub{roots: roots}
	reader, err := NewOverviewReader(overviewUoWStub{}, overviewStoreStub{paid: paymentport.PaidOverview{OrderCount: 501}, pages: map[customerdomain.CustomerID][]customerdomain.CustomerID{
		0:                              first,
		customerdomain.CustomerID(500): {last},
	}}, canonical)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.ReadPaidOverview(context.Background(), paymentport.OverviewWindow{Start: time.Now().UTC().Add(-time.Hour), End: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if result.DistinctCanonicalPayers != 3 || len(canonical.calls) != 2 || len(canonical.calls[0]) != paidOverviewPayerPageLimit || len(canonical.calls[1]) != 1 {
		t.Fatalf("canonical payer result=%+v calls=%v", result, canonical.calls)
	}
}

func TestOverviewReaderDoesNotReturnPartialCanonicalPayerCount(t *testing.T) {
	canonical := &overviewCanonicalStub{err: errors.New("identity unavailable")}
	reader, err := NewOverviewReader(overviewUoWStub{}, overviewStoreStub{paid: paymentport.PaidOverview{OrderCount: 1}, pages: map[customerdomain.CustomerID][]customerdomain.CustomerID{0: {1}}}, canonical)
	if err != nil {
		t.Fatal(err)
	}
	result, readErr := reader.ReadPaidOverview(context.Background(), paymentport.OverviewWindow{Start: time.Now().UTC().Add(-time.Hour), End: time.Now().UTC()})
	if !errors.Is(readErr, paymentport.ErrCanonicalPayerUnavailable) || result.DistinctCanonicalPayers != 0 {
		t.Fatalf("partial canonical payer result=%+v error=%v", result, readErr)
	}
}

func TestOverviewReaderDoesNotReturnCanonicalPayerCountWhenSecondPageFails(t *testing.T) {
	first := make([]customerdomain.CustomerID, paidOverviewPayerPageLimit)
	roots := make(map[customerdomain.CustomerID]customerdomain.CustomerID, paidOverviewPayerPageLimit)
	for index := range first {
		first[index] = customerdomain.CustomerID(index + 1)
		roots[first[index]] = customerdomain.CustomerID(index + 1)
	}
	last := customerdomain.CustomerID(paidOverviewPayerPageLimit + 1)
	canonical := &overviewCanonicalStub{roots: roots, err: errors.New("second root page unavailable"), errOnCall: 2}
	reader, err := NewOverviewReader(overviewUoWStub{}, overviewStoreStub{paid: paymentport.PaidOverview{OrderCount: int64(paidOverviewPayerPageLimit + 1)}, pages: map[customerdomain.CustomerID][]customerdomain.CustomerID{
		0:                              first,
		customerdomain.CustomerID(500): {last},
	}}, canonical)
	if err != nil {
		t.Fatal(err)
	}
	result, readErr := reader.ReadPaidOverview(context.Background(), paymentport.OverviewWindow{Start: time.Now().UTC().Add(-time.Hour), End: time.Now().UTC()})
	if !errors.Is(readErr, paymentport.ErrCanonicalPayerUnavailable) || result.DistinctCanonicalPayers != 0 || len(canonical.calls) != 2 || len(canonical.calls[0]) != paidOverviewPayerPageLimit || len(canonical.calls[1]) != 1 {
		t.Fatalf("partial second-page canonical payer result=%+v error=%v calls=%v", result, readErr, canonical.calls)
	}
}

func TestOverviewReaderSkipsIdentityForNoPayers(t *testing.T) {
	canonical := &overviewCanonicalStub{}
	reader, err := NewOverviewReader(overviewUoWStub{}, overviewStoreStub{pages: map[customerdomain.CustomerID][]customerdomain.CustomerID{}}, canonical)
	if err != nil {
		t.Fatal(err)
	}
	result, readErr := reader.ReadPaidOverview(context.Background(), paymentport.OverviewWindow{Start: time.Now().UTC().Add(-time.Hour), End: time.Now().UTC()})
	if readErr != nil || result.DistinctCanonicalPayers != 0 || len(canonical.calls) != 0 {
		t.Fatalf("empty payer result=%+v error=%v calls=%v", result, readErr, canonical.calls)
	}
}
