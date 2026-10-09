package http

import (
	"encoding/json"
	"errors"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	"io"
	"net/http"
)

func (h *Handler) WithInvoiceKeys(stager configport.InvoiceKeyStager) { h.invoiceKeys = stager }
func (h *Handler) invoiceKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	if _, ok := h.mutate(w, r); !ok {
		return
	}
	if h.invoiceKeys == nil {
		writeError(w, 503, "invoice_key_upload_unavailable")
		return
	}
	var cmd struct {
		Key string `json:"key"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if d.Decode(&cmd) != nil {
		writeError(w, 400, "invalid_invoice_key")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid_invoice_key")
		return
	}
	id, e := h.invoiceKeys.StageInvoiceKey(r.Context(), cmd.Key)
	if errors.Is(e, configapp.ErrInvoiceKeyInvalid) {
		writeError(w, 400, "invalid_invoice_key")
		return
	}
	if e != nil {
		writeError(w, 503, "invoice_key_upload_unavailable")
		return
	}
	writeJSON(w, 201, map[string]any{"key_file_id": id, "staged": true})
}
