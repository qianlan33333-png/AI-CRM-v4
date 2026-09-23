package http

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	addresscatalog "github.com/qianlan33333-png/AI-CRM-v3/internal/address/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PublicHandler exposes only enabled Product facts. Draft, disabled and missing
// products intentionally share the same 404 response.
type PublicHandler struct {
	catalog          PublicCatalogApplication
	media            publicProductMediaReader
	presentation     PublicPresentationAssets
	wechatPayEnabled bool
	alipayEnabled    bool
}

type publicProductMediaReader interface {
	mediaport.ImageVariantReader
	LocalImageExists(context.Context, int64) (bool, error)
}

// PublicCatalogApplication is deliberately narrower than the admin catalog:
// public routes resolve a stable product code and cannot enumerate or mutate
// the catalog. Get is only used for pre-existing numeric public-link aliases.
type PublicCatalogApplication interface {
	Get(context.Context, productport.ID) (productport.Product, error)
	GetByCode(context.Context, string) (productport.Product, error)
}

type publicProduct struct {
	ID                        productport.ID `json:"id"`
	ProductCode               string         `json:"-"`
	PromotionContext          string         `json:"-"`
	Name                      string         `json:"name"`
	Description               string         `json:"description"`
	PriceMinor                int64          `json:"price_minor"`
	Currency                  string         `json:"currency"`
	Images                    []string       `json:"images"`
	HeroURL                   string         `json:"-"`
	PaymentPath               string         `json:"-"`
	BuyButtonText             string         `json:"buy_button_text"`
	ProductKind               string         `json:"-"`
	ServicePeriodDurationDays int32          `json:"service_period_duration_days,omitempty"`
	CouponTargetRef           string         `json:"-"`
	RequireMobile             bool           `json:"require_mobile"`
	ContactCollectionLevel    string         `json:"contact_collection_level"`
	// RegionOptionsJSON is canonical, server-owned JSON embedded in the
	// checkout script. template.JS prevents html/template from turning the
	// array into a quoted string (which would render blank cascade options).
	RegionOptionsJSON template.JS `json:"-"`
}

func NewPublicHandler(catalog PublicCatalogApplication) (*PublicHandler, error) {
	if catalog == nil {
		return nil, errors.New("public product catalog is required")
	}
	return &PublicHandler{catalog: catalog, wechatPayEnabled: true}, nil
}

func (h *PublicHandler) SetPaymentMethods(wechat, alipay bool) {
	h.wechatPayEnabled, h.alipayEnabled = wechat, alipay
}

// SetPublicMediaReader supplies the existing Media read Port. The public
// product route independently checks that an enabled Product contains the
// exact image-library binding before it reads any bytes.
func (h *PublicHandler) SetPublicMediaReader(media publicProductMediaReader) error {
	if h == nil || media == nil {
		return errors.New("public product media reader is required")
	}
	h.media = media
	return nil
}

// SetPublicPresentationAssets injects either an already-verified closure or a
// deferred explicit release closure. Product's public routes remain
// unavailable when a bound release cannot verify that exact manifest closure.
func (h *PublicHandler) SetPublicPresentationAssets(assets PublicPresentationAssets) error {
	if h == nil || !assets.bound() {
		return errors.New("public product presentation assets are required")
	}
	h.presentation = assets
	return nil
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.catalog == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, publicCommerceAssetPrefix):
		if err := h.presentation.serveHTTP(w, r); err != nil {
			http.Error(w, "public presentation unavailable", http.StatusServiceUnavailable)
		}
	case strings.HasPrefix(r.URL.Path, "/api/h5/product-images/"):
		h.detailMedia(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/public/products/"):
		h.publicAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/p/"):
		h.publicPage(w, r, false)
	case strings.HasPrefix(r.URL.Path, "/pay/"):
		h.publicPage(w, r, true)
	default:
		http.NotFound(w, r)
	}
}

func (h *PublicHandler) publicAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	code, ok := publicProductCode(r, "/api/public/products/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProduct(r, code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, product)
}

