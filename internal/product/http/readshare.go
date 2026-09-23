package http

import (
	"encoding/json"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"net/http"
)

var publicGridFields = map[string]bool{"remaining_days": true, "formally_logged_in": true, "token_usage": true, "learning_plan_progress": true, "open_count_7d": true, "last_open_at": true, "renewal_count": true}

func (h *Handler) scopedShares(w http.ResponseWriter, r *http.Request, id int64) {
	access, actor, ok := h.memberGridAuthorize(w, r, id, r.Method != http.MethodGet)
	if !ok {
		return
	}
	if !access.CanShare {
		writeError(w, 403, "permission_denied")
		return
	}
	service, ok := h.workspace.(productport.ScopedMemberGridShares)
	if !ok {
		writeError(w, 503, "unavailable")
		return
	}
	if r.Method == http.MethodGet {
		items, err := service.ListScopedShares(r.Context(), productport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"shares": items})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		methodNotAllowed(w, "GET, POST, DELETE")
		return
	}
	var value readshare.Share
	if decodeJSON(r, &value) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	value.ResourceID = id
	if r.Method == http.MethodPost {
		if value.ID != 0 || (value.Mode != "metrics" && value.Mode != "details") || !readshare.ValidFields(value.Fields, publicGridFields) {
			writeError(w, 400, "invalid_request")
			return
		}
		config, err := decodeDonorGridConfig(value.Config)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		value.Config, _ = json.Marshal(config)
	} else if value.ID < 1 || value.Version < 1 {
		writeError(w, 400, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	token := ""
	if r.Method == http.MethodPost {
		token, err = service.ScopedShareToken(r.Context(), actor.AdminUserID, key)
		if err != nil {
			resultError(w, err)
			return
		}
		value.Digest = readshare.Digest(token)
	}
	out, err := service.SaveScopedShare(r.Context(), value, actor, key)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"share": out, "token": token})
}
func (h *Handler) publicScopedQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	service, ok := h.workspace.(productport.ScopedMemberGridShares)
	if !ok {
		writeError(w, 503, "unavailable")
		return
	}
	var body struct {
		Token  string          `json:"token"`
		Config json.RawMessage `json:"config"`
		Cursor string          `json:"cursor"`
	}
	if decodeJSON(r, &body) != nil || len(body.Token) != 43 || len(body.Cursor) > 4096 {
		writeError(w, 400, "invalid_request")
		return
	}
	share, err := service.ReadScopedShare(r.Context(), readshare.Digest(body.Token))
	if err != nil {
		writeError(w, 410, "share_gone")
		return
	}
	// Product lifecycle visibility is checked on every read, not only at issue.
	if _, err = h.service.GetServicePeriodProduct(r.Context(), productport.ID(share.ResourceID)); err != nil {
		writeError(w, 410, "share_gone")
		return
	}
	base, err := decodeDonorGridConfig(share.Config)
	if err != nil {
		writeError(w, 410, "share_gone")
		return
	}
	query, err := decodeDonorGridConfig(body.Config)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	allowed := map[string]bool{}
	for _, f := range share.Fields {
		allowed[f] = true
	}
	for _, c := range query.Filter.Conditions {
		if !allowed[c.Field] {
			writeError(w, 400, "invalid_request")
			return
		}
	}
	for _, o := range append(query.Groups, query.Sorts...) {
		if !allowed[o.Field] {
			writeError(w, 400, "invalid_request")
			return
		}
	}
	query.Base = &base
	var metrics memberGridMetrics
	rows, next, err := h.queryDonorGridWithMetrics(r.Context(), share.ResourceID, query, body.Cursor, 50, &metrics)
	if err != nil {
		if !productMemberGridQueryError(w, err) {
			resultError(w, err)
		}
		return
	}
	safe := []map[string]any{}
	if share.Mode == "details" {
		records := make([]string, 0, len(rows))
		for _, row := range rows {
			records = append(records, row["record_id"].(string))
		}
		references, e := service.ScopedRowReferences(r.Context(), share.ID, records)
		if e != nil {
			resultError(w, e)
			return
		}
		for _, row := range rows {
			v := row["values"].(map[string]any)
			item := map[string]any{"user_ref": references[row["record_id"].(string)]}
			for _, f := range share.Fields {
				item[f] = v[f]
			}
			counts := map[string]any{}
			values := map[string]any{}
			for _, raw := range row["group_path"].([]any) {
				g := raw.(map[string]any)
				field := g["field"].(string)
				item[field] = g["label"]
				counts[field] = g["count"]
				values[field] = []any{g["value"], g["unavailable"]}
			}
			item["__groupCounts"] = counts
			item["__groupValues"] = values
			safe = append(safe, item)
		}
	} else {
		next = ""
	}
	// Recheck revocation after the bounded query, before any response is emitted.
	if _, err = service.ReadScopedShare(r.Context(), readshare.Digest(body.Token)); err != nil {
		writeError(w, 410, "share_gone")
		return
	}
	metricBytes, _ := json.Marshal(metrics)
	var publicMetrics map[string]any
	_ = json.Unmarshal(metricBytes, &publicMetrics)
	delete(publicMetrics, "snapshot_at")
	publicMetrics = readshare.ProjectMetrics(publicMetrics, base.Presentation)
	writeJSON(w, 200, map[string]any{"items": safe, "total": metrics.Total, "metrics": publicMetrics, "snapshot_at": metrics.SnapshotAt, "next_cursor": next, "mode": share.Mode, "fields": share.Fields, "presentation": base.Presentation})
}
