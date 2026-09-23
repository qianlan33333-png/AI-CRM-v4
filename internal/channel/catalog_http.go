package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

const catalogHTTPMaxBody int64 = 64 << 10

type CatalogApplication interface {
	Get(context.Context, int64) (channeldomain.Channel, error)
	List(context.Context, channelport.CatalogFilter) (channelport.CatalogPage, error)
	Create(context.Context, CatalogMutation) (channeldomain.Channel, error)
	Update(context.Context, int64, CatalogMutation) (channeldomain.Channel, error)
}

type CatalogHTTPConfig struct {
	Application CatalogApplication
	Summaries   CatalogSummaryReader
	Security    interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
		AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
	}
	CursorSigningKey []byte
	Now              func() time.Time
}

type CatalogHTTPHandler struct {
	application CatalogApplication
	summaries   CatalogSummaryReader
	security    CatalogHTTPConfig
}

func NewCatalogHTTPHandler(config CatalogHTTPConfig) (*CatalogHTTPHandler, error) {
	if config.Application == nil || config.Security == nil || len(config.CursorSigningKey) < 32 {
		return nil, errors.New("channel catalog HTTP dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &CatalogHTTPHandler{application: config.Application, summaries: config.Summaries, security: config}, nil
}

func (handler *CatalogHTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(request.URL.Path, "/")
	if path == "/api/admin/channels" {
		switch request.Method {
		case http.MethodGet:
			handler.list(response, request)
		case http.MethodPost:
			handler.create(response, request)
		default:
			response.Header().Set("Allow", "GET, POST")
			writeCatalogError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		}
		return
	}
	prefix := "/api/admin/channels/"
	if !strings.HasPrefix(path, prefix) || strings.Contains(strings.TrimPrefix(path, prefix), "/") {
		writeCatalogError(response, http.StatusNotFound, "NOT_FOUND")
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(path, prefix), 10, 64)
	if err != nil || id < 1 {
		writeCatalogError(response, http.StatusNotFound, "NOT_FOUND")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.get(response, request, id)
	case http.MethodPatch:
		handler.update(response, request, id)
	default:
		response.Header().Set("Allow", "GET, PATCH")
		writeCatalogError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

func (handler *CatalogHTTPHandler) list(response http.ResponseWriter, request *http.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.securityError(response, err)
		return
	}
	if request.ContentLength > 0 || !onlyChannelQuery(request, "limit", "include_archived", "cursor", "status", "q") {
		writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	filter := channelport.CatalogFilter{Limit: 50, Keyword: request.URL.Query().Get("q"), Status: channeldomain.Status(request.URL.Query().Get("status"))}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		filter.Limit = value
	}
	if raw := request.URL.Query().Get("include_archived"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
			return
		}
		filter.IncludeArchived = value
	}
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		filter.AfterID = handler.parseCursor(raw, filter)
		if filter.AfterID < 1 {
			writeCatalogError(response, http.StatusBadRequest, "INVALID_CURSOR")
			return
		}
	}
	page, err := handler.application.List(request.Context(), filter)
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	items := make([]catalogListJSON, len(page.Items))
	ids := make([]int64, len(page.Items))
	for index := range page.Items {
		ids[index] = page.Items[index].ID
	}
	summaries, err := handler.readSummaries(request.Context(), ids)
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	for index, item := range page.Items {
		items[index] = projectCatalogList(item, summaries[item.ID])
	}
	next := ""
	if page.NextCursor != "" {
		next = handler.signCursor(page.Items[len(page.Items)-1].ID, filter)
	}
	writeChannelJSON(response, http.StatusOK, map[string]any{"ok": true, "channels": items, "items": items, "total": page.Total, "next_cursor": next, "reason": "channels_listed", "source": "ai_crm_next"})
}

func (handler *CatalogHTTPHandler) get(response http.ResponseWriter, request *http.Request, id int64) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.securityError(response, err)
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength > 0 {
		writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	channel, err := handler.application.Get(request.Context(), id)
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	summaries, err := handler.readSummaries(request.Context(), []int64{id})
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	response.Header().Set("ETag", fmt.Sprintf(`"%d"`, channel.Version))
	writeChannelJSON(response, http.StatusOK, map[string]any{"ok": true, "channel": projectCatalogDetail(channel, summaries[id]), "reason": "channel_loaded", "source": "ai_crm_next"})
}

func (handler *CatalogHTTPHandler) create(response http.ResponseWriter, request *http.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.securityError(response, err)
		return
	}
	key, err := singleCatalogIdempotencyKey(request)
	if err != nil || request.URL.RawQuery != "" {
		writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	input, err := decodeCatalogWrite(response, request, principal.InternalID)
	if err != nil {
		writeCatalogError(response, catalogDecodeStatus(err), "MALFORMED_REQUEST")
		return
	}
	channel, err := handler.application.Create(request.Context(), CatalogMutation{ActorID: principal.InternalID, IdempotencyKey: key, Create: channeldomain.CreateChannel{Code: input.Code, Status: input.Status, Config: input.Config}})
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	response.Header().Set("ETag", fmt.Sprintf(`"%d"`, channel.Version))
	writeChannelJSON(response, http.StatusCreated, catalogMutationResponse(channel, "channel_created"))
}

func (handler *CatalogHTTPHandler) update(response http.ResponseWriter, request *http.Request, id int64) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.securityError(response, err)
		return
	}
	key, err := singleCatalogIdempotencyKey(request)
	version, versionErr := catalogIfMatch(request.Header.Get("If-Match"))
	if err != nil || versionErr != nil || request.URL.RawQuery != "" {
		writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
		return
	}
	input, err := decodeCatalogWrite(response, request, principal.InternalID)
	if err != nil {
		writeCatalogError(response, catalogDecodeStatus(err), "MALFORMED_REQUEST")
		return
	}
	channel, err := handler.application.Update(request.Context(), id, CatalogMutation{ActorID: principal.InternalID, IdempotencyKey: key, Update: channeldomain.UpdateChannel{ExpectedVersion: version, Code: input.Code, Status: input.Status, Config: input.Config}})
	if err != nil {
		handler.applicationError(response, err)
		return
	}
	response.Header().Set("ETag", fmt.Sprintf(`"%d"`, channel.Version))
	writeChannelJSON(response, http.StatusOK, catalogMutationResponse(channel, "channel_updated"))
}

