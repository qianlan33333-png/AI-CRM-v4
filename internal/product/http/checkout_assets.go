package http

import (
	"embed"
	"net/http"
	"path"
)

//go:embed assets/wechat.svg assets/alipay.svg assets/chevron-right.svg assets/chevron-down.svg
var checkoutIcons embed.FS

func serveCheckoutIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	raw, err := checkoutIcons.ReadFile("assets/" + path.Base(r.URL.Path))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(raw)
}
