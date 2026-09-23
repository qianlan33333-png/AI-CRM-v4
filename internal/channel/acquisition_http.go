package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
)

type AcquisitionApplication interface {
	Candidates(context.Context) ([]AcquisitionCandidate, error)
	LocalCandidates(context.Context) ([]AcquisitionCandidate, error)
	Preview(context.Context, int64) (channeldomain.Channel, []AcquisitionCandidate, error)
	Replace(context.Context, int64, AssignmentMutation) (channeldomain.Channel, []AcquisitionCandidate, error)
}

type AcquisitionHTTPHandler struct {
	application          AcquisitionApplication
	providerReadEnabled  bool
	providerWriteEnabled bool
	security             interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
		AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

type AcquisitionHTTPOptions struct {
	ProviderReadEnabled  bool
	ProviderWriteEnabled bool
}

func NewAcquisitionHTTPHandler(application AcquisitionApplication, security interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}, options ...AcquisitionHTTPOptions) (*AcquisitionHTTPHandler, error) {
	if application == nil || security == nil {
		return nil, errors.New("channel acquisition HTTP dependencies are required")
	}
	if len(options) > 1 {
		return nil, errors.New("channel acquisition HTTP options must be provided once")
	}
	var option AcquisitionHTTPOptions
	if len(options) == 1 {
		option = options[0]
	}
	return &AcquisitionHTTPHandler{application: application, security: security, providerReadEnabled: option.ProviderReadEnabled, providerWriteEnabled: option.ProviderWriteEnabled}, nil
}

func (handler *AcquisitionHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(r.URL.Path, "/")
	prefix := "/api/admin/channels/"
	if !strings.HasPrefix(path, prefix) {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 {
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	channelID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || channelID < 1 || r.URL.RawQuery != "" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	switch parts[1] {
	case "acquisition-preview":
		handler.preview(w, r, channelID)
	case "acquisition-staff":
		handler.staff(w, r, channelID)
	case "assignees":
		handler.replace(w, r, channelID)
	default:
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	}
}

func (handler *AcquisitionHTTPHandler) read(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := handler.security.Authenticate(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return accessdomain.Principal{}, false
	}
	if !channelCatalogReadRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func (handler *AcquisitionHTTPHandler) preview(w http.ResponseWriter, r *http.Request, channelID int64) {
	if r.Method != http.MethodGet || r.ContentLength > 0 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
		}
		writeCatalogError(w, statusForMethod(r.Method, http.MethodGet), "MALFORMED_REQUEST")
		return
	}
	if _, ok := handler.read(w, r); !ok {
		return
	}
	channel, candidates, err := handler.application.Preview(r.Context(), channelID)
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	writeChannelJSON(w, http.StatusOK, previewJSON(channel, candidates, handler.providerReadEnabled, handler.providerWriteEnabled))
}

func (handler *AcquisitionHTTPHandler) staff(w http.ResponseWriter, r *http.Request, channelID int64) {
	if r.Method != http.MethodGet || r.ContentLength > 0 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
		}
		writeCatalogError(w, statusForMethod(r.Method, http.MethodGet), "MALFORMED_REQUEST")
		return
	}
	if _, ok := handler.read(w, r); !ok {
		return
	}
	channel, local, err := handler.application.Preview(r.Context(), channelID)
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	all, providerErr := handler.application.Candidates(r.Context())
	providerSucceeded := providerErr == nil
	if providerErr != nil {
		all = local
	}
	assigned := make(map[int64]channeldomain.Assignee, len(channel.Config.Assignment.Assignees))
	for _, item := range channel.Config.Assignment.Assignees {
		assigned[item.StaffID] = item
	}
	items := make([]map[string]any, 0, len(all))
	for _, candidate := range all {
		item := map[string]any{"wecom_userid": candidate.WeComUserID, "display_name": candidate.DisplayName, "assigned": false}
		if assignment, ok := assigned[candidate.ID]; ok {
			item["assigned"] = true
			item["priority"] = assignment.Priority
			if assignment.Ratio > 0 {
				item["ratio_percent"] = assignment.Ratio
			}
			if assignment.MaxScans24h > 0 {
				item["max_scans_24h"] = assignment.MaxScans24h
			}
		}
		items = append(items, item)
	}
	writeChannelJSON(w, http.StatusOK, map[string]any{"channel_id": channelID, "items": items, "provider_source": "wecom_follow_user_list", "provider_read_succeeded": providerSucceeded, "publish_blocked": !providerSucceeded, "blocking_reason": map[bool]string{true: "", false: "provider_read_unavailable"}[providerSucceeded], "real_external_call_executed": false})
}

type assignmentRequest struct {
	Mode      string `json:"assignment_mode"`
	Strategy  string `json:"assignment_strategy"`
	Overflow  string `json:"overflow_policy"`
	Assignees []struct {
		StaffID     string `json:"staff_id"`
		Status      string `json:"status"`
		Priority    int    `json:"priority"`
		Ratio       int    `json:"ratio_percent"`
		MaxScans24h int    `json:"max_scans_24h"`
	} `json:"assignees"`
}

