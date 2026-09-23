package main

import (
	"context"
	"errors"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productSalesAdapter struct {
	orders  orderport.ProductSalesReader
	refunds paymentport.RefundExposureReader
}

func (adapter productSalesAdapter) ReadSalesSummariesWithin(ctx context.Context, keys []productport.SalesKey) (map[productport.ID]productport.SalesSummary, error) {
	if adapter.orders == nil || adapter.refunds == nil {
		return nil, errors.New("product sales dependencies are required")
	}
	orderKeys := make([]orderport.ProductSalesKey, 0, len(keys))
	byID := make(map[int64]productport.ID, len(keys))
	byCode := make(map[string]productport.ID, len(keys))
	result := make(map[productport.ID]productport.SalesSummary, len(keys))
	for _, key := range keys {
		if key.ProductID < 1 || key.ProductCode == "" {
			return nil, errors.New("invalid product sales key")
		}
		orderKeys = append(orderKeys, orderport.ProductSalesKey{ProductID: int64(key.ProductID), ProductCode: key.ProductCode})
		byID[int64(key.ProductID)] = key.ProductID
		byCode[key.ProductCode] = key.ProductID
		result[key.ProductID] = productport.SalesSummary{}
	}
	facts, err := adapter.orders.ReadPaidProductOrdersWithin(ctx, orderKeys)
	if err != nil {
		return nil, err
	}
	orderIDs := make([]int64, 0, len(facts))
	seenOrder := make(map[int64]struct{}, len(facts))
	for _, fact := range facts {
		if _, seen := seenOrder[fact.OrderID]; !seen {
			seenOrder[fact.OrderID] = struct{}{}
			orderIDs = append(orderIDs, fact.OrderID)
		}
	}
	refundOrders, err := adapter.refunds.RefundRelatedOrderIDsWithin(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	seenProductOrder := make(map[[2]int64]struct{}, len(facts))
	for _, fact := range facts {
		var productID productport.ID
		var ok bool
		if fact.ProductID != nil {
			productID, ok = byID[*fact.ProductID]
		} else {
			productID, ok = byCode[fact.ProductCode]
		}
		if !ok {
			continue
		}
		pair := [2]int64{int64(productID), fact.OrderID}
		if _, duplicate := seenProductOrder[pair]; duplicate {
			continue
		}
		seenProductOrder[pair] = struct{}{}
		summary := result[productID]
		summary.PaidOrderCount++
		_, paymentRefunded := refundOrders[fact.OrderID]
		if fact.OrderRefunded || paymentRefunded {
			summary.RefundOrderCount++
		}
		summary.SoldCount = summary.PaidOrderCount - summary.RefundOrderCount
		if summary.SoldCount < 0 {
			summary.SoldCount = 0
		}
		result[productID] = summary
	}
	return result, nil
}

var _ productport.SalesSummaryReader = productSalesAdapter{}