type catalogWriteJSON struct {
	ChannelType           string                      `json:"channel_type"`
	CarrierType           string                      `json:"carrier_type"`
	ChannelName           string                      `json:"channel_name"`
	ChannelCode           string                      `json:"channel_code"`
	SceneValue            string                      `json:"scene_value"`
	QRCodeURL             string                      `json:"qr_url"`
	Status                string                      `json:"status"`
	OwnerStaffID          string                      `json:"owner_staff_id"`
	CustomerChannel       string                      `json:"customer_channel"`
	LinkURL               string                      `json:"link_url"`
	FinalURL              string                      `json:"final_url"`
	WelcomeMessage        string                      `json:"welcome_message"`
	WelcomeImageIDs       []int64                     `json:"welcome_image_library_ids"`
	WelcomeMiniProgramIDs []int64                     `json:"welcome_miniprogram_library_ids"`
	WelcomeAttachmentIDs  []int64                     `json:"welcome_attachment_library_ids"`
	WelcomeGroupInviteIDs []int64                     `json:"welcome_group_invite_library_ids"`
	AutoAcceptFriend      bool                        `json:"auto_accept_friend"`
	EntryTagID            string                      `json:"entry_tag_id"`
	EntryTagName          string                      `json:"entry_tag_name"`
	EntryTagGroupName     string                      `json:"entry_tag_group_name"`
	AssignmentMode        string                      `json:"assignment_mode"`
	AssignmentStrategy    string                      `json:"assignment_strategy"`
	OverflowPolicy        string                      `json:"overflow_policy"`
	AssignmentConfig      catalogAssignmentConfigJSON `json:"assignment_config_json"`
}

