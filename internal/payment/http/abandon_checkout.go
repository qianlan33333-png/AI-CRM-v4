package paymenthttp

import (
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"net/http"
	"strconv"
)

func (handler *Handler) abandonCheckout(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(r.Context(), r)
	if err != nil || principal.Kind != accessdomain.KindAdmin || (!hasRole(principal.Roles, accessdomain.RoleAdmin) && !principal.IsSuperAdmin()) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		EvidenceDigest   string `json:"evidence_digest"`
		ConfirmedNoDebit bool   `json:"confirmed_no_debit"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	app, ok := handler.app.(paymentport.CheckoutAbandoner)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	err = app.AbandonCheckout(r.Context(), paymentport.AbandonCheckoutCommand{PaymentID: id, ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), EvidenceDigest: body.EvidenceDigest, ConfirmedNoDebit: body.ConfirmedNoDebit})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_id": id, "checkout_abandoned": true, "provider_outcome_changed": false})
}
