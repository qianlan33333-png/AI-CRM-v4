package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const callbackAdminMaxBodyBytes int64 = 64 << 10

var (
	errCallbackAdminInvalidRequest = errors.New("invalid callback admin request")
	errCallbackAdminBodyTooLarge   = errors.New("callback admin request body is too large")
)

// CallbackAdminAuthenticator authenticates the complete request and returns
// only a currently active admin or staff principal.
type CallbackAdminAuthenticator interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}

// CallbackAdminCSRFAuthorizer additionally validates same-origin and CSRF
// protections for a currently active admin or staff session.
type CallbackAdminCSRFAuthorizer interface {
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type CallbackAdminReceiptStore interface {
	List(context.Context, CallbackReceiptPage) ([]CallbackReceipt, error)
	Get(context.Context, int64) (CallbackReceipt, error)
	BeginRetry(context.Context, BeginCallbackRetry) (CallbackReceipt, bool, error)
}

type CallbackAdminRetrier interface {
	Retry(context.Context, webhook.Retry) (webhook.Delivery, error)
}

type GroupMembershipRefresher interface {
	Refresh(context.Context, string) (wecomport.AudienceGroupMembership, error)
}

type CallbackAdminConfig struct {
	GroupMembership GroupMembershipRefresher
	UnitOfWork      platformport.UnitOfWork
	Authenticator   CallbackAdminAuthenticator
	CSRF            CallbackAdminCSRFAuthorizer
	Receipts        CallbackAdminReceiptStore
	Retrier         CallbackAdminRetrier
}

type CallbackAdminHandler struct {
	groups   GroupMembershipRefresher
	uow      platformport.UnitOfWork
	auth     CallbackAdminAuthenticator
	csrf     CallbackAdminCSRFAuthorizer
	receipts CallbackAdminReceiptStore
	retrier  CallbackAdminRetrier
}

func NewCallbackAdminHandler(config CallbackAdminConfig) (*CallbackAdminHandler, error) {
	if config.UnitOfWork == nil || config.Authenticator == nil || config.CSRF == nil || config.Receipts == nil || config.Retrier == nil {
		return nil, errors.New("wecom callback admin HTTP dependencies are required")
	}
	return &CallbackAdminHandler{
		uow: config.UnitOfWork, auth: config.Authenticator, csrf: config.CSRF,
		receipts: config.Receipts, retrier: config.Retrier, groups: config.GroupMembership,
	}, nil
}

func (handler *CallbackAdminHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/wecom/group-membership/refresh", handler.refreshGroupMembership)
	mux.HandleFunc("GET /api/admin/wecom/callback-receipts", handler.list)
	mux.HandleFunc("GET /api/admin/wecom/callback-receipts/{receipt_id}", handler.detail)
	mux.HandleFunc("POST /api/admin/wecom/callback-receipts/{receipt_id}/retry", handler.retry)
	return callbackAdminNoStore(mux)
}

func (handler *CallbackAdminHandler) list(response http.ResponseWriter, request *http.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	if err := callbackAdminRequireEmptyBody(response, request); err != nil {
		handler.writeError(response, err)
		return
	}
	page, err := callbackReceiptPage(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var receipts []CallbackReceipt
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var listErr error
		receipts, listErr = handler.receipts.List(txContext, page)
		return listErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items := make([]callbackReceiptJSON, len(receipts))
	for index := range receipts {
		items[index] = safeCallbackReceipt(receipts[index])
	}
	callbackAdminWriteJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (handler *CallbackAdminHandler) detail(response http.ResponseWriter, request *http.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	if err := callbackAdminRequireEmptyBody(response, request); err != nil {
		handler.writeError(response, err)
		return
	}
	receiptID, err := callbackAdminPositiveID(request.PathValue("receipt_id"))
	if err != nil || request.URL.RawQuery != "" {
		handler.writeError(response, errCallbackAdminInvalidRequest)
		return
	}
	var receipt CallbackReceipt
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var getErr error
		receipt, getErr = handler.receipts.Get(txContext, receiptID)
		return getErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	callbackAdminWriteJSON(response, http.StatusOK, safeCallbackReceipt(receipt))
}

func (handler *CallbackAdminHandler) retry(response http.ResponseWriter, request *http.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	receiptID, err := callbackAdminPositiveID(request.PathValue("receipt_id"))
	if err != nil || request.URL.RawQuery != "" {
		handler.writeError(response, errCallbackAdminInvalidRequest)
		return
	}
	key, err := callbackAdminIdempotencyKey(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var input struct {
		ExpectedStatus  webhook.Status `json:"expected_status"`
		ExpectedAttempt int            `json:"expected_attempt"`
		Reason          string         `json:"reason"`
	}
	if err = callbackAdminDecodeJSON(response, request, &input); err != nil {
		handler.writeError(response, err)
		return
	}
	if input.ExpectedAttempt < 1 ||
		(input.ExpectedStatus != webhook.StatusRetryable && input.ExpectedStatus != webhook.StatusFailed) ||
		!validCallbackAdminReason(input.Reason) {
		handler.writeError(response, errCallbackAdminInvalidRequest)
		return
	}
	operationDigest := sha256.Sum256([]byte(key))
	commandDigest := callbackRetryCommandDigest(receiptID, principal.InternalID, input.ExpectedAttempt, input.ExpectedStatus, input.Reason)
	command := BeginCallbackRetry{
		TargetReceiptID: receiptID, ExpectedAttempt: input.ExpectedAttempt,
		ExpectedInboxStatus: input.ExpectedStatus, ActorAdminUserID: principal.InternalID,
		Reason: input.Reason, OperationKeyDigest: operationDigest, CommandDigest: commandDigest,
	}
	var receipt CallbackReceipt
	var created bool
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var beginErr error
		receipt, created, beginErr = handler.receipts.BeginRetry(txContext, command)
		if beginErr != nil || !created {
			return beginErr
		}
		_, retryErr := handler.retrier.Retry(txContext, webhook.Retry{
			ID: receipt.InboxID, Provider: callbackProvider,
			ExpectedAttempt: input.ExpectedAttempt, ExpectedStatus: input.ExpectedStatus,
		})
		return retryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	callbackAdminWriteJSON(response, http.StatusOK, map[string]any{
		"receipt": safeCallbackReceipt(receipt), "replayed": !created,
	})
}

func (handler *CallbackAdminHandler) readPrincipal(request *http.Request) (accessdomain.Principal, error) {
	principal, err := handler.auth.Authenticate(request.Context(), request)
	if err != nil {
		return accessdomain.Principal{}, err
	}
	if err = principal.Validate(); err != nil {
		return accessdomain.Principal{}, accessdomain.ErrAuthentication
	}
	if principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff {
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func (handler *CallbackAdminHandler) writePrincipal(request *http.Request) (accessdomain.Principal, error) {
	principal, err := handler.csrf.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		return accessdomain.Principal{}, err
	}
	if err = principal.Validate(); err != nil {
		return accessdomain.Principal{}, accessdomain.ErrAuthentication
	}
	if !callbackAdminMayWrite(principal) {
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func callbackAdminMayWrite(principal accessdomain.Principal) bool {
	if principal.Kind != accessdomain.KindAdmin {
		return false
	}
	if principal.IsSuperAdmin() {
		return true
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin {
			return true
		}
	}
	return false
}

func (handler *CallbackAdminHandler) writeError(response http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, errCallbackAdminBodyTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "invalid_request"
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = http.StatusUnauthorized, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = http.StatusForbidden, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, ErrCallbackReceiptNotFound):
		status, code = http.StatusNotFound, "receipt_not_found"
	case errors.Is(err, ErrCallbackReceiptConflict), errors.Is(err, webhook.ErrConcurrentUpdate):
		status, code = http.StatusConflict, "state_conflict"
	case errors.Is(err, errCallbackAdminInvalidRequest), errors.Is(err, ErrInvalidCallbackReceipt),
		errors.Is(err, webhook.ErrInvalidRetry), errors.Is(err, idempotency.ErrInvalidKey):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	callbackAdminWriteJSON(response, status, map[string]any{"ok": false, "error": code})
}

type callbackReceiptJSON struct {
	ID                   int64                `json:"id"`
	InboxID              int64                `json:"inbox_id"`
	Kind                 CallbackReceiptKind  `json:"kind"`
	TargetReceiptID      int64                `json:"target_receipt_id,omitempty"`
	AttemptNumber        int                  `json:"attempt_number"`
	EventType            string               `json:"event_type,omitempty"`
	ChangeType           string               `json:"change_type,omitempty"`
	PriorInboxStatus     webhook.Status       `json:"prior_inbox_status"`
	ResultingInboxStatus webhook.Status       `json:"resulting_inbox_status"`
	ResultCodes          []CallbackResultCode `json:"result_codes"`
	ErrorCode            string               `json:"error_code,omitempty"`
	CreatedAt            string               `json:"created_at"`
}

func safeCallbackReceipt(receipt CallbackReceipt) callbackReceiptJSON {
	results := append([]CallbackResultCode(nil), receipt.ResultCodes...)
	if results == nil {
		results = []CallbackResultCode{}
	}
	return callbackReceiptJSON{
		ID: receipt.ID, InboxID: receipt.InboxID, Kind: receipt.Kind,
		TargetReceiptID: receipt.TargetReceiptID, AttemptNumber: receipt.AttemptNumber,
		EventType: receipt.EventType, ChangeType: receipt.ChangeType,
		PriorInboxStatus: receipt.PriorInboxStatus, ResultingInboxStatus: receipt.ResultingInboxStatus,
		ResultCodes: results, ErrorCode: receipt.ErrorCode,
		CreatedAt: receipt.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func callbackReceiptPage(request *http.Request) (CallbackReceiptPage, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return CallbackReceiptPage{}, errCallbackAdminInvalidRequest
	}
	for key, entries := range values {
		if (key != "before_id" && key != "limit") || len(entries) != 1 {
			return CallbackReceiptPage{}, errCallbackAdminInvalidRequest
		}
	}
	beforeID, err := callbackAdminOptionalInt64(values.Get("before_id"), 0)
	if err != nil || beforeID < 0 {
		return CallbackReceiptPage{}, errCallbackAdminInvalidRequest
	}
	limit64, err := callbackAdminOptionalInt64(values.Get("limit"), 50)
	if err != nil || limit64 < 1 || limit64 > 100 {
		return CallbackReceiptPage{}, errCallbackAdminInvalidRequest
	}
	return CallbackReceiptPage{BeforeID: beforeID, Limit: int(limit64)}, nil
}

func callbackAdminOptionalInt64(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != raw {
		return 0, errCallbackAdminInvalidRequest
	}
	return value, nil
}

func callbackAdminPositiveID(raw string) (int64, error) {
	value, err := callbackAdminOptionalInt64(raw, 0)
	if err != nil || value < 1 {
		return 0, errCallbackAdminInvalidRequest
	}
	return value, nil
}

func callbackAdminIdempotencyKey(request *http.Request) (idempotency.Key, error) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", idempotency.ErrInvalidKey
	}
	return idempotency.Parse(values[0])
}

func callbackAdminDecodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errCallbackAdminInvalidRequest
	}
	request.Body = http.MaxBytesReader(response, request.Body, callbackAdminMaxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return callbackAdminBodyError(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return callbackAdminBodyError(err)
	}
	return nil
}

func callbackAdminRequireEmptyBody(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, callbackAdminMaxBodyBytes)
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		return callbackAdminBodyError(err)
	}
	if strings.TrimSpace(string(contents)) != "" {
		return errCallbackAdminInvalidRequest
	}
	return nil
}

func callbackAdminBodyError(err error) error {
	var maximum *http.MaxBytesError
	if errors.As(err, &maximum) {
		return errCallbackAdminBodyTooLarge
	}
	return errCallbackAdminInvalidRequest
}

func validCallbackAdminReason(reason string) bool {
	return reason != "" && len(reason) <= 500 && strings.TrimSpace(reason) == reason &&
		strings.IndexFunc(reason, unicode.IsControl) < 0
}

func callbackRetryCommandDigest(receiptID, actorID int64, attempt int, status webhook.Status, reason string) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		"wecom-callback-admin-retry-v1", strconv.FormatInt(receiptID, 10),
		strconv.FormatInt(actorID, 10), strconv.Itoa(attempt), string(status), reason,
	}, "\x00")))
}

func callbackAdminNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

func callbackAdminWriteJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func (handler *CallbackAdminHandler) refreshGroupMembership(w http.ResponseWriter, r *http.Request) {
	if _, e := handler.writePrincipal(r); e != nil {
		handler.writeError(w, e)
		return
	}
	var input struct {
		ChatReference string `json:"chat_reference"`
	}
	if e := callbackAdminDecodeJSON(w, r, &input); e != nil {
		handler.writeError(w, e)
		return
	}
	if r.URL.RawQuery != "" || input.ChatReference == "" || len(input.ChatReference) > 256 || strings.TrimSpace(input.ChatReference) != input.ChatReference {
		handler.writeError(w, errCallbackAdminInvalidRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if handler.groups == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{"code": "group_membership_unavailable"})
		return
	}
	facts, e := handler.groups.Refresh(r.Context(), input.ChatReference)
	if e != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]any{"complete": e == nil && facts.Complete, "provider_complete": e == nil && facts.ProviderComplete, "observed_at": facts.ObservedAt, "external_count": facts.ExternalCount, "unresolved_count": facts.UnresolvedCount, "resolved_customer_count": len(facts.CustomerIDs)})
}
