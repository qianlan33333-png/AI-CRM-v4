package adminops

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type CPUProfileHandler struct {
	service  *CPUProfileService
	security InspectionSecurity
}

func NewCPUProfileHandler(service *CPUProfileService, security InspectionSecurity) (*CPUProfileHandler, error) {
	if service == nil || security == nil {
		return nil, ErrInspectionInvalid
	}
	return &CPUProfileHandler{service, security}, nil
}
func (h *CPUProfileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	p, err := h.security.ReadPrincipal(r.Context(), r)
	if err != nil || !inspectionSuperAdmin(p) {
		inspectionJSON(w, 403, map[string]string{"error": "permission_denied"})
		return
	}
	if r.Method != "GET" {
		p, err = h.security.AuthorizeCSRF(r.Context(), r)
		if err != nil || !inspectionSuperAdmin(p) {
			inspectionJSON(w, 403, map[string]string{"error": "csrf_or_permission_denied"})
			return
		}
	}
	const base = "/api/admin/ops-diagnostics/cpu-profiles"
	path := strings.TrimSuffix(r.URL.Path, "/")
	if r.URL.RawQuery != "" {
		cpuProfileResponse(w, nil, ErrInspectionInvalid)
		return
	}
	switch {
	case path == base && r.Method == "POST":
		if err = decodeEmptyCPUProfileRequest(r); err != nil {
			cpuProfileResponse(w, nil, err)
			return
		}
		out, e := h.service.Capture(r.Context(), p.InternalID, r.Header.Get("Idempotency-Key"))
		cpuProfileResponse(w, out, e)
	case path == base && r.Method == "GET":
		out, e := h.service.List(r.Context())
		cpuProfileResponse(w, map[string]any{"items": out, "enabled": h.service.enabled, "target": "api", "duration_seconds": 5, "worker_coverage": "not_supported"}, e)
	case strings.HasPrefix(path, base+"/") && r.Method == "GET":
		id := strings.TrimPrefix(path, base+"/")
		if strings.HasSuffix(id, "/download") {
			id = strings.TrimSuffix(id, "/download")
			data, e := h.service.Download(r.Context(), id)
			if e != nil {
				cpuProfileResponse(w, nil, e)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.cpu.pprof"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		out, e := h.service.Get(r.Context(), id)
		cpuProfileResponse(w, out, e)
	default:
		inspectionJSON(w, 404, map[string]string{"error": "not_found"})
	}
}

func decodeEmptyCPUProfileRequest(r *http.Request) error {
	if r.Body == nil {
		return ErrInspectionInvalid
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return ErrInspectionInvalid
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil || len(object) != 0 {
		return ErrInspectionInvalid
	}
	return nil
}
func cpuProfileResponse(w http.ResponseWriter, value any, err error) {
	if err == nil {
		inspectionJSON(w, 200, value)
		return
	}
	status, code := 503, "cpu_profile_unavailable"
	switch {
	case errors.Is(err, ErrCPUProfileDisabled):
		code = "cpu_profile_disabled"
	case errors.Is(err, ErrCPUProfileBusy):
		status, code = 409, "cpu_profile_busy"
	case errors.Is(err, ErrCPUProfileCapacity):
		status, code = 429, "cpu_profile_capacity"
		w.Header().Set("Retry-After", "3600")
	case errors.Is(err, ErrInspectionRateLimited):
		status, code = 429, "cpu_profile_hourly_limit"
		w.Header().Set("Retry-After", "3600")
	case errors.Is(err, ErrCPUProfileExpired):
		status, code = 410, "cpu_profile_expired"
	case errors.Is(err, ErrInspectionInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, ErrInspectionConflict):
		status, code = 409, "idempotency_conflict"
	case errors.Is(err, ErrInspectionNotFound):
		status, code = 404, "not_found"
	}
	inspectionJSON(w, status, map[string]string{"error": code})
}
