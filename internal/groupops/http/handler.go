// Package http exposes the donor Group Ops API through v3-owned
// authentication/CSRF and the local Group Ops application ports. It contains
// no Customer, OneID, Audience, or Provider operation.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const (
	PlansPath            = "/api/admin/automation-conversion/group-ops/plans"
	HistoryPath          = "/api/admin/automation-conversion/group-ops/history"
	DirectoryPath        = "/api/admin/automation-conversion/group-ops/groups"
	GroupPickerPath      = "/api/admin/automation-conversion/group-ops/group-picker"
	OperationMembersPath = "/api/admin/common/operation-members"
	BroadcastPath        = "/api/automation/group-ops/broadcast"
	ContentPackagesPath  = "/api/admin/automation-conversion/group-ops/content-packages"
)

type RequestSecurity interface {
	Authenticate(context.Context, *stdhttp.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *stdhttp.Request) (accessdomain.Principal, error)
}

type Application interface {
	List(context.Context, int32, int32) (groupopsport.PlanPage, error)
	Detail(context.Context, int64) (groupopsport.Detail, error)
	Create(context.Context, groupopsport.CreatePlanCommand) (groupopsport.Detail, error)
	Update(context.Context, groupopsport.UpdatePlanCommand) (groupopsport.Detail, error)
	Activate(context.Context, groupopsport.TransitionCommand) (groupopsport.Detail, error)
	Pause(context.Context, groupopsport.TransitionCommand) (groupopsport.Detail, error)
	Archive(context.Context, groupopsport.TransitionCommand) (groupopsport.Detail, error)
	ListMembers(context.Context, int64, int32, int32) (groupopsport.MemberPage, error)
	AddMember(context.Context, groupopsport.MemberCommand) (groupopsport.Detail, error)
	RemoveMember(context.Context, groupopsport.MemberCommand) (groupopsport.Detail, error)
	ListGroupAssets(context.Context, int64, int32, int32) (groupopsport.GroupAssetPage, error)
	AddGroupAsset(context.Context, groupopsport.GroupAssetCommand) (groupopsport.Detail, error)
	RemoveGroupAsset(context.Context, groupopsport.GroupAssetCommand) (groupopsport.Detail, error)
	ListNodes(context.Context, int64, int32, int32) (groupopsport.NodePage, error)
	AddNode(context.Context, groupopsport.NodeCreateCommand) (groupopsport.Detail, error)
	UpdateNode(context.Context, groupopsport.NodeUpdateCommand) (groupopsport.Detail, error)
	RemoveNode(context.Context, groupopsport.NodeDeleteCommand) (groupopsport.Detail, error)
	GetWebhookDescriptor(context.Context, int64) (groupopsport.WebhookDescriptor, error)
	PutWebhookDescriptor(context.Context, groupopsport.WebhookDescriptorCommand) (groupopsport.Detail, error)
	Preview(context.Context, int64) (groupopsport.ContentValidation, error)
}

// HistoryApplication is intentionally read-only. Its records are sealed
// v3-owned historical facts and cannot be activated, reconciled, sent or
// otherwise fed into the current Group Ops runtime.
type HistoryApplication interface {
	ListHistoricalPlans(context.Context, int32, int32) (groupopsport.HistoricalPlanPage, error)
	ListHistoricalDirectory(context.Context, int32, int32) (groupopsport.HistoricalDirectoryPage, error)
	ListHistoricalGroups(context.Context, int64, int32, int32) (groupopsport.HistoricalGroupPage, error)
	ListHistoricalNodes(context.Context, int64, int32, int32) (groupopsport.HistoricalNodePage, error)
}

type RuntimeApplication interface {
	PreviewRunDue(context.Context, int64) (groupopsport.RunDuePreview, error)
	RunDue(context.Context, groupopsport.RunDueCommand) (groupopsport.RunSummary, error)
	AcceptBroadcast(context.Context, int64, int64, string) (groupopsport.RunSummary, error)
	AcceptWebhook(context.Context, string, string, groupopsport.WebhookInboundCommand) (groupopsport.RunSummary, error)
	ListExecutions(context.Context, int64, int32, int32) (groupopsport.ExecutionPage, error)
	ProjectExecutionOutcome(context.Context, groupopsport.ExecutionOutcomeCommand) (groupopsport.Execution, error)
	ManualReconcile(context.Context, groupopsport.ManualReconcileCommand) (groupopsport.Execution, error)
	ReadProviderDelivery(context.Context, groupopsport.ProviderDeliveryReadCommand) (groupopsport.Execution, error)
	ListOperationMembers(context.Context, int32, string) (groupopsport.OperationMemberPage, error)
	RefreshOperationMembers(context.Context, groupopsport.OperationMemberRefreshCommand) (groupopsport.OperationMemberPage, error)
	ListGroups(context.Context, int64, string, int32, int32) (groupopsport.GroupDirectoryPage, error)
	RefreshGroups(context.Context, groupopsport.GroupRefreshCommand) (groupopsport.GroupDirectoryPage, error)
}

// ProtocolAuthenticator owns signature verification and replay storage. The
// HTTP package only passes bounded bytes and an opaque webhook key through; a
// nil authenticator keeps public inbound routes fail-closed.
type ProtocolAuthenticator interface {
	AuthenticateGroupOpsWebhook(context.Context, *stdhttp.Request, string, []byte) (string, error)
}

// Protocol errors are intentionally typed at this boundary. A transport
// adapter may reveal an idempotency conflict to an authenticated caller, but
// never exposes signature-verification detail to an unauthenticated caller.
var (
	ErrProtocolUnavailable    = errors.New("Group Ops protocol authentication unavailable")
	ErrProtocolReplayConflict = errors.New("Group Ops protocol replay payload conflict")
)

