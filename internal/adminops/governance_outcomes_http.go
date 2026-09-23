package adminops

import (
	"bytes"
	"encoding/json"
	"errors"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type GovernanceOutcomesHandler struct {
	service  *GovernanceOutcomesService
	security InspectionSecurity
}

func NewGovernanceOutcomesHandler(service *GovernanceOutcomesService, security InspectionSecurity) (*GovernanceOutcomesHandler, error) {
	if service == nil || security == nil {
		return nil, ErrInspectionInvalid
	}
	return &GovernanceOutcomesHandler{service, security}, nil
}
func (h *GovernanceOutcomesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	const base = "/api/admin/ops-governance"
	path := strings.TrimSuffix(r.URL.Path, "/")
	if r.Method == "GET" && (path == base+"/outcomes" || path == base+"/episodes") {
		query, parseErr := url.ParseQuery(r.URL.RawQuery)
		if parseErr != nil {
			governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
			return
		}
		for k, v := range query {
			if len(v) != 1 || !inGovernanceEnum(k, "from", "to", "before_id", "limit") || (path == base+"/outcomes" && (k == "before_id" || k == "limit")) {
				governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
				return
			}
		}
		var from, to time.Time
		if query.Has("from") {
			from, err = time.Parse(time.RFC3339, query.Get("from"))
			if err != nil {
				governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
				return
			}
		}
		if query.Has("to") {
			to, err = time.Parse(time.RFC3339, query.Get("to"))
			if err != nil {
				governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
				return
			}
		}
		if path == base+"/outcomes" {
			out, e := h.service.Outcomes(r.Context(), from, to)
			governanceOutcomesResponse(w, out, e)
			return
		}
		limit, before := 50, int64(0)
		if query.Has("limit") {
			limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil {
				governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
				return
			}
		}
		if query.Has("before_id") {
			before, err = strconv.ParseInt(query.Get("before_id"), 10, 64)
			if err != nil {
				governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
				return
			}
		}
		out, e := h.service.Episodes(r.Context(), from, to, before, limit)
		governanceOutcomesResponse(w, out, e)
		return
	}
	if r.URL.RawQuery != "" {
		governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
		return
	}
	if strings.HasPrefix(path, base+"/episodes/") {
		suffix := strings.TrimPrefix(path, base+"/episodes/")
		write := strings.HasSuffix(suffix, "/attribution")
		if write {
			suffix = strings.TrimSuffix(suffix, "/attribution")
		}
		id, e := strconv.ParseInt(suffix, 10, 64)
		if e != nil || id < 1 {
			governanceOutcomesResponse(w, nil, ErrInspectionInvalid)
			return
		}
		if !write && r.Method == "GET" {
			out, e := h.service.Episode(r.Context(), id)
			governanceOutcomesResponse(w, out, e)
			return
		}
		if write && r.Method == "PUT" {
			var c opsport.IncidentAttributionCommand
			if e = decodeGovernanceAttribution(r, &c); e != nil {
				governanceOutcomesResponse(w, nil, e)
				return
			}
			out, e := h.service.Attribute(r.Context(), id, p.InternalID, r.Header.Get("Idempotency-Key"), c)
			governanceOutcomesResponse(w, out, e)
			return
		}
	}
	inspectionJSON(w, 404, map[string]string{"error": "not_found"})
}
func decodeGovernanceAttribution(r *http.Request, out *opsport.IncidentAttributionCommand) error {
	if r.Body == nil {
		return ErrInspectionInvalid
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return ErrInspectionInvalid
	}
	// Token walk rejects duplicate keys as well as null/arrays. These flat, fixed
	// DTOs cannot carry arbitrary conclusion text or nested diagnostic payloads.
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInspectionInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return ErrInspectionInvalid
		}
		key, ok := k.(string)
		if !ok || seen[key] {
			return ErrInspectionInvalid
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return ErrInspectionInvalid
		}
	}
	if _, err = d.Token(); err != nil {
		return ErrInspectionInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInspectionInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || !validateAttribution(*out) {
		return ErrInspectionInvalid
	}
	return nil
}
func governanceOutcomesResponse(w http.ResponseWriter, value any, err error) {
	if err == nil {
		inspectionJSON(w, 200, value)
		return
	}
	status, code := 503, "governance_outcomes_unavailable"
	switch {
	case errors.Is(err, ErrInspectionInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, ErrInspectionConflict):
		status, code = 409, "version_or_idempotency_conflict"
	case errors.Is(err, ErrInspectionNotFound):
		status, code = 404, "not_found"
	}
	inspectionJSON(w, status, map[string]string{"error": code})
}
