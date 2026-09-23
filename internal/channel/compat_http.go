package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// LegacyChannelCatalogReader is intentionally narrow. Product forms can read
// the current Channel catalog through this owner boundary; PR04 does not
// create a second channel table or infer channel identity from Product data.
type LegacyChannelCatalogReader interface {
	ListLegacyChannels(context.Context, int32, bool) ([]LegacyChannelListItem, error)
}

type LegacyChannelListItem struct {
	ID                  int64  `json:"id"`
	ChannelName         string `json:"channel_name"`
	ChannelCode         string `json:"channel_code"`
	Status              string `json:"status"`
	AssigneeCount       int32  `json:"assignee_count"`
	ChannelContactCount int32  `json:"channel_contact_count"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

// legacyChannelCatalogAdapter returns an empty, truthful compatibility
// projection until a Channel-owned catalog port is available. It never reads
// acquisition state as if it were the channel definition catalog.
type legacyChannelCatalogAdapter struct{}

func NewLegacyChannelCatalogAdapter() LegacyChannelCatalogReader {
	return legacyChannelCatalogAdapter{}
}

func (legacyChannelCatalogAdapter) ListLegacyChannels(context.Context, int32, bool) ([]LegacyChannelListItem, error) {
	return []LegacyChannelListItem{}, nil
}

type LegacyChannelHTTPHandler struct {
	reader   LegacyChannelCatalogReader
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
		AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

func NewLegacyChannelHTTPHandler(reader LegacyChannelCatalogReader, security interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}) (*LegacyChannelHTTPHandler, error) {
	if reader == nil || security == nil {
		return nil, errors.New("channel catalog compatibility dependencies are required")
	}
	return &LegacyChannelHTTPHandler{reader: reader, security: security}, nil
}

func (handler *LegacyChannelHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if handler == nil || handler.reader == nil || handler.security == nil {
		writeChannelJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	if r.URL.Path != "/api/admin/channels" && r.URL.Path != "/api/admin/channels/" {
		writeChannelJSON(w, http.StatusNotFound, map[string]any{"ok": false, "code": "NOT_FOUND"})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeChannelJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "code": "METHOD_NOT_ALLOWED"})
		return
	}
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil {
		writeChannelJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "code": "UNAUTHORIZED"})
		return
	}
	if !channelCatalogReadRole(principal) {
		writeChannelJSON(w, http.StatusForbidden, map[string]any{"ok": false, "code": "FORBIDDEN"})
		return
	}
	if !onlyChannelQuery(r, "limit", "include_archived") {
		writeChannelJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "code": "MALFORMED_REQUEST"})
		return
	}
	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 32)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeChannelJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "code": "MALFORMED_REQUEST"})
			return
		}
		limit = int32(parsed)
	}
	includeArchived := false
	if raw := r.URL.Query().Get("include_archived"); raw != "" {
		if raw != "true" && raw != "false" {
			writeChannelJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "code": "MALFORMED_REQUEST"})
			return
		}
		includeArchived = raw == "true"
	}
	channels, err := handler.reader.ListLegacyChannels(r.Context(), limit, includeArchived)
	if err != nil {
		writeChannelJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	if channels == nil {
		channels = []LegacyChannelListItem{}
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"ok": true, "channels": channels, "reason": "channels_listed", "source": "ai_crm_next"})
}

func channelCatalogReadRole(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func onlyChannelQuery(r *http.Request, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key, values := range r.URL.Query() {
		if _, ok := set[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func writeChannelJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var _ LegacyChannelCatalogReader = legacyChannelCatalogAdapter{}
