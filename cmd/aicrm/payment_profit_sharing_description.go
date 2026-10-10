package main

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type paymentProfitSharingDescriptionAdapter struct {
	orders    orderport.CheckoutSnapshotReader
	lineage   identityport.LockedCanonicalLineageReader
	nicknames customerport.WeChatNicknameReader
}

func (a paymentProfitSharingDescriptionAdapter) ReadProfitSharingDescriptionWithin(ctx context.Context, orderID, buyerID int64) (string, error) {
	if a.orders == nil || a.lineage == nil || a.nicknames == nil || orderID < 1 || buyerID < 1 {
		return "", paymentport.ErrUnavailable
	}
	checkout, err := a.orders.ReadCheckoutSnapshotWithin(ctx, orderID)
	if err != nil {
		return "", err
	}
	if checkout.OrderID != orderID || checkout.ProductName == "" {
		return "", paymentport.ErrConflict
	}
	lineage, err := a.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(buyerID))
	if err != nil {
		return "", err
	}
	if len(lineage) == 0 || lineage[0] < 1 {
		return "", paymentport.ErrConflict
	}
	nickname, err := a.nicknames.ReadWeChatNickname(ctx, lineage[0])
	if err != nil {
		return "", err
	}
	return paymentport.FormatProfitSharingPurchaseDescription(nickname, checkout.ProductName), nil
}
