package http

import (
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"net/http"
)

func (h *Handler) SetShopRefresh(service shopport.ProductRefresh) { h.shop = service }
func (h *Handler) shopRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/products/wechat-shop/refresh" {
		if _, ok := h.write(w, r); !ok {
			return
		}
		if h.shop == nil {
			writeError(w, 503, "shop_unavailable")
			return
		}
		status, err := h.shop.SubmitProductRefresh(r.Context())
		if err != nil {
			writeError(w, 503, "shop_unavailable")
			return
		}
		writeJSON(w, 202, status)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/products/wechat-shop/refresh-status" {
		principal, err := h.security.Authenticate(r.Context(), r)
		if err != nil {
			writeError(w, 401, "unauthorized")
			return
		}
		if !canWrite(principal) {
			writeError(w, 403, "permission_denied")
			return
		}
		if h.shop == nil {
			writeError(w, 503, "shop_unavailable")
			return
		}
		status, err := h.shop.ProductRefreshStatus(r.Context())
		if err != nil {
			writeError(w, 503, "shop_unavailable")
			return
		}
		writeJSON(w, 200, status)
		return
	}
	http.NotFound(w, r)
}
