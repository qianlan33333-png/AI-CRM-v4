package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type HistoryApplication interface {
	History(context.Context, int64, int, int) ([]HistoryContact, int64, []HistoryAssignee, error)
	Recent(context.Context, int64, int, int64) ([]RecentEntrant, error)
}

type HistoryHTTPHandler struct {
	app      HistoryApplication
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	}
	key []byte
	now func() time.Time
}

func NewHistoryHTTPHandler(app HistoryApplication, security interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}, key []byte) (*HistoryHTTPHandler, error) {
	if app == nil || security == nil || len(key) < 32 {
		return nil, errors.New("channel history HTTP dependencies are required")
	}
	return &HistoryHTTPHandler{app: app, security: security, key: key, now: time.Now}, nil
}

func (handler *HistoryHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	if !channelCatalogReadRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	prefix := "/api/admin/channels/"
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if !strings.HasPrefix(path, prefix) || len(parts) != 2 {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	switch parts[1] {
	case "history":
		handler.history(w, r, id)
	case "contacts":
		handler.contacts(w, r, id)
	default:
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	}
}

func (handler *HistoryHTTPHandler) history(w http.ResponseWriter, r *http.Request, id int64) {
	if !onlyChannelQuery(r, "limit", "offset") || r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	limit, offset := 50, 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
	}
	contacts, total, assignees, err := handler.app.History(r.Context(), id, limit, offset)
	if err != nil {
		handler.appError(w, err)
		return
	}
	cj := make([]map[string]any, 0, len(contacts))
	for _, item := range contacts {
		cj = append(cj, map[string]any{"id": item.ID, "channel_id": item.ChannelID, "source_contact_id": item.SourceContactID, "customer_id": item.CustomerID, "owner_reference": item.OwnerReference, "first_entered_at": item.FirstEnteredAt.UTC().Format(time.RFC3339Nano), "last_entered_at": item.LastEnteredAt.UTC().Format(time.RFC3339Nano), "enter_count": item.EnterCount, "created_at": item.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": item.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	aj := make([]map[string]any, 0, len(assignees))
	for _, item := range assignees {
		aj = append(aj, map[string]any{"id": item.ID, "channel_id": item.ChannelID, "source_assignee_id": item.SourceAssigneeID, "staff_reference": item.StaffReference, "display_name_snapshot": item.DisplayNameSnapshot, "priority": item.Priority, "ratio_percent": item.RatioPercent, "max_scans_24h": item.MaxScans24h, "status": item.Status, "source_created_at": item.SourceCreatedAt.Format("2006-01-02T15:04:05.000000"), "source_updated_at": item.SourceUpdatedAt.Format("2006-01-02T15:04:05.000000")})
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"ok": true, "source": "v1_history", "read_only": true, "real_external_call_executed": false, "channel_id": id, "contacts": cj, "total": total, "limit": limit, "offset": offset, "assignees": aj})
}

func (handler *HistoryHTTPHandler) contacts(w http.ResponseWriter, r *http.Request, id int64) {
	if !onlyChannelQuery(r, "limit", "cursor") || r.ContentLength > 0 {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		limit = value
	}
	offset := int64(0)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		offset = handler.parseCursor(raw, id, limit)
		if offset < 1 {
			writeCatalogError(w, http.StatusUnprocessableEntity, "INVALID_CURSOR")
			return
		}
	}
	items, err := handler.app.Recent(r.Context(), id, limit+1, offset)
	if err != nil {
		handler.appError(w, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	projected := make([]map[string]any, 0, len(items))
	for _, item := range items {
		label := "待匹配历史用户"
		var customerID any
		if item.CustomerID != nil {
			customerID = *item.CustomerID
			label = safeCustomerLabel(*item.CustomerID)
		}
		projected = append(projected, map[string]any{"customer_id": customerID, "display_name": label, "resolution": item.Resolution, "source": item.Source, "enter_count": item.EnterCount, "added_at": item.AddedAt.UTC().Format(time.RFC3339Nano), "last_entered_at": item.LastEnteredAt.UTC().Format(time.RFC3339Nano), "last_interact_at": nil})
	}
	next := ""
	if hasMore && len(items) > 0 {
		next = handler.signCursor(offset+int64(limit), id, limit)
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"channel_id": id, "items": projected, "limit": limit, "has_more": hasMore, "next_cursor": next, "local_projection": true, "provider_execution_eligible": false, "real_external_call_executed": false})
}

type historyCursor struct {
	Before, ChannelID int64
	Limit             int
	Expires           int64
}

func (handler *HistoryHTTPHandler) signCursor(before, id int64, limit int) string {
	raw, _ := json.Marshal(historyCursor{before, id, limit, handler.now().Add(15 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, handler.key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (handler *HistoryHTTPHandler) parseCursor(value string, id int64, limit int) int64 {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return 0
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return 0
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return 0
	}
	mac := hmac.New(sha256.New, handler.key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return 0
	}
	var cursor historyCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.ChannelID != id || cursor.Limit != limit || cursor.Expires < handler.now().Unix() {
		return 0
	}
	return cursor.Before
}
func (handler *HistoryHTTPHandler) appError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCatalogNotFound):
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, ErrInvalidCatalogCommand):
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
	default:
		writeCatalogError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
	}
}
