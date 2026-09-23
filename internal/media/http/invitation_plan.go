package http

import (
	"encoding/json"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

type InvitationHandler struct {
	Service  *app.InvitationService
	Security RequestSecurity
}

func (h InvitationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/gi/") {
		h.public(w, r)
		return
	}
	guard := Handler{security: h.Security}
	if r.Method == http.MethodGet {
		if !guard.read(w, r) {
			return
		}
	} else {
		if _, ok := guard.write(w, r); !ok {
			return
		}
	}
	ctx := r.Context()
	if strings.HasPrefix(r.URL.Path, "/api/admin/group-directory") {
		switch {
		case r.URL.Path == "/api/admin/group-directory" && r.Method == http.MethodGet:
			limit, offset := 50, 0
			if v := r.URL.Query().Get("limit"); v != "" {
				n, e := strconv.Atoi(v)
				if e != nil || n < 1 || n > 100 {
					writeError(w, 400, "invalid_limit")
					return
				}
				limit = n
			}
			if v := r.URL.Query().Get("offset"); v != "" {
				n, e := strconv.Atoi(v)
				if e != nil || n < 0 {
					writeError(w, 400, "invalid_offset")
					return
				}
				offset = n
			}
			out, e := h.Service.Catalog.ListCatalog(ctx, r.URL.Query().Get("q"), limit, offset)
			if e != nil {
				writeError(w, 503, "directory_unavailable")
				return
			}
			writeJSON(w, 200, out)
		case r.URL.Path == "/api/admin/group-directory/status" && r.Method == http.MethodGet:
			out, e := h.Service.Catalog.CatalogStatus(ctx)
			if e != nil {
				writeError(w, 503, "directory_unavailable")
				return
			}
			writeJSON(w, 200, out)
		case r.URL.Path == "/api/admin/group-directory/refresh" && r.Method == http.MethodPost:
			out, e := h.Service.Catalog.RequestCatalogSync(ctx, true)
			if e != nil {
				writeError(w, 503, "directory_refresh_unavailable")
				return
			}
			writeJSON(w, 202, out)
		default:
			if r.Method == http.MethodGet {
				id := strings.TrimPrefix(r.URL.Path, "/api/admin/group-directory/")
				v, e := h.Service.Catalog.ReadCatalogGroup(ctx, id)
				if e != nil {
					writeError(w, 404, "not_found")
					return
				}
				writeJSON(w, 200, v)
				return
			}
			writeError(w, 405, "method_not_allowed")
		}
		return
	}
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/group-invitations"), "/")
	if tail == "" && r.Method == http.MethodGet {
		ids, e := h.Service.Store.InvitationPlanIDs(ctx, false)
		if e != nil {
			writeError(w, 503, "unavailable")
			return
		}
		out := []p.InvitationPlan{}
		for _, id := range ids {
			v, e := h.Service.Store.ReadInvitationPlan(ctx, id)
			if e != nil {
				writeError(w, 503, "unavailable")
				return
			}
			out = append(out, v)
		}
		writeJSON(w, 200, map[string]any{"items": out, "write_enabled": h.Service.WriteEnabled})
		return
	}
	if tail == "" && r.Method == http.MethodPost {
		var v p.InvitationInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&v) != nil || d.ValidateInvitationInput(v) != nil {
			writeError(w, 400, "invalid_plan")
			return
		}
		actor, e := h.Security.Authenticate(ctx, r)
		if e != nil {
			writeError(w, 401, "unauthorized")
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if len(key) < 16 || len(key) > 128 {
			writeError(w, 400, "idempotency_key_required")
			return
		}
		out, e := h.Service.Save(ctx, v, actor.InternalID, key)
		if e != nil {
			writeError(w, 409, "plan_save_failed")
			return
		}
		writeJSON(w, 200, out)
		return
	}
	parts := strings.Split(tail, "/")
	id, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || id < 1 {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "history" && r.Method == http.MethodGet {
		out, e := h.Service.Store.InvitationHistory(ctx, id)
		if e != nil {
			writeError(w, 503, "unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"items": out})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		out, e := h.Service.Store.ReadInvitationPlan(ctx, id)
		if e != nil {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, out)
		return
	}
	writeError(w, 405, "method_not_allowed")
}

var invitationPublicTemplate = template.Must(template.New("invitation").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/static/admin_console/invitation_public.css"></head><body><main><h1 id="title">{{.Title}}</h1><p id="description">{{.Description}}</p><p id="status">正在获取入群方式…</p><img id="groupCode" alt="当前群聊入群二维码" hidden><p>长按识别二维码加入群聊</p><p id="usageNotice">此链接仅用于加入群聊，不触发渠道欢迎语或入渠标签；如需触发渠道欢迎语，请使用渠道码中心的渠道二维码或获客链接。</p></main><script src="/static/admin_console/invitation_public.js" defer></script></body></html>`))

func (h InvitationHandler) public(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method_not_allowed")
		return
	}
	pathValue := strings.TrimPrefix(r.URL.Path, "/gi/")
	if len(pathValue) != 48 || strings.Trim(pathValue, "0123456789abcdef") != "" {
		writeError(w, 404, "not_found")
		return
	}
	plan, e := h.Service.Public(r.Context(), pathValue)
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if r.URL.Query().Get("format") == "json" {
		qr := ""
		if plan.State == "active" {
			qr = plan.ProviderQRCode
		}
		if qr == "" && plan.State == "active" {
			for _, b := range plan.Bindings {
				if b.ChatID == plan.CurrentChatID {
					qr = b.QRCode
				}
			}
		}
		writeJSON(w, 200, map[string]any{"title": plan.Title, "description": plan.Description, "state": plan.State, "qr_code": qr, "version": plan.Version})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = invitationPublicTemplate.Execute(w, plan)
}
