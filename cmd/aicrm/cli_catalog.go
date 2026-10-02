package main

import (
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	cliport "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"io"
	"net/http"
	"strings"
)

// Human discovery never promotes a human session to a machine principal.
func mountCLIHuman(next http.Handler, auth accessAuthentication) http.Handler {
	mux := http.NewServeMux()
	serve := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, e := auth.Authenticate(r.Context(), cookieValue(r, "aicrm_admin_session"))
		if e != nil || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) || p.InternalID < 1 {
			http.Error(w, "authentication_required", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			catalog := cliport.Load()
			for i := range catalog.Operations {
				op := &catalog.Operations[i]
				if op.Binding != nil && op.Binding.Auth == "machine" {
					op.Binding = nil
					op.BlockedReason = "independent machine identity required"
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"kind": "human", "actor_id": p.InternalID, "roles": p.Roles, "catalog": catalog, "authorization_at_execution": true})
			return
		}
		// Can-I is a read, but its POST transport still observes normal session CSRF.
		if _, e := auth.AuthorizeCSRF(r.Context(), cookieValue(r, "aicrm_admin_session"), cookieValue(r, "aicrm_admin_csrf"), r.Header.Get("X-CSRF-Token")); e != nil {
			http.Error(w, "csrf_required", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		raw, err := io.ReadAll(r.Body)
		if err != nil || !openport.ValidJSONObject(raw) {
			http.Error(w, "invalid_request", 400)
			return
		}
		input, err := openport.ParsePermissionQuery(raw)
		if err != nil {
			http.Error(w, "invalid_request", 400)
			return
		}
		op, ok := cliport.Find(input.OperationID)
		decision := "denied"
		reason := "not_registered_for_human"
		if ok && op.Binding != nil && op.Binding.Auth == "human" {
			decision = "requires_resource_check"
			reason = "owner_route_authorization_required"
			isViewer := false
			writeRole := false
			for _, role := range p.Roles {
				if role == accessdomain.RoleViewer {
					isViewer = true
				}
				if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
					writeRole = true
				}
			}
			if isViewer && !writeRole && op.Binding.Method != "GET" {
				decision = "denied"
				reason = "viewer_read_only"
			}
			if (strings.Contains(op.Binding.Path, "/open-platform/") || strings.Contains(op.Binding.Path, "/ops-")) && !p.IsSuperAdmin() {
				decision = "denied"
				reason = "super_admin_required"
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"operation_id": input.OperationID, "decision": decision, "reason": reason, "authorization_at_execution": true})
	}
	mux.HandleFunc("GET /api/admin/cli/capabilities", serve)
	mux.HandleFunc("POST /api/admin/cli/can-i", serve)
	mux.Handle("/", next)
	return mux
}
