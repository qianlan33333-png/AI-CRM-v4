package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
)

func groupQuery(r *http.Request) (*string, error) {
	if _, ok := r.URL.Query()["category"]; !ok {
		return nil, nil
	}
	value, err := scalarQuery(r, "category")
	if err != nil {
		return nil, err
	}
	if len([]rune(value)) > 100 || strings.TrimSpace(value) != value {
		return nil, mediaapp.ErrHTTPInvalid
	}
	return &value, nil
}
func (h *Handler) materialGroupRoute(w http.ResponseWriter, r *http.Request, kind, tail string) bool {
	if tail == "group-members" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return true
		}
		service, ok := h.service.(mediaapp.GroupMemberReader)
		if !ok {
			writeError(w, 503, "unavailable")
			return true
		}
		ids := []int64{}
		for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
			v, e := id(raw)
			if e != nil {
				writeError(w, 400, "invalid_request")
				return true
			}
			ids = append(ids, v)
		}
		items, e := service.MaterialGroupMembers(r.Context(), kind, ids)
		if e != nil {
			resultError(w, e)
			return true
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return true
	}
	parts := strings.Split(tail, "/")
	isFacets := tail == "groups"
	if tail == "group-moves" || (isFacets && r.Method != http.MethodGet) || (len(parts) == 2 && parts[0] == "groups") {
		return h.manageGroup(w, r, kind, tail)
	}
	isUpdate := len(parts) == 2 && parts[1] == "group"
	if !isFacets && !isUpdate {
		return false
	}
	service, ok := h.service.(mediaapp.MaterialGrouping)
	if !ok {
		writeError(w, 503, "unavailable")
		return true
	}
	if isFacets {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return true
		}
		groups, e := service.MaterialGroups(r.Context(), kind)
		if e != nil {
			resultError(w, e)
			return true
		}
		principal, _ := h.security.Authenticate(r.Context(), r)
		writeJSON(w, 200, map[string]any{"items": groups, "can_write": writeRole(principal)})
		return true
	}
	if !method(w, r.Method, http.MethodPut) {
		return true
	}
	actor, ok := h.write(w, r)
	if !ok {
		return true
	}
	resource, e := id(parts[0])
	if e != nil {
		writeError(w, 400, "invalid_request")
		return true
	}
	var body struct {
		Category string `json:"category"`
		Version  int64  `json:"expected_version"`
	}
	if decode(r, &body) != nil {
		writeError(w, 400, "invalid_request")
		return true
	}
	result, e := service.SetMaterialGroup(r.Context(), kind, resource, actor.InternalID, body.Version, mutationKey(r), body.Category)
	if e != nil {
		resultError(w, e)
		return true
	}
	writeJSON(w, 200, result)
	return true
}

func (h *Handler) manageGroup(w http.ResponseWriter, r *http.Request, kind, tail string) bool {
	operation := ""
	switch {
	case tail == "groups" && r.Method == http.MethodPost:
		operation = "create"
	case tail == "group-moves" && r.Method == http.MethodPost:
		operation = "move"
	case strings.HasPrefix(tail, "groups/") && r.Method == http.MethodPut:
		operation = "rename"
	case strings.HasPrefix(tail, "groups/") && r.Method == http.MethodDelete:
		operation = "delete"
	default:
		writeError(w, 405, "method_not_allowed")
		return true
	}
	actor, ok := h.write(w, r)
	if !ok {
		return true
	}
	service, ok := h.service.(mediaapp.GroupManagement)
	if !ok {
		writeError(w, 503, "unavailable")
		return true
	}
	var cmd mediaapp.GroupCommand
	if decode(r, &cmd) != nil {
		writeError(w, 400, "invalid_request")
		return true
	}
	if operation == "rename" || operation == "delete" {
		resource, e := id(strings.TrimPrefix(tail, "groups/"))
		if e != nil {
			writeError(w, 400, "invalid_request")
			return true
		}
		cmd.ID = resource
	}
	out, e := service.ManageMaterialGroup(r.Context(), kind, operation, actor.InternalID, mutationKey(r), cmd)
	if e != nil {
		resultError(w, e)
		return true
	}
	writeJSON(w, 200, out)
	return true
}

func optionalGroupID(raw string) (*int64, error) {
	if raw == "" || raw == "null" {
		return nil, nil
	}
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || v < 1 {
		return nil, errors.New("invalid group")
	}
	return &v, nil
}
