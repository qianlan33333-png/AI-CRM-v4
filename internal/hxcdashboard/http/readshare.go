package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
	"net/http"
	"strconv"
	"time"
)

var publicFields = map[string]bool{"stage": true, "subscription_tier": true, "subscription_expires_at": true, "sessions_7d": true, "user_messages_7d": true, "last_used_at": true}

func (h Handler) shares(w http.ResponseWriter, r *http.Request) {
	p, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !p.IsSuperAdmin() {
		writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	if r.Method == http.MethodGet {
		items, err := h.Store.ListReadShares(r.Context(), 1)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"shares": items})
		return
	}
	if _, err = h.Auth.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, err)
		return
	}
	var v readshare.Share
	if decode(r, &v) != nil {
		writeError(w, errors.New("invalid_share"))
		return
	}
	v.ResourceID = 1
	if r.Method == http.MethodPost {
		var config struct {
			Query        queryRequest    `json:"query"`
			Presentation json.RawMessage `json:"presentation"`
		}
		if v.ID != 0 || len(v.Config) > 16384 || json.Unmarshal(v.Config, &config) != nil || config.Query.ExactHXCUserID != "" || config.Query.Cursor != "" || config.Query.ProjectionID != 0 || !validQuery(config.Query) || (v.Mode != "metrics" && v.Mode != "details") || !readshare.ValidFields(v.Fields, publicFields) {
			writeError(w, errors.New("invalid_share"))
			return
		}
	} else if v.ID < 1 || v.Version < 1 {
		writeError(w, errors.New("invalid_share"))
		return
	}
	rawKey := r.Header.Get("Idempotency-Key")
	if _, err = idempotency.Parse(rawKey); err != nil {
		writeError(w, err)
		return
	}
	token := ""
	if r.Method == http.MethodPost {
		secret, e := h.Store.ReadShareKey(r.Context())
		if e != nil || len(secret) != 32 {
			writeError(w, errors.New("unavailable"))
			return
		}
		token = readshare.Token(secret, "hxc", p.InternalID, rawKey)
		v.Digest = readshare.Digest(token)
	}
	key := sha256.Sum256([]byte("share\x00" + rawKey))
	payload, _ := json.Marshal(struct {
		Share  readshare.Share
		Digest []byte
	}{v, v.Digest})
	digest := sha256.Sum256(append([]byte(r.Method+"\x00"), payload...))
	var out readshare.Share
	if h.Service.UOW == nil || h.Service.Audit == nil {
		writeError(w, errors.New("unavailable"))
		return
	}
	err = h.Service.UOW.Within(r.Context(), func(ctx context.Context) error {
		var replay bool
		var e error
		out, replay, e = h.Store.SaveReadShareReceipted(ctx, p.InternalID, key[:], digest[:], v)
		if e != nil || replay {
			return e
		}
		_, e = h.Service.Audit.Append(ctx, platformaudit.Event{IdempotencyKey: idempotency.Key("hxc-share:" + strconv.FormatInt(p.InternalID, 10) + ":" + hex.EncodeToString(key[:])), Action: "hxc.dashboard_share_changed", ActorType: "admin", ActorID: strconv.FormatInt(p.InternalID, 10), ResourceType: "hxc_dashboard_share", ResourceID: strconv.FormatInt(out.ID, 10), Payload: json.RawMessage(`{"changed":true}`)})
		return e
	})
	if errors.Is(err, hxcstore.ErrViewConflict) {
		writeJSON(w, 409, map[string]string{"error": "share_conflict"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"share": out, "token": token})
}

