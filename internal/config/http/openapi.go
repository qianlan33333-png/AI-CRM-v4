package http

import (
	_ "embed"
	"net/http"
)

// embeddedOpenAPISpec is copied mechanically from api/openapi.yaml at release
// build time. It makes the authenticated documentation download independent
// of a source checkout and must remain byte-for-byte equal to that source.
//
//go:embed openapi.yaml
var embeddedOpenAPISpec []byte

func (h *Handler) openapi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(embeddedOpenAPISpec)
}
