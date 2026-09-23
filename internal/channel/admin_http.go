package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const entrantAdminMaxBodyBytes int64 = 64 << 10

var (
	errEntrantAdminInvalidRequest = errors.New("invalid channel entrant admin request")
	errEntrantAdminBodyTooLarge   = errors.New("channel entrant admin request body is too large")
)

// EntrantAdminAuthenticator authenticates the complete request and returns
// only a currently active admin or staff principal.
type EntrantAdminAuthenticator interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}

// EntrantAdminCSRFAuthorizer additionally validates same-origin and CSRF
// protections for a currently active admin or staff session.
type EntrantAdminCSRFAuthorizer interface {
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type EntrantAdminReceiptStore interface {
	ListUnassigned(context.Context, EntrantReceiptPage) ([]EntrantReceipt, error)
	ReconcileAdmin(context.Context, ReconcileEntrantReceipt) (EntrantReceipt, bool, error)
}

type EntrantAdminAuditor interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type EntrantAdminConfig struct {
	UnitOfWork    platformport.UnitOfWork
	Authenticator EntrantAdminAuthenticator
	CSRF          EntrantAdminCSRFAuthorizer
	Receipts      EntrantAdminReceiptStore
	Audit         EntrantAdminAuditor
	Now           func() time.Time
}

type EntrantAdminHandler struct {
	uow      platformport.UnitOfWork
	auth     EntrantAdminAuthenticator
	csrf     EntrantAdminCSRFAuthorizer
	receipts EntrantAdminReceiptStore
	audit    EntrantAdminAuditor
	now      func() time.Time
}

func NewEntrantAdminHandler(config EntrantAdminConfig) (*EntrantAdminHandler, error) {
	if config.UnitOfWork == nil || config.Authenticator == nil || config.CSRF == nil || config.Receipts == nil || config.Audit == nil {
		return nil, errors.New("channel entrant admin HTTP dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &EntrantAdminHandler{
		uow: config.UnitOfWork, auth: config.Authenticator, csrf: config.CSRF,
		receipts: config.Receipts, audit: config.Audit, now: config.Now,
	}, nil
}

func (handler *EntrantAdminHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/channel-acquisition-entrant-receipts/unassigned", handler.listUnassigned)
	mux.HandleFunc("POST /api/admin/channel-acquisition-entrant-receipts/{receipt_id}/reconcile", handler.reconcile)
	return entrantAdminNoStore(mux)
}

func (handler *EntrantAdminHandler) listUnassigned(response http.ResponseWriter, request *http.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	if err := entrantAdminRequireEmptyBody(response, request); err != nil {
		handler.writeError(response, err)
		return
	}
	page, err := entrantReceiptPage(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var receipts []EntrantReceipt
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var listErr error
		receipts, listErr = handler.receipts.ListUnassigned(txContext, page)
		return listErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items := make([]entrantReceiptJSON, len(receipts))
	for index := range receipts {
		items[index] = safeEntrantReceipt(receipts[index])
	}
	entrantAdminWriteJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (handler *EntrantAdminHandler) reconcile(response http.ResponseWriter, request *http.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	receiptID, err := entrantAdminPositiveID(request.PathValue("receipt_id"))
	if err != nil || request.URL.RawQuery != "" {
		handler.writeError(response, errEntrantAdminInvalidRequest)
		return
	}
	key, err := entrantAdminIdempotencyKey(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var input struct {
		ExpectedStatus EntrantStatus `json:"expected_status"`
		BindingID      int64         `json:"binding_id"`
		CustomerID     int64         `json:"customer_id"`
		Reason         string        `json:"reason"`
	}
	if err = entrantAdminDecodeJSON(response, request, &input); err != nil {
		handler.writeError(response, err)
		return
	}
	if !reconcilableEntrantStatus(input.ExpectedStatus) || input.BindingID < 1 || input.CustomerID < 1 || !validEntrantAdminReason(input.Reason) {
		handler.writeError(response, errEntrantAdminInvalidRequest)
		return
	}
	operationDigest := sha256.Sum256([]byte(key))
	reconciledAt := handler.now().UTC().Truncate(time.Microsecond)
	command := ReconcileEntrantReceipt{
		ReceiptID: receiptID, ExpectedStatus: input.ExpectedStatus,
		BindingID: input.BindingID, CustomerID: customerdomain.CustomerID(input.CustomerID),
		ActorAdminUserID: principal.InternalID, Reason: input.Reason,
		OperationKeyDigest: operationDigest,
		CommandDigest: entrantReconcileCommandDigest(
			receiptID, principal.InternalID, input.ExpectedStatus, input.BindingID, input.CustomerID, input.Reason,
		),
		ReconciledAt: reconciledAt,
	}
	var receipt EntrantReceipt
	var created bool
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var reconcileErr error
		receipt, created, reconcileErr = handler.receipts.ReconcileAdmin(txContext, command)
		if reconcileErr != nil || !created {
			return reconcileErr
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"receipt_id": receipt.ID, "prior_status": receipt.PriorStatus,
			"resulting_status": receipt.Status, "binding_id": receipt.BindingID,
			"channel_id": receipt.ChannelID, "asset_kind": receipt.AssetKind,
			"asset_version": receipt.AssetVersion, "customer_id": receipt.CustomerID,
		})
		if marshalErr != nil {
			return marshalErr
		}
		_, auditErr := handler.audit.Append(txContext, platformaudit.Event{
			IdempotencyKey: entrantAuditKey(principal.InternalID, operationDigest),
			Action:         "channel.acquisition_entrant_reconciled",
			ActorType:      string(principal.Kind),
			ActorID:        strconv.FormatInt(principal.InternalID, 10),
			ResourceType:   "channel_acquisition_entrant_receipt",
			ResourceID:     strconv.FormatInt(receipt.ID, 10),
			Payload:        payload,
			OccurredAt:     reconciledAt,
		})
		return auditErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	entrantAdminWriteJSON(response, http.StatusOK, map[string]any{
		"receipt": safeEntrantReceipt(receipt), "replayed": !created,
	})
}

// ReconcileAdmin makes a server-timestamped HTTP operation replayable across
// process restarts. The durable actor/key receipt is checked under the same
// advisory lock used by Reconcile. A first command continues through the
// existing Store state machine; an exact replay returns its safe projection
// without manufacturing a new timestamp or append-only fact.
func (store *PostgreSQLStore) ReconcileAdmin(ctx context.Context, command ReconcileEntrantReceipt) (EntrantReceipt, bool, error) {
	if store == nil || !validReconcileCommand(command) {
		return EntrantReceipt{}, false, ErrInvalidEntrantReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel.entrant.reconcile:' || $1::bigint::text || ':' || encode($2::bytea, 'hex'), 0))`, command.ActorAdminUserID, command.OperationKeyDigest[:]); err != nil {
		return EntrantReceipt{}, false, err
	}
	existing, err := scanReconciliationFact(tx.QueryRow(ctx, reconciliationByKeyForUpdateSQL, command.ActorAdminUserID, command.OperationKeyDigest[:]))
	if err == nil {
		if !sameAdminReconciliation(existing, command) {
			return EntrantReceipt{}, false, ErrEntrantReceiptConflict
		}
		receipt, getErr := getEntrant(ctx, tx, existing.EntrantReceiptID)
		return receipt, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, err
	}
	return store.Reconcile(ctx, command)
}

func sameAdminReconciliation(fact reconciliationFact, command ReconcileEntrantReceipt) bool {
	return fact.EntrantReceiptID == command.ReceiptID && fact.ActorAdminUserID == command.ActorAdminUserID &&
		fact.OperationKeyDigest == command.OperationKeyDigest && fact.CommandDigest == command.CommandDigest &&
		fact.PriorStatus == command.ExpectedStatus && fact.ResultingStatus == EntrantReconciled &&
		fact.BindingID == command.BindingID && fact.CustomerID == command.CustomerID && fact.Reason == command.Reason
}

func (handler *EntrantAdminHandler) readPrincipal(request *http.Request) (accessdomain.Principal, error) {
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

func (handler *EntrantAdminHandler) writePrincipal(request *http.Request) (accessdomain.Principal, error) {
	principal, err := handler.csrf.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		return accessdomain.Principal{}, err
	}
	if err = principal.Validate(); err != nil {
		return accessdomain.Principal{}, accessdomain.ErrAuthentication
	}
	if !channelBusinessWriteRole(principal) {
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func channelBusinessWriteRole(principal accessdomain.Principal) bool {
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

func (handler *EntrantAdminHandler) writeError(response http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, errEntrantAdminBodyTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "invalid_request"
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = http.StatusUnauthorized, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = http.StatusForbidden, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, ErrEntrantReceiptNotFound):
		status, code = http.StatusNotFound, "receipt_not_found"
	case errors.Is(err, ErrEntrantReceiptConflict), errors.Is(err, ErrEntrantReconcileForbidden):
		status, code = http.StatusConflict, "state_conflict"
	case errors.Is(err, errEntrantAdminInvalidRequest), errors.Is(err, ErrInvalidEntrantReceipt), errors.Is(err, idempotency.ErrInvalidKey):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	entrantAdminWriteJSON(response, status, map[string]any{"ok": false, "error": code})
}

type entrantReceiptJSON struct {
	ID           int64                     `json:"id"`
	InboxID      int64                     `json:"inbox_id"`
	ChangeType   string                    `json:"change_type"`
	Status       EntrantStatus             `json:"status"`
	PriorStatus  EntrantStatus             `json:"prior_status"`
	BindingID    int64                     `json:"binding_id,omitempty"`
	ChannelID    int64                     `json:"channel_id,omitempty"`
	AssetKind    AcquisitionAssetKind      `json:"asset_kind,omitempty"`
	AssetVersion int64                     `json:"asset_version,omitempty"`
	CustomerID   customerdomain.CustomerID `json:"customer_id,omitempty"`
	OccurredAt   string                    `json:"occurred_at"`
	ReconciledAt *string                   `json:"reconciled_at,omitempty"`
	CreatedAt    string                    `json:"created_at"`
}

func safeEntrantReceipt(receipt EntrantReceipt) entrantReceiptJSON {
	var reconciledAt *string
	if receipt.ReconciledAt != nil {
		value := receipt.ReconciledAt.UTC().Format(time.RFC3339Nano)
		reconciledAt = &value
	}
	return entrantReceiptJSON{
		ID: receipt.ID, InboxID: receipt.InboxID, ChangeType: receipt.ChangeType,
		Status: receipt.Status, PriorStatus: receipt.PriorStatus,
		BindingID: receipt.BindingID, ChannelID: receipt.ChannelID,
		AssetKind: receipt.AssetKind, AssetVersion: receipt.AssetVersion,
		CustomerID:   receipt.CustomerID,
		OccurredAt:   receipt.OccurredAt.UTC().Format(time.RFC3339Nano),
		ReconciledAt: reconciledAt, CreatedAt: receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func entrantReceiptPage(request *http.Request) (EntrantReceiptPage, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return EntrantReceiptPage{}, errEntrantAdminInvalidRequest
	}
	for key, entries := range values {
		if (key != "before_id" && key != "limit") || len(entries) != 1 {
			return EntrantReceiptPage{}, errEntrantAdminInvalidRequest
		}
	}
	beforeID, err := entrantAdminOptionalInt64(values.Get("before_id"), 0)
	if err != nil || beforeID < 0 {
		return EntrantReceiptPage{}, errEntrantAdminInvalidRequest
	}
	limit64, err := entrantAdminOptionalInt64(values.Get("limit"), 50)
	if err != nil || limit64 < 1 || limit64 > 100 {
		return EntrantReceiptPage{}, errEntrantAdminInvalidRequest
	}
	return EntrantReceiptPage{BeforeID: beforeID, Limit: int(limit64)}, nil
}

func entrantAdminOptionalInt64(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != raw {
		return 0, errEntrantAdminInvalidRequest
	}
	return value, nil
}

func entrantAdminPositiveID(raw string) (int64, error) {
	value, err := entrantAdminOptionalInt64(raw, 0)
	if err != nil || value < 1 {
		return 0, errEntrantAdminInvalidRequest
	}
	return value, nil
}

func entrantAdminIdempotencyKey(request *http.Request) (idempotency.Key, error) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", idempotency.ErrInvalidKey
	}
	return idempotency.Parse(values[0])
}

func entrantAdminDecodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errEntrantAdminInvalidRequest
	}
	request.Body = http.MaxBytesReader(response, request.Body, entrantAdminMaxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return entrantAdminBodyError(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return entrantAdminBodyError(err)
	}
	return nil
}

func entrantAdminRequireEmptyBody(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, entrantAdminMaxBodyBytes)
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		return entrantAdminBodyError(err)
	}
	if strings.TrimSpace(string(contents)) != "" {
		return errEntrantAdminInvalidRequest
	}
	return nil
}

func entrantAdminBodyError(err error) error {
	var maximum *http.MaxBytesError
	if errors.As(err, &maximum) {
		return errEntrantAdminBodyTooLarge
	}
	return errEntrantAdminInvalidRequest
}

func validEntrantAdminReason(reason string) bool {
	return reason != "" && len(reason) <= 500 && strings.TrimSpace(reason) == reason &&
		strings.IndexFunc(reason, unicode.IsControl) < 0
}

func entrantReconcileCommandDigest(receiptID, actorID int64, status EntrantStatus, bindingID, customerID int64, reason string) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{
		"channel-entrant-admin-reconcile-v1", strconv.FormatInt(receiptID, 10),
		strconv.FormatInt(actorID, 10), string(status), strconv.FormatInt(bindingID, 10),
		strconv.FormatInt(customerID, 10), reason,
	}, "\x00")))
}

func entrantAuditKey(actorID int64, operationDigest [32]byte) idempotency.Key {
	return idempotency.Key("channel-entrant-reconcile-audit:" + strconv.FormatInt(actorID, 10) + ":" + hex.EncodeToString(operationDigest[:]))
}

func entrantAdminNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

func entrantAdminWriteJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
