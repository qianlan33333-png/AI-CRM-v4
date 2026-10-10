package main

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type paymentProfitSharingDescriptionAdapter struct {
	orders orderport.CheckoutSnapshotReader
	names  customerport.DirectoryDisplayNameReader
}

func (a paymentProfitSharingDescriptionAdapter) ReadProfitSharingDescriptionWithin(ctx context.Context, orderID, buyerID int64) (string, error) {
	if a.orders == nil || a.names == nil || orderID < 1 || buyerID < 1 {
		return "", paymentport.ErrUnavailable
	}
	checkout, err := a.orders.ReadCheckoutSnapshotWithin(ctx, orderID)
	if err != nil {
		return "", err
	}
	if checkout.OrderID != orderID || checkout.ProductName == "" {
		return "", paymentport.ErrConflict
	}
	names, err := a.names.DisplayNames(ctx, []customerdomain.CustomerID{customerdomain.CustomerID(buyerID)})
	if err != nil {
		return "", err
	}
	return paymentport.FormatProfitSharingPurchaseDescription(names[customerdomain.CustomerID(buyerID)], checkout.ProductName), nil
}
