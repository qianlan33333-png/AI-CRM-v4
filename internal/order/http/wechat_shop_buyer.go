package http

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (h *Handler) shopBuyerDisplays(ctx context.Context, orders []domain.Snapshot) (map[int64]orderport.ShopBuyerDisplay, error) {
	result := map[int64]orderport.ShopBuyerDisplay{}
	if h.shopBuyers == nil {
		return result, nil
	}
	ids := []int64{}
	for _, o := range orders {
		if o.Provider == domain.ProviderWeChatShop && o.RecordOrigin == domain.RecordOriginExternal {
			ids = append(ids, o.ID)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	return h.shopBuyers.ReadShopBuyerDisplays(ctx, ids)
}

func populateShopBuyer(buyer orderport.ShopBuyerDisplay, response *orderResponse) {
	if buyer.Nickname != "" {
		response.PayerName = buyer.Nickname
	}
	if buyer.PhoneMasked != "" {
		response.PayerPhoneMasked = buyer.PhoneMasked
		response.ShippingMobileMasked = buyer.PhoneMasked
		if response.ContactCollectionLevel == "" || response.ContactCollectionLevel == "none" {
			response.ContactCollectionLevel = "mobile"
		}
	}
}
