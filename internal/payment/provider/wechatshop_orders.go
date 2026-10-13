package provider

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"math"
	"time"
)

func (p *ShopProvider) ListShopOrders(ctx context.Context, from, until time.Time, next string) (shopport.OrderPage, error) {
	if !until.After(from) || until.Sub(from) > 7*24*time.Hour {
		return shopport.OrderPage{}, ErrInvalidMaterial
	}
	var response struct {
		IDs     []exactShopReference `json:"order_id_list"`
		Next    string               `json:"next_key"`
		HasMore *bool                `json:"has_more"`
	}
	e := p.shopRead(ctx, "/channels/ec/order/list/get", map[string]any{"update_time_range": map[string]int64{"start_time": from.Unix(), "end_time": until.Unix()}, "page_size": 100, "next_key": next}, &response)
	if e != nil {
		return shopport.OrderPage{}, e
	}
	if response.IDs == nil || response.HasMore == nil {
		return shopport.OrderPage{}, ErrInvalidShopResponse
	}
	page := shopport.OrderPage{Next: response.Next, HasMore: *response.HasMore}
	for _, id := range response.IDs {
		page.IDs = append(page.IDs, id.value)
	}
	if page.HasMore && page.Next == "" {
		return shopport.OrderPage{}, ErrInvalidShopResponse
	}
	return page, nil
}
func (p *ShopProvider) ReadShopOrder(ctx context.Context, id string) (orderport.ShopOrder, error) {
	var response struct {
		Order struct {
			ID        exactShopReference `json:"order_id"`
			Status    exactShopInteger   `json:"status"`
			Create    exactShopInteger   `json:"create_time"`
			Update    exactShopInteger   `json:"update_time"`
			OpenID    string             `json:"openid"`
			UnionID   string             `json:"unionid"`
			Giver     string             `json:"present_giver_openid"`
			IsPresent bool               `json:"is_present"`
			Detail    struct {
				Price struct {
					Amount exactShopInteger `json:"order_price"`
				} `json:"price_info"`
				Pay struct {
					Method      exactShopInteger `json:"payment_method"`
					Time        exactShopInteger `json:"pay_time"`
					Transaction string           `json:"transaction_id"`
				} `json:"pay_info"`
				Products []struct {
					Product exactShopReference `json:"product_id"`
					SKU     exactShopReference `json:"sku_id"`
					Title   string             `json:"title"`
					Price   exactShopInteger   `json:"sale_price"`
					Actual  exactShopInteger   `json:"real_price"`
					Count   exactShopInteger   `json:"sku_cnt"`
				} `json:"product_infos"`
				Delivery struct {
					Address struct {
						VirtualPhone string `json:"virtual_order_tel_number"`
					} `json:"address_info"`
					Recharge struct {
						Account, Type string
						AccountNo     string `json:"account_no"`
						AccountType   string `json:"account_type"`
					} `json:"recharge_info"`
				} `json:"delivery_info"`
				Sources []struct {
					Nickname string `json:"account_nickname"`
				} `json:"source_infos"`
			} `json:"order_detail"`
			AfterSales struct {
				List []struct {
					ID exactShopReference `json:"aftersale_order_id"`
				} `json:"aftersale_order_list"`
			} `json:"aftersale_detail"`
		} `json:"order"`
	}
	if e := p.shopRead(ctx, shopOrderPath, map[string]string{"order_id": id}, &response); e != nil {
		return orderport.ShopOrder{}, e
	}
	o := response.Order
	if !o.ID.set || o.ID.value != id {
		return orderport.ShopOrder{}, ErrInvalidShopResponse
	}
	result := orderport.ShopOrder{AppID: p.config.AppID, ID: id}
	if !o.Detail.Pay.Time.set {
		if o.Status.set && (o.Status.value == 10 || o.Status.value == 250) {
			return result, nil // Unpaid or cancelled without a payment fact.
		}
		return result, ErrInvalidShopResponse
	}
	if o.Detail.Pay.Time.value == 0 {
		if o.Status.set && paidShopStatus(o.Status.value) {
			return result, ErrInvalidShopResponse
		}
		return result, nil
	}
	if !o.Detail.Pay.Method.set {
		return result, ErrInvalidShopResponse
	}
	if o.Detail.Pay.Method.value == 3 || o.Detail.Pay.Method.value == 4 || (o.Detail.Pay.Method.value == 2 && o.Detail.Pay.Transaction == "") {
		return result, nil
	}
	if o.Detail.Pay.Time.value < 0 || !o.Create.set || o.Create.value < 1 || !o.Detail.Price.Amount.set || o.Detail.Price.Amount.value < 0 {
		return result, ErrInvalidShopResponse
	}
	result.PaidAt = time.Unix(o.Detail.Pay.Time.value, 0).UTC()
	result.CreatedAt = time.Unix(o.Create.value, 0).UTC()
	result.UpdatedAt = result.PaidAt
	if o.Update.set {
		result.UpdatedAt = time.Unix(o.Update.value, 0).UTC()
	}
	result.TransactionID = o.Detail.Pay.Transaction
	result.PaidMinor = o.Detail.Price.Amount.value
	if o.OpenID != "" && o.Giver == "" && !o.IsPresent {
		fact, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:" + p.config.AppID, Value: o.OpenID, Source: "wechat-shop.order-api"})
		if e != nil {
			return result, ErrInvalidShopResponse
		}
		result.Buyer = fact
	}
	if o.UnionID != "" && p.config.UnionIDOpenPlatformID != "" && o.Giver == "" && !o.IsPresent {
		fact, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:" + p.config.UnionIDOpenPlatformID, Value: o.UnionID, Source: "wechat-shop.order-api"})
		if e != nil {
			return result, ErrInvalidShopResponse
		}
		result.BuyerUnion = fact
	}
	for _, v := range o.Detail.Products {
		if !v.Product.set || !v.SKU.set || !v.Price.set || !v.Actual.set || !v.Count.set || v.Count.value < 1 || v.Count.value > math.MaxInt32 {
			return result, ErrInvalidShopResponse
		}
		result.Lines = append(result.Lines, orderport.ShopOrderLine{ProductID: v.Product.value, SKUID: v.SKU.value, Name: v.Title, UnitMinor: v.Price.value, PaidMinor: v.Actual.value, Quantity: int32(v.Count.value)})
	}
	result.Account = o.Detail.Delivery.Recharge.AccountNo
	if o.Detail.Delivery.Recharge.AccountType == "phone_number" {
		result.Phone = result.Account
	}
	if result.Phone == "" {
		result.Phone = o.Detail.Delivery.Address.VirtualPhone
	}
	for _, source := range o.Detail.Sources {
		if source.Nickname != "" {
			result.Nickname = source.Nickname
			break
		}
	}
	seen := map[string]bool{}
	for _, after := range o.AfterSales.List {
		if !after.ID.set {
			return result, ErrInvalidShopResponse
		}
		if seen[after.ID.value] {
			continue
		}
		seen[after.ID.value] = true
		amount, e := p.shopRefundAmount(ctx, id, after.ID.value)
		if e != nil {
			return result, e
		}
		if result.RefundedMinor > math.MaxInt64-amount {
			return result, ErrInvalidShopResponse
		}
		result.RefundedMinor += amount
	}
	return result, nil
}

// Read all official aftersale types; only completed monetary refunds contribute.
func (p *ShopProvider) shopRefundAmount(ctx context.Context, orderID, afterID string) (int64, error) {
	var response struct {
		After struct {
			ID     exactShopReference `json:"after_sale_order_id"`
			Order  exactShopReference `json:"order_id"`
			Status string             `json:"status"`
			Refund struct {
				Amount exactShopInteger `json:"amount"`
			} `json:"refund_info"`
		} `json:"after_sale_order"`
	}
	if e := p.shopRead(ctx, shopRefundQueryPath, map[string]string{"after_sale_order_id": afterID}, &response); e != nil {
		return 0, e
	}
	v := response.After
	if !v.ID.set || v.ID.value != afterID || !v.Order.set || v.Order.value != orderID || v.Status == "" {
		return 0, ErrInvalidShopResponse
	}
	if v.Status != "MERCHANT_REFUND_SUCCESS" && v.Status != "MERCHANT_RETURN_SUCCESS" {
		return 0, nil
	}
	if !v.Refund.Amount.set || v.Refund.Amount.value < 0 {
		return 0, ErrInvalidShopResponse
	}
	return v.Refund.Amount.value, nil
}
