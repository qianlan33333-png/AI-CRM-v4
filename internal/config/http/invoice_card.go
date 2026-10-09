package http

import (
	"errors"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"net/http"
)

func (h *Handler) WithInvoiceCard(service p.CardSetupAdmin) { h.invoiceCard = service }
func (h *Handler) invoiceCardSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.invoiceCard == nil {
		writeError(w, 503, "invoice_card_unavailable")
		return
	}
	var out p.CardSetup
	var err error
	switch r.Method {
	case http.MethodGet:
		if _, ok := h.read(w, r); !ok {
			return
		}
		out, err = h.invoiceCard.ReadCardSetup(r.Context())
	case http.MethodPost:
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		out, err = h.invoiceCard.AcceptCardSetup(r.Context(), actor.InternalID)
	default:
		writeError(w, 405, "method_not_allowed")
		return
	}
	switch {
	case errors.Is(err, p.ErrInvalid):
		writeError(w, 400, "invoice_card_config_incomplete")
	case errors.Is(err, p.ErrConflict):
		writeError(w, 409, "invoice_card_config_not_applied")
	case errors.Is(err, p.ErrPending):
		writeError(w, 409, "invoice_card_pending")
	case errors.Is(err, p.ErrDisabled):
		writeError(w, 409, "invoice_card_not_enabled")
	case err != nil:
		writeError(w, 503, "invoice_card_unavailable")
	default:
		writeJSON(w, 200, out)
	}
}
