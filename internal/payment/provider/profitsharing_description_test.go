package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
)

func TestOfficialProfitSharingPurchaseDescriptionReachesRequest(t *testing.T) {
	merchant, platform := newProfitSharingAuthKey(t), newProfitSharingAuthKey(t)
	for _, row := range []struct {
		name, reason, want string
		invalid            bool
	}{
		{"purchase", "崔晓玲购买裂变商品分账", "崔晓玲购买裂变商品分账", false},
		{"legacy", "", paymentport.LegacyProfitSharingDescription, false},
		{"oversized", strings.Repeat("字", 40), "", true},
		{"invalid utf8", string([]byte{0xff}), "", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			sdk, err := NewOfficialProfitSharingSDKWithPublicKey(context.Background(), "merchant", "merchant-serial", "PUB_KEY_ID_test", merchant, &platform.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client, err := core.NewClient(context.Background(), option.WithWechatPayPublicKeyAuthCipher("merchant", "merchant-serial", merchant, "PUB_KEY_ID_test", &platform.PublicKey), option.WithHTTPClient(&http.Client{Transport: absenceTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body struct {
					TransactionID string `json:"transaction_id"`
					OutOrderNo    string `json:"out_order_no"`
					Receivers     []struct {
						Description, Account string
						Amount               int64
					} `json:"receivers"`
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v3/profitsharing/orders" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Receivers) != 1 || body.Receivers[0].Description != row.want || body.Receivers[0].Account != "trusted-openid" || body.Receivers[0].Amount != 204 || body.TransactionID != "trusted-transaction" || body.OutOrderNo != "v3ps_ABCDEFGHIJKLMNOPQRST" {
					t.Fatal("official SDK request changed buyer wording or money target")
				}
				response := `{"transaction_id":"trusted-transaction","out_order_no":"v3ps_ABCDEFGHIJKLMNOPQRST","state":"PROCESSING"}`
				ts, nonce := fmt.Sprint(time.Now().Unix()), "description-fixture"
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{"Wechatpay-Timestamp": {ts}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {"PUB_KEY_ID_test"}, "Wechatpay-Signature": {signTest(t, platform, ts+"\n"+nonce+"\n"+response+"\n")}}}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			sdk.orders.Client = client
			err = sdk.CreateOrder(context.Background(), ProfitSharingMaterial{PayloadDigest: effectport.Hash("description-fixture"), AppID: "wx-test", ProviderOrderNo: "v3ps_ABCDEFGHIJKLMNOPQRST", TransactionReference: "trusted-transaction", ReceiverAccount: "trusted-openid", AmountMinor: 204, Reason: row.reason})
			if row.invalid {
				if err == nil || calls != 0 {
					t.Fatal("invalid text reached provider")
				}
			} else if err != nil || calls != 1 {
				t.Fatalf("CreateOrder error=%v calls=%d", err, calls)
			}
		})
	}
}
