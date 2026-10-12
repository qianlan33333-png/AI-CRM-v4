package http

import (
	"context"
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	nethttp "net/http"
	"strconv"
)

// Uses the same permission and CSRF boundary as phone-reveal. Ordinary scoped
// sidebar access only receives the masked projection, never full contact data.
func (h *Handler) revealContact(w nethttp.ResponseWriter, r *nethttp.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	actor, err := h.csrf.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !hasRole(actor, accessdomain.RoleAdmin) && !hasRole(actor, accessdomain.RoleSuperAdmin) {
		h.writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	id, err := positiveID(r.PathValue("customer_id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	if h.contacts == nil {
		h.writeError(w, customerapp.ErrNotFound)
		return
	}
	var profile customerport.ContactProfile
	err = h.uow.Within(r.Context(), func(ctx context.Context) error {
		canonical, e := h.canonical.ResolveCanonicalCustomer(ctx, customerdomain.CustomerID(id))
		if e != nil {
			return e
		}
		profile, e = h.contacts.ReadContactWithin(ctx, canonical.CustomerID, true)
		if e != nil {
			return e
		}
		key, e := revealAuditKey(actor.InternalID, int64(canonical.CustomerID))
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]string{"purpose": "contact_query"})
		_, e = h.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.contact_revealed", ActorType: string(actor.Kind), ActorID: strconv.FormatInt(actor.InternalID, 10), ResourceType: "customer", ResourceID: strconv.FormatInt(int64(canonical.CustomerID), 10), Payload: payload})
		return e
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, profile)
}
