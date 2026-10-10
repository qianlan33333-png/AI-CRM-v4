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

type purchaseDescriptionNicknames struct {
	got   customerdomain.CustomerID
	value string
	err   error
}

func (s *purchaseDescriptionNicknames) ReadWeChatNickname(_ context.Context, id customerdomain.CustomerID) (string, error) {
	s.got = id
	return s.value, s.err
}

type purchaseDescriptionLineage struct {
	requested customerdomain.CustomerID
	values    []customerdomain.CustomerID
	err       error
}

func (s *purchaseDescriptionLineage) LockedCanonicalLineage(_ context.Context, id customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	s.requested = id
	return s.values, s.err
}

func TestProfitSharingDescriptionUsesOriginalBuyerAndFrozenProduct(t *testing.T) {
	orders := &purchaseDescriptionOrders{value: orderport.CheckoutSnapshot{OrderID: 7, ProductName: "原下单商品"}}
	nicknames := &purchaseDescriptionNicknames{value: "购买人"}
	lineage := &purchaseDescriptionLineage{values: []customerdomain.CustomerID{19, 11}}
	a := paymentProfitSharingDescriptionAdapter{orders: orders, lineage: lineage, nicknames: nicknames}
	got, err := a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11)
	if err != nil || got != "购买人购买原下单商品分账" || orders.got != 7 || lineage.requested != 11 || nicknames.got != 19 {
		t.Fatal("description did not use Payment buyer's current OneID root and frozen Order snapshot")
	}
	orders.value.OrderID = 8
	if _, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11); err == nil {
		t.Fatal("wrong Order accepted")
	}
	orders.value.OrderID = 7
	lineage.err = errors.New("identity unavailable")
	if _, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11); err == nil {
		t.Fatal("unavailable identity reader silently accepted")
	}
	lineage.err = nil
	nicknames.err = errors.New("customer reader unavailable")
	if _, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 11); err == nil {
		t.Fatal("unavailable Customer reader silently accepted")
	}
	nicknames.err = nil
	nicknames.value = ""
	got, err = a.ReadProfitSharingDescriptionWithin(context.Background(), 7, 12)
	if err != nil || got != "微信用户购买原下单商品分账" {
		t.Fatal("missing nickname did not use neutral fallback")
	}
}