// intersectValues represents disjoint constraints with an impossible value,
// never an empty slice (which means unrestricted to the query builder).
func intersectValues(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	if len(base) == 0 {
		return extra
	}
	out := []string{}
	for _, v := range base {
		for _, e := range extra {
			if v == e {
				out = append(out, v)
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{"__no_match__"}
	}
	return out
}
func (h Handler) publicQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string       `json:"token"`
		Query queryRequest `json:"query"`
	}
	if decode(r, &body) != nil || len(body.Token) != 43 {
		writeError(w, errors.New("invalid_query"))
		return
	}
	share, err := h.Store.ReadShare(r.Context(), readshare.Digest(body.Token))
	if err != nil {
		writeJSON(w, 410, map[string]string{"error": "share_gone"})
		return
	}
	var config struct {
		Query        queryRequest    `json:"query"`
		Presentation json.RawMessage `json:"presentation"`
	}
	if json.Unmarshal(share.Config, &config) != nil {
		writeError(w, errors.New("unavailable"))
		return
	}
	q := body.Query
	if !validSort(q.Sort) || !validGroup(q.GroupBy) || q.ExactHXCUserID != "" || q.ProjectionID != 0 || len(q.Cursor) > 4096 || len(q.Filters.IdentityState)+len(q.Filters.MatchedBy)+len(q.Filters.IdentityReason)+len(q.Filters.LastCapability)+len(q.Filters.BusinessStage)+len(q.Filters.UserSegment) > 0 || !validFreeform(q.Filters.Stage) || !validFreeform(q.Filters.SubscriptionTier) {
		writeError(w, errors.New("invalid_query"))
		return
	}
	allowed := map[string]bool{}
	for _, f := range share.Fields {
		allowed[f] = true
	}
	if (q.GroupBy != "" && !allowed[q.GroupBy]) || (len(q.Filters.Stage) > 0 && !allowed["stage"]) || (len(q.Filters.SubscriptionTier) > 0 && !allowed["subscription_tier"]) {
		writeError(w, errors.New("invalid_query"))
		return
	}
	sortField := map[string]string{"last_used_at_desc": "last_used_at", "source_updated_at_desc": "source_updated_at", "subscription_expires_at_asc": "subscription_expires_at", "subscription_expires_at_desc": "subscription_expires_at", "messages_7d_desc": "user_messages_7d"}[q.Sort]
	if q.Sort != "" && !allowed[sortField] {
		writeError(w, errors.New("invalid_query"))
		return
	}
	summary, err := h.Store.Summary(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	offset := 0
	if q.Cursor != "" {
		c, e := h.verifyCursor(q.Cursor)
		if e != nil || c.ProjectionID != summary.ID || c.QueryHash != queryFingerprint(q, body.Token) {
			writeError(w, errors.New("invalid_cursor"))
			return
		}
		offset = c.Offset
	}
	f := config.Query.Filters
	query := hxcstore.Query{ProjectionID: summary.ID, Stages: intersectValues(f.Stage, q.Filters.Stage), SubscriptionTiers: intersectValues(f.SubscriptionTier, q.Filters.SubscriptionTier), LastCapabilities: f.LastCapability, BusinessStages: f.BusinessStage, UserSegments: f.UserSegment, IdentityStates: f.IdentityState, MatchedBy: f.MatchedBy, IdentityReasonCodes: f.IdentityReason, Sort: q.Sort, GroupBy: q.GroupBy, Limit: 50, Offset: offset}
	result, err := h.Store.QueryWorkspace(r.Context(), query)
	if err != nil {
		writeError(w, err)
		return
	}
	metrics, tiers := result.Metrics, result.Tiers
	safe := []map[string]any{}
	next := ""
	groups := []hxcstore.Group{}
	if share.Mode == "details" {
		rows, more := result.Items, result.More
		groups = result.Groups
		for _, row := range rows {
			raw, _ := json.Marshal(row)
			var source map[string]any
			_ = json.Unmarshal(raw, &source)
			item := map[string]any{"user_ref": row.UserRef}
			for _, field := range share.Fields {
				item[field] = source[field]
			}
			safe = append(safe, item)
		}
		if more {
			next = h.signCursor(cursor{ProjectionID: summary.ID, Offset: offset + len(rows), QueryHash: queryFingerprint(q, body.Token)})
		}
	}
	if _, err = h.Store.ReadShare(r.Context(), readshare.Digest(body.Token)); err != nil {
		writeJSON(w, 410, map[string]string{"error": "share_gone"})
		return
	}
	publicMetrics := map[string]any{}
	for key, value := range metrics {
		publicMetrics[key] = value
	}
	publicMetrics["tiers"] = tiers
	publicMetrics = readshare.ProjectMetrics(publicMetrics, config.Presentation)
	publicTiers := publicMetrics["tiers"]
	delete(publicMetrics, "tiers")
	writeJSON(w, 200, map[string]any{"items": safe, "metrics": publicMetrics, "tiers": publicTiers, "total": metrics["total"], "groups": groups, "next_cursor": next, "mode": share.Mode, "fields": share.Fields, "presentation": config.Presentation, "snapshot_at": summary.AsOf, "stale": time.Since(summary.PublishedAt) > 8*time.Hour})
}
