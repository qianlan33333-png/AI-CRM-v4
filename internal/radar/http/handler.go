package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/presentationtime"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

const sessionCookie = "__Host-aicrm-radar"

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	manager  radarport.Manager
	query    radarport.QueryService
	visitors radarport.VisitorQueryService
	public   radarport.PublicService
	security RequestSecurity
	origin   string
}

func NewHandler(manager radarport.Manager, query radarport.QueryService, public radarport.PublicService, security RequestSecurity, origin string) (*Handler, error) {
	parsed, e := url.Parse(origin)
	if manager == nil || query == nil || public == nil || security == nil || e != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" {
		return nil, errors.New("radar HTTP dependencies are required")
	}
	visitors, ok := query.(radarport.VisitorQueryService)
	if !ok || visitors == nil {
		return nil, errors.New("radar visitor query dependencies are required")
	}
	return &Handler{manager: manager, query: query, visitors: visitors, public: public, security: security, origin: strings.TrimSuffix(origin, "/")}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/admin/radar-links"):
		h.admin(w, r)
	case strings.HasPrefix(r.URL.Path, "/r/"):
		h.open(w, r)
	case r.URL.Path == "/api/public/radar/oauth/callback":
		h.oauthCallback(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/public/radar/"):
		h.canonicalPublic(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/h5/radar-contents/"):
		h.event(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/radar-links"), "/")
	if tail == "new/options" {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if !h.read(w, r) {
			return
		}
		writeJSON(w, 200, map[string]any{"statuses": []string{"draft", "enabled", "disabled"}, "status_filters": []string{"all", "draft", "enabled", "disabled"}, "sorts": []string{"updated_desc", "created_desc", "name_asc"}, "defaults": map[string]any{"initial_status": "draft", "status_filter": "all", "sort": "updated_desc", "limit": 20}, "limits": map[string]any{"name_runes": 120, "title_runes": 200, "destination_url_bytes": 2048, "list_limit_minimum": 1, "list_limit_maximum": 100, "list_offset_maximum": 1000000, "request_body_bytes": 65536, "idempotency_key_bytes_minimum": 16, "idempotency_key_bytes_maximum": 128}, "destination_schemes": []string{"https"}, "local_projection": true, "public_route_ready": true, "real_external_call_executed": false})
		return
	}
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.list(w, r)
			}
		case http.MethodPost:
			if p, ok := h.write(w, r); ok {
				h.create(w, r, p)
			}
		default:
			method(w, "GET, POST")
		}
		return
	}
	parts := strings.Split(tail, "/")
	id, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || id < 1 {
		notFound(w)
		return
	}
	radarID := radar.RadarID(id)
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.get(w, r, radarID)
			}
		case http.MethodPut, http.MethodPatch:
			if p, ok := h.write(w, r); ok {
				h.update(w, r, p, radarID)
			}
		default:
			method(w, "GET, PUT, PATCH")
		}
		return
	}
	if len(parts) == 3 && parts[1] == "events" && parts[2] == "export" {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.exportRead(w, r) {
			h.exportEvents(w, r, radarID)
		}
		return
	}
	if len(parts) == 3 && parts[1] == "visitors" && parts[2] == "export" {
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.visitorRead(w, r) {
			h.visitorExport(w, r, radarID)
		}
		return
	}
	if len(parts) != 2 {
		notFound(w)
		return
	}
	switch parts[1] {
	case "enable", "disable":
		if r.Method != http.MethodPost {
			method(w, "POST")
			return
		}
		if p, ok := h.write(w, r); ok {
			h.status(w, r, p, radarID, parts[1])
		}
	case "share":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.read(w, r) {
			h.share(w, r, radarID)
		}
	case "stats":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.read(w, r) {
			h.stats(w, r, radarID)
		}
	case "events":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.read(w, r) {
			h.events(w, r, radarID, false)
		}
	case "visitors":
		if r.Method != http.MethodGet {
			method(w, "GET")
			return
		}
		if h.visitorRead(w, r) {
			h.visitorList(w, r, radarID)
		}
	default:
		notFound(w)
	}
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		problem(w, 401, "unauthorized")
		return false
	}
	if !role(p, false) {
		problem(w, 403, "forbidden")
		return false
	}
	return true
}