type Handler struct {
	application     Application
	runtime         RuntimeApplication
	history         HistoryApplication
	security        RequestSecurity
	protocols       ProtocolAuthenticator
	contentDelivery mediaport.ContentDeliveryService
}

// legacyExecutionPage is the presentation adapter for the frozen Group Ops
// detail page. The runtime's state remains provider_accepted after a verified
// delivery read; the donor DTO instead requires delivery_proven as its display
// state when that fact and its Provider receipt are both present. Keep the
// canonical runtime state alongside the adapted display state so this response
// never hides the owner projection.
type legacyExecutionPage struct {
	Items   []legacyExecution `json:"items"`
	Total   int64             `json:"total"`
	Limit   int32             `json:"limit"`
	Offset  int32             `json:"offset"`
	HasMore bool              `json:"has_more"`
	groupopsport.RuntimeSafety
}

type legacyExecution struct {
	groupopsport.Execution
	State        groupopsport.ExecutionState `json:"state"`
	RuntimeState groupopsport.ExecutionState `json:"runtime_state,omitempty"`
}

func legacyExecutionPageResponse(value groupopsport.ExecutionPage) legacyExecutionPage {
	items := make([]legacyExecution, len(value.Items))
	for index, execution := range value.Items {
		item := legacyExecution{Execution: execution, State: execution.State}
		if execution.State == groupopsport.ExecutionProviderAccepted && execution.ProviderAccepted && execution.DeliveryProven && execution.ProviderReceiptPresent {
			item.State = groupopsport.ExecutionDeliveryProven
			item.RuntimeState = execution.State
		}
		items[index] = item
	}
	return legacyExecutionPage{Items: items, Total: value.Total, Limit: value.Limit, Offset: value.Offset, HasMore: value.HasMore, RuntimeSafety: value.RuntimeSafety}
}

func NewHandler(application Application, security RequestSecurity) (*Handler, error) {
	if application == nil || security == nil {
		return nil, errors.New("Group Ops HTTP dependencies are required")
	}
	return &Handler{application: application, security: security}, nil
}