func (h *PublicHandler) publicPage(w http.ResponseWriter, r *http.Request, payment bool) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	prefix := "/p/"
	if payment {
		prefix = "/pay/"
	}
	code, ok := publicProductCode(r, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProduct(r, code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	promotionContext, accepted := publicPromotionContext(r)
	if !accepted {
		clearLegacyPromotionCookies(w)
		http.NotFound(w, r)
		return
	}
	if promotionContext == "" {
		clearLegacyPromotionCookies(w)
	}
	presentation := PublicPresentationAssets{}
	if h.presentation.bound() {
		var err error
		presentation, err = h.presentation.resolved()
		if err != nil {
			http.Error(w, "public presentation unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	product.PromotionContext = promotionContext
	product.PaymentPath = publicPaymentPath(product.PaymentPath, promotionContext)
	if !payment && len(product.Images) == 0 {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, product.PaymentPath, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", publicCommerceContentSecurityPolicy())
	data := publicProductPageView{Product: product, Payment: true, Detail: !payment && len(product.Images) > 0, Presentation: publicPresentationTemplateFor(presentation), WeChatPayEnabled: h.wechatPayEnabled, AlipayEnabled: h.alipayEnabled}
	if err := publicProductPage.Execute(w, data); err != nil {
		return
	}
}

func (h *PublicHandler) enabledProduct(r *http.Request, code string) (publicProduct, bool) {
	value, ok := h.enabledProductValue(r, code)
	if !ok {
		return publicProduct{}, false
	}
	var projection struct {
		BuyButtonText          string `json:"buy_button_text"`
		RequireMobile          bool   `json:"require_mobile"`
		ContactCollectionLevel string `json:"contact_collection_level"`
	}
	if json.Unmarshal(value.LegacyAdminProjection, &projection) != nil {
		return publicProduct{}, false
	}
	if strings.TrimSpace(projection.BuyButtonText) == "" {
		projection.BuyButtonText = "立即购买"
	}
	level := projection.ContactCollectionLevel
	if level == "" {
		if projection.RequireMobile {
			level = "mobile"
		} else {
			level = "none"
		}
	}
	images, imageErr := productapp.PublicProductImageURLs(value)
	if imageErr != nil {
		return publicProduct{}, false
	}
	heroURL := ""
	if len(images) > 0 {
		heroURL = images[0]
	}
	return publicProduct{ID: value.ID, ProductCode: value.ProductCode, Name: value.Name, Description: value.Description, PriceMinor: value.PriceMinor, Currency: value.Currency, Images: images, HeroURL: heroURL, PaymentPath: "/pay/" + url.PathEscape(value.ProductCode), BuyButtonText: projection.BuyButtonText, ProductKind: "standard", CouponTargetRef: "standard_product:" + strconv.FormatInt(int64(value.ID), 10), RequireMobile: level != "none", ContactCollectionLevel: level, RegionOptionsJSON: template.JS(addresscatalog.OptionsJSON())}, true
}

func (h *PublicHandler) enabledProductValue(r *http.Request, code string) (productport.Product, bool) {
	value, err := h.catalog.GetByCode(r.Context(), code)
	if err != nil {
		legacyID, isLegacyID := legacyPublicProductID(code)
		if !isLegacyID || !errors.Is(err, productapp.ErrNotFound) {
			return productport.Product{}, false
		}
		value, err = h.catalog.Get(r.Context(), legacyID)
		if err != nil {
			return productport.Product{}, false
		}
	}
	local, err := productapp.ProjectLocalProduct(value)
	if err != nil || local.Lifecycle != productport.LocalProductEnabled || !local.Enabled {
		return productport.Product{}, false
	}
	return value, true
}

func (h *PublicHandler) detailMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || h.media == nil {
		http.NotFound(w, r)
		return
	}
	const prefix = "/api/h5/product-images/"
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), prefix), "/")
	if len(parts) != 4 || parts[2] != "variants" || parts[3] != "original" {
		http.NotFound(w, r)
		return
	}
	code, err := url.PathUnescape(parts[0])
	id, idErr := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') || idErr != nil || id < 1 || strconv.FormatInt(id, 10) != parts[1] {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProductValue(r, code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ids, idsErr := productapp.PublicProductImageIDs(product)
	if idsErr != nil || !containsImageID(ids, id) {
		http.NotFound(w, r)
		return
	}
	exists, existsErr := h.media.LocalImageExists(r.Context(), id)
	if existsErr != nil {
		http.Error(w, "media unavailable", http.StatusServiceUnavailable)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	variant, variantErr := h.media.GetImageVariant(r.Context(), id, "original")
	if variantErr != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", variant.MediaType)
	w.Header().Set("ETag", variant.ETag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(variant.Content)
}

func containsImageID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// legacyPublicProductID recognizes only the numeric route format generated by
// the prior V3 public-sharing code. It is a read-only compatibility alias;
// every newly generated link uses product_code.
func legacyPublicProductID(code string) (productport.ID, bool) {
	id, err := strconv.ParseInt(code, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return productport.ID(id), true
}

func publicProductCode(r *http.Request, prefix string) (string, bool) {
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, prefix) {
		return "", false
	}
	escapedCode := strings.TrimPrefix(escapedPath, prefix)
	if escapedCode == "" || strings.Contains(escapedCode, "/") {
		return "", false
	}
	code, err := url.PathUnescape(escapedCode)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') {
		return "", false
	}
	return code, true
}

type publicProductPageView struct {
	Product          publicProduct
	Payment          bool
	Detail           bool
	Presentation     publicPresentationTemplate
	WeChatPayEnabled bool
	AlipayEnabled    bool
}

type publicPresentationTemplate struct {
	Enabled       bool
	StylesheetURL string
	HostURL       string
}

func publicPresentationTemplateFor(assets PublicPresentationAssets) publicPresentationTemplate {
	return publicPresentationTemplate{Enabled: assets.configured(), StylesheetURL: assets.StylesheetURL, HostURL: assets.HostURL}
}

var publicProductPage = template.Must(template.New("public-product").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>{{.Product.Name}}</title>
<style>
.detail-image{display:block;width:100%;height:auto;background:#fff}.detail-image[data-public-media-state="loading"]{min-height:34vh;background:linear-gradient(100deg,#f4f5f7 20%,#fff 40%,#f4f5f7 60%);background-size:220% 100%;animation:detail-loading 1.2s ease-in-out infinite}.detail-image[data-public-media-state="delayed"]{min-height:64px;background:#f4f5f7}.checkout-footer a.buy{text-align:center;text-decoration:none}*{box-sizing:border-box}body{margin:0;background:#f5f6f8;color:#20242b;font:15px/1.5 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}.card{max-width:560px;margin:auto;padding:20px 16px calc(118px + env(safe-area-inset-bottom));min-height:100dvh}.card[data-public-commerce-route="detail"]{padding:0 0 calc(118px + env(safe-area-inset-bottom));background:#fff}.panel{background:#fff;border-radius:18px;padding:20px;margin-bottom:14px}.auth-gate{min-height:calc(100dvh - 40px);display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;padding:34px 24px}.card[data-public-commerce-route="detail"]>.auth-gate{margin:20px 16px}.auth-badge{display:inline-flex;align-items:center;height:28px;padding:0 12px;border-radius:999px;background:#eff4ff;color:#3268ff;font-size:13px;font-weight:600}.auth-gate h1{margin:18px 0 0;font-size:26px;line-height:1.3}.auth-message{max-width:22em;margin:12px auto 0;color:#858b95;font-size:14px;line-height:1.65}.auth-button{width:100%;min-height:48px;margin-top:26px;border:0;border-radius:12px;background:#3268ff;color:#fff;display:grid;place-items:center;font-size:16px;font-weight:600;text-decoration:none}.auth-button[aria-disabled="true"]{background:#a9bfff;pointer-events:none}.auth-note{margin-top:14px;color:#a1a6ae;font-size:12px}.auth-chevron{margin-top:16px;color:#3268ff;animation:auth-dip 1.6s ease-in-out infinite}@keyframes auth-dip{0%,100%{transform:translateY(0)}50%{transform:translateY(6px)}}@keyframes detail-loading{0%{background-position:100% 0}100%{background-position:-100% 0}}.product-info{min-width:0}h1{font-size:19px;line-height:1.4;margin:0 0 5px;font-weight:650;overflow-wrap:anywhere}.desc{color:#9298a2;font-size:13px;white-space:pre-wrap;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.price{font-size:22px;font-weight:650;margin-top:8px}.period{font-size:13px;color:#7b8390;margin-top:6px}.row{display:flex;align-items:center;justify-content:space-between;gap:14px;padding:17px 0;border-bottom:1px solid #f0f1f4}.row:first-child{padding-top:0}.row:last-child{padding-bottom:0;border:0}.label{color:#858b95;flex-shrink:0}.amount{font-variant-numeric:tabular-nums;white-space:nowrap}.total{font-size:21px;font-weight:600}.coupon-choice{min-width:0;text-align:right}.coupon-panel .row{border:0;padding:0}.coupon-status{font-size:13px;color:#7d8591;margin:12px 0 0}.coupon-refresh{display:block;margin:10px 0 0 auto;padding:5px 0;border:0;background:none;color:#3268ff;font:inherit;font-size:13px;cursor:pointer}.coupon-refresh:disabled{color:#9aa4b3;cursor:default}.field-error{display:block;color:#e3343b;font-size:12px;margin:5px 2px 0}.shipping-purpose{font-size:12px;color:#e3343b;margin-left:8px}.payment-errors{flex-basis:100%;color:#e3343b;font-size:12px;text-align:right;min-height:0}.buy[data-feedback="true"]{transform:scale(.985);filter:brightness(.94)}[aria-invalid="true"]{border-color:#e3343b!important;box-shadow:0 0 0 1px #e3343b}.checkout-footer{flex-wrap:wrap}.coupon{max-width:220px;width:100%;border:0;background:#fff;color:#56606e;font:inherit;text-align:right;outline-offset:4px}.discount{color:#df5b45;font-size:12px;margin-top:4px}.mobile,.shipping-input{width:100%;height:46px;border:1px solid #e6e8ec;border-radius:10px;padding:0 12px;margin-top:12px;font:inherit;background:#fff}.mobile:focus,.shipping-input:focus,.shipping-select:focus{outline:2px solid #b4c8ff;border-color:#3268ff}.shipping-selects{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:12px}.shipping-select{width:100%;height:46px;border:1px solid #e6e8ec;border-radius:10px;padding:0 8px;font:inherit;background:#fff}.method{display:flex;align-items:center;gap:12px;margin-top:14px;min-height:44px}.method input{width:18px;height:18px;margin-left:auto;accent-color:#3268ff}.alipay-guide{background:#fff;border-radius:18px;padding:20px;margin:14px 0}.alipay-guide input{width:100%;padding:10px;border:1px solid #e6e8ec;border-radius:8px;font:inherit}.alipay-actions{display:flex;gap:10px;margin-top:12px}.alipay-actions button,.alipay-actions a{flex:1;text-align:center;padding:10px;border:1px solid #d8e2ff;border-radius:10px;background:#fff;color:#3268ff;font:inherit;text-decoration:none}.alipay-guide p{margin:0 0 10px}.wechat-icon{width:34px;height:34px;background:#09b761;color:#fff;display:grid;place-items:center;border-radius:10px;font-size:21px}.selected{margin-left:auto;border-radius:50%;width:20px;height:20px;display:grid;place-items:center;background:#3268ff;color:#fff;font-size:13px}.notice{padding:12px;background:#fff5e7;color:#946728;border-radius:12px;margin-bottom:14px;font-size:13px}.checkout-footer{position:fixed;bottom:0;left:50%;transform:translateX(-50%);width:100%;max-width:560px;background:#fff;border-top:1px solid #ebedf0;padding:14px 18px calc(14px + env(safe-area-inset-bottom));display:flex;align-items:center;gap:18px;z-index:2}.footer-price{min-width:100px;flex:1}.footer-price .label{font-size:12px}.footer-price strong{display:block;font-size:24px;line-height:1.35;font-variant-numeric:tabular-nums}.buy{flex:1.1;min-height:48px;border:0;border-radius:13px;background:#3268ff;color:#fff;font-family:inherit;font-size:17px;font-weight:600;line-height:1.4;padding:12px 18px;cursor:pointer}.buy:disabled{background:#a9bfff;cursor:default}.restart{width:100%;margin-top:12px;background:#fff;color:#3268ff;border:1px solid #d8e2ff}.status{color:#7d8591;font-size:13px;text-align:center;overflow-wrap:anywhere;margin:14px 4px}.completion-qr{display:block;max-width:220px;width:100%;height:auto;margin:14px auto}.completion-result{background:#fff;border-radius:18px;margin:0;padding:30px 20px;color:#747d89}.completion-check{width:56px;height:56px;border-radius:50%;background:#e8f8ef;color:#08ad60;font-size:32px;margin:0 auto 14px}.completion-result h2{font-size:24px;color:#20242b;margin:0}.completion-product{color:#20242b;font-size:16px;font-weight:600;margin:10px 0 0}.completion-description{font-size:13px;color:#747d89;margin:4px 0 0}.completion-amount{font-size:15px;color:#20242b;margin:12px 0 24px}.completion-title{font-size:18px;font-weight:600;color:#20242b;margin:0}.completion-subtitle{margin-top:4px}button:focus-visible,select:focus-visible,.auth-button:focus-visible{outline:3px solid #a6bfff;outline-offset:3px}[hidden]{display:none!important}@media(prefers-reduced-motion:reduce){.auth-chevron,.detail-image[data-public-media-state="loading"]{animation:none}}@media(max-width:360px){.card:not([data-public-commerce-route="detail"]){padding-left:12px;padding-right:12px}.panel{padding:16px}.coupon{max-width:180px}h1{font-size:17px}}
</style>{{if .Presentation.Enabled}}<link rel="stylesheet" href="{{.Presentation.StylesheetURL}}"><script type="module" src="{{.Presentation.HostURL}}"></script>{{end}}</head>
<body><main class="card" data-v3-public-commerce data-public-commerce-route="{{if .Detail}}detail{{else}}payment{{end}}" data-product-kind="{{.Product.ProductKind}}" data-promotion-context="{{.Product.PromotionContext}}"><section id="identityGate" class="panel auth-gate"><span class="auth-badge">微信身份验证</span><h1 id="identityTitle">正在核验微信身份</h1><p id="identityMessage" class="auth-message">正在核验当前微信授权状态…</p><a id="authContinue" class="auth-button" href="#" hidden>重新尝试微信授权</a><svg class="auth-chevron" width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6"></path></svg><p class="auth-note">授权成功后会自动返回当前页面</p></section>{{if .Detail}}<section id="detailContent" hidden>{{range $index, $image := .Product.Images}}<img class="detail-image" data-src="{{$image}}" data-detail-index="{{$index}}" alt="商品详情" decoding="async" {{if eq $index 0}}loading="eager" fetchpriority="high"{{else}}loading="lazy"{{end}}>{{end}}<footer class="checkout-footer"><div class="footer-price"><span class="label">价格</span><strong>¥<span id="detailPrice"></span></strong></div><a class="buy" href="{{.Product.PaymentPath}}">{{.Product.BuyButtonText}}</a></footer></section>{{end}}<div id="checkoutContent" hidden><section class="panel product"><div class="product-info"><h1>{{.Product.Name}}</h1>{{with .Product.Description}}<div class="desc">{{.}}</div>{{end}}{{if gt .Product.ServicePeriodDurationDays 0}}<div class="period">服务周期 {{.Product.ServicePeriodDurationDays}} 天</div>{{end}}<div class="price amount">¥<span id="price"></span></div></div></section>
{{if .Payment}}<div id="wechatNotice" class="notice" hidden>请在微信内打开此页面完成支付。</div><section id="couponPanel" class="panel coupon-panel" aria-label="优惠券"><div class="row"><label class="label" for="coupon">优惠券</label><div class="coupon-choice"><select id="coupon" class="coupon"><option value="0">自动选择最优优惠券</option></select></div></div><div id="couponStatus" class="coupon-status" role="status" aria-live="polite">正在查询可用优惠券…</div><div id="discountAmount" class="discount" hidden></div><button id="refreshCoupons" class="coupon-refresh" type="button">刷新优惠券</button></section><section id="paymentDetails" class="panel" aria-label="支付明细"><div class="row"><span class="label">实付金额</span><strong class="amount total" id="payableAmount"></strong></div></section>
{{if .Product.RequireMobile}}<section id="mobilePanel" class="panel"><label class="label" for="mobile">手机号</label><input id="mobile" class="mobile" type="tel" autocomplete="tel-national" inputmode="numeric" maxlength="11" placeholder="请输入手机号"><span id="mobileError" class="field-error" hidden></span></section>{{end}}{{if eq .Product.ContactCollectionLevel "shipping_address"}}<section id="shippingPanel" class="panel"><label class="label">收货信息<span class="shipping-purpose">收实物商品使用</span></label><div class="shipping-selects"><select id="province" class="shipping-select"><option value="">请选择省</option></select><select id="city" class="shipping-select" disabled><option value="">请选择市</option></select><select id="district" class="shipping-select" disabled><option value="">请选择区/县</option></select></div><span id="provinceError" class="field-error" hidden></span><span id="cityError" class="field-error" hidden></span><span id="districtError" class="field-error" hidden></span><input id="recipientName" class="shipping-input" type="text" maxlength="80" autocomplete="name" placeholder="请输入收件人"><span id="recipientNameError" class="field-error" hidden></span><input id="detailAddress" class="shipping-input" type="text" maxlength="300" autocomplete="street-address" placeholder="请输入详细地址"><span id="detailAddressError" class="field-error" hidden></span></section>{{end}}
<section id="paymentMethod" class="panel"><div class="label">支付方式</div>{{if .WeChatPayEnabled}}<label class="method"><span class="wechat-icon" aria-hidden="true">✓</span><span>微信支付</span><input type="radio" name="paymentMethod" value="wechat_pay" checked></label>{{end}}{{if .AlipayEnabled}}<label class="method"><span class="wechat-icon" style="background:#1677ff" aria-hidden="true">支</span><span>支付宝</span><input type="radio" name="paymentMethod" value="alipay" {{if not .WeChatPayEnabled}}checked{{end}}></label>{{end}}</section><section id="alipayGuide" class="alipay-guide" hidden><p id="alipayGuideMessage">请复制付款链接，在手机系统浏览器地址栏粘贴打开；返回本页后确认支付结果。</p><input id="alipayPaymentURL" aria-label="原订单支付宝付款链接" readonly><div class="alipay-actions"><button id="alipayCopy" type="button">复制付款链接</button><button id="alipayPaid" type="button">我已支付</button><a id="alipayOpen" href="#" rel="noopener noreferrer" hidden>打开支付宝付款</a></div></section><div id="status" class="status" role="status" aria-live="polite"></div>{{if eq .Product.ProductKind "service_period"}}<button id="renew" class="buy restart" hidden>续费</button>{{end}}<footer class="checkout-footer"><div class="footer-price"><span class="label">实付</span><strong id="footerAmount"></strong></div><div id="paymentErrors" class="payment-errors" role="alert" hidden></div><button id="buy" class="buy">立即支付</button></footer>{{end}}</div>
</main><script>document.getElementById('price').textContent=({{.Product.PriceMinor}}/100).toFixed(2);{{if .Payment}}
const button=document.getElementById('buy'),statusBox=document.getElementById('status'),couponField=document.getElementById('coupon'),identityGate=document.getElementById('identityGate'),identityTitle=document.getElementById('identityTitle'),identityMessage=document.getElementById('identityMessage'),authContinue=document.getElementById('authContinue'),checkoutContent=document.getElementById('checkoutContent'),inWechat=/MicroMessenger/i.test(navigator.userAgent),checkoutStorageKey='aicrm.checkout.tab.v2:'+{{.Product.ID}}+':{{.Product.ProductKind}}',promotionContext='{{.Product.PromotionContext}}';authContinue.href='/api/h5/wechat-pay/oauth/start?return_url='+encodeURIComponent(location.pathname+(location.search||''))
let purchaseState='available',detailImageObserver;const authAttemptKey='aicrm.oauth.auto:'+location.pathname;function authorizeMissingSession(message){showIdentityGate(message);if(!inWechat)return;identityTitle.textContent='正在前往微信授权';try{if(sessionStorage.getItem(authAttemptKey))return;sessionStorage.setItem(authAttemptKey,'1')}catch(_){return}authContinue.hidden=true;identityMessage.textContent='正在前往微信授权…';location.assign(authContinue.href)}authContinue.addEventListener('click',()=>{try{sessionStorage.removeItem(authAttemptKey)}catch(_){}});function showIdentityGate(message){const detail=document.getElementById('detailContent');if(detail)detail.hidden=true;checkoutContent.hidden=true;identityGate.hidden=false;identityMessage.textContent=message;const timedOut=message==='网络连接超时，请刷新重试';if(inWechat){identityTitle.textContent=timedOut?'页面加载失败':'需要微信授权';authContinue.hidden=false;authContinue.textContent=timedOut?'刷新重试':'重新尝试微信授权';authContinue.href=timedOut?location.href:authContinue.href;authContinue.removeAttribute('aria-disabled')}else{identityTitle.textContent='请在微信中打开';authContinue.hidden=false;authContinue.textContent='请在微信中打开';authContinue.setAttribute('aria-disabled','true')}}function revealCheckout(){try{sessionStorage.removeItem(authAttemptKey)}catch(_){}identityGate.hidden=true;checkoutContent.hidden=false}function requestDetailImage(image){const source=image.dataset.src;if(!source)return;image.dataset.publicMediaState='loading';let settled=false,loadingTimer;const settle=state=>{if(settled)return;settled=true;clearTimeout(loadingTimer);image.dataset.publicMediaState=state};image.addEventListener('load',()=>settle('loaded'),{once:true});image.addEventListener('error',()=>settle('unavailable'),{once:true});image.src=source;delete image.dataset.src;loadingTimer=setTimeout(()=>{if(!settled)image.dataset.publicMediaState='delayed'},10000)}function revealProduct(){const detail=document.getElementById('detailContent');if(!detail)return;checkoutContent.hidden=true;detail.hidden=false;const images=Array.from(detail.querySelectorAll('img[data-src]'));const first=images.shift();if(first)requestDetailImage(first);if(images.length){if(typeof IntersectionObserver==='function'){detailImageObserver?.disconnect();detailImageObserver=new IntersectionObserver(entries=>{for(const entry of entries){if(!entry.isIntersecting)continue;detailImageObserver.unobserve(entry.target);requestDetailImage(entry.target)}},{rootMargin:'900px 0px'});for(const image of images)detailImageObserver.observe(image)}else for(const image of images)requestDetailImage(image)}document.getElementById('detailPrice').textContent=({{.Product.PriceMinor}}/100).toFixed(2)}
function storedPromotionContext(value){if(value===undefined||value==='')return '';return typeof value==='string'&&/^dpc_[A-Za-z0-9_-]{43}$/.test(value)?value:null}function checkoutPayload(value){if(!value||typeof value!=='object'||value.product_id!=={{.Product.ID}}||value.product_kind!=='{{.Product.ProductKind}}'||value.beneficiary_selection!=='payer_self'||!Number.isSafeInteger(value.coupon_claim_id)||value.coupon_claim_id<0)return null;const level='{{.Product.ContactCollectionLevel}}'||'none';const frozenPromotionContext=storedPromotionContext(value.promotion_context);if(frozenPromotionContext===null)return null;const provider=value.provider||'wechat_pay',channel=value.channel||'';if(provider!=='wechat_pay'&&provider!=='alipay')return null;if(provider==='alipay'&&!['alipay_wap','alipay_page'].includes(channel))return null;if(provider==='wechat_pay'&&channel!=='')return null;const normalized={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:value.coupon_claim_id,contact_collection_level:level};if(provider==='alipay'){normalized.provider=provider;normalized.channel=channel}if(frozenPromotionContext)normalized.promotion_context=frozenPromotionContext;if(level!=='none'&&(typeof value.mobile!=='string'||!/^\+861[3-9][0-9]{9}$/.test(value.mobile)))return null;if(level==='none'&&(value.mobile!==undefined||value.recipient_name!==undefined||value.province_code!==undefined||value.city_code!==undefined||value.district_code!==undefined||value.detail_address!==undefined))return null;if(level!=='none')normalized.mobile=value.mobile;if(level==='shipping_address'){for(const field of ['recipient_name','province_code','province_name','city_code','city_name','district_code','district_name','detail_address'])if(typeof value[field]!=='string'||!value[field].trim())return null;Object.assign(normalized,{recipient_name:value.recipient_name.trim(),province_code:value.province_code,province_name:value.province_name,city_code:value.city_code,city_name:value.city_name,district_code:value.district_code,district_name:value.district_name,detail_address:value.detail_address.trim()})}return normalized}
function checkoutBinding(value){return typeof value==='string'&&/^[A-Za-z0-9_-]{43}$/.test(value)?value:null}
const regionOptions={{.Product.RegionOptionsJSON}};const provinceField=document.getElementById('province'),cityField=document.getElementById('city'),districtField=document.getElementById('district');function refillRegionSelect(field,items,placeholder){if(!field)return;field.innerHTML='<option value="">'+placeholder+'</option>';for(const item of items||[]){const option=document.createElement('option');option.value=String(item.c);option.textContent=item.n;field.appendChild(option)}field.disabled=!items||items.length===0}function resetRegionFields(){if(!provinceField)return;refillRegionSelect(provinceField,regionOptions,'请选择省');refillRegionSelect(cityField,[], '请选择市');refillRegionSelect(districtField,[],'请选择区/县')}if(provinceField){resetRegionFields();provinceField.addEventListener('change',()=>{const province=(regionOptions||[]).find(item=>String(item.c)===provinceField.value);refillRegionSelect(cityField,province?.ch||[],'请选择市');refillRegionSelect(districtField,[],'请选择区/县')});cityField.addEventListener('change',()=>{const province=(regionOptions||[]).find(item=>String(item.c)===provinceField.value),city=(province?.ch||[]).find(item=>String(item.c)===cityField.value);refillRegionSelect(districtField,city?.ch||[],'请选择区/县')})}
function checkoutRecord(){let raw;try{raw=sessionStorage.getItem(checkoutStorageKey)}catch(_){return {state:'unavailable'}}if(raw===null)return {state:'none'};let value;try{value=JSON.parse(raw)}catch(_){return {state:'invalid'}}const payload=checkoutPayload(value&&value.payload),binding=checkoutBinding(value&&value.session_binding);if(!value||typeof value.key!=='string'||value.key.length<8||typeof value.merchant_order_no!=='string'||!payload||(value.terminal_status!==undefined&&value.terminal_status!=='paid'))return {state:'invalid'};value.payload=payload;if(binding){value.session_binding=binding;return {state:'valid',value}}value.legacy_unbound=true;return {state:'valid',value}}function readCheckout(){const record=checkoutRecord();return record.state==='valid'?record.value:null}
function writeCheckout(value){try{sessionStorage.setItem(checkoutStorageKey,JSON.stringify(value));return true}catch(_){return false}}
function clearCheckout(){try{sessionStorage.removeItem(checkoutStorageKey)}catch(_){}}function retainPaidCheckout(orderNo){const checkpoint=readCheckout();if(!checkpoint||checkpoint.merchant_order_no!==orderNo)return;checkpoint.terminal_status='paid';writeCheckout(checkpoint)}
function checkoutKey(payload,binding){const record=checkoutRecord();if(record.state==='valid')return record.value;if(record.state==='invalid')throw requestFailure('checkout_checkpoint_invalid','原订单恢复信息异常，已保留，请联系管理员核对');if(record.state==='unavailable')return null;const created={key:crypto.randomUUID(),merchant_order_no:'',payload:checkoutPayload(payload),session_binding:checkoutBinding(binding)};return created.payload&&created.session_binding&&writeCheckout(created)?created:null}
const alipayGuide=document.getElementById('alipayGuide'),alipayGuideMessage=document.getElementById('alipayGuideMessage'),alipayPaymentURL=document.getElementById('alipayPaymentURL');function selectedPaymentMethod(){return document.querySelector('input[name=paymentMethod]:checked')?.value||''}function restorePaymentMethod(checkpoint){const method=checkpoint?.payload?.provider||'wechat_pay';const option=document.querySelector('input[name=paymentMethod][value='+method+']');if(option)option.checked=true}function showAlipayGuide(handoff,orderNo){let target;try{target=new URL(handoff?.redirectUrl)}catch(_){throw new Error('原订单付款链接暂不可用，请稍后查询')}if(target.protocol!=='https:')throw new Error('原订单付款链接不安全，请联系客服核对');const checkpoint=readCheckout();if(!checkpoint||checkpoint.merchant_order_no!==orderNo)throw new Error('原订单标识已变化，请刷新后核对');alipayPaymentURL.value=target.href;const open=document.getElementById('alipayOpen');open.href=target.href;open.hidden=inWechat;alipayGuide.hidden=false;statusBox.textContent='原订单已创建，请继续完成付款';alipayGuideMessage.textContent=inWechat?'请复制原订单付款链接，在手机系统浏览器地址栏粘贴打开；付款后回到本页确认结果。':'请打开原订单支付宝链接完成付款，返回本页后确认结果。';button.disabled=true;button.textContent='已生成订单'}document.getElementById('alipayCopy').addEventListener('click',async()=>{if(!alipayPaymentURL.value)return;try{await navigator.clipboard.writeText(alipayPaymentURL.value);alipayGuideMessage.textContent='已复制。请到系统浏览器地址栏粘贴打开原订单链接。'}catch(_){alipayPaymentURL.focus();alipayPaymentURL.select();alipayGuideMessage.textContent='自动复制失败，请手动复制上方链接。'}});document.getElementById('alipayPaid').addEventListener('click',async()=>{const checkpoint=readCheckout();if(!checkpoint?.merchant_order_no||checkpoint.payload.provider!=='alipay')return;try{const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(checkpoint.merchant_order_no),{credentials:'same-origin'});if(value.status==='paid'){retainPaidCheckout(checkpoint.merchant_order_no);alipayGuide.hidden=true;showCompletionAction(value.completion_action);button.disabled=true;button.textContent='已购买'}else alipayGuideMessage.textContent='服务端尚未确认收款，请完成付款后再查询原订单；不会创建新订单。'}catch(_){alipayGuideMessage.textContent='暂时无法确认原订单付款结果，请稍后重试。'}});const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));function requestFailure(code,message){const error=new Error(message);error.code=code;return error}async function requestJSON(url,options={}){const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),12000);try{const response=await fetch(url,{...options,signal:controller.signal});let body={};try{body=await response.json()}catch(_){}if(response.status===401)throw requestFailure('payment_session_required','请先完成微信授权');if(!response.ok){const code=body.code||body.error||'';if(code==='payment_provider_disabled'||code==='payment_h5_oauth_disabled')throw requestFailure(code,'支付服务暂未启用');if(code==='session_mismatch')throw requestFailure(code,'付款授权已变化，原订单标识已保留；请恢复原授权后继续');if(code==='already_purchased')throw requestFailure(code,'您已购买本课程');if(code==='purchase_pending')throw requestFailure(code,'支付状态需要核验，请稍后重试');if(code==='conflict')throw requestFailure(code,'商品或手机号状态不符合购买要求');throw requestFailure(code,'请求失败')}return body}catch(error){if(error&&error.name==='AbortError')throw requestFailure('request_timeout','网络连接超时，请刷新重试');throw error}finally{clearTimeout(timeout)}}
function showCompletionAction(action){
for(const selector of ['.product','#couponPanel','#paymentDetails','#mobilePanel','#shippingPanel','#paymentMethod','#alipayGuide','.checkout-footer']){const panel=checkoutContent.querySelector(selector);if(panel)panel.hidden=true}
statusBox.className='status completion-result';statusBox.replaceChildren();
const mark=document.createElement('div');mark.className='completion-check';mark.textContent='✓';mark.setAttribute('aria-hidden','true');statusBox.appendChild(mark);
const heading=document.createElement('h2');heading.textContent='支付完成';statusBox.appendChild(heading);
const productName=document.createElement('p');productName.className='completion-product';productName.textContent=checkoutContent.querySelector('.product h1')?.textContent||'';statusBox.appendChild(productName);
const description=checkoutContent.querySelector('.product .desc')?.textContent;if(description){const productDescription=document.createElement('p');productDescription.className='completion-description';productDescription.textContent=description;statusBox.appendChild(productDescription)}
const paidCheckpoint=readCheckout();if(originalAmount&&paidCheckpoint&&originalAmount.orderNo===paidCheckpoint.merchant_order_no){const paidAmount=document.createElement('p');paidAmount.className='completion-amount';paidAmount.textContent='实付 ¥'+(originalAmount.amount/100).toFixed(2);statusBox.appendChild(paidAmount)}
if(!action||action.state==='unavailable'){const note=document.createElement('p');note.textContent='支付成功，后续指引暂不可用，请稍后刷新。';statusBox.appendChild(note);return}
if(action.state!=='available')return;
if(action.mode==='redirect'&&typeof action.redirect_url==='string'&&action.redirect_url){const note=document.createElement('p');note.textContent='正在跳转…';statusBox.appendChild(note);location.assign(action.redirect_url);return}
if(action.mode!=='qr'||!action.lead_qr||typeof action.lead_qr.url!=='string'||!action.lead_qr.url)return;
const lead=action.lead_qr,title=document.createElement('h3');title.className='completion-title';title.textContent=lead.title||'添加企微领取后续资料';statusBox.appendChild(title);
const image=document.createElement('img');image.className='completion-qr';image.src=lead.url;image.alt=lead.title||'添加企微二维码';statusBox.appendChild(image);
const subtitle=document.createElement('p');subtitle.className='completion-subtitle';subtitle.textContent=lead.subtitle||'长按识别二维码，添加企微领取后续资料';statusBox.appendChild(subtitle);
image.addEventListener('error',()=>{subtitle.textContent='二维码暂时加载失败，请刷新页面重试。'});
}
function resetCompletionView(){alipayGuide.hidden=true;statusBox.className='status';statusBox.replaceChildren();for(const selector of ['.product','#couponPanel','#paymentDetails','#mobilePanel','#shippingPanel','#paymentMethod','.checkout-footer']){const panel=checkoutContent.querySelector(selector);if(panel)panel.hidden=false}}

