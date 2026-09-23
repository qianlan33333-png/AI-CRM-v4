package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type ownerHandoffPreviewBody struct {
	Mode               string   `json:"mode"`
	SourceStaffID      int64    `json:"source_staff_id"`
	TargetStaffID      int64    `json:"target_staff_id"`
	CustomerIDs        []int64  `json:"customer_ids"`
	WelcomeMessage     string   `json:"welcome_message"`
	ConfirmationPhrase string   `json:"confirmation_phrase"`
	IdempotencyKey     string   `json:"idempotency_key"`
	Scope              string   `json:"scope"`
	ExternalUserIDs    []string `json:"external_userids"`
}
type ownerHandoffConfirmBody struct {
	PreviewID          string `json:"preview_id"`
	PreviewHash        string `json:"preview_hash"`
	ConfirmationPhrase string `json:"confirmation_phrase"`
	IdempotencyKey     string `json:"idempotency_key"`
}
type ownerHandoffTransferResultBody struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type ownerHandoffAllPreviewer interface {
	PreviewAllOwnerHandoff(context.Context, customerport.OwnerHandoffPreviewCommand) (customerport.OwnerHandoffPreview, error)
}

func (handler *Handler) ownerHandoffPrincipal(response nethttp.ResponseWriter, request *nethttp.Request, csrf bool) (accessdomain.Principal, bool) {
	var principal accessdomain.Principal
	var err error
	if csrf {
		principal, err = handler.csrf.AuthorizeCSRF(request.Context(), request)
	} else {
		principal, err = handler.auth.Authenticate(request.Context(), request)
	}
	if err != nil {
		handler.writeError(response, err)
		return accessdomain.Principal{}, false
	}
	if !ownerHandoffWriteRole(principal) {
		handler.writeError(response, accessdomain.ErrPermissionDenied)
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func ownerHandoffWriteRole(principal accessdomain.Principal) bool {
	if principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func decodeOwnerHandoff(request *nethttp.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return customerapp.ErrInvalidQuery
	}
	if decoder.More() {
		return customerapp.ErrInvalidQuery
	}
	return nil
}
func (handler *Handler) ownerHandoffPreview(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffPreviewBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	ids := make([]customerdomain.CustomerID, len(body.CustomerIDs))
	for i, id := range body.CustomerIDs {
		ids[i] = customerdomain.CustomerID(id)
	}
	if len(body.ExternalUserIDs) > 0 {
		if len(body.CustomerIDs) != 0 || handler.ownerHandoffIdentity == nil || len(body.ExternalUserIDs) > 20000 {
			handler.writeError(response, customerapp.ErrInvalidQuery)
			return
		}
		ids = make([]customerdomain.CustomerID, 0, len(body.ExternalUserIDs))
		err := handler.uow.Within(request.Context(), func(tx context.Context) error {
			seen := make(map[customerdomain.CustomerID]struct{}, len(body.ExternalUserIDs))
			for _, externalID := range body.ExternalUserIDs {
				result, resolveErr := handler.ownerHandoffIdentity.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: handler.ownerHandoffCorpScope, Value: externalID, Assurance: identitydomain.AssuranceDeclared, Source: "owner_handoff_excel"})
				if resolveErr != nil {
					return resolveErr
				}
				if result.Status != identityport.ResolveFound || result.CustomerID < 1 {
					return customerapp.ErrOwnerHandoffForbidden
				}
				if _, exists := seen[result.CustomerID]; exists {
					continue
				}
				seen[result.CustomerID] = struct{}{}
				ids = append(ids, result.CustomerID)
			}
			return nil
		})
		if err != nil {
			handler.writeError(response, err)
			return
		}
	}
	command := customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: principal.InternalID, Mode: customerport.OwnerHandoffMode(body.Mode), SourceStaffID: body.SourceStaffID, TargetStaffID: body.TargetStaffID, CorpScope: handler.ownerHandoffCorpScope, CustomerIDs: ids, WelcomeMessage: body.WelcomeMessage, ConfirmationPhrase: body.ConfirmationPhrase, IdempotencyKey: body.IdempotencyKey}
	var preview customerport.OwnerHandoffPreview
	var err error
	if body.Scope == "all" {
		all, ok := handler.ownerHandoff.(ownerHandoffAllPreviewer)
		if !ok {
			handler.writeError(response, customerapp.ErrOwnerHandoffForbidden)
			return
		}
		preview, err = all.PreviewAllOwnerHandoff(request.Context(), command)
	} else {
		preview, err = handler.ownerHandoff.PreviewOwnerHandoff(request.Context(), command)
	}
	if err != nil {
		handler.writeError(response, err)
		return
	}
	if handler.ownerHandoffPresentation != nil {
		presentation, presentErr := handler.ownerHandoffPresentation.PresentOwnerHandoffPreview(request.Context(), handler.ownerHandoffCorpScope, preview.SourceStaffID, preview.Rows)
		if presentErr != nil {
			handler.writeError(response, presentErr)
			return
		}
		for index := range preview.Rows {
			item := presentation[preview.Rows[index].CustomerID]
			preview.Rows[index].ExternalUserID = item.ExternalUserID
			preview.Rows[index].CustomerDisplayName = item.CustomerDisplayName
			preview.Rows[index].CurrentOwnerUserID = item.CurrentOwnerUserID
		}
	}
	writePrivateJSON(response, nethttp.StatusOK, preview)
}
func (handler *Handler) ownerHandoffConfirm(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffConfirmBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	batch, err := handler.ownerHandoff.ConfirmOwnerHandoff(request.Context(), customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: principal.InternalID, PreviewID: body.PreviewID, PreviewHash: body.PreviewHash, ConfirmationPhrase: body.ConfirmationPhrase, IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusAccepted, batch)
}
func (handler *Handler) ownerHandoffPreviewRead(response nethttp.ResponseWriter, request *nethttp.Request) {
	_, ok := handler.ownerHandoffPrincipal(response, request, false)
	if !ok {
		return
	}
	id := strings.TrimSpace(request.PathValue("preview_id"))
	var preview customerport.OwnerHandoffPreview
	err := handler.uow.Within(request.Context(), func(tx context.Context) error {
		var e error
		preview, e = handler.ownerHandoffReader.OwnerHandoffPreview(tx, id)
		return e
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, preview)
}
func (handler *Handler) ownerHandoffBatchRead(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, ok := handler.ownerHandoffPrincipal(response, request, false); !ok {
		return
	}
	id := strings.TrimSpace(request.PathValue("batch_id"))
	var batch customerport.OwnerHandoffBatch
	err := handler.uow.Within(request.Context(), func(tx context.Context) error {
		var e error
		batch, e = handler.ownerHandoffReader.OwnerHandoffBatch(tx, id)
		return e
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, batch)
}

func (handler *Handler) ownerHandoffTransferResult(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffTransferResultBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	batchID := strings.TrimSpace(request.PathValue("batch_id"))
	batch, err := handler.ownerHandoffTransfers.RefreshOwnerHandoffTransferResult(request.Context(), customerport.OwnerHandoffTransferResultCommand{ActorAdminUserID: principal.InternalID, BatchID: batchID, IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, batch)
}

type ownerHandoffContextResponse struct {
	Staff    []customerport.OwnerHandoffStaff `json:"staff"`
	Operator string                           `json:"operator"`
}

// ownerHandoffContext exposes only the safe Access projection needed by the
// frozen owner-migration picker. The trusted Corp scope is never serialized:
// mutations receive it from this handler configuration, not browser input.
func (handler *Handler) ownerHandoffContext(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, false)
	if !ok {
		return
	}
	if handler.ownerHandoffStaff == nil || strings.TrimSpace(handler.ownerHandoffCorpScope) == "" {
		handler.writeError(response, customerapp.ErrOwnerHandoffForbidden)
		return
	}
	staff, err := handler.ownerHandoffStaff.ListOwnerHandoffStaff(request.Context())
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, ownerHandoffContextResponse{Staff: staff, Operator: "管理员 #" + strconv.FormatInt(principal.InternalID, 10)})
}

// OwnerHandoffOperationMembersHandler adapts the verified shared picker
// endpoint only for scope=owner_migration. Composition dispatches all other
// scopes to their owning Group Ops handler.
func (handler *Handler) OwnerHandoffOperationMembersHandler() nethttp.Handler {
	return nethttp.HandlerFunc(handler.ownerHandoffOperationMembers)
}

type ownerHandoffOperationMember struct {
	StaffID     int64  `json:"staff_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

func (handler *Handler) ownerHandoffOperationMembers(response nethttp.ResponseWriter, request *nethttp.Request) {
	if request.Method != nethttp.MethodGet || request.URL.Query().Get("scope") != "owner_migration" {
		handler.writeError(response, customerapp.ErrOwnerHandoffForbidden)
		return
	}
	if _, ok := handler.ownerHandoffPrincipal(response, request, false); !ok {
		return
	}
	if handler.ownerHandoffStaff == nil {
		handler.writeError(response, customerapp.ErrOwnerHandoffForbidden)
		return
	}
	includeInactive := request.URL.Query().Get("include_inactive") == "true"
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	pageSize := 100
	if raw := strings.TrimSpace(request.URL.Query().Get("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			handler.writeError(response, customerapp.ErrInvalidQuery)
			return
		}
		pageSize = parsed
	}
	staff, err := handler.ownerHandoffStaff.ListOwnerHandoffStaff(request.Context())
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items := make([]ownerHandoffOperationMember, 0, min(pageSize, len(staff)))
	for _, member := range staff {
		if strings.TrimSpace(member.UserID) == "" || (!includeInactive && !member.Active) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(member.UserID), query) && !strings.Contains(strings.ToLower(member.DisplayName), query) {
			continue
		}
		items = append(items, ownerHandoffOperationMember{StaffID: member.ID, UserID: member.UserID, DisplayName: member.DisplayName, Active: member.Active})
		if len(items) == pageSize {
			break
		}
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"items": items})
}
