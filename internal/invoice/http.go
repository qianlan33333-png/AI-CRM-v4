package invoice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	payment "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	Service *Service
	Origins []string
	Dist    string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path := r.URL.Path
	if path == "/my/orders" {
		h.page(w, r)
		return
	}
	if strings.HasPrefix(path, "/my/orders-assets/") {
		h.asset(w, r)
		return
	}
	if path == "/api/callbacks/wechat-invoice" {
		// Development-config reachability checks carry no business notification.
		// A GET/HEAD never bypasses verification or mutates an invoice.
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			h.fail(w, p.ErrInvalid)
			return
		}
		verifier := h.Service.CallbackVerifier
		if verifier == nil {
			verifier = h.Service.Provider
		}
		if verifier == nil {
			h.fail(w, p.ErrDisabled)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
		n, e := verifier.Verify(r.Context(), r)
		if e != nil {
			h.fail(w, e)
			return
		}
		if e = h.Service.Notification(r.Context(), n); e != nil {
			h.fail(w, e)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	token := ""
	if c, e := r.Cookie(payment.TrustedSessionCookieName); e == nil {
		token = c.Value
	}
	if token == "" {
		h.fail(w, payment.ErrSessionRequired)
		return
	}
	if r.Method == http.MethodPost && !h.sameOrigin(r) {
		h.fail(w, errCSRF)
		return
	}
	if path == "/api/h5/me/orders" && r.Method == http.MethodGet {
		q, e := parseOrdersQuery(r.URL.Query())
		if e != nil {
			h.fail(w, e)
			return
		}
		page, e := h.Service.List(r.Context(), token, q)
		h.reply(w, page, e)
		return
	}
	if path == "/api/h5/me/invoice-jssdk" && r.Method == http.MethodGet {
		// Authorize before using OA credentials; sign only this official page.
		e := h.Service.UoW.Within(r.Context(), func(tx context.Context) error { _, _, e := h.Service.actor(tx, token); return e })
		if e != nil {
			h.fail(w, e)
			return
		}
		u, e := url.Parse(r.URL.Query().Get("url"))
		if e != nil || u.User != nil || u.Fragment != "" || u.Path != "/my/orders" || !slices.Contains(h.Origins, u.Scheme+"://"+u.Host) {
			h.fail(w, p.ErrInvalid)
			return
		}
		if h.Service.Provider == nil {
			h.fail(w, p.ErrDisabled)
			return
		}
		config, e := h.Service.Provider.SignPage(r.Context(), u.String())
		h.reply(w, config, e)
		return
	}
	if path == "/api/h5/me/invoices" && r.Method == http.MethodGet {
		q := p.HistoryQuery{Cursor: r.URL.Query().Get("cursor"), Limit: 20}
		for key, values := range r.URL.Query() {
			if (key != "cursor" && key != "limit") || len(values) != 1 {
				h.fail(w, p.ErrInvalid)
				return
			}
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var e error
			q.Limit, e = strconv.Atoi(raw)
			if e != nil || q.Limit < 1 || q.Limit > 50 {
				h.fail(w, p.ErrInvalid)
				return
			}
		}
		page, e := h.Service.History(r.Context(), token, q)
		h.reply(w, page, e)
		return
	}
	if path == "/api/h5/me/invoices" && r.Method == http.MethodPost {
		var input struct {
			OrderID        int64         `json:"order_id"`
			Key            string        `json:"idempotency_key"`
			Buyer          *p.TitleInput `json:"buyer,omitempty"`
			ExpectedAmount *int64        `json:"expected_amount_minor,omitempty"`
		}
		if decode(w, r, &input) != nil {
			h.fail(w, p.ErrInvalid)
			return
		}
		var a p.Receipt
		var e error
		if input.Buyer != nil {
			if input.ExpectedAmount == nil {
				h.fail(w, p.ErrInvalid)
				return
			}
			a, e = h.Service.SubmitTitle(r.Context(), token, input.OrderID, input.Key, p.TitleSubmission{Buyer: *input.Buyer, ExpectedAmount: *input.ExpectedAmount})
		} else {
			if input.ExpectedAmount != nil {
				h.fail(w, p.ErrInvalid)
				return
			}
			a, e = h.Service.Create(r.Context(), token, input.OrderID, input.Key)
		}
		h.reply(w, a, e)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/h5/me/invoices/"), "/")
	if !strings.HasPrefix(path, "/api/h5/me/invoices/") || len(parts) > 2 || !validApplicationID(parts[0]) {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		a, e := h.Service.Read(r.Context(), token, parts[0])
		h.reply(w, p.MarshalReceipt(a), e)
		return
	}
	if len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost {
		var input struct{}
		if decode(w, r, &input) != nil {
			h.fail(w, p.ErrInvalid)
			return
		}
		a, e := h.Service.Resume(r.Context(), token, parts[0])
		h.reply(w, a, e)
		return
	}
	if len(parts) == 2 && parts[1] == "file" && r.Method == http.MethodGet {
		disposition := r.URL.Query().Get("disposition")
		if disposition != "" && disposition != "inline" && disposition != "attachment" {
			h.fail(w, p.ErrInvalid)
			return
		}
		b, e := h.Service.File(r.Context(), token, parts[0])
		if e != nil {
			h.fail(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		if disposition == "inline" {
			w.Header().Set("Content-Disposition", `inline; filename="invoice.pdf"`)
		} else {
			w.Header().Set("Content-Disposition", `attachment; filename="invoice.pdf"`)
		}
		w.Write(b)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	w.WriteHeader(http.StatusMethodNotAllowed)
}

var errCSRF = errors.New("invoice cross-site request rejected")

func validApplicationID(id string) bool {
	if len(id) != 32 || !strings.HasPrefix(id, "inv_") {
		return false
	}
	for _, c := range id[4:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (h *Handler) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if !slices.Contains(h.Origins, origin) {
		return false
	}
	u, e := url.Parse(origin)
	return e == nil && u.Host == r.Host
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return p.ErrInvalid
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return p.ErrInvalid
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return p.ErrInvalid
	}
	return nil
}
func (h *Handler) reply(w http.ResponseWriter, v any, e error) {
	if e != nil {
		h.fail(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func (h *Handler) fail(w http.ResponseWriter, e error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(e, payment.ErrSessionRequired):
		status, code = 401, "session_required"
	case errors.Is(e, p.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(e, errCSRF):
		status, code = 403, "cross_site_rejected"
	case errors.Is(e, p.ErrInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(e, p.ErrIneligible):
		status, code = 409, "ineligible"
	case errors.Is(e, p.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(e, p.ErrDisabled):
		code = "not_enabled"
	case errors.Is(e, p.ErrPending):
		status, code = 409, "pending"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}
func parseOrdersQuery(v url.Values) (p.OrdersQuery, error) {
	allowed := map[string]bool{"state": true, "keyword": true, "order_no": true, "from": true, "until": true, "cursor": true, "limit": true, "eligible": true}
	for key, values := range v {
		if !allowed[key] || len(values) != 1 {
			return p.OrdersQuery{}, p.ErrInvalid
		}
	}
	q := p.OrdersQuery{State: v.Get("state"), Keyword: strings.TrimSpace(v.Get("keyword")), OrderNo: strings.TrimSpace(v.Get("order_no")), Cursor: v.Get("cursor"), Limit: 20}
	if q.State != "" && q.State != "all" && q.State != "paid" && q.State != "pending" && q.State != "closed" {
		return q, p.ErrInvalid
	}
	if raw := v.Get("limit"); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 32)
		if e != nil || n < 1 || n > 50 {
			return q, p.ErrInvalid
		}
		q.Limit = int32(n)
	}
	if raw := v.Get("eligible"); raw != "" {
		if raw != "true" && raw != "false" {
			return q, p.ErrInvalid
		}
		q.EligibleOnly = raw == "true"
	}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	for key, dest := range map[string]**time.Time{"from": &q.From, "until": &q.Until} {
		if raw := v.Get(key); raw != "" {
			d, e := time.ParseInLocation("2006-01-02", raw, zone)
			if e != nil {
				return q, p.ErrInvalid
			}
			if key == "until" {
				d = d.AddDate(0, 0, 1)
			}
			d = d.UTC()
			*dest = &d
		}
	}
	if len(q.Cursor) > 400 || len(q.Keyword) > 200 || len(q.OrderNo) > 200 || q.From != nil && q.Until != nil && !q.From.Before(*q.Until) {
		return q, p.ErrInvalid
	}
	return q, nil
}

type assetManifest struct {
	Entries map[string]string `json:"entries"`
	Files   map[string]struct {
		SHA     string `json:"sha256"`
		Imports []struct {
			Path string `json:"path"`
		} `json:"imports"`
	} `json:"files"`
}

func (h *Handler) assets() (assetManifest, error) {
	var m assetManifest
	raw, e := os.ReadFile(filepath.Join(h.Dist, "asset-manifest.json"))
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(raw, &m)
	if e != nil {
		return m, e
	}
	for _, k := range []string{"customerOrdersHost", "customerOrdersStyles"} {
		v := m.Entries[k]
		if !strings.HasPrefix(v, "assets/") || filepath.Clean(v) != v {
			return m, p.ErrUnavailable
		}
		if _, ok := m.Files[v]; !ok {
			return m, p.ErrUnavailable
		}
	}
	return m, nil
}
func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(405)
		return
	}
	m, e := h.assets()
	if e != nil {
		http.Error(w, "订单页面暂不可用", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://res.wx.qq.com; style-src 'self' 'unsafe-inline'; connect-src 'self' https://*.weixin.qq.com; img-src 'self' data: https://*.wx.qq.com; frame-src https://*.weixin.qq.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	page := template.Must(template.New("orders").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>我的订单</title><link rel="stylesheet" href="/my/orders-assets/{{.CSS}}"><script type="module" src="/my/orders-assets/{{.JS}}"></script></head><body><main id="customer-orders-root" data-v3-public-commerce aria-live="polite"><p role="status">正在读取订单…</p></main></body></html>`))
	if r.Method != http.MethodHead {
		page.Execute(w, map[string]string{"CSS": m.Entries["customerOrdersStyles"], "JS": m.Entries["customerOrdersHost"]})
	}
}
func (h *Handler) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(405)
		return
	}
	m, e := h.assets()
	if e != nil {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/my/orders-assets/")
	if !strings.HasPrefix(relative, "assets/") || filepath.Clean(relative) != relative {
		http.NotFound(w, r)
		return
	}
	allowed := map[string]bool{}
	var visit func(string)
	visit = func(k string) {
		if allowed[k] {
			return
		}
		f, ok := m.Files[k]
		if !ok {
			return
		}
		allowed[k] = true
		for _, i := range f.Imports {
			visit(i.Path)
		}
	}
	visit(m.Entries["customerOrdersHost"])
	visit(m.Entries["customerOrdersStyles"])
	if !allowed[relative] {
		http.NotFound(w, r)
		return
	}
	b, e := os.ReadFile(filepath.Join(h.Dist, relative))
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(b)) != m.Files[relative].SHA {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, relative, time.Time{}, bytes.NewReader(b))
}
