package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// audienceOperationMemberDirectory binds the Access-owned staff read to a
// read-only Unit of Work. Access stores intentionally reject unbound request
// contexts, so the HTTP adapter must not pass r.Context() directly through.
type audienceOperationMemberDirectory struct {
	uow       platformport.UnitOfWork
	directory interface {
		ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error)
	}
}

// audienceOperationMemberSubtree keeps the shared picker refresh endpoint in
// the audience scope. Group Ops owns the existing /sync contract for all
// other scopes; audience sender refresh is a read-only Access directory read.
type audienceOperationMemberSubtree struct {
	audience http.Handler
	groupOps http.Handler
}

func (handler audienceOperationMemberSubtree) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("scope") == "audience_senders" {
		handler.audience.ServeHTTP(w, r)
		return
	}
	handler.groupOps.ServeHTTP(w, r)
}

func (directory audienceOperationMemberDirectory) ListEligibleStaff(ctx context.Context) (items []groupopsport.OperationMember, err error) {
	if directory.uow == nil || directory.directory == nil {
		return nil, errors.New("audience operation-member directory is unavailable")
	}
	err = directory.uow.Within(ctx, func(tx context.Context) error {
		items, err = directory.directory.ListEligibleStaff(tx)
		return err
	})
	return items, err
}

// audienceOperationMemberPicker exposes the Access-owned eligible staff
// directory to the audience sender allowlist. It is deliberately read-only:
// selecting a member only returns the verified WeCom userid; the Segment
// command remains the owner of the persisted sender set.
type audienceOperationMemberPicker struct {
	directory interface {
		ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error)
	}
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

func (picker audienceOperationMemberPicker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if picker.directory == nil || picker.security == nil || r.URL.Query().Get("scope") != "audience_senders" {
		writeAudienceOperationMemberError(w, http.StatusNotFound, "not_found")
		return
	}
	principal, err := picker.security.Authenticate(r.Context(), r)
	if err != nil || principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		writeAudienceOperationMemberError(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeAudienceOperationMemberError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	pageSize := 100
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 {
			writeAudienceOperationMemberError(w, http.StatusBadRequest, "invalid_page")
			return
		}
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	items, err := picker.directory.ListEligibleStaff(r.Context())
	if err != nil {
		writeAudienceOperationMemberError(w, http.StatusServiceUnavailable, "staff_directory_unavailable")
		return
	}
	filtered := make([]audienceOperationMember, 0, len(items))
	for _, item := range items {
		if item.StaffID < 1 || strings.TrimSpace(item.SenderUserID) == "" || strings.TrimSpace(item.DisplayName) == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.SenderUserID), query) && !strings.Contains(strings.ToLower(item.DisplayName), query) {
			continue
		}
		filtered = append(filtered, audienceOperationMember{StaffID: item.StaffID, UserID: item.SenderUserID, DisplayName: item.DisplayName, Active: true})
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].DisplayName == filtered[j].DisplayName {
			return filtered[i].StaffID < filtered[j].StaffID
		}
		return filtered[i].DisplayName < filtered[j].DisplayName
	})
	if len(filtered) > pageSize {
		filtered = filtered[:pageSize]
	}
	writeAudienceOperationMemberJSON(w, http.StatusOK, map[string]any{"scope": "audience_senders", "page_size": pageSize, "items": filtered})
}

type audienceOperationMember struct {
	StaffID     int64  `json:"staff_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

func writeAudienceOperationMemberError(w http.ResponseWriter, status int, code string) {
	writeAudienceOperationMemberJSON(w, status, map[string]any{"ok": false, "error": code})
}

func writeAudienceOperationMemberJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
