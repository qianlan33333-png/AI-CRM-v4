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
	"net/http"
	"strconv"
	"strings"
)

func (h Handler) views(w http.ResponseWriter, r *http.Request) {
	p, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		views, err := h.Store.ListViews(r.Context(), string(p.Kind), p.InternalID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"views": views})
		return
	}
	if _, err = h.Auth.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, err)
		return
	}
	if !dashboardRefreshWriteRole(p) {
		writeError(w, accessdomain.ErrPermissionDenied)
		return
	}
	var v hxcstore.View
	if decode(r, &v) != nil || v.ID < 0 || (v.ID > 0 && v.Version < 1) {
		writeError(w, errors.New("invalid_view"))
		return
	}
	if r.Method != http.MethodDelete {
		var config struct {
			Query        queryRequest    `json:"query"`
			Presentation json.RawMessage `json:"presentation"`
		}
		if len(v.Config) > 16384 || json.Unmarshal(v.Config, &config) != nil || strings.TrimSpace(v.Name) == "" || len(v.Name) > 60 || config.Query.ExactHXCUserID != "" || config.Query.Cursor != "" || config.Query.ProjectionID != 0 || !validQuery(config.Query) {
			writeError(w, errors.New("invalid_view"))
			return
		}
	}
	rawKey := r.Header.Get("Idempotency-Key")
	if _, err = idempotency.Parse(rawKey); err != nil {
		writeError(w, err)
		return
	}
	key := sha256.Sum256([]byte(rawKey))
	payload, _ := json.Marshal(v)
	digest := sha256.Sum256(append([]byte(r.Method+"\x00"), payload...))
	if h.Service.UOW == nil || h.Service.Audit == nil {
		writeError(w, errors.New("unavailable"))
		return
	}
	var out hxcstore.View
	err = h.Service.UOW.Within(r.Context(), func(ctx context.Context) error {
		var replay bool
		var e error
		out, replay, e = h.Store.SaveView(ctx, string(p.Kind), p.InternalID, key[:], digest[:], v, r.Method == http.MethodDelete)
		if e != nil || replay {
			return e
		}
		_, e = h.Service.Audit.Append(ctx, platformaudit.Event{IdempotencyKey: idempotency.Key("hxc-view:" + string(p.Kind) + ":" + strconv.FormatInt(p.InternalID, 10) + ":" + hex.EncodeToString(key[:])), Action: "hxc.dashboard_view_changed", ActorType: string(p.Kind), ActorID: strconv.FormatInt(p.InternalID, 10), ResourceType: "hxc_dashboard_view", ResourceID: strconv.FormatInt(out.ID, 10), Payload: json.RawMessage(`{"changed":true}`)})
		return e
	})
	if errors.Is(err, hxcstore.ErrViewConflict) {
		writeJSON(w, 409, map[string]string{"error": "view_conflict"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"view": out})
}
