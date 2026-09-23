package http

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
)

// frozenCouponPublicTemplateSHA256 identifies the read-only donor page copied
// from frozen Commerce coupon template source
// coupon_public.html. The Host only translates its server-template dialect;
// DOM, CSS, copy and browser workflow continue to originate in that file.
const frozenCouponPublicTemplateSHA256 = "2a6feccf9621138b62e3cd04d523f980a16ad7aa8901d8af834822f2361a5cf6"

//go:embed donor/coupon_public.html
var frozenCouponPublicTemplate string

type frozenCouponPageState struct {
	Coupon           couponPublicView
	Products         []publicCouponProduct
	Claimable        bool
	IsWechat         bool
	Claimed          bool
	ShowProducts     bool
	UserLimitReached bool
	DisplayState     string
	ValidityText     string
	Message          string
}

type couponPublicView struct {
	Name                string
	DiscountAmountTotal int64
	Instructions        string
	PublicSlug          string
}

func mustFrozenCouponPublicPage() *template.Template {
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenCouponPublicTemplate)))
	if actual != frozenCouponPublicTemplateSHA256 {
		panic("frozen coupon public donor hash mismatch")
	}
	// The donor uses Jinja's small conditional/range subset. This is a fixed
	// adapter of that syntax to html/template, not a replacement page. Keep the
	// substitution list next to the exact donor bytes so a future donor change
	// fails parsing or the digest check instead of silently changing the UI.
	source := strings.NewReplacer(
		`{{ state.coupon.name or "优惠券" }}`, `{{.Coupon.Name}}`,
		`{% set coupon = state.coupon or {} %}`, ``,
		`{% set products = state.products or coupon.products or [] %}`, ``,
		`{% set discount = coupon.discount_amount_total or 0 %}`, ``,
		`{{ coupon.name or "优惠券" }}`, `{{.Coupon.Name}}`,
		`{{ "%.2f"|format(discount / 100) }}`, `{{printf "%.2f" (cny .Coupon.DiscountAmountTotal)}}`,
		`{{ state.validity_text or coupon.validity_text or "请在有效期内使用" }}`, `{{.ValidityText}}`,
		`{{ products|length }}`, `{{len .Products}}`,
		`{{ public_slug }}`, `{{.Coupon.PublicSlug}}`,
		`{% set claimable = state.claimable and is_wechat and identity_ready %}`, ``,
		`{% if not is_wechat %}`, `{{if not .IsWechat}}`,
		`{% if not claimable %}`, `{{if not .Claimable}}`,
		`{% if not is_wechat %}请使用微信打开此页面后领取优惠券`, `{{if not .IsWechat}}请使用微信打开此页面后领取优惠券`,
		`{% elif state.user_limit_reached %}`, `{{else if .UserLimitReached}}`,
		`{% elif state.display_state == "sold_out" %}`, `{{else if eq .DisplayState "sold_out"}}`,
		`{% elif state.display_state == "scheduled" %}`, `{{else if eq .DisplayState "scheduled"}}`,
		`{% elif state.display_state in ["ended", "stopped", "archived"] %}`, `{{else if terminalCouponState .DisplayState}}`,
		`{% elif state.claimed %}`, `{{else if .Claimed}}`,
		`{% else %}`, `{{else}}`,
		`{% endif %}`, `{{end}}`,
		`{% if state.claimed %}领取成功，可选择下方商品使用{% else %}{{ state.message or "" }}{% endif %}`, `{{if .Claimed}}领取成功，可选择下方商品使用{{else}}{{.Message}}{{end}}`,
		`{% if state.claimed or state.user_claim_count %}`, `{{if .ShowProducts}}`,
		`{% for product in products %}`, `{{range .Products}}`,
		`{{ product.title or product.name }}`, `{{.Name}}`,
		`{{ "周期商品" if product.product_type == "service_period" else "普通商品" }}`, `{{.KindLabel}}`,
		`{{ "%.2f"|format((product.amount_total or product.price_cents or 0) / 100) }}`, `{{printf "%.2f" (cny .PriceMinor)}}`,
		`{% if not product.available %}`, `{{if not .Available}}`,
		`{{ product.purchase_url if product.available else '#' }}`, `{{if .Available}}{{.URL}}{{else}}#{{end}}`,
		`{{ "立即使用" if product.available else "暂不可售" }}`, `{{if .Available}}立即使用{{else}}暂不可售{{end}}`,
		`{% endfor %}`, `{{end}}`,
		`{% if coupon.instructions %}`, `{{if .Coupon.Instructions}}`,
		`{{ coupon.instructions }}`, `{{.Coupon.Instructions}}`,
	).Replace(frozenCouponPublicTemplate)
	return template.Must(template.New("frozen-coupon-public").Funcs(template.FuncMap{
		"cny":                 func(value int64) float64 { return float64(value) / 100 },
		"terminalCouponState": func(value string) bool { return value == "ended" || value == "stopped" || value == "archived" },
	}).Parse(source))
}

var publicCouponPage = mustFrozenCouponPublicPage()
