// Package http exposes the private, read-only Message Archive Host API.
package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Reader interface {
	CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error)
	CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error)
	ReadPrivateMedia(context.Context, customerdomain.CustomerID, int64) (archiveport.MediaContent, error)
}
type Auditor interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type Handler struct {
	auth   Authenticator
	reader Reader
	audit  Auditor
	uow    platformport.UnitOfWork
	now    func() time.Time
}

func NewHandler(auth Authenticator, reader Reader, audit Auditor, uow platformport.UnitOfWork) (*Handler, error) {
	if auth == nil || reader == nil || audit == nil || uow == nil {
		return nil, errors.New("message archive HTTP dependencies are required")
	}
	return &Handler{auth: auth, reader: reader, audit: audit, uow: uow}, nil
}
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}", h.customerMessages)
	mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}/staff", h.customerStaff)
	mux.HandleFunc("GET /api/admin/message-archive/customers/{customer_id}/media/{media_id}", h.customerMedia)
	return mux
}

func (h *Handler) customerStaff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	principal, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !archiveReadRole(principal) {
		h.writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	customerID, err := positiveID(r.PathValue("customer_id"))
	if err != nil || len(r.URL.Query()) != 0 {
		h.writeError(w, errInvalid)
		return
	}
	items, err := h.reader.CustomerStaff(r.Context(), customerdomain.CustomerID(customerID))
	if err != nil {
		h.writeError(w, err)
		return
	}
	if err = h.auditRead(r.Context(), principal, customerID, len(items), false); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer_id": customerID, "items": items})
}
func (h *Handler) customerMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	principal, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !archiveReadRole(principal) {
		h.writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	id, err := positiveID(r.PathValue("customer_id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !only(r, "limit", "chat_type", "message_type", "direction", "staff_user_id", "start_at", "end_at", "q", "watermark", "after_at", "after_id") {
		h.writeError(w, errInvalid)
		return
	}
	query, err := parseQuery(r, customerdomain.CustomerID(id), h.nowTime())
	if err != nil {
		h.writeError(w, err)
		return
	}
	page, err := h.reader.CustomerMessages(r.Context(), query)
	if err != nil {
		h.writeError(w, err)
		return
	}
	items := visible(query, page)
	if err = h.auditRead(r.Context(), principal, int64(id), len(items), query.Search != ""); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer_id": id, "items": items, "as_of": page.AsOf, "next": next(query, page)})
}

func (h *Handler) customerMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	principal, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !archiveReadRole(principal) {
		h.writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	customerID, err := positiveID(r.PathValue("customer_id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	mediaID, err := positiveID(r.PathValue("media_id"))
	if err != nil || len(r.URL.Query()) != 0 {
		h.writeError(w, errInvalid)
		return
	}
	content, err := h.reader.ReadPrivateMedia(r.Context(), customerdomain.CustomerID(customerID), mediaID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	mimeType := safeImageMIME(content.Kind, content.Data)
	if mimeType == "" {
		h.writeError(w, errInvalid)
		return
	}
	if err = h.auditRead(r.Context(), principal, customerID, 1, false); err != nil {
		h.writeError(w, err)
		return
	}
	// No provider MIME is trusted. The response carries only a recognized
	// raster signature; HTML, SVG, and unknown bytes never reach the page.
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Data)
}

func safeImageMIME(kind string, data []byte) string {
	if kind != "image" {
		return ""
	}
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func archiveReadRole(principal accessdomain.Principal) bool {
	if (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) || principal.InternalID < 1 {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func (h *Handler) auditRead(ctx context.Context, principal accessdomain.Principal, customerID int64, count int, searched bool) error {
	key, err := archiveReadAuditKey()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"returned_count": count, "searched": searched})
	if err != nil {
		return err
	}
	return h.uow.Within(ctx, func(tx context.Context) error {
		_, err := h.audit.Append(tx, platformaudit.Event{IdempotencyKey: key, Action: "message_archive.read", ActorType: string(principal.Kind), ActorID: strconv.FormatInt(principal.InternalID, 10), ResourceType: "customer", ResourceID: strconv.FormatInt(customerID, 10), Payload: payload})
		return err
	})
}
func archiveReadAuditKey() (idempotency.Key, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return idempotency.Parse("message-archive-read:" + hex.EncodeToString(raw[:]))
}

var errInvalid = errors.New("invalid archive request")

func positiveID(value string) (int64, error) {
	id, e := strconv.ParseInt(value, 10, 64)
	if e != nil || id < 1 || strconv.FormatInt(id, 10) != value {
		return 0, errInvalid
	}
	return id, nil
}
func only(r *http.Request, allowed ...string) bool {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for k, v := range r.URL.Query() {
		if !ok[k] || len(v) != 1 {
			return false
		}
	}
	return true
}
func parseQuery(r *http.Request, id customerdomain.CustomerID, now time.Time) (archiveport.CustomerQuery, error) {
	raw := r.URL.Query()
	limit := 50
	if item := raw.Get("limit"); item != "" {
		v, e := strconv.Atoi(item)
		if e != nil || v < 1 || v > 100 || strconv.Itoa(v) != item {
			return archiveport.CustomerQuery{}, errInvalid
		}
		limit = v
	}
	chat := raw.Get("chat_type")
	if chat != "" && chat != "private" && chat != "group" {
		return archiveport.CustomerQuery{}, errInvalid
	}
	messageType := raw.Get("message_type")
	if !validMessageType(messageType) {
		return archiveport.CustomerQuery{}, errInvalid
	}
	direction := raw.Get("direction")
	if direction != "" && direction != "customer_to_staff" && direction != "staff_to_customer" {
		return archiveport.CustomerQuery{}, errInvalid
	}
	staffUserID := int64(0)
	if item := raw.Get("staff_user_id"); item != "" {
		value, e := positiveID(item)
		if e != nil {
			return archiveport.CustomerQuery{}, errInvalid
		}
		staffUserID = value
	}
	startAt := time.Time{}
	if item := raw.Get("start_at"); item != "" {
		value, e := time.Parse(time.RFC3339Nano, item)
		if e != nil {
			return archiveport.CustomerQuery{}, errInvalid
		}
		startAt = value.UTC()
	}
	text := raw.Get("q")
	if strings.TrimSpace(text) != text || len(text) > 300 {
		return archiveport.CustomerQuery{}, errInvalid
	}
	watermark := now.UTC()
	if item := raw.Get("end_at"); item != "" {
		value, e := time.Parse(time.RFC3339Nano, item)
		if e != nil {
			return archiveport.CustomerQuery{}, errInvalid
		}
		watermark = value.UTC()
	}
	if item := raw.Get("watermark"); item != "" {
		value, e := time.Parse(time.RFC3339Nano, item)
		if e != nil {
			return archiveport.CustomerQuery{}, errInvalid
		}
		if raw.Get("end_at") != "" && !value.UTC().Equal(watermark) {
			return archiveport.CustomerQuery{}, errInvalid
		}
		watermark = value.UTC()
	}
	if !startAt.IsZero() && startAt.After(watermark) {
		return archiveport.CustomerQuery{}, errInvalid
	}
	afterAt := time.Time{}
	afterID := int64(0)
	if item := raw.Get("after_at"); item != "" {
		value, e := time.Parse(time.RFC3339Nano, item)
		if e != nil {
			return archiveport.CustomerQuery{}, errInvalid
		}
		afterAt = value.UTC()
		itemID := raw.Get("after_id")
		v, e := strconv.ParseInt(itemID, 10, 64)
		if e != nil || v < 1 || strconv.FormatInt(v, 10) != itemID {
			return archiveport.CustomerQuery{}, errInvalid
		}
		afterID = v
	} else if raw.Get("after_id") != "" {
		return archiveport.CustomerQuery{}, errInvalid
	}
	return archiveport.CustomerQuery{CustomerID: id, ChatType: chat, MessageType: messageType, Direction: direction, StaffUserID: staffUserID, StartAt: startAt, Search: text, Limit: limit + 1, Watermark: watermark, AfterAt: afterAt, AfterID: afterID}, nil
}

func validMessageType(value string) bool {
	if value == "" || len(value) > 120 {
		return value == ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
func visible(q archiveport.CustomerQuery, p archiveport.CustomerPage) []archiveport.MessageItem {
	limit := q.Limit - 1
	if len(p.Items) <= limit {
		return p.Items
	}
	return p.Items[:limit]
}

func next(q archiveport.CustomerQuery, p archiveport.CustomerPage) any {
	if len(p.Items) <= q.Limit-1 {
		return nil
	}
	item := p.Items[q.Limit-2]
	return map[string]any{"watermark": q.Watermark.Format(time.RFC3339Nano), "after_at": item.OccurredAt.Format(time.RFC3339Nano), "after_id": item.ID}
}
func (h *Handler) nowTime() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "archive_unavailable"
	if errors.Is(err, accessdomain.ErrAuthentication) {
		status = http.StatusUnauthorized
		code = "unauthenticated"
	} else if errors.Is(err, accessdomain.ErrPermissionDenied) {
		status = http.StatusForbidden
		code = "forbidden"
	} else if errors.Is(err, errInvalid) {
		status = http.StatusBadRequest
		code = "invalid_request"
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