function releaseExpiredCheckout(value,orderNo){if(purchaseState==='owned'||value.checkout_restart_allowed!==true)return false;const current=readCheckout();if(!current||current.merchant_order_no!==orderNo)throw new Error('订单状态已更新，请刷新页面后继续');clearCheckout();if(readCheckout())throw new Error('无法清除已核验的旧订单记录，请重新打开页面');originalAmount=null;purchaseState='available';updateAmounts();if(renewButton)renewButton.hidden=true;button.disabled=false;button.dataset.invoked='';statusBox.textContent='请填写手机号并确认支付';void loadCoupons().catch(error=>{if(error&&error.code==='payment_session_required')authorizeMissingSession('授权已失效，请重新完成微信授权。');else statusBox.textContent='优惠券读取失败，请稍后重试'});return true}
async function poll(orderNo){for(let i=0;i<90;i++){const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(orderNo),{credentials:'same-origin'});readOriginalAmount(value,orderNo);if(value.status==='paid'){retainPaidCheckout(orderNo);statusBox.textContent='支付成功';showCompletionAction(value.completion_action);button.disabled=true;if(renewButton)renewButton.hidden=false;button.textContent='已购买';return}if(releaseExpiredCheckout(value,orderNo))return;if(value.checkout_abandoned===true){statusBox.textContent='原支付流程已停止，订单记录已保留，尚未确认最终结果。请联系管理员核对后再购买。';button.disabled=true;if(renewButton)renewButton.hidden=true;return}if(value.status==='failed'||value.status==='cancelled'){clearCheckout();originalAmount=null;updateAmounts();throw new Error('支付未完成，请确认后重新购买')}if(readCheckout()?.payload?.provider==='alipay'&&value.ready&&value.handoff){showAlipayGuide(value.handoff,orderNo);return}if(value.prepay_state==='outcome_unknown')throw new Error('微信支付下单结果尚未确认，原订单已保留，请稍后查看；请勿重复下单');if(value.prepay_state==='final_failed')throw new Error('微信支付暂不可用，原订单已保留，请联系客服处理');if(value.ready&&value.handoff&&!button.dataset.invoked){button.dataset.invoked='1';try{await invokePay(value.handoff)}catch(error){button.dataset.invoked='';throw error}}await sleep(1500)}button.dataset.invoked='';throw new Error('支付结果确认超时，请稍后刷新查看')}
function invokePay(handoff){return new Promise((resolve,reject)=>{let finished=false,timer;const finish=error=>{if(finished)return;finished=true;clearTimeout(timer);document.removeEventListener('WeixinJSBridgeReady',call);error?reject(error):resolve()};const call=()=>{if(finished)return;clearTimeout(timer);timer=setTimeout(()=>finish(new Error('微信支付结果尚未确认，请使用原订单继续查看')),120000);try{WeixinJSBridge.invoke('getBrandWCPayRequest',handoff,result=>finish(result&&result.err_msg==='get_brand_wcpay_request:ok'?null:new Error('支付未完成，请使用原订单继续支付')))}catch(_){finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单'))}};if(typeof WeixinJSBridge==='undefined'){timer=setTimeout(()=>finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单')),10000);document.addEventListener('WeixinJSBridgeReady',call,{once:true})}else call()})}
const couponDiscounts=new Map(),couponNames=new Map(),couponRefresh=document.getElementById('refreshCoupons');let originalAmount=null;function readOriginalAmount(value,orderNo){originalAmount=value&&Number.isSafeInteger(value.amount_minor)&&value.amount_minor>0&&value.currency==='CNY'?{orderNo,amount:value.amount_minor}:null;updateAmounts()}function updateAmounts(){if(purchaseState==='owned'&&!readCheckout()){document.getElementById('payableAmount').textContent='已购买';document.getElementById('footerAmount').textContent='已购买';return}const checkpoint=readCheckout(),mobileField=document.getElementById('mobile');couponField.disabled=!!checkpoint;if(couponRefresh)couponRefresh.disabled=!!checkpoint;for(const option of document.querySelectorAll('input[name=paymentMethod]'))option.disabled=!!checkpoint;if(mobileField)mobileField.disabled=!!checkpoint;for(const field of [provinceField,cityField,districtField,document.getElementById('recipientName'),document.getElementById('detailAddress')])if(field)field.disabled=!!checkpoint;if(checkpoint){const amount=originalAmount&&originalAmount.orderNo===checkpoint.merchant_order_no?'¥'+(originalAmount.amount/100).toFixed(2):'待确认';document.getElementById('payableAmount').textContent=amount;document.getElementById('footerAmount').textContent=amount;document.getElementById('discountAmount').hidden=true;return}const gross={{.Product.PriceMinor}},selected=Number(couponField.value)||0;const discount=selected?(couponDiscounts.get(selected)||0):Math.max(0,...couponDiscounts.values());const money=value=>'¥'+(value/100).toFixed(2);document.getElementById('payableAmount').textContent=money(gross-discount);document.getElementById('footerAmount').textContent=money(gross-discount);const badge=document.getElementById('discountAmount');badge.hidden=!discount;badge.textContent=discount?'优惠 −'+money(discount):'';if(couponNames.size){const best=[...couponDiscounts.entries()].sort((a,b)=>b[1]-a[1])[0]?.[0];document.getElementById('couponStatus').textContent='已领取：'+(couponNames.get(selected||best)||'可用优惠券')}}couponField.addEventListener('change',updateAmounts);updateAmounts();
async function loadCoupons(){if(!couponField||readCheckout()||purchaseState==='owned')return;const value=await requestJSON('/api/h5/coupons/available?target_ref='+encodeURIComponent('{{.Product.CouponTargetRef}}'),{credentials:'same-origin'});couponDiscounts.clear();couponNames.clear();couponField.innerHTML='<option value="0">自动选择最优优惠券</option>';for(const item of value.items||[]){if(!Number.isSafeInteger(item.claim_id)||item.claim_id<1||typeof item.name!=='string'||!Number.isSafeInteger(item.discount_amount_minor)||item.discount_amount_minor<1||item.discount_amount_minor>={{.Product.PriceMinor}}||item.currency!=='CNY')continue;const option=document.createElement('option');option.value=String(item.claim_id);option.textContent=item.name+'（优惠 ¥'+(item.discount_amount_minor/100).toFixed(2)+'）';couponField.appendChild(option);couponDiscounts.set(item.claim_id,item.discount_amount_minor);couponNames.set(item.claim_id,item.name)}document.getElementById('couponStatus').textContent=couponNames.size?'已领取：'+(couponNames.get([...couponDiscounts.entries()].sort((a,b)=>b[1]-a[1])[0][0])):'暂无可用优惠券';updateAmounts()}
if(couponRefresh)couponRefresh.addEventListener('click',async()=>{if(readCheckout()||purchaseState==='owned')return;couponRefresh.disabled=true;document.getElementById('couponStatus').textContent='正在刷新优惠券…';try{await loadCoupons()}catch(_){document.getElementById('couponStatus').textContent='优惠券读取失败，请稍后重试'}finally{couponRefresh.disabled=!!readCheckout()}});
function showOwnedPurchase(action){purchaseState='owned';const detail=document.getElementById('detailContent');if(detail)detail.hidden=true;revealCheckout();for(const id of ['couponPanel','paymentDetails','mobilePanel','shippingPanel','paymentMethod']){const panel=document.getElementById(id);if(panel)panel.hidden=true}button.disabled=true;button.textContent='已购买';statusBox.textContent='您已购买本课程';updateAmounts();if(readCheckout()?.terminal_status==='paid'){void restorePaidCheckout();return}try{showCompletionAction(action)}catch(_){statusBox.textContent='已购买，后续指引暂不可用'} }
let checkoutCanCreate=false;async function currentCheckoutBinding(forCreation=false){const value=await requestJSON('/api/v1/wechat-pay/checkout-session',{credentials:'same-origin'}),binding=checkoutBinding(value.checkout_session_binding);if(!binding)throw requestFailure('unavailable','付款授权状态暂不可用');checkoutCanCreate=value.can_create_checkout===true;if(forCreation&&!checkoutCanCreate)throw requestFailure('payment_session_required','请重新授权后支付');return binding}
const renewButton=document.getElementById('renew');if(renewButton)renewButton.addEventListener('click',async()=>{if(readCheckout()?.terminal_status!=='paid')return;clearCheckout();resetCompletionView();originalAmount=null;updateAmounts();renewButton.hidden=true;button.disabled=true;button.dataset.invoked='';button.textContent='立即支付';checkoutContent.hidden=true;identityGate.hidden=false;authContinue.hidden=true;identityTitle.textContent='正在核验微信身份';identityMessage.textContent='正在核验当前微信授权状态…';statusBox.textContent='请确认续费信息后支付';await bootstrapCheckout()});async function restorePaidCheckout(){const checkpoint=readCheckout();if(!checkpoint||!checkpoint.merchant_order_no)return;button.disabled=true;if(renewButton)renewButton.hidden=true;statusBox.textContent='正在恢复支付结果…';try{const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(checkpoint.merchant_order_no),{credentials:'same-origin'});readOriginalAmount(value,checkpoint.merchant_order_no);if(value.status==='paid'){retainPaidCheckout(checkpoint.merchant_order_no);if(renewButton)renewButton.hidden=false;button.textContent='已购买';statusBox.textContent='支付成功';showCompletionAction(value.completion_action);return}if(checkpoint.terminal_status==='paid')throw new Error('付款状态暂未确认，请使用原订单继续确认');if(releaseExpiredCheckout(value,checkpoint.merchant_order_no))return;if(value.checkout_abandoned===true){statusBox.textContent='原支付流程已停止，订单记录已保留，尚未确认最终结果。请联系管理员核对后再购买。';return}button.disabled=false;statusBox.textContent='已恢复原订单，请继续确认支付。';restorePaymentMethod(checkpoint);if(checkpoint.payload.provider==='alipay')await poll(checkpoint.merchant_order_no)}catch(error){if(error&&error.code==='payment_session_required'){authorizeMissingSession('授权已失效，请重新完成微信授权。');return}button.disabled=checkpoint.terminal_status==='paid';statusBox.textContent=error instanceof Error?error.message:'支付结果暂不可用'}}async function bootstrapCheckout(){if(!inWechat&&!{{.AlipayEnabled}}){showIdentityGate('请复制当前链接到微信中打开并完成授权。');return}try{await currentCheckoutBinding();button.disabled=true}catch(error){if(error&&error.code==='payment_session_required')authorizeMissingSession('正在获取微信授权。');else if(error&&error.code==='request_timeout')showIdentityGate(error.message);else showIdentityGate('授权状态暂时无法核验，请稍后重试。');return}try{const purchase=await requestJSON('/api/v1/wechat-pay/purchase-status?product_type={{.Product.ProductKind}}&product_id={{.Product.ID}}',{credentials:'same-origin'});if(!['available','owned','pending'].includes(purchase.purchase_state)||typeof purchase.can_purchase!=='boolean')throw new Error('购买状态暂不可用，请刷新重试');purchaseState=purchase.purchase_state;if(purchaseState==='owned'){showOwnedPurchase(purchase.completion_action);return}if(!readCheckout()&&!checkoutCanCreate)throw requestFailure('payment_session_required','请重新授权后支付');revealCheckout();button.disabled=false;}catch(error){if(error&&error.code==='payment_session_required'){authorizeMissingSession('授权已失效，请重新完成微信授权。');return}button.disabled=true;showIdentityGate(error instanceof Error?error.message:'购买状态暂不可用，请刷新重试');return}revealProduct();if(document.getElementById('detailContent'))return;void loadCoupons().catch(error=>{if(error&&error.code==='payment_session_required'){authorizeMissingSession('授权已失效，请重新完成微信授权。');return}document.getElementById('couponStatus').textContent='优惠券读取失败，请稍后重试';statusBox.textContent=error instanceof Error?error.message:'优惠券读取失败'});void restorePaidCheckout()}
function setCheckoutError(id,message){const field=document.getElementById(id),error=document.getElementById(id+'Error');if(field){if(message)field.setAttribute('aria-invalid','true');else field.removeAttribute('aria-invalid')}if(error){error.textContent=message||'';error.hidden=!message}}
for(const id of ['mobile','province','city','district','recipientName','detailAddress']){const field=document.getElementById(id);if(field)field.addEventListener(id==='province'||id==='city'||id==='district'?'change':'input',()=>{setCheckoutError(id,'');const summary=document.getElementById('paymentErrors');if(summary)summary.hidden=true})}
function validateCheckoutFields(level){const errors=[];const fail=(id,label,message)=>{errors.push(label);setCheckoutError(id,message)};for(const id of ['mobile','province','city','district','recipientName','detailAddress'])setCheckoutError(id,'');let mobile='';const phone=document.getElementById('mobile');if(level!=='none'){mobile=phone?.value.trim()||'';if(!mobile)fail('mobile','手机号','请填写手机号');else if(!/^1[3-9][0-9]{9}$/.test(mobile))fail('mobile','正确的手机号','请输入正确的大陆 11 位手机号')};let shipping=null;if(level==='shipping_address'){const province=(regionOptions||[]).find(item=>String(item.c)===provinceField?.value),city=(province?.ch||[]).find(item=>String(item.c)===cityField?.value),district=(city?.ch||[]).find(item=>String(item.c)===districtField?.value),recipient=document.getElementById('recipientName')?.value.trim(),detail=document.getElementById('detailAddress')?.value.trim();if(!province)fail('province','省','请选择省');if(!city)fail('city','市','请选择市');if(!district)fail('district','区/县','请选择区/县');if(!recipient)fail('recipientName','收件人','请填写收件人');if(!detail)fail('detailAddress','详细地址','请填写详细地址');if(!errors.length)shipping={recipient_name:recipient,province_code:String(province.c),province_name:province.n,city_code:String(city.c),city_name:city.n,district_code:String(district.c),district_name:district.n,detail_address:detail}}if(errors.length){const message='请填写'+errors.join('、');const summary=document.getElementById('paymentErrors');if(summary){summary.textContent=message;summary.hidden=false}const first=document.querySelector?.('[aria-invalid="true"]');first?.focus?.();throw new Error(message)}const summary=document.getElementById('paymentErrors');if(summary)summary.hidden=true;return {mobile,shipping}}
button.addEventListener('click',async()=>{if(purchaseState==='owned')return;button.dataset.feedback='true';setTimeout(()=>{button.dataset.feedback='false'},220);button.disabled=true;let checkpoint=null;try{checkpoint=readCheckout();if(!checkpoint){const method=selectedPaymentMethod();if(!method)throw new Error('当前没有可用的支付方式');const level='{{.Product.ContactCollectionLevel}}'||'none',fields=validateCheckoutFields(level);const payload={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:Number(couponField&&couponField.value)||0,contact_collection_level:level};if(method==='alipay'){payload.provider='alipay';payload.channel=inWechat?'alipay_wap':'alipay_page'}if(promotionContext)payload.promotion_context=promotionContext;if(fields.mobile)payload.mobile='+86'+fields.mobile;if(fields.shipping)Object.assign(payload,fields.shipping);checkpoint=checkoutKey(payload,await currentCheckoutBinding(true));if(!checkpoint)throw new Error('无法保存本次订单恢复信息，请检查浏览器存储后重试')}restorePaymentMethod(checkpoint);updateAmounts();if(checkpoint.legacy_unbound){if(!checkpoint.merchant_order_no)throw requestFailure('legacy_checkpoint_unbound','旧版订单恢复标识缺少付款会话绑定，已保留原标识，请勿重新下单');statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}if(checkpoint.merchant_order_no){statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}const currentBinding=await currentCheckoutBinding();if(currentBinding!==checkpoint.session_binding)throw requestFailure('session_mismatch','付款授权已变化，原订单标识已保留；请恢复原授权后继续');statusBox.textContent='正在恢复原订单…';const created=await requestJSON('/api/v1/wechat-pay/checkouts',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','Idempotency-Key':checkpoint.key},body:JSON.stringify({...checkpoint.payload,checkout_session_binding:checkpoint.session_binding})});checkpoint.merchant_order_no=created.merchant_order_no;if(!writeCheckout(checkpoint))throw new Error('无法保存原订单恢复信息，请勿重新下单');statusBox.textContent='等待微信支付…';await poll(created.merchant_order_no)}catch(error){if(error&&error.code==='already_purchased'){showOwnedPurchase();return}if(error&&error.code==='payment_session_required'){authorizeMissingSession('本次微信授权已失效，请重新授权后继续。');return}statusBox.textContent=error instanceof Error?error.message:'支付结果尚未确认，请使用原订单重试';button.disabled=false}});void bootstrapCheckout();{{end}}</script></body></html>`))
