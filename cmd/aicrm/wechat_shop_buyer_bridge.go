package main

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// Mutable composition pointer is wired before any runtime starts. Every copy
// of OneID retains this same bridge, including WeCom and OAuth adapters.
type shopBuyerIdentityBridge struct {
	Orders orderport.ShopBuyerAssociationObserver
}

func (b *shopBuyerIdentityBridge) ObserveVerifiedIdentityWithin(ctx context.Context, id customerdomain.CustomerID, fact identitydomain.VerifiedFact) error {
	if b == nil || b.Orders == nil {
		return nil
	}
	return b.Orders.ObserveShopBuyerIdentityWithin(ctx, int64(id), fact)
}
