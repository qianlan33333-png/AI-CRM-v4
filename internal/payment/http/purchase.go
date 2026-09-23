package paymenthttp

import (
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/http"
	"strconv"
)

func (h *Handler) purchaseStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, 401, "payment_session_required")
		return
	}
	reader, ok := h.app.(paymentport.PurchaseStatusReader)
	if !ok {
		writeError(w, 503, "capability_not_ready")
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("product_id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_product")
		return
	}
	state, err := reader.PurchaseStatus(r.Context(), cookie.Value, r.URL.Query().Get("product_type"), id)
	if err != nil {
		resultError(w, err)
		return
	}
	result := map[string]any{"purchase_state": state.State, "can_purchase": state.CanPurchase}
	if state.State == "owned" {
		result["completion_action"] = h.paidPurchaseAction(r.Context(), state.PaidOrderID, state.MerchantOrderNo)
	}
	writeJSON(w, 200, result)
}
