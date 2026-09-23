package adapter

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type passthroughUoW struct{}

func (passthroughUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type audienceReaderStub struct{ ids []customerdomain.CustomerID }

func (s audienceReaderStub) ActiveWithin(_ context.Context, reference time.Time, _ int) ([]customerdomain.CustomerID, time.Time, error) {
	return s.ids, reference.Add(-time.Minute), nil
}

type canonicalStub struct{ batchCalls int }

func (canonicalStub) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	if id == 2 {
		return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: 9, Merged: true}, nil
	}
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

func (s *canonicalStub) ResolveCanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerport.CanonicalCustomer, error) {
	s.batchCalls++
	result := make([]customerport.CanonicalCustomer, len(ids))
	for index, id := range ids {
		canonical := id
		if id == 2 {
			canonical = 9
		}
		result[index] = customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: canonical, Merged: id != canonical}
	}
	return result, nil
}

func TestCustomerAdaptersReadCanonicalCustomerPorts(t *testing.T) {
	reference := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	source := CustomerSource{UoW: passthroughUoW{}, Customers: audienceReaderStub{ids: []customerdomain.CustomerID{2, 3}}}
	result, err := source.Evaluate(context.Background(), segmentport.Definition{SchemaVersion: 1, Expression: []byte(`{"schema_version":1,"template":"active_contacts","predicate":{"field":"last_active_at","op":"within_days","values":["30"]}}`)}, reference)
	if err != nil || len(result.CustomerIDs) != 2 || !result.ReferenceAt.Equal(reference) || len(result.Watermarks) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	resolver := &canonicalStub{}
	canonical, err := (CanonicalCustomers{UoW: passthroughUoW{}, Resolver: resolver}).CanonicalCustomers(context.Background(), result.CustomerIDs)
	if err != nil || len(canonical) != 2 || canonical[0] != 9 || canonical[1] != 3 {
		t.Fatalf("canonical=%v err=%v", canonical, err)
	}
	if resolver.batchCalls != 1 {
		t.Fatalf("batch calls=%d", resolver.batchCalls)
	}
}
