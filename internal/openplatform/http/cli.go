package http

import (
	"net/http"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	cliport "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

func (handler *Handler) cliPrincipal(w http.ResponseWriter, r *http.Request) (accessdomain.MachinePrincipal, bool) {
	p, e := handler.authenticateMachine(r)
	if e != nil {
		writeV1Error(w, http.StatusUnauthorized, openport.ErrorAuthentication, requestID(r))
		return p, false
	}
	if e = handler.allowMachineRequest(r, p); e != nil {
		setMachineRetryAfter(w, e)
		writeV1Error(w, statusForMachineError(e), operationErrorForMachineError(e), requestID(r))
		return p, false
	}
	return p, true
}

// Context is a V2 self-discovery endpoint. It adds no business capability to V1.
func (handler *Handler) cliContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := handler.cliPrincipal(w, r)
	if !ok {
		return
	}
	available, e := handler.operations.Available(r.Context(), p)
	if e != nil {
		writeV1Error(w, 503, openport.ErrorDependencyUnavailable, requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"version": 2, "kind": "machine", "server_origin": handler.publicOrigin, "client_id": p.ClientID, "corp_id": p.CorpID, "owner_scope": p.OwnerScope, "scopes": p.Scopes, "capabilities": p.Capabilities, "operations": available, "authorization_at_execution": true})
}

// CanI reports action availability without impersonation or invoking a business command.
// Where no Owner-specific checker exists, it explicitly refuses to assert resource access.
func (handler *Handler) cliCanI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := handler.cliPrincipal(w, r)
	if !ok {
		return
	}
	raw, e := requestJSONInput(r)
	if e != nil {
		writeV1Error(w, 400, openport.ErrorValidation, requestID(r))
		return
	}
	input, e := openport.ParsePermissionQuery(raw)
	if e != nil {
		writeV1Error(w, 400, openport.ErrorValidation, requestID(r))
		return
	}
	op, known := cliport.Find(input.OperationID)
	decision := "denied"
	reason := "not_registered_for_machine"
	if known && op.Binding != nil && op.Binding.Auth == "machine" {
		available, e := handler.operations.Available(r.Context(), p)
		if e != nil {
			writeV1Error(w, 503, openport.ErrorDependencyUnavailable, requestID(r))
			return
		}
		for _, a := range available {
			if string(a.OperationID) == input.OperationID {
				decision = "requires_resource_check"
				reason = "action_granted_owner_validation_required"
				break
			}
		}
		if decision != "denied" {
			if checker, ok := handler.operations.(openport.PermissionChecker); ok {
				result, e := checker.CheckPermission(r.Context(), p, openport.OperationID(input.OperationID), input.Resource)
				if e != nil {
					writeV1Error(w, 503, openport.ErrorDependencyUnavailable, requestID(r))
					return
				}
				decision = result.Decision
				reason = result.Reason
			}
		}
	}
	writeJSON(w, 200, map[string]any{"operation_id": input.OperationID, "decision": decision, "reason": reason, "authorization_at_execution": true, "provider_conditions": "checked_by_owner_at_execution"})
}
