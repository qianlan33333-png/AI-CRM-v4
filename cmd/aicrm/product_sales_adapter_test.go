package main

import (
	"context"
	"reflect"
	"testing"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productSalesOrderStub struct {
	facts []orderport.ProductOrderFact
}

func (stub productSalesOrderStub) ReadPaidProductOrdersWithin(context.Context, []orderport.ProductSalesKey) ([]orderport.ProductOrderFact, error) {
	return append([]orderport.ProductOrderFact(nil), stub.facts...), nil
}

type productSalesRefundStub map[int64]struct{}

func (stub productSalesRefundStub) RefundRelatedOrderIDsWithin(context.Context, []int64) (map[int64]struct{}, error) {
	return stub, nil
}

func TestProductSalesAdapterDeduplicatesAndUsesCodeOnlyForLegacyRows(t *testing.T) {
	productOne, unknown := int64(1), int64(99)
	adapter := productSalesAdapter{
		orders: productSalesOrderStub{facts: []orderport.ProductOrderFact{
			{OrderID: 10, ProductID: &productOne, ProductCode: "old-code"},
			{OrderID: 10, ProductID: &productOne, ProductCode: "old-code"}, // duplicate line
			{OrderID: 11, ProductID: &productOne, ProductCode: "P-1", OrderRefunded: true},
			{OrderID: 12, ProductID: nil, ProductCode: "P-2"},      // legacy fallback
			{OrderID: 13, ProductID: &unknown, ProductCode: "P-2"}, // mismatched id must not fall back
		}},
		refunds: productSalesRefundStub{10: {}},
	}
	got, err := adapter.ReadSalesSummariesWithin(context.Background(), []productport.SalesKey{{ProductID: 1, ProductCode: "P-1"}, {ProductID: 2, ProductCode: "P-2"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[productport.ID]productport.SalesSummary{
		1: {PaidOrderCount: 2, RefundOrderCount: 2, SoldCount: 0},
		2: {PaidOrderCount: 1, RefundOrderCount: 0, SoldCount: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}
