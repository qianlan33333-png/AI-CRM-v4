package http

import (
	"html/template"
	"strconv"
)

// The public detail has no customer-specific facts. Keeping its document
// separate from checkout lets the first image start before any OAuth or
// purchase-status request and avoids shipping checkout code to visitors who
// only want to read the product.
var publicProductDetailPage = template.Must(template.New("public-product-detail").Funcs(template.FuncMap{
	"yuan": func(minor int64) string {
		return strconv.FormatInt(minor/100, 10) + "." + twoDigits(minor%100)
	},
}).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>{{.Product.Name}}</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#fff;color:#20242b;font:15px/1.5 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}
.card{max-width:560px;min-height:100dvh;margin:auto;padding:0 0 calc(108px + env(safe-area-inset-bottom));background:#fff}
.visually-hidden{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
.detail-image{display:block;width:100%;height:auto;background:#f5f6f8}
.checkout-footer{position:fixed;bottom:0;left:50%;transform:translateX(-50%);width:100%;max-width:560px;background:#fff;border-top:1px solid #ebedf0;padding:14px 18px calc(14px + env(safe-area-inset-bottom));display:flex;align-items:center;gap:18px;z-index:2}
.footer-price{min-width:100px;flex:1}.footer-price .label{display:block;color:#858b95;font-size:12px}.footer-price strong{display:block;font-size:24px;line-height:1.35;font-variant-numeric:tabular-nums}
.buy{flex:1.1;min-height:48px;border-radius:13px;background:#3268ff;color:#fff;font-size:17px;font-weight:600;line-height:1.4;padding:12px 18px;text-align:center;text-decoration:none}.buy:focus-visible{outline:3px solid #a6bfff;outline-offset:3px}
@media(max-width:360px){.footer-price{min-width:0}.buy{min-width:0;padding-inline:12px}}
</style>{{if .Presentation.Enabled}}<link rel="stylesheet" href="{{.Presentation.StylesheetURL}}"><script type="module" src="{{.Presentation.HostURL}}"></script>{{end}}</head>
<body><main class="card" data-v3-public-commerce data-public-commerce-route="detail"><h1 class="visually-hidden">{{.Product.Name}}</h1>{{if gt .Product.ServicePeriodDurationDays 0}}<span class="visually-hidden">服务周期 {{.Product.ServicePeriodDurationDays}} 天</span>{{end}}<section id="detailContent">{{range $index, $image := .Product.Images}}<img class="detail-image" src="{{$image}}" alt="商品详情" decoding="async" {{if eq $index 0}}loading="eager" fetchpriority="high"{{else}}loading="lazy"{{end}}>{{end}}<footer class="checkout-footer"><div class="footer-price"><span class="label">价格</span><strong>¥{{yuan .Product.PriceMinor}}</strong></div><a class="buy" href="{{.Product.PaymentPath}}">{{.Product.BuyButtonText}}</a></footer></section></main></body></html>`))

func twoDigits(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}
