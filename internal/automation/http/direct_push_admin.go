package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type DirectPushAdminHandler struct {
	service  automationport.DirectPushAdminService
	security RequestSecurity
}

func NewDirectPushAdminHandler(service automationport.DirectPushAdminService, security RequestSecurity) (*DirectPushAdminHandler, error) {
	if service == nil || security == nil {
		return nil, errors.New("audience direct push admin dependencies are required")
	}
	return &DirectPushAdminHandler{service: service, security: security}, nil
}

func (h *DirectPushAdminHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	packageID, records, ok := directPushAdminRoute(request.URL.Path)
	if !ok {
		errorJSON(writer, http.StatusNotFound, "audience_push_config_not_found")
		return
	}
	if records {
		if request.Method != http.MethodGet {
			method(writer, "GET")
			return
		}
		if _, ok = h.principal(writer, request, false); !ok {
			return
		}
		items, err := h.service.DirectPushAdminRecords(request.Context(), packageID, 100)
		if err != nil {
			directPushError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
		return
	}
	switch request.Method {
	case http.MethodGet:
		if _, ok = h.principal(writer, request, false); !ok {
			return
		}
		config, err := h.service.DirectPushConfig(request.Context(), packageID)
		if err != nil {
			directPushError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": config})
	case http.MethodPut:
		principal, allowed := h.principal(writer, request, true)
		if !allowed {
			return
		}
		var input struct {
			Enabled           bool  `json:"enabled"`
			MaxPerCustomer24h int   `json:"max_per_customer_24h"`
			ExpectedVersion   int64 `json:"expected_version"`
		}
		if decode(request, &input) != nil {
			errorJSON(writer, http.StatusBadRequest, "invalid_audience_push_config")
			return
		}
		key, valid := requestKey(writer, request)
		if !valid {
			return
		}
		config, err := h.service.ConfigureDirectPush(request.Context(), automationport.DirectPushConfigCommand{PackageID: packageID, Enabled: input.Enabled, MaxPerCustomer24h: input.MaxPerCustomer24h, ExpectedVersion: input.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			directPushError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": config})
	default:
		method(writer, "GET, PUT")
	}
}

func (h *DirectPushAdminHandler) principal(writer http.ResponseWriter, request *http.Request, write bool) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(request.Context(), request)
	if err != nil {
		errorJSON(writer, http.StatusUnauthorized, "unauthorized")
		return principal, false
	}
	if !role(principal, write) {
		errorJSON(writer, http.StatusForbidden, "forbidden")
		return principal, false
	}
	if write {
		if _, err = h.security.AuthorizeCSRF(request.Context(), request); err != nil {
			errorJSON(writer, http.StatusForbidden, "csrf_required")
			return principal, false
		}
	}
	return principal, true
}

func directPushAdminRoute(path string) (int64, bool, bool) {
	const prefix = "/api/admin/ai-audience/packages/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false, false
	}
	records := strings.HasSuffix(path, "/direct-pushes")
	suffix := "/direct-push"
	if records {
		suffix = "/direct-pushes"
	} else if !strings.HasSuffix(path, suffix) {
		return 0, false, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, records, err == nil && id > 0
}

var _ http.Handler = (*DirectPushAdminHandler)(nil)
