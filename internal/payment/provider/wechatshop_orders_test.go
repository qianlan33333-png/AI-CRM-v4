package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWeChatShopOrderContractUpdateWindowAndRefund(t *testing.T) {
	const id = "90071992547409931234"
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case shopTokenPath:
			return shopResponse(`{"access_token":"fixture","expires_in":7200}`), nil
		case "/channels/ec/order/list/get":
			if !strings.Contains(string(raw), `"update_time_range"`) || strings.Contains(string(raw), `create_time_range`) {
				t.Fatal("wrong order window")
			}
			return shopResponse(`{"errcode":0,"order_id_list":[90071992547409931234],"has_more":false}`), nil
		case shopOrderPath:
			return shopResponse(`{"errcode":0,"order":{"order_id":90071992547409931234,"create_time":1700000000,"update_time":1700010000,"order_detail":{"price_info":{"order_price":85},"pay_info":{"payment_method":1,"pay_time":1700009999,"transaction_id":"wx-tx"},"product_infos":[{"product_id":90071992547409939876,"sku_id":90071992547409939877,"title":"课程","sale_price":100,"real_price":80,"sku_cnt":2}],"delivery_info":{"recharge_info":{"account_type":"phone_number","account_no":"13800138000"}}},"aftersale_detail":{"aftersale_order_list":[{"aftersale_order_id":"refund-1"},{"aftersale_order_id":"refund-1"},{"aftersale_order_id":"return-2"}]}}}`), nil
		case shopRefundQueryPath:
			var request map[string]string
			_ = json.Unmarshal(raw, &request)
			aid := request["after_sale_order_id"]
			if aid == "refund-1" {
				return shopResponse(`{"errcode":0,"after_sale_order":{"after_sale_order_id":"refund-1","order_id":90071992547409931234,"status":"MERCHANT_REFUND_SUCCESS","refund_info":{"amount":40}}}`), nil
			}
			if aid == "return-2" {
				return shopResponse(`{"errcode":0,"after_sale_order":{"after_sale_order_id":"return-2","order_id":90071992547409931234,"status":"MERCHANT_RETURN_SUCCESS","refund_info":{"amount":45}}}`), nil
			}
		}
		t.Fatal("unexpected API")
		return nil, nil
	})
	p, _ := NewWeChatShop(ShopConfig{Enabled: true, AppID: "shop-app", AppSecret: "fixture", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, client)
	from := time.Unix(1700000000, 0)
	page, e := p.ListShopOrders(context.Background(), from, from.Add(24*time.Hour), "")
	if e != nil || len(page.IDs) != 1 || page.IDs[0] != id {
		t.Fatal("exact list ID", e)
	}
	v, e := p.ReadShopOrder(context.Background(), id)
	if e != nil || v.ID != id || v.Lines[0].ProductID != "90071992547409939876" || v.PaidMinor != 85 || v.Lines[0].PaidMinor != 80 || v.Lines[0].Quantity != 2 || v.RefundedMinor != 85 || v.Phone != "13800138000" || v.Account != v.Phone {
		t.Fatal("official details not preserved", e)
	}
	if _, e = p.ListShopOrders(context.Background(), from, from.Add(8*24*time.Hour), ""); e == nil {
		t.Fatal("oversized window accepted")
	}
}
func TestWeChatShopPaidCallbackSignatureScopeAndExactID(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cred, _ := NewShopCallbackCredential("shop-app", "fixture", strings.TrimSuffix(base64.StdEncoding.EncodeToString(key), "="))
	v, _ := NewShopCallbackVerifier(cred)
	now := time.Now().Truncate(time.Second)
	v.now = func() time.Time { return now }
	ts := strconv.FormatInt(now.Unix(), 10)
	encode := func(app, plain string) ([]byte, map[string]string) {
		c := encryptShopCallbackTest(t, key, app, []byte(plain))
		body, _ := json.Marshal(map[string]string{"ToUserName": "gh_shop", "Encrypt": c})
		return body, map[string]string{"timestamp": ts, "nonce": "fixture", "msg_signature": testShopSignature("fixture", ts, "fixture", c)}
	}
	plain := `{"MsgType":"event","Event":"channels_ec_order_pay","order_info":{"order_id":90071992547409931234}}`
	body, q := encode("shop-app", plain)
	event, e := v.VerifyEvent(context.Background(), body, q)
	if e != nil || !event.Relevant || event.OrderID != "90071992547409931234" {
		t.Fatal("paid hint rejected", e)
	}
	q["msg_signature"] = strings.Repeat("0", 40)
	if _, e = v.VerifyEvent(context.Background(), body, q); e == nil {
		t.Fatal("bad signature accepted")
	}
	body, q = encode("wrong-app", plain)
	if _, e = v.VerifyEvent(context.Background(), body, q); e == nil {
		t.Fatal("cross app encrypted message accepted")
	}
	body, q = encode("shop-app", `{"MsgType":"event","Event":"unknown"}`)
	event, e = v.VerifyEvent(context.Background(), body, q)
	if e != nil || event.Relevant {
		t.Fatal("unknown event created order")
	}
}
func TestWeChatShopDeferredOrNonPaymentNeverActivates(t *testing.T) {
	for _, method := range []int{2, 3, 4} {
		t.Run(strconv.Itoa(method), func(t *testing.T) {
			client := doerFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == shopTokenPath {
					return shopResponse(`{"access_token":"fixture","expires_in":7200}`), nil
				}
				return shopResponse(`{"errcode":0,"order":{"order_id":"x","order_detail":{"pay_info":{"payment_method":` + strconv.Itoa(method) + `,"pay_time":1700000000}}}}`), nil
			})
			p, _ := NewWeChatShop(ShopConfig{Enabled: true, AppID: "shop", AppSecret: "fixture", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{}, client)
			v, e := p.ReadShopOrder(context.Background(), "x")
			if e != nil || !v.PaidAt.IsZero() {
				t.Fatal("unpaid treated as captured", e)
			}
		})
	}
}
