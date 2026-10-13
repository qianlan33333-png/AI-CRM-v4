package provider

import (
	"context"
	"fmt"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"net/http"
	"strings"
	"testing"
)

func TestWeChatShopBuyerMetadataContract(t *testing.T) {
	for _, tc := range []struct {
		name, platform, gift, recharge, phone string
		verified                              bool
	}{
		{"virtual delivery", "shared-platform", "", "", "13800138000", true},
		{"recharge priority", "shared-platform", "", `,"recharge_info":{"account_type":"phone_number","account_no":"13900139000"}`, "13900139000", true},
		{"missing platform", "", "", "", "13800138000", false},
		{"gift recipient is not payer", "shared-platform", `,"is_present":true`, "", "13800138000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := doerFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == shopTokenPath {
					return shopResponse(`{"access_token":"fixture","expires_in":7200}`), nil
				}
				return shopResponse(`{"errcode":0,"order":{"order_id":3739797044778671872,"openid":"shop-openid-fixture","unionid":"union-fixture"` + tc.gift + `,"create_time":1700000000,"order_detail":{"price_info":{"order_price":29900},"pay_info":{"payment_method":1,"pay_time":1700000001,"transaction_id":"fixture-tx"},"product_infos":[{"product_id":"p","sku_id":"s","title":"课程","sale_price":29900,"real_price":29900,"sku_cnt":1}],"delivery_info":{"address_info":{"virtual_order_tel_number":"13800138000"}` + tc.recharge + `},"source_infos":[{"account_nickname":"指定昵称"}]}}}`), nil
			})
			p, err := NewWeChatShop(ShopConfig{Enabled: true, AppID: "shop-app", AppSecret: "fixture", UnionIDOpenPlatformID: tc.platform, APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, client)
			if err != nil {
				t.Fatal(err)
			}
			order, err := p.ReadShopOrder(context.Background(), "3739797044778671872")
			if err != nil || order.Phone != tc.phone || order.Nickname != "指定昵称" || order.BuyerUnion.Valid() != tc.verified {
				t.Fatal("buyer metadata contract", err)
			}
			if tc.verified && (order.BuyerUnion.Reference().Kind != identitydomain.KindUnionID || order.BuyerUnion.Reference().Scope != "wechat-open-platform:shared-platform") {
				t.Fatal("unscoped buyer identity")
			}
			if tc.gift != "" && order.Buyer.Valid() {
				t.Fatal("gift recipient used as payer")
			}
			if output := fmt.Sprintf("%+v", order); strings.Contains(output, tc.phone) || strings.Contains(output, "union-fixture") || strings.Contains(output, "指定昵称") {
				t.Fatal("buyer data exposed through formatting")
			}
		})
	}
}