func (handler *AcquisitionHTTPHandler) replace(w http.ResponseWriter, r *http.Request, channelID int64) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		writeCatalogError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	principal, err := handler.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeCatalogError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	if !catalogWriteRole(principal) {
		writeCatalogError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	key, keyErr := singleCatalogIdempotencyKey(r)
	version, versionErr := catalogIfMatch(r.Header.Get("If-Match"))
	if keyErr != nil || versionErr != nil || r.Header.Get("Content-Type") != "application/json" {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalogHTTPMaxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body assignmentRequest
	if decoder.Decode(&body) != nil || func() bool { err := decoder.Decode(&struct{}{}); return !errors.Is(err, io.EOF) }() {
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	members := make([]AssignmentMember, len(body.Assignees))
	for index, item := range body.Assignees {
		if item.Status != "" && item.Status != "active" {
			writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		members[index] = AssignmentMember{WeComUserID: item.StaffID, Priority: item.Priority, Ratio: item.Ratio, MaxScans24h: item.MaxScans24h}
	}
	channel, selected, err := handler.application.Replace(r.Context(), channelID, AssignmentMutation{ActorID: principal.InternalID, IdempotencyKey: key, ExpectedVersion: version, Mode: channeldomain.AssignmentMode(body.Mode), Strategy: channeldomain.AssignmentStrategy(body.Strategy), OverflowPolicy: body.Overflow, Assignees: members})
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	w.Header().Set("ETag", "\""+strconv.FormatInt(channel.Version, 10)+"\"")
	writeChannelJSON(w, http.StatusOK, map[string]any{"channel_id": channelID, "assignees": acquisitionAssignees(channel, selected), "local_only": true, "provider_execution_eligible": false, "real_external_call_executed": false})
}

func previewJSON(channel channeldomain.Channel, candidates []AcquisitionCandidate, providerReadEnabled, providerWriteEnabled bool) map[string]any {
	state := "local_prerequisites_ready"
	blockers := make([]string, 0, 4)
	if !providerReadEnabled {
		blockers = append(blockers, "provider_read_disabled")
	}
	if !providerWriteEnabled {
		blockers = append(blockers, "provider_write_disabled")
	}
	if channel.Status == channeldomain.StatusInactive {
		state = "paused"
	}
	if channel.Status == channeldomain.StatusArchived {
		state = "archived"
	}
	if channel.Status != channeldomain.StatusActive {
		blockers = append(blockers, "channel_not_active")
	}
	if len(channel.Config.Assignment.Assignees) == 0 {
		blockers = append(blockers, "assignees_missing")
	}
	providerEligible := len(blockers) == 0
	qrcodeStatus := "not_generated"
	if channel.Config.QRCodeURL != "" {
		qrcodeStatus = "legacy_untracked"
	}
	return map[string]any{"channel_id": channel.ID, "channel_code": channel.Code, "channel_name": channel.Config.Name,
		"assignees": acquisitionAssignees(channel, candidates), "lifecycle": map[string]any{"state": state, "entrant_ready": providerEligible, "readiness_blockers": blockers},
		"qrcode": map[string]any{"status": qrcodeStatus, "scene_value": channel.Config.SceneValue, "url": channel.Config.QRCodeURL},
		"share":  map[string]any{"url": channel.Config.FinalURL, "copy_text": channel.Config.FinalURL}, "local_only": true, "provider_execution_eligible": providerEligible, "real_external_call_executed": false}
}

func acquisitionAssignees(channel channeldomain.Channel, candidates []AcquisitionCandidate) []map[string]any {
	byID := make(map[int64]AcquisitionCandidate, len(candidates))
	for _, item := range candidates {
		byID[item.ID] = item
	}
	result := make([]map[string]any, 0, len(channel.Config.Assignment.Assignees))
	for _, item := range channel.Config.Assignment.Assignees {
		candidate, ok := byID[item.StaffID]
		if !ok {
			continue
		}
		projected := map[string]any{"wecom_userid": candidate.WeComUserID, "display_name": candidate.DisplayName, "status": "active", "priority": item.Priority}
		if item.Ratio > 0 {
			projected["ratio_percent"] = item.Ratio
		}
		if item.MaxScans24h > 0 {
			projected["max_scans_24h"] = item.MaxScans24h
		}
		result = append(result, projected)
	}
	return result
}

func writeAcquisitionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCatalogNotFound):
		writeCatalogError(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, ErrCatalogConflict):
		writeCatalogError(w, http.StatusConflict, "VERSION_CONFLICT")
	case errors.Is(err, ErrInvalidCatalogCommand):
		writeCatalogError(w, http.StatusBadRequest, "MALFORMED_REQUEST")
	default:
		writeCatalogError(w, http.StatusServiceUnavailable, "PROVIDER_READ_UNAVAILABLE")
	}
}

func statusForMethod(actual, expected string) int {
	if actual != expected {
		return http.StatusMethodNotAllowed
	}
	return http.StatusBadRequest
}

type CenterHTTPHandler struct{ Catalog, Acquisition, History, Assets http.Handler }

func (handler CenterHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasSuffix(path, "/acquisition-preview") || strings.HasSuffix(path, "/acquisition-staff") || strings.HasSuffix(path, "/assignees") {
		handler.Acquisition.ServeHTTP(w, r)
		return
	}
	if strings.HasSuffix(path, "/history") || strings.HasSuffix(path, "/contacts") {
		handler.History.ServeHTTP(w, r)
		return
	}
	if strings.Contains(path, "/acquisition-assets") || strings.Contains(path, "/qrcode/") {
		handler.Assets.ServeHTTP(w, r)
		return
	}
	handler.Catalog.ServeHTTP(w, r)
}