func NewHandlerWithRuntime(application Application, runtime RuntimeApplication, security RequestSecurity, protocols ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (*Handler, error) {
	if application == nil || runtime == nil || security == nil {
		return nil, errors.New("Group Ops runtime HTTP dependencies are required")
	}
	var delivery mediaport.ContentDeliveryService
	if len(contentDelivery) > 0 {
		delivery = contentDelivery[0]
	}
	return &Handler{application: application, runtime: runtime, security: security, protocols: protocols, contentDelivery: delivery}, nil
}

func NewHandlerWithRuntimeAndHistory(application Application, runtime RuntimeApplication, history HistoryApplication, security RequestSecurity, protocols ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (*Handler, error) {
	if application == nil || runtime == nil || history == nil || security == nil {
		return nil, errors.New("Group Ops history HTTP dependencies are required")
	}
	handler, err := NewHandlerWithRuntime(application, runtime, security, protocols, contentDelivery...)
	if err != nil {
		return nil, err
	}
	handler.history = history
	return handler, nil
}

func (h *Handler) ServeHTTP(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h == nil || h.application == nil || h.security == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.URL.Path == BroadcastPath {
		h.broadcast(w, r)
		return
	}
	if r.URL.Path == ContentPackagesPath || strings.HasPrefix(r.URL.Path, ContentPackagesPath+"/") {
		h.contentPackages(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/automation/group-ops/webhooks/") {
		h.webhook(w, r)
		return
	}
	if r.URL.Path == OperationMembersPath || r.URL.Path == OperationMembersPath+"/sync" {
		h.operationMembers(w, r)
		return
	}
	if r.URL.Path == HistoryPath+"/plans" || r.URL.Path == HistoryPath+"/directory" || strings.HasPrefix(r.URL.Path, HistoryPath+"/plans/") {
		h.historyRoutes(w, r)
		return
	}
	if r.URL.Path == DirectoryPath || r.URL.Path == DirectoryPath+"/sync" || r.URL.Path == GroupPickerPath || r.URL.Path == GroupPickerPath+"/sync" {
		h.directory(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, PlansPath) {
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	h.plans(w, r)
}

func (h *Handler) historyRoutes(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.history == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.Method != stdhttp.MethodGet {
		methodNotAllowed(w, stdhttp.MethodGet)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !h.read(w, r) {
		return
	}
	limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_page")
		return
	}
	switch r.URL.Path {
	case HistoryPath + "/plans":
		value, err := h.history.ListHistoricalPlans(r.Context(), limit, offset)
		h.respond(w, value, err)
		return
	case HistoryPath + "/directory":
		value, err := h.history.ListHistoricalDirectory(r.Context(), limit, offset)
		h.respond(w, value, err)
		return
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, HistoryPath+"/plans/"))
	if len(parts) != 2 || (parts[1] != "groups" && parts[1] != "nodes") {
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	planID, valid := positiveID(parts[0])
	if !valid {
		writeError(w, stdhttp.StatusNotFound, "plan_not_found")
		return
	}
	if parts[1] == "groups" {
		value, err := h.history.ListHistoricalGroups(r.Context(), planID, limit, offset)
		h.respond(w, value, err)
		return
	}
	value, err := h.history.ListHistoricalNodes(r.Context(), planID, limit, offset)
	h.respond(w, value, err)
}

// contentPackages is a transport adapter for the Media-owned content
// package port. The donor has no content-package editor page, so these routes
// are intentionally API-only: the existing Group Ops detail page continues
// to edit typed materialPlan/history facts and never grows a second frontend
// shell. Package writes remain owned by Media, including its versioned
// snapshots, idempotency receipts, audit and outbox rows.
func (h *Handler) contentPackages(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.contentDelivery == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "content_delivery_unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, ContentPackagesPath)
	if path == "" || path == "/" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body contentPackageRequest
		if !decodeJSON(r, &body) || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.contentDelivery.Create(r.Context(), body.command(actor.InternalID, key))
		h.respondContent(w, stdhttp.StatusCreated, value, err)
		return
	}
	if path == "/preview" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.readPrincipal(w, r)
		if !ok {
			return
		}
		var body contentPackageRequest
		if !decodeJSON(r, &body) || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.contentDelivery.Preview(r.Context(), body.command(actor.InternalID, "content-preview-request"))
		h.respondContent(w, stdhttp.StatusOK, value, err)
		return
	}
	if path == "/bind" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body contentBindingRequest
		if !decodeJSON(r, &body) || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.contentDelivery.Bind(r.Context(), body.command(actor.InternalID, key))
		h.respondContent(w, stdhttp.StatusOK, value, err)
		return
	}
	if path == "/bindings" || strings.HasPrefix(path, "/bindings/") {
		parts := splitPath(strings.TrimPrefix(path, "/"))
		if len(parts) != 3 || parts[0] != "bindings" {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		if r.Method != stdhttp.MethodGet {
			methodNotAllowed(w, stdhttp.MethodGet)
			return
		}
		if !h.read(w, r) {
			return
		}
		campaignCode, campaignErr := url.PathUnescape(parts[1])
		planID, planErr := url.PathUnescape(parts[2])
		if campaignErr != nil || planErr != nil || !validOpaque(campaignCode) || !validOpaque(planID) {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		value, err := h.contentDelivery.GetBinding(r.Context(), campaignCode, planID)
		h.respondContent(w, stdhttp.StatusOK, value, err)
		return
	}
	parts := splitPath(path)
	if len(parts) == 1 {
		packageID, valid := positiveID(parts[0])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		if r.Method != stdhttp.MethodPut && r.Method != stdhttp.MethodPatch {
			methodNotAllowed(w, stdhttp.MethodPut, stdhttp.MethodPatch)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, keyValid := requiredIdempotencyKey(r)
		if !keyValid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body contentPackageRequest
		if !decodeJSON(r, &body) || body.ExpectedVersion < 1 || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.contentDelivery.Update(r.Context(), body.updateCommand(packageID, actor.InternalID, key))
		h.respondContent(w, stdhttp.StatusOK, value, err)
		return
	}
	if len(parts) == 2 && parts[1] == "versions" {
		packageID, valid := positiveID(parts[0])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, keyValid := requiredIdempotencyKey(r)
		if !keyValid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body contentPackageRequest
		if !decodeJSON(r, &body) || body.ExpectedVersion < 1 || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.contentDelivery.Update(r.Context(), body.updateCommand(packageID, actor.InternalID, key))
		h.respondContent(w, stdhttp.StatusOK, value, err)
		return
	}
	writeError(w, stdhttp.StatusNotFound, "not_found")
}

type contentPackageRequest struct {
	Name            string                 `json:"name"`
	ContentText     string                 `json:"content_text"`
	Enabled         bool                   `json:"enabled"`
	Refs            []mediaport.ContentRef `json:"refs"`
	ExpectedVersion int64                  `json:"expected_version"`
}

func (request contentPackageRequest) command(actor int64, key string) mediaport.ContentPackageCommand {
	return mediaport.ContentPackageCommand{Name: request.Name, ContentText: request.ContentText, Enabled: request.Enabled, Refs: append([]mediaport.ContentRef(nil), request.Refs...), Actor: actor, IdempotencyKey: key}
}

func (request contentPackageRequest) valid() bool {
	if request.Name == "" || strings.TrimSpace(request.Name) != request.Name || len(request.Refs) > 100 || len(request.ContentText) > 10000 {
		return false
	}
	if strings.TrimSpace(request.ContentText) == "" && len(request.Refs) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(request.Refs))
	for _, ref := range request.Refs {
		if ref.ID < 1 || (ref.Kind != "image" && ref.Kind != "attachment" && ref.Kind != "miniprogram" && ref.Kind != "group_invite") {
			return false
		}
		key := ref.Kind + ":" + strconv.FormatInt(ref.ID, 10)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func (request contentPackageRequest) updateCommand(id, actor int64, key string) mediaport.ContentPackageUpdateCommand {
	return mediaport.ContentPackageUpdateCommand{ID: id, ExpectedVersion: request.ExpectedVersion, ContentPackageCommand: request.command(actor, key)}
}

type contentBindingRequest struct {
	CampaignCode    string `json:"campaign_code"`
	PlanID          string `json:"plan_id"`
	PackageID       int64  `json:"package_id"`
	GroupInviteID   int64  `json:"group_invite_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (request contentBindingRequest) valid() bool {
	return validOpaque(request.CampaignCode) && validOpaque(request.PlanID) && request.PackageID > 0 && request.GroupInviteID > 0
}

func (request contentBindingRequest) command(actor int64, key string) mediaport.DeliveryBindingCommand {
	return mediaport.DeliveryBindingCommand{CampaignCode: request.CampaignCode, PlanID: request.PlanID, PackageID: request.PackageID, GroupInviteID: request.GroupInviteID, ExpectedVersion: request.ExpectedVersion, Actor: actor, IdempotencyKey: key}
}

func (h *Handler) readPrincipal(w stdhttp.ResponseWriter, r *stdhttp.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, stdhttp.StatusUnauthorized, "authentication_required")
		return accessdomain.Principal{}, false
	}
	if !canRead(principal) {
		writeError(w, stdhttp.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func (h *Handler) respondContent(w stdhttp.ResponseWriter, status int, value any, err error) {
	if err != nil {
		// Media owns its typed errors; Group Ops deliberately does not import
		// Media app/store layers merely to classify them. A failed adapter call
		// is therefore a deterministic dependency failure at this boundary.
		writeError(w, stdhttp.StatusServiceUnavailable, "content_delivery_unavailable")
		return
	}
	writeJSON(w, status, value)
}

func (h *Handler) plans(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	path := strings.TrimPrefix(r.URL.Path, PlansPath)
	if path == "" || path == "/" {
		switch r.Method {
		case stdhttp.MethodGet:
			if !h.read(w, r) {
				return
			}
			limit, offset, ok := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
			if !ok {
				writeError(w, stdhttp.StatusBadRequest, "invalid_page")
				return
			}
			value, err := h.application.List(r.Context(), limit, offset)
			h.respond(w, value, err)
		case stdhttp.MethodPost:
			actor, ok := h.mutate(w, r)
			if !ok {
				return
			}
			var body struct {
				Name string `json:"name"`
			}
			if !decodeJSON(r, &body) {
				writeError(w, stdhttp.StatusBadRequest, "invalid_request")
				return
			}
			value, err := h.application.Create(r.Context(), groupopsport.CreatePlanCommand{Name: body.Name, Actor: actor.InternalID, IdempotencyKey: idempotencyKey(r)})
			h.respondStatus(w, stdhttp.StatusCreated, value, err)
		default:
			methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPost)
		}
		return
	}
	parts := splitPath(path)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	// The frozen donor generator also exposes reconciliation without a plan
	// segment. Keep that URL reachable while the plan-scoped route below adds a
	// stronger resource context for new callers.
	if len(parts) == 3 && parts[0] == "executions" && parts[2] == "reconcile" {
		executionID, valid := positiveID(parts[1])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		h.reconcileExecution(w, r, executionID)
		return
	}
	if len(parts) == 3 && parts[0] == "executions" && parts[2] == "delivery" {
		executionID, valid := positiveID(parts[1])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		h.readProviderDelivery(w, r, executionID)
		return
	}
	planID, ok := positiveID(parts[0])
	if !ok {
		writeError(w, stdhttp.StatusNotFound, "plan_not_found")
		return
	}
	if len(parts) == 1 {
		h.planRoot(w, r, planID)
		return
	}
	if h.runtime != nil && len(parts) == 3 && parts[1] == "run-due" {
		if len(parts) != 3 {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		switch parts[2] {
		case "preview":
			if r.Method != stdhttp.MethodPost || !h.read(w, r) {
				if r.Method != stdhttp.MethodPost {
					methodNotAllowed(w, stdhttp.MethodPost)
				}
				return
			}
			value, err := h.runtime.PreviewRunDue(r.Context(), planID)
			h.respond(w, value, err)
		default:
			writeError(w, stdhttp.StatusNotFound, "not_found")
		}
		return
	}
	if len(parts) == 2 && parts[1] == "run-due" && h.runtime != nil {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, allowed := h.mutate(w, r)
		if !allowed {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		value, err := h.runtime.RunDue(r.Context(), groupopsport.RunDueCommand{PlanID: planID, ActorID: actor.InternalID, IdempotencyKey: key})
		h.respondStatus(w, stdhttp.StatusAccepted, value, err)
		return
	}
	if parts[1] == "executions" && len(parts) == 2 && h.runtime != nil {
		if r.Method != stdhttp.MethodGet {
			methodNotAllowed(w, stdhttp.MethodGet)
			return
		}
		if !h.read(w, r) {
			return
		}
		limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		value, err := h.runtime.ListExecutions(r.Context(), planID, limit, offset)
		if err != nil {
			h.respond(w, value, err)
			return
		}
		h.respond(w, legacyExecutionPageResponse(value), nil)
		return
	}
	if parts[1] == "executions" && len(parts) == 4 && parts[3] == "reconcile" && h.runtime != nil {
		executionID, valid := positiveID(parts[2])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		h.reconcileExecution(w, r, executionID)
		return
	}
	if parts[1] == "executions" && len(parts) == 4 && parts[3] == "delivery" && h.runtime != nil {
		executionID, valid := positiveID(parts[2])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "not_found")
			return
		}
		h.readProviderDelivery(w, r, executionID)
		return
	}
	h.planSubresource(w, r, planID, parts[1:])
}

func (h *Handler) planRoot(w stdhttp.ResponseWriter, r *stdhttp.Request, planID int64) {
	if r.Method == stdhttp.MethodGet {
		if !h.read(w, r) {
			return
		}
		value, err := h.application.Detail(r.Context(), planID)
		h.respond(w, value, err)
		return
	}
	if r.Method != stdhttp.MethodPatch && r.Method != stdhttp.MethodPut && r.Method != stdhttp.MethodDelete {
		methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPatch, stdhttp.MethodPut, stdhttp.MethodDelete)
		return
	}
	actor, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key, valid := requiredIdempotencyKey(r)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	var revision int64
	if r.Method == stdhttp.MethodDelete {
		var body revisionRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.Archive(r.Context(), groupopsport.TransitionCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	var body updatePlanRequest
	if !decodeJSON(r, &body) || body.ExpectedRevision < 1 || body.Name == "" {
		writeError(w, stdhttp.StatusBadRequest, "invalid_request")
		return
	}
	revision = body.ExpectedRevision
	command := groupopsport.UpdatePlanCommand{PlanID: planID, ExpectedRevision: revision, Name: body.Name, PlanType: body.PlanType, Actor: actor.InternalID, IdempotencyKey: key}
	if body.OwnerStaffID != nil {
		command.OwnerStaffIDSet = true
		command.OwnerStaffID = *body.OwnerStaffID
	}
	value, err := h.application.Update(r.Context(), command)
	h.respond(w, value, err)
}

func (h *Handler) planSubresource(w stdhttp.ResponseWriter, r *stdhttp.Request, planID int64, parts []string) {
	name := parts[0]
	if len(parts) == 1 && h.runtime != nil {
		switch name {
		case "activate", "enable", "pause", "disable", "archive":
			if r.Method != stdhttp.MethodPost {
				methodNotAllowed(w, stdhttp.MethodPost)
				return
			}
			actor, ok := h.mutate(w, r)
			if !ok {
				return
			}
			key, valid := requiredIdempotencyKey(r)
			if !valid {
				writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
				return
			}
			var body revisionRequest
			if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
				writeError(w, stdhttp.StatusBadRequest, "invalid_request")
				return
			}
			command := groupopsport.TransitionCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, Actor: actor.InternalID, IdempotencyKey: key}
			var value groupopsport.Detail
			var err error
			switch name {
			case "activate", "enable":
				value, err = h.application.Activate(r.Context(), command)
			case "pause", "disable":
				value, err = h.application.Pause(r.Context(), command)
			case "archive":
				value, err = h.application.Archive(r.Context(), command)
			}
			h.respond(w, value, err)
			return
		}
	}
	if name == "content" && len(parts) == 2 && parts[1] == "preview" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		if !h.read(w, r) {
			return
		}
		value, err := h.application.Preview(r.Context(), planID)
		h.respond(w, value, err)
		return
	}
	if name == "webhook-descriptor" && len(parts) == 1 {
		if r.Method == stdhttp.MethodGet {
			if !h.read(w, r) {
				return
			}
			value, err := h.application.GetWebhookDescriptor(r.Context(), planID)
			h.respond(w, value, err)
			return
		}
		if r.Method != stdhttp.MethodPut {
			methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPut)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body webhookRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.PutWebhookDescriptor(r.Context(), groupopsport.WebhookDescriptorCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, Reference: body.Reference, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	if name == "webhook" && len(parts) == 1 {
		if r.Method != stdhttp.MethodGet || !h.read(w, r) {
			if r.Method != stdhttp.MethodGet {
				methodNotAllowed(w, stdhttp.MethodGet)
			}
			return
		}
		value, err := h.application.GetWebhookDescriptor(r.Context(), planID)
		h.respond(w, value, err)
		return
	}
	if name == "run-due" && len(parts) == 1 && h.runtime != nil {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		value, err := h.runtime.RunDue(r.Context(), groupopsport.RunDueCommand{PlanID: planID, ActorID: actor.InternalID, IdempotencyKey: key})
		h.respondStatus(w, stdhttp.StatusAccepted, value, err)
		return
	}
	if name == "members" {
		h.members(w, r, planID, parts[1:])
		return
	}
	if name == "group-assets" || name == "groups" {
		h.assets(w, r, planID, parts[1:])
		return
	}
	if name == "nodes" {
		h.nodes(w, r, planID, parts[1:])
		return
	}
	if name == "executions" && len(parts) == 1 && h.runtime != nil {
		if r.Method != stdhttp.MethodGet || !h.read(w, r) {
			if r.Method != stdhttp.MethodGet {
				methodNotAllowed(w, stdhttp.MethodGet)
			}
			return
		}
		limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		value, err := h.runtime.ListExecutions(r.Context(), planID, limit, offset)
		if err != nil {
			h.respond(w, value, err)
			return
		}
		h.respond(w, legacyExecutionPageResponse(value), nil)
		return
	}
	writeError(w, stdhttp.StatusNotFound, "not_found")
}

func (h *Handler) members(w stdhttp.ResponseWriter, r *stdhttp.Request, planID int64, extra []string) {
	if len(extra) == 0 && r.Method == stdhttp.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		value, err := h.application.ListMembers(r.Context(), planID, limit, offset)
		h.respond(w, value, err)
		return
	}
	if len(extra) == 0 && r.Method == stdhttp.MethodPost {
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body memberRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 || body.StaffID < 1 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.AddMember(r.Context(), groupopsport.MemberCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, StaffID: body.StaffID, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	if len(extra) == 1 {
		staffID, valid := positiveID(extra[0])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "invalid_staff_id")
			return
		}
		if r.Method != stdhttp.MethodDelete {
			methodNotAllowed(w, stdhttp.MethodDelete)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body revisionRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.RemoveMember(r.Context(), groupopsport.MemberCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, StaffID: staffID, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPost, stdhttp.MethodDelete)
}

func (h *Handler) assets(w stdhttp.ResponseWriter, r *stdhttp.Request, planID int64, extra []string) {
	if len(extra) == 0 && r.Method == stdhttp.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		value, err := h.application.ListGroupAssets(r.Context(), planID, limit, offset)
		h.respond(w, value, err)
		return
	}
	if len(extra) == 0 && r.Method == stdhttp.MethodPost {
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body assetRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 || body.AssetReference == "" {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.AddGroupAsset(r.Context(), groupopsport.GroupAssetCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, AssetRef: body.AssetReference, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	if len(extra) == 1 && r.Method == stdhttp.MethodDelete {
		asset, unescapeErr := url.PathUnescape(extra[0])
		if unescapeErr != nil || asset == "" {
			writeError(w, stdhttp.StatusNotFound, "invalid_asset_reference")
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, keyValid := requiredIdempotencyKey(r)
		if !keyValid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body revisionRequest
		if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.RemoveGroupAsset(r.Context(), groupopsport.GroupAssetCommand{PlanID: planID, ExpectedRevision: body.ExpectedRevision, AssetRef: asset, Actor: actor.InternalID, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPost, stdhttp.MethodDelete)
}

func (h *Handler) nodes(w stdhttp.ResponseWriter, r *stdhttp.Request, planID int64, extra []string) {
	if len(extra) == 0 && r.Method == stdhttp.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit, offset, valid := pageQuery(r, groupopsapp.DefaultLimit, groupopsapp.MaximumLimit)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		value, err := h.application.ListNodes(r.Context(), planID, limit, offset)
		h.respond(w, value, err)
		return
	}
	if len(extra) == 0 && r.Method == stdhttp.MethodPost {
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body nodeRequest
		if !decodeJSON(r, &body) || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.AddNode(r.Context(), body.create(planID, actor.InternalID, key))
		h.respond(w, value, err)
		return
	}
	if len(extra) == 1 {
		nodeID, valid := positiveID(extra[0])
		if !valid {
			writeError(w, stdhttp.StatusNotFound, "invalid_node_id")
			return
		}
		if r.Method == stdhttp.MethodDelete {
			actor, ok := h.mutate(w, r)
			if !ok {
				return
			}
			key, valid := requiredIdempotencyKey(r)
			if !valid {
				writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
				return
			}
			var body revisionRequest
			if !decodeJSON(r, &body) || body.ExpectedRevision < 1 {
				writeError(w, stdhttp.StatusBadRequest, "invalid_request")
				return
			}
			value, err := h.application.RemoveNode(r.Context(), groupopsport.NodeDeleteCommand{PlanID: planID, NodeID: nodeID, ExpectedRevision: body.ExpectedRevision, Actor: actor.InternalID, IdempotencyKey: key})
			h.respond(w, value, err)
			return
		}
		if r.Method != stdhttp.MethodPatch && r.Method != stdhttp.MethodPut {
			methodNotAllowed(w, stdhttp.MethodPatch, stdhttp.MethodPut, stdhttp.MethodDelete)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body nodeRequest
		if !decodeJSON(r, &body) || !body.valid() {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.application.UpdateNode(r.Context(), body.update(planID, nodeID, actor.InternalID, key))
		h.respond(w, value, err)
		return
	}
	methodNotAllowed(w, stdhttp.MethodGet, stdhttp.MethodPost, stdhttp.MethodPatch, stdhttp.MethodPut, stdhttp.MethodDelete)
}

func (h *Handler) directory(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.runtime == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	base := DirectoryPath
	if strings.HasPrefix(r.URL.Path, GroupPickerPath) {
		base = GroupPickerPath
	}
	suffix := strings.TrimPrefix(r.URL.Path, base)
	if suffix == "/sync" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body directorySyncRequest
		if !decodeJSON(r, &body) || body.OwnerStaffID < 1 || body.Limit < 1 || body.Limit > 200 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.runtime.RefreshGroups(r.Context(), groupopsport.GroupRefreshCommand{OwnerStaffID: body.OwnerStaffID, ActorID: actor.InternalID, Limit: body.Limit, IdempotencyKey: key})
		if err == nil && value.CatalogSync != nil {
			writeJSON(w, stdhttp.StatusAccepted, value)
			return
		}
		h.respond(w, value, err)
		return
	}
	if suffix != "" && suffix != "/" {
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	if r.Method != stdhttp.MethodGet {
		methodNotAllowed(w, stdhttp.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	owner := int64(0)
	if raw := r.URL.Query().Get("owner_userid"); raw != "" {
		value, valid := positiveID(raw)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_staff_id")
			return
		}
		owner = value
	}
	limit, offset, valid := pageQuery(r, 50, 200)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_page")
		return
	}
	query, valid := directoryQuery(r.URL.Query().Get("q"))
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_query")
		return
	}
	value, err := h.runtime.ListGroups(r.Context(), owner, query, limit, offset)
	h.respond(w, value, err)
}

func directoryQuery(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, utf8.ValidString(value) && utf8.RuneCountInString(value) <= 160
}

func (h *Handler) operationMembers(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.runtime == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.URL.Path == OperationMembersPath+"/sync" {
		if r.Method != stdhttp.MethodPost {
			methodNotAllowed(w, stdhttp.MethodPost)
			return
		}
		actor, ok := h.mutate(w, r)
		if !ok {
			return
		}
		key, valid := requiredIdempotencyKey(r)
		if !valid {
			writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
			return
		}
		var body struct {
			Scope    string `json:"scope"`
			PageSize int32  `json:"page_size"`
		}
		if !decodeJSON(r, &body) || body.Scope != "group_ops" || body.PageSize < 1 || body.PageSize > 100 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_request")
			return
		}
		value, err := h.runtime.RefreshOperationMembers(r.Context(), groupopsport.OperationMemberRefreshCommand{ActorID: actor.InternalID, PageSize: body.PageSize, IdempotencyKey: key})
		h.respond(w, value, err)
		return
	}
	if scope := r.URL.Query().Get("scope"); scope != "" && scope != "group_ops" {
		// This endpoint is a Group Ops local staff projection. It is not an
		// alias for the Audience operation-member surface.
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	if r.Method != stdhttp.MethodGet {
		methodNotAllowed(w, stdhttp.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	pageSize := int32(100)
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 1 || value > 100 {
			writeError(w, stdhttp.StatusBadRequest, "invalid_page")
			return
		}
		pageSize = int32(value)
	}
	value, err := h.runtime.ListOperationMembers(r.Context(), pageSize, r.URL.Query().Get("q"))
	h.respond(w, value, err)
}

func (h *Handler) reconcileExecution(w stdhttp.ResponseWriter, r *stdhttp.Request, executionID int64) {
	if h.runtime == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.Method != stdhttp.MethodPost {
		methodNotAllowed(w, stdhttp.MethodPost)
		return
	}
	actor, allowed := h.mutate(w, r)
	if !allowed {
		return
	}
	key, valid := requiredIdempotencyKey(r)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	var body reconcileRequest
	if !decodeJSON(r, &body) || body.Generation < 1 || body.Fence < 1 || body.LeaseExpiresAt == "" || body.EvidenceDigest == "" {
		writeError(w, stdhttp.StatusBadRequest, "invalid_request")
		return
	}
	lease, parseErr := parseTime(body.LeaseExpiresAt)
	if parseErr != nil {
		writeError(w, stdhttp.StatusBadRequest, "invalid_request")
		return
	}
	value, err := h.runtime.ManualReconcile(r.Context(), groupopsport.ManualReconcileCommand{ExecutionID: executionID, ActorID: actor.InternalID, IdempotencyKey: key, Generation: body.Generation, Fence: body.Fence, LeaseExpiresAt: lease, EvidenceDigest: body.EvidenceDigest, DeliveryProven: body.DeliveryProven})
	h.respond(w, value, err)
}

func (h *Handler) readProviderDelivery(w stdhttp.ResponseWriter, r *stdhttp.Request, executionID int64) {
	if h.runtime == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.Method != stdhttp.MethodPost {
		methodNotAllowed(w, stdhttp.MethodPost)
		return
	}
	actor, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key, valid := requiredIdempotencyKey(r)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	value, err := h.runtime.ReadProviderDelivery(r.Context(), groupopsport.ProviderDeliveryReadCommand{ExecutionID: executionID, ActorID: actor.InternalID, IdempotencyKey: key})
	h.respond(w, value, err)
}

func (h *Handler) broadcast(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.runtime == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "group_ops_unavailable")
		return
	}
	if r.Method != stdhttp.MethodPost {
		methodNotAllowed(w, stdhttp.MethodPost)
		return
	}
	actor, ok := h.mutate(w, r)
	if !ok {
		return
	}
	key, valid := requiredIdempotencyKey(r)
	if !valid {
		writeError(w, stdhttp.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	var body struct {
		PlanID int64 `json:"plan_id"`
	}
	if !decodeJSON(r, &body) || body.PlanID < 1 {
		writeError(w, stdhttp.StatusBadRequest, "invalid_plan_id")
		return
	}
	value, err := h.runtime.AcceptBroadcast(r.Context(), body.PlanID, actor.InternalID, key)
	h.respondStatus(w, stdhttp.StatusAccepted, value, err)
}

func (h *Handler) webhook(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if h.runtime == nil || h.protocols == nil {
		writeError(w, stdhttp.StatusServiceUnavailable, "protocol_auth_unavailable")
		return
	}
	if r.Method != stdhttp.MethodPost {
		methodNotAllowed(w, stdhttp.MethodPost)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/automation/group-ops/webhooks/")
	if !validOpaque(key) {
		writeError(w, stdhttp.StatusNotFound, "not_found")
		return
	}
	const maxWebhookBody = 64 << 10
	// Read one byte beyond the limit so an otherwise-valid JSON prefix cannot
	// hide an over-limit suffix that would be omitted from the HMAC input.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil || len(body) == 0 || len(body) > maxWebhookBody || !json.Valid(body) {
		writeError(w, stdhttp.StatusBadRequest, "invalid_request")
		return
	}
	// Decode and bind the signed body to this URL before the protocol adapter
	// claims its replay receipt. A valid request copied to another descriptor
	// must leave no replay receipt behind there, or the caller could not retry
	// the same event at the descriptor named in its signed body.
	var inbound groupopsport.WebhookInboundCommand
	if !decodeWebhookBody(body, &inbound) || inbound.WebhookReference != key || groupopsdomain.ValidateWebhookInbound(inbound) != nil {
		writeError(w, stdhttp.StatusBadRequest, "invalid_request")
		return
	}
	idempotency, err := h.protocols.AuthenticateGroupOpsWebhook(r.Context(), r, key, body)
	if errors.Is(err, ErrProtocolUnavailable) {
		writeError(w, stdhttp.StatusServiceUnavailable, "protocol_auth_unavailable")
		return
	}
	if errors.Is(err, ErrProtocolReplayConflict) {
		writeError(w, stdhttp.StatusConflict, "idempotency_conflict")
		return
	}
	if err != nil || !validIdempotency(idempotency) {
		writeError(w, stdhttp.StatusUnauthorized, "protocol_authentication_failed")
		return
	}
	value, err := h.runtime.AcceptWebhook(r.Context(), key, idempotency, inbound)
	h.respondStatus(w, stdhttp.StatusAccepted, value, err)
}

func (h *Handler) read(w stdhttp.ResponseWriter, r *stdhttp.Request) bool {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, stdhttp.StatusUnauthorized, "authentication_required")
		return false
	}
	if !canRead(principal) {
		writeError(w, stdhttp.StatusForbidden, "permission_denied")
		return false
	}
	return true
}

func (h *Handler) mutate(w stdhttp.ResponseWriter, r *stdhttp.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, stdhttp.StatusUnauthorized, "authentication_required")
		return accessdomain.Principal{}, false
	}
	if !canWrite(principal) {
		writeError(w, stdhttp.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, stdhttp.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func canRead(p accessdomain.Principal) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func canWrite(p accessdomain.Principal) bool {
	if !canRead(p) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

type revisionRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}
type updatePlanRequest struct {
	PlanType         string `json:"plan_type"`
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
	OwnerStaffID     *int64 `json:"owner_staff_id"`
}
type memberRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
	StaffID          int64 `json:"staff_id"`
}
type assetRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	AssetReference   string `json:"asset_reference"`
}
type webhookRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reference        string `json:"reference"`
}
type directorySyncRequest struct {
	OwnerStaffID int64 `json:"owner_staff_id"`
	Limit        int32 `json:"limit"`
}
type reconcileRequest struct {
	Generation     int64  `json:"generation"`
	Fence          int64  `json:"fence"`
	LeaseExpiresAt string `json:"lease_expires_at"`
	EvidenceDigest string `json:"evidence_digest"`
	DeliveryProven bool   `json:"delivery_proven"`
}
type nodeRequest struct {
	ExpectedRevision int64                     `json:"expected_revision"`
	Position         int32                     `json:"position"`
	Kind             groupopsport.NodeKind     `json:"kind"`
	DayIndex         int32                     `json:"day_index"`
	ScheduledTime    string                    `json:"scheduled_time"`
	TriggerTimeLabel string                    `json:"trigger_time_label"`
	ActionTitle      string                    `json:"action_title"`
	Status           string                    `json:"status"`
	MessageText      string                    `json:"message_text"`
	DelayMinutes     int32                     `json:"delay_minutes"`
	MaterialRef      string                    `json:"material_reference"`
	MaterialPlan     groupopsport.MaterialPlan `json:"material_plan"`
}

func (b nodeRequest) valid() bool {
	return b.ExpectedRevision > 0 && b.Position > 0 && (b.Kind == groupopsport.NodeMessage || b.Kind == groupopsport.NodeDelay)
}
func (b nodeRequest) create(planID, actor int64, key string) groupopsport.NodeCreateCommand {
	return groupopsport.NodeCreateCommand{PlanID: planID, ExpectedRevision: b.ExpectedRevision, Position: b.Position, Kind: b.Kind, DayIndex: b.DayIndex, ScheduledTime: b.ScheduledTime, TriggerTimeLabel: b.TriggerTimeLabel, ActionTitle: b.ActionTitle, Status: b.Status, MessageText: b.MessageText, DelayMinutes: b.DelayMinutes, MaterialRef: b.MaterialRef, MaterialPlan: b.MaterialPlan, Actor: actor, IdempotencyKey: key}
}
func (b nodeRequest) update(planID, nodeID, actor int64, key string) groupopsport.NodeUpdateCommand {
	return groupopsport.NodeUpdateCommand{PlanID: planID, NodeID: nodeID, ExpectedRevision: b.ExpectedRevision, Position: b.Position, Kind: b.Kind, DayIndex: b.DayIndex, ScheduledTime: b.ScheduledTime, TriggerTimeLabel: b.TriggerTimeLabel, ActionTitle: b.ActionTitle, Status: b.Status, MessageText: b.MessageText, DelayMinutes: b.DelayMinutes, MaterialRef: b.MaterialRef, MaterialPlan: b.MaterialPlan, Actor: actor, IdempotencyKey: key}
}

func (h *Handler) respond(w stdhttp.ResponseWriter, value any, err error) {
	h.respondStatus(w, stdhttp.StatusOK, value, err)
}
func (h *Handler) respondStatus(w stdhttp.ResponseWriter, status int, value any, err error) {
	if err != nil {
		var diagnostic *groupopsapp.GroupDirectoryReadError
		if errors.As(err, &diagnostic) {
			writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{"ok": false, "message": diagnostic.Message(), "error": map[string]string{"code": diagnostic.Error()}, "provider_execution_eligible": false, "real_external_call_executed": false})
			return
		}
		writeError(w, errorStatus(err), errorCode(err))
		return
	}
	writeJSON(w, status, value)
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, groupopsapp.ErrNotFound):
		return stdhttp.StatusNotFound
	case errors.Is(err, groupopsapp.ErrConflict), errors.Is(err, groupopsapp.ErrStateConflict):
		return stdhttp.StatusConflict
	case errors.Is(err, groupopsapp.ErrProviderDisabled):
		return stdhttp.StatusServiceUnavailable
	case errors.Is(err, groupopsapp.ErrMiniProgramCoverUnsupported):
		return stdhttp.StatusBadRequest
	case errors.Is(err, groupopsapp.ErrMiniProgramCoverResolverUnavailable):
		return stdhttp.StatusServiceUnavailable
	case errors.Is(err, groupopsapp.ErrUnavailable):
		return stdhttp.StatusServiceUnavailable
	case errors.Is(err, groupopsapp.ErrInvalid), errors.Is(err, groupopsapp.ErrRuntimeInvalid):
		return stdhttp.StatusBadRequest
	default:
		return stdhttp.StatusServiceUnavailable
	}
}
func errorCode(err error) string {
	switch {
	case errors.Is(err, groupopsapp.ErrNotFound):
		return "plan_not_found"
	case errors.Is(err, groupopsapp.ErrConflict), errors.Is(err, groupopsapp.ErrStateConflict):
		return "operations_conflict"
	case errors.Is(err, groupopsapp.ErrProviderDisabled):
		return "provider_disabled"
	case errors.Is(err, groupopsapp.ErrMiniProgramCoverUnsupported):
		return "miniprogram_cover_unsupported"
	case errors.Is(err, groupopsapp.ErrMiniProgramCoverResolverUnavailable):
		return "miniprogram_cover_unavailable"
	case errors.Is(err, groupopsapp.ErrInvalid), errors.Is(err, groupopsapp.ErrRuntimeInvalid):
		return "invalid_request"
	default:
		return "group_ops_unavailable"
	}
}

func writeError(w stdhttp.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"code": code}, "provider_execution_eligible": false, "real_external_call_executed": false})
}
func writeJSON(w stdhttp.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func methodNotAllowed(w stdhttp.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, stdhttp.StatusMethodNotAllowed, "method_not_allowed")
}
func decodeJSON(r *stdhttp.Request, value any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}

func decodeWebhookBody(body []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}
func pageQuery(r *stdhttp.Request, defaultLimit, max int32) (int32, int32, bool) {
	limit, offset := defaultLimit, int32(0)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 32)
		err = parseErr
		limit = int32(value)
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 32)
		if err == nil {
			err = parseErr
		}
		offset = int32(value)
	}
	return limit, offset, err == nil && limit >= 1 && limit <= max && offset >= 0 && offset <= groupopsapp.MaximumOffset
}
func splitPath(value string) []string {
	return strings.Split(strings.Trim(value, "/"), "/")
}
func positiveID(value string) (int64, bool) {
	if value == "" || len(value) > 19 || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
func requiredIdempotencyKey(r *stdhttp.Request) (string, bool) {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key")), validIdempotency(strings.TrimSpace(r.Header.Get("Idempotency-Key")))
}
func validIdempotency(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
}
func idempotencyKey(r *stdhttp.Request) string {
	value, _ := requiredIdempotencyKey(r)
	return value
}
func validOpaque(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}
func parseTime(value string) (t time.Time, err error) {
	return time.Parse(time.RFC3339Nano, value)
}

var _ = idempotencyKey
