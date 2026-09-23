package adminops

import (
	"net/http"
	"strings"
)

type RetentionHandler struct {
	service  *RetentionService
	security InspectionSecurity
}

func NewRetentionHandler(s *RetentionService, security InspectionSecurity) (*RetentionHandler, error) {
	if s == nil || security == nil {
		return nil, ErrInspectionInvalid
	}
	return &RetentionHandler{s, security}, nil
}
func (h *RetentionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	p, e := h.security.ReadPrincipal(r.Context(), r)
	if e != nil || !inspectionSuperAdmin(p) {
		inspectionJSON(w, 403, map[string]string{"error": "permission_denied"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/admin/ops-retention/resources" && r.Method == http.MethodGet {
		if r.URL.RawQuery != "" {
			inspectionJSON(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		out, e := h.service.Resources()
		inspectionResponse(w, out, e)
		return
	}
	if path == "/api/admin/ops-retention" && r.Method == http.MethodGet {
		inspectionJSON(w, 200, map[string]any{"items": h.service.Policies()})
		return
	}
	if path == "/api/admin/ops-retention/runs" && r.Method == http.MethodGet {
		out, e := h.service.Runs(r.Context())
		inspectionResponse(w, map[string]any{"items": out, "host": h.service.HostLatest(r.Context()), "release": h.service.ReleaseLatest(r.Context())}, e)
		return
	}
	if path == "/api/admin/ops-retention/preview" && r.Method == http.MethodGet {
		out, e := h.service.PreviewRetention(r.Context(), r.URL.Query().Get("policy"))
		inspectionResponse(w, out, e)
		return
	}
	// Execution is scheduled with immutable server policies, not arbitrary
	// web requests. The preview endpoint cannot mutate production state.
	inspectionJSON(w, 404, map[string]string{"error": "not_found"})
}
