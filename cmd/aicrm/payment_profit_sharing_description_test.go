package main

import (
	"context"
	"errors"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type purchaseDescriptionOrders struct {
	value orderport.CheckoutSnapshot
	err   error
	got   int64
}

func (s *purchaseDescriptionOrders) ReadCheckoutSnapshotWithin(_ context.Context, id int64) (orderport.CheckoutSnapshot, error) {
	s.got = id
	return s.value, s.err
}

type purchaseDescriptionNames struct {
	got []customerdomain.CustomerID
	err error
}

func (s *purchaseDescriptionNames) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	s.got = ids
	return map[customerdomain.CustomerID]string{11: "购买人", 19: "推荐人"}, s.err
}
func TestProfitSharingDescriptionUsesOriginalBuyerAndFrozenProduct(t *testing.T) {
	orders := &purchaseDescriptionOrders{value: orderport.CheckoutSnapshot{OrderID: 7, ProductName: "原下单商品"}}
	names := &purchaseDescriptionNames{}
	a := paymentProfitSharingDescriptionAdapter{orders: orders, names: names}
	got, err := a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11)
	if err != nil || got != "购买人购买原下单商品分账" || orders.got != 7 || len(names.got) != 1 || names.got[0] != 11 {
		t.Fatal("description did not use original Payment buyer and Order snapshot")
	}
	orders.value.OrderID = 8
	if _, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11); err == nil {
		t.Fatal("wrong Order accepted")
	}
	orders.value.OrderID = 7
	names.err = errors.New("directory unavailable")
	if _, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11); err == nil {
		t.Fatal("unavailable presentation reader silently accepted")
	}
	names.err = nil
	got, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 12)
	if err != nil || got != "微信用户购买原下单商品分账" {
		t.Fatal("missing nickname did not use neutral fallback")
	}
}
