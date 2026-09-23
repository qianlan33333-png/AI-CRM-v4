package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
)

// channelOperationMemberDirectory is the narrow local staff directory used by
// the Channel Host. It has no provider-read or mutation capability: opening a
// picker must not depend on unrelated data or a live WeCom request.
type channelOperationMemberDirectory interface {
	LocalCandidates(context.Context) ([]channelstore.AcquisitionCandidate, error)
}

type channelOperationMemberPicker struct {
	directory channelOperationMemberDirectory
	security  interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

func (picker channelOperationMemberPicker) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeChannelOperationMemberError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if picker.directory == nil || picker.security == nil || !validChannelOperationMemberQuery(request) {
		writeChannelOperationMemberError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	principal, err := picker.security.Authenticate(request.Context(), request)
	if err != nil || !channelOperationMemberReadRole(principal) {
		status, code := http.StatusUnauthorized, "authentication_required"
		if err == nil {
			status, code = http.StatusForbidden, "permission_denied"
		}
		writeChannelOperationMemberError(response, status, code)
		return
	}
	pageSize := 100
	if raw := request.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 || strconv.Itoa(pageSize) != raw {
			writeChannelOperationMemberError(response, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	candidates, err := picker.directory.LocalCandidates(request.Context())
	if err != nil {
		writeChannelOperationMemberError(response, http.StatusServiceUnavailable, "staff_directory_unavailable")
		return
	}
	items := make([]channelOperationMember, 0, min(pageSize, len(candidates)))
	for _, candidate := range candidates {
		if candidate.ID < 1 || strings.TrimSpace(candidate.WeComUserID) == "" || strings.TrimSpace(candidate.DisplayName) == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(candidate.WeComUserID), query) && !strings.Contains(strings.ToLower(candidate.DisplayName), query) {
			continue
		}
		items = append(items, channelOperationMember{StaffID: candidate.ID, UserID: candidate.WeComUserID, DisplayName: candidate.DisplayName, Active: true})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DisplayName == items[j].DisplayName {
			return items[i].StaffID < items[j].StaffID
		}
		return items[i].DisplayName < items[j].DisplayName
	})
	if len(items) > pageSize {
		items = items[:pageSize]
	}
	writeChannelOperationMemberJSON(response, http.StatusOK, map[string]any{"scope": "channel_code", "page_size": pageSize, "items": items})
}

type channelOperationMember struct {
	StaffID     int64  `json:"staff_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

func validChannelOperationMemberQuery(request *http.Request) bool {
	for key, values := range request.URL.Query() {
		if len(values) != 1 || (key != "scope" && key != "page_size" && key != "q") {
			return false
		}
	}
	return request.URL.Query().Get("scope") == "channel_code" && len(request.URL.Query().Get("q")) <= 200
}

func channelOperationMemberReadRole(principal accessdomain.Principal) bool {
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

func writeChannelOperationMemberError(response http.ResponseWriter, status int, code string) {
	writeChannelOperationMemberJSON(response, status, map[string]any{"ok": false, "error": code})
}

func writeChannelOperationMemberJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
