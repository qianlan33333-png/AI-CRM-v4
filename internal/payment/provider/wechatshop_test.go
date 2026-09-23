package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestWeChatShopRefundUsesOfficialCreateAndExactQuery(t *testing.T) {
	now := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	payloadDigest := effectport.Hash("shop-payload")
	material := Material{Intent: paymentport.ProviderIntent{Kind: effectport.KindWeChatShopRefund, MerchantOrderNo: "merchant-1", RefundNo: "refund-1", ProviderOrderID: "shop-order-1", ProductID: "product-1", SKUID: "sku-1", RefundCount: 2, ReasonCode: "10000014", AmountMinor: 200, TotalMinor: 1000, Currency: "CNY", PayloadDigest: payloadDigest}}
	calls := 0
	client := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		switch request.URL.Path {
		case shopTokenPath:
			if !strings.Contains(string(body), `"appid":"app"`) || strings.Contains(request.URL.RawQuery, "secret") {
				t.Fatalf("unsafe token request url=%s body=%s", request.URL, body)
			}
			return shopResponse(`{"access_token":"token-value","expires_in":7200}`), nil
		case shopRefundPath:
			if request.URL.Query().Get("access_token") != "token-value" || !strings.Contains(string(body), `"request_id":"refund-1"`) || !strings.Contains(string(body), `"sku_id":"sku-1"`) {
				t.Fatalf("refund request url=%s body=%s", request.URL, body)
			}
			return shopResponse(`{"errcode":0,"after_sale_order_id":"after-1"}`), nil
		case shopOrderPath:
			return shopResponse(`{"errcode":0,"order":{"order_id":"shop-order-1","status":20,"order_detail":{"price_info":{"order_price":1000},"product_infos":[{"product_id":"product-1","sku_id":"sku-1","sku_cnt":3,"on_aftersale_sku_cnt":0,"finish_aftersale_sku_cnt":0,"real_price":100}]}}}`), nil
		case shopRefundQueryPath:
			return shopResponse(`{"errcode":0,"after_sale_order":{"after_sale_order_id":"after-1","status":"MERCHANT_REFUND_SUCCESS","order_id":"shop-order-1","type":"REFUND","update_time":1788408000,"product_info":{"product_id":"product-1","sku_id":"sku-1","count":2},"refund_info":{"amount":200}}}`), nil
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
			return nil, nil
		}
	})
	provider, err := NewWeChatShop(ShopConfig{Enabled: true, AppID: "app", AppSecret: "secret", APIBaseURL: "https://api.weixin.qq.com"}, loaderStub{material}, client)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	if err = provider.ValidateRefundMaterial(context.Background(), paymentport.ShopRefundMaterial{ProviderOrderID: "shop-order-1", ProductID: "product-1", SKUID: "sku-1", RefundCount: 2, AmountMinor: 200, Currency: "CNY", ReasonCode: "10000014"}); err != nil {
		t.Fatalf("validate material: %v", err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatShopRefund, payloadDigest), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.Artifact.Valid() || result.Artifact.Kind != "wechat_shop_refund_acceptance_v1" || !strings.Contains(string(result.Artifact.Payload), "after-1") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	query, err := provider.QueryRefund(context.Background(), "after-1")
	if err != nil || query.Status != "MERCHANT_REFUND_SUCCESS" || query.AmountMinor != 200 || query.Count != 2 || !effectport.ValidDigest(query.EvidenceDigest) || calls != 4 {
		t.Fatalf("query=%+v calls=%d err=%v", query, calls, err)
	}
}

func TestWeChatShopCallbackSeparatesURLAndEncryptedPOST(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encodedKey := base64.StdEncoding.EncodeToString(key)
	encodedKey = strings.TrimSuffix(encodedKey, "=")
	credential, err := NewShopCallbackCredential("shop-app", "callback-token", encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, _ := NewShopCallbackVerifier(credential)
	now := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return now }
	timestamp, nonce := strconv.FormatInt(now.Unix(), 10), "nonce"
	urlSignature := testShopSignature("callback-token", timestamp, nonce)
	echo, err := verifier.VerifyURL(context.Background(), map[string]string{"signature": urlSignature, "timestamp": timestamp, "nonce": nonce, "echostr": "echo-safe"})
	if err != nil || echo != "echo-safe" {
		t.Fatalf("echo=%q err=%v", echo, err)
	}
	plain := []byte(`{"CreateTime":1788408000,"MsgType":"event","Event":"channels_ec_aftersale_update","finder_shop_aftersale_status_update":{"status":"MERCHANT_REFUND_SUCCESS","after_sale_order_id":"after-1","order_id":"shop-order-1"}}`)
	encrypted := encryptShopCallbackTest(t, key, "shop-app", plain)
	body, _ := json.Marshal(map[string]string{"ToUserName": "shop-app", "Encrypt": encrypted})
	messageSignature := testShopSignature("callback-token", timestamp, nonce, encrypted)
	callback, err := verifier.VerifyRefund(context.Background(), body, map[string]string{"msg_signature": messageSignature, "timestamp": timestamp, "nonce": nonce})
	if err != nil || callback.AfterSaleID != "after-1" || callback.ProviderOrderID != "shop-order-1" || callback.EventDigest == ([32]byte{}) {
		t.Fatalf("callback=%+v err=%v", callback, err)
	}
}

func shopResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func testShopSignature(parts ...string) string {
	sort.Strings(parts)
	digest := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(digest[:])
}

func encryptShopCallbackTest(t *testing.T, key []byte, appID string, message []byte) string {
	t.Helper()
	plain := make([]byte, 20+len(message)+len(appID))
	copy(plain[:16], []byte("0123456789abcdef"))
	binary.BigEndian.PutUint32(plain[16:20], uint32(len(message)))
	copy(plain[20:], message)
	copy(plain[20+len(message):], appID)
	padding := 32 - len(plain)%32
	if padding == 0 {
		padding = 32
	}
	plain = append(plain, make([]byte, padding)...)
	for index := len(plain) - padding; index < len(plain); index++ {
		plain[index] = byte(padding)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plain)
	return base64.StdEncoding.EncodeToString(ciphertext)
}