type catalogAssignmentConfigJSON struct {
	Assignees []struct {
		StaffID     int64 `json:"staff_id"`
		Priority    int   `json:"priority"`
		Ratio       int   `json:"ratio_percent"`
		MaxScans24h int   `json:"max_scans_24h"`
	} `json:"assignees"`
}

type catalogWrite struct {
	Code   string
	Status channeldomain.Status
	Config channeldomain.Config
}

func decodeCatalogWrite(response http.ResponseWriter, request *http.Request, defaultStaffID int64) (catalogWrite, error) {
	if request.Header.Get("Content-Type") != "application/json" {
		return catalogWrite{}, errors.New("content type")
	}
	request.Body = http.MaxBytesReader(response, request.Body, catalogHTTPMaxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body catalogWriteJSON
	if err := decoder.Decode(&body); err != nil {
		return catalogWrite{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return catalogWrite{}, errors.New("trailing JSON")
	}
	tagID := int64(0)
	if body.EntryTagID != "" {
		value, err := strconv.ParseInt(body.EntryTagID, 10, 64)
		if err != nil || value < 1 {
			return catalogWrite{}, errors.New("entry tag")
		}
		tagID = value
	}
	assignment := channeldomain.Assignment{Mode: channeldomain.AssignmentMode(body.AssignmentMode), Strategy: channeldomain.AssignmentStrategy(body.AssignmentStrategy), OverflowPolicy: body.OverflowPolicy}
	if assignment.Mode == "" {
		assignment.Mode = channeldomain.AssignmentSingle
	}
	if assignment.Strategy == "" {
		assignment.Strategy = channeldomain.StrategyRatio
	}
	for index, item := range body.AssignmentConfig.Assignees {
		priority := item.Priority
		if priority == 0 {
			priority = index + 1
		}
		assignment.Assignees = append(assignment.Assignees, channeldomain.Assignee{StaffID: item.StaffID, Priority: priority, Ratio: item.Ratio, MaxScans24h: item.MaxScans24h})
	}
	if len(assignment.Assignees) == 0 {
		staffID := defaultStaffID
		if body.OwnerStaffID != "" {
			parsed, err := strconv.ParseInt(body.OwnerStaffID, 10, 64)
			if err != nil || parsed < 1 {
				return catalogWrite{}, errors.New("owner staff")
			}
			staffID = parsed
		}
		assignee := channeldomain.Assignee{StaffID: staffID, Priority: 1}
		if assignment.Strategy == channeldomain.StrategyCapSwitch {
			assignee.MaxScans24h = 1
		} else {
			assignee.Ratio = 100
		}
		assignment.Assignees = []channeldomain.Assignee{assignee}
	}
	return catalogWrite{Code: body.ChannelCode, Status: channeldomain.Status(body.Status), Config: channeldomain.Config{
		Type: channeldomain.ChannelType(body.ChannelType), Carrier: channeldomain.CarrierType(body.CarrierType), Name: body.ChannelName,
		SceneValue: body.SceneValue, QRCodeURL: body.QRCodeURL, CustomerChannel: body.CustomerChannel, LinkURL: body.LinkURL, FinalURL: body.FinalURL,
		WelcomeMessage: body.WelcomeMessage, Media: channeldomain.MediaReferences{Images: body.WelcomeImageIDs, MiniPrograms: body.WelcomeMiniProgramIDs, Attachments: body.WelcomeAttachmentIDs, GroupInvites: body.WelcomeGroupInviteIDs},
		AutoAcceptFriend: body.AutoAcceptFriend, EntryTagID: tagID, EntryTagName: body.EntryTagName, EntryTagGroupName: body.EntryTagGroupName, Assignment: assignment,
	}}, nil
}

type catalogListJSON struct {
	ID                    int64                            `json:"id"`
	ChannelType           channeldomain.ChannelType        `json:"channel_type"`
	CarrierType           channeldomain.CarrierType        `json:"carrier_type"`
	ChannelName           string                           `json:"channel_name"`
	ChannelCode           string                           `json:"channel_code"`
	SceneValue            string                           `json:"scene_value"`
	QRCodeURL             string                           `json:"qr_url"`
	Status                channeldomain.Status             `json:"status"`
	OwnerStaffID          string                           `json:"owner_staff_id"`
	CustomerChannel       string                           `json:"customer_channel"`
	LinkURL               string                           `json:"link_url"`
	FinalURL              string                           `json:"final_url"`
	WelcomeMessage        string                           `json:"welcome_message"`
	WelcomeImageIDs       []int64                          `json:"welcome_image_library_ids"`
	WelcomeMiniProgramIDs []int64                          `json:"welcome_miniprogram_library_ids"`
	WelcomeAttachmentIDs  []int64                          `json:"welcome_attachment_library_ids"`
	WelcomeGroupInviteIDs []int64                          `json:"welcome_group_invite_library_ids"`
	AutoAcceptFriend      bool                             `json:"auto_accept_friend"`
	EntryTagID            string                           `json:"entry_tag_id"`
	EntryTagName          string                           `json:"entry_tag_name"`
	EntryTagGroupName     string                           `json:"entry_tag_group_name"`
	AssignmentMode        channeldomain.AssignmentMode     `json:"assignment_mode"`
	AssignmentStrategy    channeldomain.AssignmentStrategy `json:"assignment_strategy"`
	OverflowPolicy        string                           `json:"overflow_policy"`
	AssignmentConfig      map[string]any                   `json:"assignment_config_json"`
	AssigneeCount         int                              `json:"assignee_count"`
	ChannelContactCount   int                              `json:"channel_contact_count"`
	ChannelEnterCount     int64                            `json:"channel_enter_count"`
	LatestEnteredAt       string                           `json:"latest_channel_entered_at"`
	QRCodeAssetID         int64                            `json:"qrcode_asset_id"`
	QRCodeStatus          string                           `json:"qrcode_status"`
	QRCodeOrigin          string                           `json:"qrcode_origin"`
	QRDownloadURL         string                           `json:"qr_download_url"`
	CreatedAt             string                           `json:"created_at"`
	UpdatedAt             string                           `json:"updated_at"`
}

func projectCatalogList(channel channeldomain.Channel, summary CatalogSummary) catalogListJSON {
	assignment := make([]map[string]any, len(channel.Config.Assignment.Assignees))
	for index, item := range channel.Config.Assignment.Assignees {
		assignment[index] = map[string]any{"staff_id": item.StaffID, "priority": item.Priority, "ratio_percent": item.Ratio, "max_scans_24h": item.MaxScans24h}
	}
	return catalogListJSON{
		ID: channel.ID, ChannelType: channel.Config.Type, CarrierType: channel.Config.Carrier, ChannelName: channel.Config.Name, ChannelCode: channel.Code,
		SceneValue: channel.Config.SceneValue, QRCodeURL: channel.Config.QRCodeURL, Status: channel.Status, CustomerChannel: channel.Config.CustomerChannel, LinkURL: channel.Config.LinkURL, FinalURL: channel.Config.FinalURL,
		WelcomeMessage: channel.Config.WelcomeMessage, WelcomeImageIDs: channel.Config.Media.Images, WelcomeMiniProgramIDs: channel.Config.Media.MiniPrograms, WelcomeAttachmentIDs: channel.Config.Media.Attachments, WelcomeGroupInviteIDs: channel.Config.Media.GroupInvites,
		AutoAcceptFriend: channel.Config.AutoAcceptFriend, EntryTagID: optionalCatalogID(channel.Config.EntryTagID), EntryTagName: channel.Config.EntryTagName, EntryTagGroupName: channel.Config.EntryTagGroupName,
		AssignmentMode: channel.Config.Assignment.Mode, AssignmentStrategy: channel.Config.Assignment.Strategy, OverflowPolicy: channel.Config.Assignment.OverflowPolicy, AssignmentConfig: map[string]any{"assignees": assignment},
		AssigneeCount: len(assignment), ChannelContactCount: int(summary.UniqueUsers), ChannelEnterCount: summary.EnterCount, LatestEnteredAt: optionalCatalogTime(summary.LatestEnteredAt), QRCodeAssetID: summary.QRCodeAssetID, QRCodeStatus: summary.QRCodeStatus, QRCodeOrigin: summary.QRCodeOrigin, QRDownloadURL: catalogVisibleQRDownloadURL(channel, summary),
		CreatedAt: channel.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: channel.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func projectCatalogDetail(channel channeldomain.Channel, summary CatalogSummary) map[string]any {
	assignment := make([]map[string]any, len(channel.Config.Assignment.Assignees))
	for index, item := range channel.Config.Assignment.Assignees {
		assignment[index] = map[string]any{"staff_id": item.StaffID, "priority": item.Priority, "ratio_percent": item.Ratio, "max_scans_24h": item.MaxScans24h}
	}
	return map[string]any{
		"id": channel.ID, "version": channel.Version, "config_version": channel.ConfigVersion,
		"channel_type": channel.Config.Type, "carrier_type": channel.Config.Carrier, "channel_name": channel.Config.Name, "channel_code": channel.Code,
		"scene_value": channel.Config.SceneValue, "qr_url": channel.Config.QRCodeURL, "status": channel.Status, "owner_staff_id": "",
		"customer_channel": channel.Config.CustomerChannel, "link_url": channel.Config.LinkURL, "final_url": channel.Config.FinalURL,
		"welcome_message": channel.Config.WelcomeMessage, "welcome_image_library_ids": channel.Config.Media.Images, "welcome_miniprogram_library_ids": channel.Config.Media.MiniPrograms,
		"welcome_attachment_library_ids": channel.Config.Media.Attachments, "welcome_group_invite_library_ids": channel.Config.Media.GroupInvites,
		"auto_accept_friend": channel.Config.AutoAcceptFriend, "entry_tag_id": optionalCatalogID(channel.Config.EntryTagID), "entry_tag_name": channel.Config.EntryTagName, "entry_tag_group_name": channel.Config.EntryTagGroupName,
		"assignment_mode": channel.Config.Assignment.Mode, "assignment_strategy": channel.Config.Assignment.Strategy, "overflow_policy": channel.Config.Assignment.OverflowPolicy,
		"assignment_config_json": map[string]any{"assignees": assignment}, "assignees": []any{}, "assignment_stats_24h": []any{}, "assignee_count": len(assignment),
		"channel_contact_count": summary.UniqueUsers, "channel_enter_count": summary.EnterCount, "latest_channel_entered_at": optionalCatalogTime(summary.LatestEnteredAt),
		"qrcode_asset_id": summary.QRCodeAssetID, "qrcode_status": summary.QRCodeStatus, "qrcode_origin": summary.QRCodeOrigin, "qr_download_url": catalogVisibleQRDownloadURL(channel, summary), "share_url": "", "copy_text": "",
		"created_at": channel.CreatedAt.Format(time.RFC3339Nano), "updated_at": channel.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// catalogVisibleQRDownloadURL only exposes a QR download from a channel that
// can actually accept entrants. Inactive and archived channel definitions
// retain their asset projection for history and explicit recovery, but a
// download link would falsely present them as scan-ready even though the
// callback contract suppresses welcome and entry-tag effects.
func catalogVisibleQRDownloadURL(channel channeldomain.Channel, summary CatalogSummary) string {
	if channel.Status != channeldomain.StatusActive {
		return ""
	}
	return summary.QRDownloadURL
}

func optionalCatalogID(id int64) string {
	if id < 1 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
func optionalCatalogTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func catalogMutationResponse(channel channeldomain.Channel, reason string) map[string]any {
	return map[string]any{"ok": true, "channel": projectCatalogDetail(channel, CatalogSummary{}), "reason": reason, "source": "ai_crm_next", "fallback_used": false, "provider_execution_eligible": false, "real_external_call_executed": false}
}

func (handler *CatalogHTTPHandler) readSummaries(ctx context.Context, ids []int64) (map[int64]CatalogSummary, error) {
	if handler.summaries == nil {
		return map[int64]CatalogSummary{}, nil
	}
	return handler.summaries.Summaries(ctx, ids)
}

func (handler *CatalogHTTPHandler) readPrincipal(request *http.Request) (accessdomain.Principal, error) {
	principal, err := handler.security.Security.Authenticate(request.Context(), request)
	if err != nil || !channelCatalogReadRole(principal) {
		if err != nil {
			return accessdomain.Principal{}, err
		}
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func (handler *CatalogHTTPHandler) writePrincipal(request *http.Request) (accessdomain.Principal, error) {
	principal, err := handler.security.Security.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		return accessdomain.Principal{}, err
	}
	if principal.InternalID < 1 || !catalogWriteRole(principal) {
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func catalogWriteRole(principal accessdomain.Principal) bool {
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func (handler *CatalogHTTPHandler) securityError(response http.ResponseWriter, err error) {
	if errors.Is(err, accessdomain.ErrPermissionDenied) {
		writeCatalogError(response, http.StatusForbidden, "FORBIDDEN")
		return
	}
	writeCatalogError(response, http.StatusUnauthorized, "UNAUTHORIZED")
}
func (handler *CatalogHTTPHandler) applicationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, channeldomain.ErrInvalidWelcomeTemplate):
		writeCatalogMessageError(response, http.StatusBadRequest, "WELCOME_TEMPLATE_INVALID", "欢迎语目前仅支持 {{客户名}}；请删除或改正其他变量后保存。")
	case errors.Is(err, ErrCatalogNotFound):
		writeCatalogError(response, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, ErrCatalogConflict):
		writeCatalogError(response, http.StatusConflict, "VERSION_CONFLICT")
	case errors.Is(err, ErrInvalidCatalogCommand):
		writeCatalogError(response, http.StatusBadRequest, "MALFORMED_REQUEST")
	default:
		writeCatalogError(response, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
	}
}
func writeCatalogError(response http.ResponseWriter, status int, code string) {
	writeChannelJSON(response, status, map[string]any{"ok": false, "code": code})
}
func writeCatalogMessageError(response http.ResponseWriter, status int, code, message string) {
	writeChannelJSON(response, status, map[string]any{"ok": false, "code": code, "message": message})
}

func singleCatalogIdempotencyKey(request *http.Request) (string, error) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 || !validOperationKey(values[0]) {
		return "", errors.New("idempotency key")
	}
	return values[0], nil
}
func catalogIfMatch(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return 0, errors.New("If-Match")
	}
	return value, nil
}
func catalogDecodeStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

type catalogCursor struct {
	AfterID         int64  `json:"a"`
	Limit           int    `json:"l"`
	IncludeArchived bool   `json:"i"`
	Status          string `json:"s"`
	Keyword         string `json:"q"`
	ExpiresAt       int64  `json:"e"`
}

func (handler *CatalogHTTPHandler) signCursor(afterID int64, filter channelport.CatalogFilter) string {
	payload, _ := json.Marshal(catalogCursor{afterID, filter.Limit, filter.IncludeArchived, string(filter.Status), filter.Keyword, handler.security.Now().Add(15 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, handler.security.CursorSigningKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (handler *CatalogHTTPHandler) parseCursor(raw string, filter channelport.CatalogFilter) int64 {
	pieces := strings.Split(raw, ".")
	if len(pieces) != 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(pieces[0])
	if err != nil {
		return 0
	}
	signature, err := base64.RawURLEncoding.DecodeString(pieces[1])
	if err != nil {
		return 0
	}
	mac := hmac.New(sha256.New, handler.security.CursorSigningKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return 0
	}
	var cursor catalogCursor
	if json.Unmarshal(payload, &cursor) != nil || cursor.ExpiresAt < handler.security.Now().Unix() || cursor.Limit != filter.Limit || cursor.IncludeArchived != filter.IncludeArchived || cursor.Status != string(filter.Status) || cursor.Keyword != filter.Keyword {
		return 0
	}
	return cursor.AfterID
}

var _ http.Handler = (*CatalogHTTPHandler)(nil)
