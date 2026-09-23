package adminops

import (
	"context"
	"encoding/json"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformdiagnostics "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type InspectionSecurity interface {
	ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type InspectionHandler struct {
	service  *InspectionService
	security InspectionSecurity
	manual   opsport.ManualInspectionEnqueuer
}

func NewInspectionHandler(service *InspectionService, security InspectionSecurity) (*InspectionHandler, error) {
	if service == nil || security == nil {
		return nil, ErrInspectionInvalid
	}
	return &InspectionHandler{service: service, security: security}, nil
}
func (h *InspectionHandler) BindManualEnqueuer(enqueuer opsport.ManualInspectionEnqueuer) error {
	if enqueuer == nil || h.manual != nil {
		return ErrInspectionInvalid
	}
	h.manual = enqueuer
	return nil
}
func (h *InspectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	p, e := h.security.ReadPrincipal(r.Context(), r)
	if e != nil || !inspectionSuperAdmin(p) {
		inspectionJSON(w, 403, map[string]string{"error": "permission_denied"})
		return
	}
	if r.Method != "GET" {
		p, e = h.security.AuthorizeCSRF(r.Context(), r)
		if e != nil || !inspectionSuperAdmin(p) {
			inspectionJSON(w, 403, map[string]string{"error": "csrf_or_permission_denied"})
			return
		}
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/admin/ops-diagnostics/client-events" && r.Method == "POST":
		var body struct {
			Code string `json:"code"`
		}
		if e = decodeInspectionBody(r, &body); e != nil || (body.Code != "frontend_error" && body.Code != "frontend_unhandled_rejection") {
			inspectionResponse(w, nil, ErrInspectionInvalid)
			return
		}
		correlation := platformdiagnostics.FromContext(r.Context())
		if correlation.RequestID == "" {
			inspectionResponse(w, nil, ErrInspectionNotFound)
			return
		}
		e = h.service.RecordDiagnosticObservation(r.Context(), opsport.DiagnosticObservation{Component: "frontend", Code: body.Code, Correlation: correlation.RequestID, RouteTemplate: "/admin/{page}"})
		inspectionResponse(w, map[string]bool{"recorded": e == nil}, e)
	case path == "/api/admin/ops-diagnostics" && r.Method == "GET":
		out, e := h.service.DiagnosticsByCorrelation(r.Context(), r.URL.Query().Get("correlation"))
		inspectionResponse(w, map[string]any{"items": out, "scope": "recorded_application_errors"}, e)
	case path == "/api/admin/ops-inspections" && r.Method == "GET":
		out, e := h.service.Overview(r.Context())
		inspectionResponse(w, out, e)
	case path == "/api/admin/ops-inspections/catalog" && r.Method == "GET":
		inspectionResponse(w, map[string]any{"items": InspectionCatalog()}, nil)
	case path == "/api/admin/ops-inspections/reports" && r.Method == "GET":
		out, e := h.service.Reports(r.Context())
		inspectionResponse(w, map[string]any{"items": out}, e)
	case path == "/api/admin/ops-inspections/runs" && r.Method == "POST":
		// HTTP accepts a durable command; only the registered River worker scans.
		var body struct{}
		if e = decodeInspectionBody(r, &body); e != nil {
			inspectionResponse(w, nil, e)
			return
		}
		if h.manual == nil {
			inspectionResponse(w, nil, errors.New("manual inspection queue unavailable"))
			return
		}
		out, e := h.manual.EnqueueManualInspection(r.Context(), p.InternalID, r.Header.Get("Idempotency-Key"))
		if e != nil {
			inspectionResponse(w, nil, e)
			return
		}
		inspectionJSON(w, http.StatusAccepted, out)
	case strings.HasPrefix(path, "/api/admin/ops-inspections/commands/") && r.Method == "GET":
		jobID, e := strconv.ParseInt(strings.TrimPrefix(path, "/api/admin/ops-inspections/commands/"), 10, 64)
		if e != nil || jobID < 1 {
			inspectionResponse(w, nil, ErrInspectionInvalid)
			return
		}
		out, e := h.service.ManualInspectionCommand(r.Context(), p.InternalID, jobID)
		inspectionResponse(w, out, e)
	case strings.HasPrefix(path, "/api/admin/ops-inspections/runs/") && r.Method == "GET":
		id, e := strconv.ParseInt(strings.TrimPrefix(path, "/api/admin/ops-inspections/runs/"), 10, 64)
		if e != nil || id < 1 {
			inspectionResponse(w, nil, ErrInspectionInvalid)
			return
		}
		out, e := h.service.Run(r.Context(), id)
		inspectionResponse(w, out, e)
	case strings.HasPrefix(path, "/api/admin/ops-inspections/issues/") && r.Method == "PATCH":
		id, e := strconv.ParseInt(strings.TrimPrefix(path, "/api/admin/ops-inspections/issues/"), 10, 64)
		if e != nil || id < 1 {
			inspectionResponse(w, nil, ErrInspectionInvalid)
			return
		}
		var body struct {
			Version int64  `json:"version"`
			Status  string `json:"status"`
		}
		if e = decodeInspectionBody(r, &body); e != nil {
			inspectionResponse(w, nil, e)
			return
		}
		e = h.service.UpdateIssue(r.Context(), id, body.Version, p.InternalID, body.Status, r.Header.Get("Idempotency-Key"))
		inspectionResponse(w, map[string]any{"ok": e == nil}, e)
	default:
		inspectionJSON(w, 404, map[string]string{"error": "not_found"})
	}
}
func inspectionSuperAdmin(p accessdomain.Principal) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, r := range p.Roles {
		if r == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func decodeInspectionBody(r *http.Request, out any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 4097))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return ErrInspectionInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInspectionInvalid
	}
	return nil
}
func inspectionResponse(w http.ResponseWriter, value any, e error) {
	if e == nil {
		inspectionJSON(w, 200, value)
		return
	}
	status, code := 503, "source_unavailable"
	switch {
	case errors.Is(e, ErrInspectionRateLimited):
		status, code = http.StatusTooManyRequests, "inspection_hourly_limit"
		w.Header().Set("Retry-After", "3600")
	case errors.Is(e, ErrInspectionInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(e, ErrInspectionConflict):
		status, code = 409, "version_or_idempotency_conflict"
	case errors.Is(e, ErrInspectionNotFound):
		status, code = 404, "not_found"
	}
	inspectionJSON(w, status, map[string]string{"error": code})
}
func inspectionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