// exportRead keeps normal event pages available to the read-only role while
// preventing their bulk extraction. Exports remain an administrator business
// capability and do not need CSRF because this GET route has no state change.
func (h *Handler) exportRead(w http.ResponseWriter, r *http.Request) bool {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		problem(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !role(p, true) {
		problem(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// visitorRead is intentionally stricter than Radar's ordinary read path:
// /events is a privacy-safe projection that viewer roles may inspect, while a
// visitor row can reveal a scoped external-contact value. This uses the same
// CSRF-backed session authorization boundary as existing sensitive Customer
// reads and never permits KindStaff, even if a stale role set names admin.
func (h *Handler) visitorRead(w http.ResponseWriter, r *http.Request) bool {
	if _, err := h.security.Authenticate(r.Context(), r); err != nil {
		problem(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	principal, err := h.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		problem(w, http.StatusForbidden, "csrf_required")
		return false
	}
	if principal.Kind != accessdomain.KindAdmin || principal.InternalID < 1 {
		problem(w, http.StatusForbidden, "forbidden")
		return false
	}
	for _, value := range principal.Roles {
		if value == accessdomain.RoleAdmin || value == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	problem(w, http.StatusForbidden, "forbidden")
	return false
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		problem(w, 401, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !role(p, true) {
		problem(w, 403, "forbidden")
		return accessdomain.Principal{}, false
	}
	if _, e = h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		problem(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return p, true
}
func role(p accessdomain.Principal, write bool) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, r := range p.Roles {
		if r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin || (!write && r == accessdomain.RoleViewer) {
			return true
		}
	}
	return false
}

type linkRequest struct {
	Expected     int64  `json:"expected_version"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Destination  string `json:"destination_url"`
	CoverID      *int64 `json:"cover_image_id"`
	AttachmentID *int64 `json:"attachment_id"`
	AuthPolicy   string `json:"auth_policy"`
}

func decode(r *http.Request, target any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return errors.New("media")
	}
	d := json.NewDecoder(http.MaxBytesReader(nilReader{}, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return e
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		if e == nil {
			return errors.New("trailing")
		}
		return e
	}
	return nil
}

// nilReader is only the ResponseWriter required by MaxBytesReader; overflow is
// still surfaced through its returned reader.
type nilReader struct{}

func (nilReader) Header() http.Header       { return http.Header{} }
func (nilReader) Write([]byte) (int, error) { return 0, nil }
func (nilReader) WriteHeader(int)           {}
func content(req linkRequest) (radar.Content, error) {
	switch {
	case req.CoverID != nil && *req.CoverID > 0 && req.AttachmentID == nil:
		return radar.Content{Type: radar.ContentTypeImage, MediaID: radar.MediaID(*req.CoverID)}, nil
	case req.AttachmentID != nil && *req.AttachmentID > 0 && req.CoverID == nil:
		return radar.Content{Type: radar.ContentTypePDF, MediaID: radar.MediaID(*req.AttachmentID)}, nil
	case req.CoverID == nil && req.AttachmentID == nil:
		return radar.Content{Type: radar.ContentTypeLink, DestinationURL: req.Destination}, nil
	default:
		return radar.Content{}, radar.ErrInvalidArgument
	}
}
func policy(raw string) radar.AuthPolicy {
	if raw == string(radar.AuthPolicyAnonymous) {
		return radar.AuthPolicyAnonymous
	}
	return radar.AuthPolicyUnionIDRequired
}
func key(r *http.Request) string {
	if v := r.Header.Get("Idempotency-Key"); len(v) >= 16 && len(v) <= 128 && strings.TrimSpace(v) == v {
		return v
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "radar-web-" + hex.EncodeToString(b)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, ok := number(r, "limit", 20, 1, 100)
	if !ok {
		problem(w, 400, "invalid_query")
		return
	}
	offset, ok := number(r, "offset", 0, 0, 1000000)
	if !ok {
		problem(w, 400, "invalid_query")
		return
	}
	var status radar.Status
	if raw := r.URL.Query().Get("status"); raw != "" && raw != "all" {
		status = radar.Status(raw)
	}
	page, e := h.manager.List(r.Context(), radarport.ListQuery{
		Search:      r.URL.Query().Get("search"),
		ContentType: radar.ContentType(r.URL.Query().Get("content_type")),
		Status:      status,
		Limit:       int32(limit),
		Offset:      int32(offset),
	})
	if e != nil {
		h.err(w, e)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, i := range page.Items {
		items = append(items, h.linkSummary(i))
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset, "has_more": page.HasMore, "status_filter": value(r.URL.Query().Get("status"), "all"), "sort": value(r.URL.Query().Get("sort"), "updated_desc"), "local_projection": true, "real_external_call_executed": false})
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	d, e := h.manager.Get(r.Context(), id)
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"link": h.link(d.Link), "local_projection": true, "real_external_call_executed": false})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request, p accessdomain.Principal) {
	var req linkRequest
	if e := decode(r, &req); e != nil {
		problem(w, 400, "invalid_body")
		return
	}
	c, e := content(req)
	if e != nil {
		h.err(w, e)
		return
	}
	d, e := h.manager.Create(r.Context(), radarport.CreateCommand{Name: req.Name, Title: req.Title, Description: req.Description, Content: c, AuthPolicy: policy(req.AuthPolicy), ActorID: p.InternalID, IdempotencyKey: key(r)})
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"link": h.link(d.Link), "local_projection": true, "real_external_call_executed": false})
}
func (h *Handler) update(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id radar.RadarID) {
	var req linkRequest
	if e := decode(r, &req); e != nil {
		problem(w, 400, "invalid_body")
		return
	}
	c, e := content(req)
	if e != nil {
		h.err(w, e)
		return
	}
	current, e := h.manager.Get(r.Context(), id)
	if e != nil {
		h.err(w, e)
		return
	}
	name, title, description := req.Name, req.Title, req.Description
	if name == "" {
		name = current.Link.Name
	}
	if title == "" {
		title = current.Link.Title
	}
	if description == "" {
		description = current.Link.Description
	}
	d, e := h.manager.Update(r.Context(), radarport.UpdateCommand{RadarID: id, Expected: radar.LinkVersion(req.Expected), Revision: radar.Revision{Name: name, Title: title, Description: description, Content: c, AuthPolicy: policy(req.AuthPolicy)}, ActorID: p.InternalID, IdempotencyKey: key(r)})
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"link": h.link(d.Link), "local_projection": true, "real_external_call_executed": false})
}
func (h *Handler) status(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id radar.RadarID, command string) {
	var req struct {
		Expected int64 `json:"expected_version"`
	}
	if e := decode(r, &req); e != nil {
		problem(w, 400, "invalid_body")
		return
	}
	target := radar.StatusEnabled
	if command == "disable" {
		target = radar.StatusDisabled
	}
	d, e := h.manager.SetStatus(r.Context(), radarport.SetStatusCommand{RadarID: id, Expected: radar.LinkVersion(req.Expected), Target: target, ActorID: p.InternalID, IdempotencyKey: key(r)})
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"link": h.link(d.Link), "local_projection": true, "real_external_call_executed": false})
}
func (h *Handler) share(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	d, e := h.manager.Get(r.Context(), id)
	if e != nil {
		h.err(w, e)
		return
	}
	path := "/r/" + string(d.Link.PublicCode)
	writeJSON(w, 200, map[string]any{"link_id": id, "public_code": d.Link.PublicCode, "status": d.Link.Status, "available": d.Link.Status == radar.StatusEnabled, "share_path": path, "qr_payload": path, "local_projection": true, "public_route_ready": true, "real_external_call_executed": false})
}
func (h *Handler) stats(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	s, e := h.query.Stats(r.Context(), id)
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"link_id": id, "total_events": s.TotalEvents, "total_clicks": s.ViewCount, "total_landings": s.TotalLandings, "authorized_clicks": s.AuthorizedViews, "unique_users": s.AuthorizedUsers, "authorized_users": s.AuthorizedUsers, "redirects": s.Redirects, "viewer_opens": s.ViewCount, "view_opens": s.ViewCount, "image_loaded": s.ImageLoaded, "pdf_opened": s.PDFOpened, "today_clicks": s.TodayViews, "today_landings": s.TodayLandings, "last_clicked_at": s.LastViewedAt})
}
func (h *Handler) events(w http.ResponseWriter, r *http.Request, id radar.RadarID, export bool) {
	limit, ok := number(r, "limit", 500, 1, 500)
	if !ok {
		problem(w, 400, "invalid_query")
		return
	}
	offset, ok := number(r, "offset", 0, 0, 1000000)
	if !ok {
		problem(w, 400, "invalid_query")
		return
	}
	q := radarport.EventQuery{RadarID: id, Limit: int32(limit), Offset: int32(offset)}
	if raw := r.URL.Query().Get("stage"); raw != "" {
		q.Stage = radarport.EventStage(raw)
	}
	var e error
	q.Start, e = parseTime(r.URL.Query().Get("start_at"))
	if e != nil {
		problem(w, 400, "invalid_query")
		return
	}
	q.End, e = parseTime(r.URL.Query().Get("end_at"))
	if e != nil {
		problem(w, 400, "invalid_query")
		return
	}
	page, e := h.query.Events(r.Context(), q)
	if e != nil {
		h.err(w, e)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, i := range page.Items {
		items = append(items, map[string]any{"event_id": i.EventID, "receipt_id": i.ReceiptID, "link_id": i.RadarID, "stage": i.Stage, "source": "public_event", "created_at": i.OccurredAt})
	}
	writeJSON(w, 200, map[string]any{"items": items, "events": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset, "has_more": page.HasMore, "identity_attributed": false, "real_external_call_executed": false})
}

func (h *Handler) exportEvents(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	q := radarport.EventQuery{RadarID: id, Limit: 500, Offset: 0}
	var e error
	q.Start, e = parseTime(r.URL.Query().Get("start_at"))
	if e != nil {
		problem(w, 400, "invalid_query")
		return
	}
	q.End, e = parseTime(r.URL.Query().Get("end_at"))
	if e != nil {
		problem(w, 400, "invalid_query")
		return
	}
	page, e := h.query.Events(r.Context(), q)
	if e != nil {
		h.err(w, e)
		return
	}
	if page.HasMore {
		problem(w, 409, "export_range_too_large")
		return
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"receipt_id", "stage", "attribution", "customer_ref", "occurred_at"})
	for _, event := range page.Items {
		_ = writer.Write([]string{event.ReceiptID, radarEventStageLabel(event.Stage), radarAttributionLabel(event.Attribution), event.CustomerRef, presentationtime.FormatShanghaiDateTime(event.OccurredAt)})
	}
	writer.Flush()
	if writer.Error() != nil {
		problem(w, 503, "unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="radar-events.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buffer.Bytes())
}

func (h *Handler) visitorList(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	query, search, ok := visitorQuery(r, id, false)
	if !ok {
		problem(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.visitors.Visitors(r.Context(), query, search)
	if err != nil {
		h.err(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) visitorExport(w http.ResponseWriter, r *http.Request, id radar.RadarID) {
	query, search, ok := visitorQuery(r, id, true)
	if !ok {
		problem(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.visitors.Visitors(r.Context(), query, search)
	if err != nil {
		h.err(w, err)
		return
	}
	if page.HasMore {
		problem(w, http.StatusConflict, "export_range_too_large")
		return
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"昵称", "外部联系人ID", "外部联系人ID状态", "OneID", "打开时间", "身份状态"})
	for _, visitor := range page.Items {
		_ = writer.Write([]string{
			csvSafe(visitorString(visitor.Nickname)),
			csvSafe(visitorString(visitor.ExternalContactID)),
			csvSafe(radarVisitorExternalContactLabel(visitor.ExternalContactStatus)),
			csvSafe(visitorString(visitor.OneID)),
			presentationtime.FormatShanghaiDateTime(visitor.OpenedAt),
			radarAttributionLabel(visitor.AttributionStatus),
		})
	}
	writer.Flush()
	if writer.Error() != nil {
		problem(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="radar-visitors.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buffer.Bytes())
}

func visitorQuery(r *http.Request, id radar.RadarID, export bool) (radarport.VisitorQuery, string, bool) {
	allowed := map[string]struct{}{"search": {}, "start_at": {}, "end_at": {}}
	if !export {
		allowed["limit"] = struct{}{}
		allowed["offset"] = struct{}{}
	}
	for name, values := range r.URL.Query() {
		if _, exists := allowed[name]; !exists || len(values) != 1 {
			return radarport.VisitorQuery{}, "", false
		}
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len([]rune(search)) > radarport.MaximumVisitorSearchChars {
		return radarport.VisitorQuery{}, "", false
	}
	start, err := parseTime(r.URL.Query().Get("start_at"))
	if err != nil {
		return radarport.VisitorQuery{}, "", false
	}
	end, err := parseTime(r.URL.Query().Get("end_at"))
	if err != nil || start != nil && end != nil && !start.Before(*end) {
		return radarport.VisitorQuery{}, "", false
	}
	query := radarport.VisitorQuery{RadarID: id, Start: start, End: end, Limit: radarport.MaximumVisitorLimit}
	if export {
		return query, search, true
	}
	limit, ok := number(r, "limit", int(radarport.MaximumVisitorLimit), 1, int(radarport.MaximumVisitorLimit))
	if !ok {
		return radarport.VisitorQuery{}, "", false
	}
	offset, ok := number(r, "offset", 0, 0, int(radarport.MaximumVisitorOffset))
	if !ok {
		return radarport.VisitorQuery{}, "", false
	}
	query.Limit, query.Offset = int32(limit), int32(offset)
	return query, search, true
}

func visitorString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

func radarVisitorExternalContactLabel(value radarport.VisitorExternalContactStatus) string {
	switch value {
	case radarport.VisitorExternalContactAvailable:
		return "可确认"
	case radarport.VisitorExternalContactMissing:
		return "暂缺"
	case radarport.VisitorExternalContactAmbiguous:
		return "存在多个待确认身份"
	default:
		return "暂不可用"
	}
}

func radarEventStageLabel(value radarport.EventStage) string {
	switch value {
	case radarport.EventLanding:
		return "访问落地页"
	case radarport.EventOAuthStarted:
		return "开始微信授权"
	case radarport.EventOAuthVerified:
		return "微信授权已验证"
	case radarport.EventIdentityResolved:
		return "客户身份已确认"
	case radarport.EventContentOpened:
		return "打开内容"
	case radarport.EventRedirected:
		return "跳转完成"
	case radarport.EventImageLoaded:
		return "图片已加载"
	case radarport.EventPDFOpened:
		return "PDF 已打开"
	case radarport.EventFailed:
		return "访问失败"
	default:
		return "事件阶段待确认"
	}
}

func radarAttributionLabel(value radarport.AttributionStatus) string {
	switch value {
	case radarport.AttributionAnonymous:
		return "匿名访问"
	case radarport.AttributionResolved:
		return "已关联客户"
	case radarport.AttributionPending:
		return "关联待确认"
	case radarport.AttributionConflict:
		return "关联冲突"
	case radarport.AttributionFailed:
		return "关联失败"
	default:
		return "关联状态待确认"
	}
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		method(w, "GET, HEAD")
		return
	}
	code := radar.PublicCode(strings.TrimPrefix(r.URL.Path, "/r/"))
	token := ""
	if c, e := r.Cookie(sessionCookie); e == nil {
		token = c.Value
	}
	access, e := h.public.Open(r.Context(), code, token)
	if e != nil {
		h.err(w, e)
		return
	}
	if access.SessionToken != "" && access.SessionToken != token {
		setSession(w, access.SessionToken)
	}
	if access.Action == radarport.PublicOAuthRedirect {
		http.SetCookie(w, &http.Cookie{Name: "radar_oauth_return", Value: string(code), Path: "/api/public/radar/oauth/callback", MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	}
	if access.Action == radarport.PublicOAuthRedirect || access.Action == radarport.PublicLinkRedirect {
		http.Redirect(w, r, access.Location, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; frame-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	kind := string(access.Link.Content.Type)
	proof := eventProof(access.SessionToken, code)
	_, _ = fmt.Fprintf(w, viewerHTML, template.HTMLEscapeString(access.Link.Title), template.HTMLEscapeString(access.Link.Title), template.URLQueryEscaper(string(code)), kind, proof)
}

const viewerHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>html,body{margin:0;background:#f5f6f8;color:#1f2329;font-family:system-ui}.bar{padding:14px 18px;background:#fff;border-bottom:1px solid #ddd;font-weight:600}.view{display:block;max-width:100%%;margin:auto}.pdf{width:100%%;height:calc(100vh - 54px);border:0}</style><div class="bar">%s</div><main id="root"></main><script>const code='%s',kind='%s',proof='%s',root=document.getElementById('root'),src='/api/public/radar/'+code+'/content';if(kind==='image'){const i=new Image;i.className='view';i.onload=()=>track('image_loaded');i.src=src;root.append(i)}else{const f=document.createElement('iframe');f.className='pdf';f.onload=()=>track('pdf_opened');f.src=src;root.append(f)}function track(stage){fetch('/api/public/radar/'+code+'/events',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({stage,event_token:proof})})}</script></html>`

func (h *Handler) canonicalPublic(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/public/radar/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 {
		notFound(w)
		return
	}
	copy := r.Clone(r.Context())
	switch parts[1] {
	case "content":
		copy.URL.Path = "/api/public/radar/content/" + parts[0]
		h.content(w, copy)
	case "events":
		copy.URL.Path = "/api/h5/radar-contents/" + parts[0] + "/events"
		h.event(w, copy)
	default:
		notFound(w)
	}
}

func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	token, path, e := h.public.CompleteOAuth(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if e != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
		w.WriteHeader(http.StatusServiceUnavailable)
		retry := ""
		if prior, err := r.Cookie("radar_oauth_return"); err == nil && radar.PublicCode(prior.Value).Valid() {
			retry = `<p><a href="/r/` + template.HTMLEscapeString(prior.Value) + `">重试微信授权</a></p>`
		}
		_, _ = fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>微信授权未完成</title><body><h1>微信授权未完成</h1><p>请重试；若仍失败，请重新打开原雷达链接。</p>`+retry+`</body></html>`)
		return
	}
	setSession(w, token)
	http.Redirect(w, r, path, http.StatusFound)
}
func (h *Handler) content(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		method(w, "GET, HEAD")
		return
	}
	cookie, e := r.Cookie(sessionCookie)
	if e != nil {
		notFound(w)
		return
	}
	code := radar.PublicCode(strings.TrimPrefix(r.URL.Path, "/api/public/radar/content/"))
	c, e := h.public.Content(r.Context(), code, cookie.Value)
	if e != nil {
		h.err(w, e)
		return
	}
	w.Header().Set("Content-Type", c.MediaType)
	name := "content"
	if c.MediaType == "application/pdf" {
		name = "content.pdf"
	}
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if c.ETag != "" {
		w.Header().Set("ETag", c.ETag)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(c.Bytes))
}
func (h *Handler) event(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	if !h.sameOrigin(r) {
		problem(w, 403, "cross_site_event_rejected")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/api/h5/radar-contents/")
	if !strings.HasSuffix(tail, "/events") {
		notFound(w)
		return
	}
	code := radar.PublicCode(strings.TrimSuffix(tail, "/events"))
	cookie, e := r.Cookie(sessionCookie)
	if e != nil {
		notFound(w)
		return
	}
	var req struct {
		Stage      string `json:"stage"`
		Page       int    `json:"page"`
		EventToken string `json:"event_token"`
	}
	if e = decode(r, &req); e != nil {
		problem(w, 400, "invalid_body")
		return
	}
	if !hmac.Equal([]byte(req.EventToken), []byte(eventProof(cookie.Value, code))) {
		problem(w, 403, "event_token_required")
		return
	}
	event, replay, e := h.public.Record(r.Context(), code, cookie.Value, radarport.EventStage(req.Stage), strconv.Itoa(req.Page))
	if e != nil {
		h.err(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "event_id": event.EventID, "receipt_id": event.ReceiptID, "created_at": event.OccurredAt, "replayed": replay, "local_receipt": true, "identity_attributed": event.Attribution == radarport.AttributionResolved, "real_external_call_executed": false})
}

func eventProof(sessionToken string, code radar.PublicCode) string {
	mac := hmac.New(sha256.New, []byte(sessionToken))
	_, _ = mac.Write([]byte("radar-event\x00" + string(code)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) sameOrigin(r *http.Request) bool {
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" && site != "same-site" && site != "none" {
		return false
	}
	for _, raw := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme+"://"+parsed.Host != h.origin {
			return false
		}
		return true
	}
	return true
}

func (h *Handler) link(l radar.Link) map[string]any {
	var cover, attachment any
	destination := l.Content.DestinationURL
	if l.Content.Type == radar.ContentTypeImage {
		cover = l.Content.MediaID
		destination = h.origin + "/api/public/radar/" + string(l.PublicCode) + "/content"
	}
	if l.Content.Type == radar.ContentTypePDF {
		attachment = l.Content.MediaID
		destination = h.origin + "/api/public/radar/" + string(l.PublicCode) + "/content"
	}
	return map[string]any{"link_id": l.ID, "public_code": l.PublicCode, "name": l.Name, "title": l.Title, "description": l.Description, "destination_url": destination, "cover_image_id": cover, "attachment_id": attachment, "auth_policy": l.AuthPolicy, "status": l.Status, "version": l.Version, "created_by": l.CreatedBy, "updated_by": l.UpdatedBy, "created_at": l.CreatedAt, "updated_at": l.UpdatedAt}
}

func (h *Handler) linkSummary(summary radarport.LinkSummary) map[string]any {
	link := h.link(summary.Link)
	status := summary.StatisticsStatus
	if status != radarport.LinkStatisticsReady {
		link["statistics_status"] = radarport.LinkStatisticsUnavailable
		link["total_landings"] = nil
		link["authorized_users"] = nil
		link["authorized_views"] = nil
		link["view_count"] = nil
		link["last_viewed_at"] = nil
		return link
	}
	link["statistics_status"] = status
	link["total_landings"] = summary.TotalLandings
	link["authorized_users"] = summary.AuthorizedUsers
	link["authorized_views"] = summary.AuthorizedViews
	link["view_count"] = summary.ViewCount
	link["last_viewed_at"] = summary.LastViewedAt
	return link
}
func (h *Handler) err(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, radarport.ErrNotFound):
		notFound(w)
	case errors.Is(e, radarport.ErrGone):
		problem(w, http.StatusGone, "radar_disabled")
	case errors.Is(e, radar.ErrInvalidArgument):
		problem(w, 400, "invalid_argument")
	case errors.Is(e, radar.ErrVersionConflict), errors.Is(e, radarport.ErrConflict), errors.Is(e, radarport.ErrIdempotencyConflict):
		problem(w, 409, "conflict")
	case errors.Is(e, radarport.ErrVisitorSearchTooWide):
		problem(w, 409, "visitor_search_range_too_large")
	default:
		problem(w, 503, "unavailable")
	}
}
func setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: 1800, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func number(r *http.Request, name string, def, min, max int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	v, e := strconv.Atoi(raw)
	return v, e == nil && v >= min && v <= max
}
func parseTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	v, e := time.Parse(time.RFC3339, raw)
	if e != nil {
		return nil, e
	}
	return &v, nil
}
func value(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"code": code, "message": code})
}
func notFound(w http.ResponseWriter) { problem(w, 404, "not_found") }
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	problem(w, 405, "method_not_allowed")
}
