package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

const (
	shopTokenPath       = "/cgi-bin/stable_token"
	shopRefundPath      = "/channels/ec/aftersale/genaftersaleorder"
	shopRefundQueryPath = "/channels/ec/aftersale/getaftersaleorder"
	shopOrderPath       = "/channels/ec/order/get"
)

var ErrInvalidShopResponse = errors.New("invalid wechat shop response")

type ShopConfig struct {
	Enabled          bool
	AppID, AppSecret string
	APIBaseURL       string
}

func (ShopConfig) String() string   { return "WeChatShopConfig{credentials:[REDACTED]}" }
func (ShopConfig) GoString() string { return "WeChatShopConfig{credentials:[REDACTED]}" }

type ShopProvider struct {
	config ShopConfig
	base   *url.URL
	loader MaterialLoader
	client HTTPDoer
	now    func() time.Time
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func NewWeChatShop(config ShopConfig, loader MaterialLoader, client HTTPDoer) (*ShopProvider, error) {
	if !config.Enabled {
		return &ShopProvider{config: config}, nil
	}
	base, err := url.Parse(config.APIBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.Path != "" || config.AppID == "" || config.AppSecret == "" || strings.TrimSpace(config.AppID) != config.AppID || strings.TrimSpace(config.AppSecret) != config.AppSecret || loader == nil || client == nil {
		return nil, ErrInvalidConfig
	}
	return &ShopProvider{config: config, base: base, loader: loader, client: client, now: time.Now}, nil
}

func (provider *ShopProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || envelope.Owner != effectport.OwnerPayment || envelope.Kind != effectport.KindWeChatShopRefund {
		return final("wechatshop.unsupported", envelope, attempt), nil
	}
	if !provider.config.Enabled {
		return final("wechatshop.disabled", envelope, attempt), nil
	}
	material, err := provider.loader.Load(ctx, envelope.Kind, envelope.SourceRefDigest)
	if err != nil || material.Intent.PayloadDigest != envelope.PayloadDigest || !validShopIntent(material.Intent) {
		return final("wechatshop.material", envelope, attempt), nil
	}
	payload := map[string]any{
		"request_id": material.Intent.RefundNo, "order_id": material.Intent.ProviderOrderID,
		"product_id": material.Intent.ProductID, "sku_id": material.Intent.SKUID,
		"count": material.Intent.RefundCount, "amount": material.Intent.AmountMinor,
		"reason": material.Intent.ReasonCode, "type": "REFUND",
	}
	body, dispatched, err := provider.postWithToken(ctx, shopRefundPath, payload, false)
	if err != nil {
		state := effectport.StateRetryable
		if dispatched {
			state = effectport.StateUnknown
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: receipt("wechatshop.request.error", envelope, attempt), CallAttempted: dispatched, RealExternalCallExecuted: dispatched}, err
	}
	var response struct {
		ErrCode          exactShopInteger   `json:"errcode"`
		AfterSaleID      exactShopReference `json:"aftersale_id"`
		AfterSaleOrderID exactShopReference `json:"after_sale_order_id"`
	}
	if !decodeShopObject(body, &response) || !response.ErrCode.set {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatshop.response.invalid", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	if response.ErrCode.value != 0 {
		state := effectport.StateUnknown
		if finalShopRejection(response.ErrCode.value) {
			state = effectport.StateFinalFailed
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("wechatshop.rejected", string(envelope.Fingerprint()), strconv.FormatInt(response.ErrCode.value, 10), digestBody(body)), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	afterSaleID, ok := canonicalShopReference(response.AfterSaleID, response.AfterSaleOrderID)
	if !ok {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatshop.aftersale.invalid", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	artifactPayload, _ := json.Marshal(map[string]string{"afterSaleId": afterSaleID})
	artifact := effectport.ResultArtifact{Kind: "wechat_shop_refund_acceptance_v1", Payload: artifactPayload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("wechatshop.accepted", string(envelope.Fingerprint()), digestBody(body)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

func (provider *ShopProvider) QueryRefund(ctx context.Context, afterSaleID string) (paymentport.ShopRefundQuery, error) {
	if provider == nil || !provider.config.Enabled || !validShopReference(afterSaleID) {
		return paymentport.ShopRefundQuery{}, ErrInvalidMaterial
	}
	body, _, err := provider.postWithToken(ctx, shopRefundQueryPath, map[string]string{"after_sale_order_id": afterSaleID}, true)
	if err != nil {
		return paymentport.ShopRefundQuery{}, err
	}
	var response struct {
		ErrCode        exactShopInteger `json:"errcode"`
		AfterSaleOrder struct {
			AfterSaleOrderID exactShopReference `json:"after_sale_order_id"`
			Status           string             `json:"status"`
			OrderID          exactShopReference `json:"order_id"`
			Type             string             `json:"type"`
			UpdateTime       exactShopInteger   `json:"update_time"`
			ProductInfo      struct {
				ProductID exactShopReference `json:"product_id"`
				SKUID     exactShopReference `json:"sku_id"`
				Count     exactShopInteger   `json:"count"`
			} `json:"product_info"`
			RefundInfo struct {
				Amount exactShopInteger `json:"amount"`
			} `json:"refund_info"`
		} `json:"after_sale_order"`
	}
	if !decodeShopObject(body, &response) || !response.ErrCode.set || response.ErrCode.value != 0 {
		return paymentport.ShopRefundQuery{}, ErrInvalidShopResponse
	}
	row := response.AfterSaleOrder
	if !row.AfterSaleOrderID.set || row.AfterSaleOrderID.value != afterSaleID || !row.OrderID.set || !row.ProductInfo.ProductID.set || !row.ProductInfo.SKUID.set || !row.ProductInfo.Count.set || row.ProductInfo.Count.value < 1 || row.ProductInfo.Count.value > 1_000_000 || !row.RefundInfo.Amount.set || row.RefundInfo.Amount.value < 1 || row.RefundInfo.Amount.value > 1_000_000_000 || !row.UpdateTime.set || row.UpdateTime.value < 1 || row.Type != "REFUND" || !validShopStatus(row.Status) {
		return paymentport.ShopRefundQuery{}, ErrInvalidShopResponse
	}
	return paymentport.ShopRefundQuery{
		AfterSaleID: afterSaleID, ProviderOrderID: row.OrderID.value, ProductID: row.ProductInfo.ProductID.value,
		SKUID: row.ProductInfo.SKUID.value, Count: row.ProductInfo.Count.value, AmountMinor: row.RefundInfo.Amount.value,
		Currency: "CNY", Status: row.Status, OccurredAt: time.Unix(row.UpdateTime.value, 0).UTC(),
		EvidenceDigest:       effectport.Hash("wechat-shop/aftersale-query-response/v1", afterSaleID, digestBody(body)),
		ProviderRefundDigest: effectport.Hash("wechat-shop/aftersale-id/v1", afterSaleID),
	}, nil
}

func (provider *ShopProvider) ValidateRefundMaterial(ctx context.Context, requested paymentport.ShopRefundMaterial) error {
	if provider == nil || !provider.config.Enabled || !validShopReference(requested.ProviderOrderID) || !validShopReference(requested.ProductID) || !validShopReference(requested.SKUID) || requested.RefundCount < 1 || requested.AmountMinor < 1 || requested.Currency != "CNY" || !validShopReason(requested.ReasonCode) {
		return ErrInvalidMaterial
	}
	body, _, err := provider.postWithToken(ctx, shopOrderPath, map[string]string{"order_id": requested.ProviderOrderID}, true)
	if err != nil {
		return err
	}
	var response struct {
		ErrCode exactShopInteger `json:"errcode"`
		Order   struct {
			OrderID exactShopReference `json:"order_id"`
			Status  exactShopInteger   `json:"status"`
			Detail  struct {
				PriceInfo struct {
					OrderPrice exactShopInteger `json:"order_price"`
				} `json:"price_info"`
				Products []struct {
					ProductID              exactShopReference `json:"product_id"`
					SKUID                  exactShopReference `json:"sku_id"`
					Count                  exactShopInteger   `json:"sku_cnt"`
					OnAfterSaleCount       exactShopInteger   `json:"on_aftersale_sku_cnt"`
					FinishedAfterSaleCount exactShopInteger   `json:"finish_aftersale_sku_cnt"`
					RealPrice              exactShopInteger   `json:"real_price"`
				} `json:"product_infos"`
			} `json:"order_detail"`
		} `json:"order"`
	}
	if !decodeShopObject(body, &response) || response.ErrCode.set && response.ErrCode.value != 0 || !response.Order.OrderID.set || response.Order.OrderID.value != requested.ProviderOrderID || !response.Order.Status.set || !paidShopStatus(response.Order.Status.value) || !response.Order.Detail.PriceInfo.OrderPrice.set || requested.AmountMinor > response.Order.Detail.PriceInfo.OrderPrice.value {
		return ErrInvalidShopResponse
	}
	for _, line := range response.Order.Detail.Products {
		if !line.ProductID.set || !line.SKUID.set || line.ProductID.value != requested.ProductID || line.SKUID.value != requested.SKUID {
			continue
		}
		if !line.Count.set || !line.OnAfterSaleCount.set || !line.FinishedAfterSaleCount.set || !line.RealPrice.set || line.Count.value < 1 || line.Count.value > 1_000_000 || line.OnAfterSaleCount.value < 0 || line.FinishedAfterSaleCount.value < 0 || line.OnAfterSaleCount.value+line.FinishedAfterSaleCount.value > line.Count.value || requested.RefundCount > line.Count.value-line.OnAfterSaleCount.value-line.FinishedAfterSaleCount.value || line.RealPrice.value < 1 || line.RealPrice.value > 1_000_000_000 || requested.AmountMinor > line.RealPrice.value*requested.RefundCount {
			return ErrInvalidShopResponse
		}
		return nil
	}
	return ErrInvalidShopResponse
}

func (provider *ShopProvider) postWithToken(ctx context.Context, path string, payload any, queryOnly bool) ([]byte, bool, error) {
	token, err := provider.stableToken(ctx, false)
	if err != nil {
		return nil, false, err
	}
	body, code, dispatched, err := provider.post(ctx, path, url.Values{"access_token": []string{token}}, payload)
	if err == nil && invalidShopToken(code) {
		token, err = provider.stableToken(ctx, true)
		if err != nil {
			return nil, dispatched, err
		}
		body, _, dispatched, err = provider.post(ctx, path, url.Values{"access_token": []string{token}}, payload)
	}
	_ = queryOnly // Query calls remain Provider reads; the flag documents call intent.
	return body, dispatched, err
}

func (provider *ShopProvider) stableToken(ctx context.Context, force bool) (string, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !force && provider.token != "" && provider.expiry.After(provider.now().UTC().Add(time.Minute)) {
		return provider.token, nil
	}
	body, _, _, err := provider.post(ctx, shopTokenPath, nil, map[string]string{"grant_type": "client_credential", "appid": provider.config.AppID, "secret": provider.config.AppSecret})
	if err != nil {
		return "", err
	}
	var response struct {
		AccessToken string           `json:"access_token"`
		ExpiresIn   exactShopInteger `json:"expires_in"`
		ErrCode     exactShopInteger `json:"errcode"`
	}
	if !decodeShopObject(body, &response) || response.AccessToken == "" || strings.TrimSpace(response.AccessToken) != response.AccessToken || len(response.AccessToken) > 4096 || !response.ExpiresIn.set || response.ExpiresIn.value < 120 || response.ErrCode.set && response.ErrCode.value != 0 {
		return "", ErrInvalidShopResponse
	}
	provider.token, provider.expiry = response.AccessToken, provider.now().UTC().Add(time.Duration(response.ExpiresIn.value)*time.Second)
	return provider.token, nil
}

func (provider *ShopProvider) post(ctx context.Context, path string, query url.Values, payload any) ([]byte, int64, bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, false, err
	}
	endpoint := *provider.base
	endpoint.Path = path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, 0, false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, 0, true, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, true, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, 0, true, ErrInvalidShopResponse
	}
	var envelope struct {
		ErrCode exactShopInteger `json:"errcode"`
	}
	if !decodeShopObject(body, &envelope) {
		return nil, 0, true, ErrInvalidShopResponse
	}
	return body, envelope.ErrCode.value, true, nil
}

type exactShopInteger struct {
	value int64
	set   bool
}

func (number *exactShopInteger) UnmarshalJSON(raw []byte) error {
	if number == nil || len(raw) == 0 || raw[0] == '"' || bytes.ContainsAny(raw, ".eE") {
		return ErrInvalidShopResponse
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return ErrInvalidShopResponse
	}
	number.value, number.set = value, true
	return nil
}

type exactShopReference struct {
	value string
	set   bool
}

func (reference *exactShopReference) UnmarshalJSON(raw []byte) error {
	if reference == nil {
		return ErrInvalidShopResponse
	}
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalidShopResponse
		}
	} else if len(raw) > 0 && !bytes.ContainsAny(raw, ".eE") {
		value = string(raw)
	} else {
		return ErrInvalidShopResponse
	}
	if !validShopReference(value) {
		return ErrInvalidShopResponse
	}
	reference.value, reference.set = value, true
	return nil
}

func decodeShopObject(raw []byte, destination any) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if decoder.Decode(destination) != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}

func validShopIntent(intent paymentport.ProviderIntent) bool {
	return validShopReference(intent.ProviderOrderID) && validShopReference(intent.ProductID) && validShopReference(intent.SKUID) && validShopReference(intent.RefundNo) && intent.RefundCount >= 1 && intent.RefundCount <= 1_000_000 && intent.AmountMinor >= 1 && intent.AmountMinor <= intent.TotalMinor && intent.Currency == "CNY" && validShopReason(intent.ReasonCode)
}

func validShopReference(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validShopReason(value string) bool {
	switch value {
	case "10000000", "10000001", "10000002", "10000006", "10000007", "10000008", "10000014", "10000015", "10000017", "10000021":
		return true
	default:
		return false
	}
}

func validShopStatus(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'A' || character > 'Z') {
			return false
		}
	}
	return true
}

func finalShopRejection(code int64) bool {
	return code == 10021083 || code == 10021084 || code == 10021086 || code == 10021088
}

func invalidShopToken(code int64) bool { return code == 40014 || code == 42001 || code == 40001 }

func paidShopStatus(status int64) bool {
	return status == 20 || status == 21 || status == 30 || status == 100
}

func canonicalShopReference(left, right exactShopReference) (string, bool) {
	if left.set && right.set && left.value != right.value {
		return "", false
	}
	if left.set {
		return left.value, true
	}
	if right.set {
		return right.value, true
	}
	return "", false
}

func digestBody(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest[:])
}

var _ effectport.ProviderAdapter = (*ShopProvider)(nil)
var _ paymentport.ShopRefundReconciler = (*ShopProvider)(nil)
