// Package http exposes the frozen OneID administration API.
package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	nethttp "net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const maxRequestBodyBytes int64 = 64 << 10

var errRequestTooLarge = errors.New("oneid request body is too large")

// Authenticator must return only a currently active admin/staff principal.
// It owns extraction and validation of credentials from the complete request.
type Authenticator interface {
	Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

// CSRFAuthorizer authenticates the request and validates its CSRF proof. It
// must return only a currently active admin/staff principal.
type CSRFAuthorizer interface {
	AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

// Auditor is satisfied directly by platform/audit.Service.
type Auditor interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type OneIDService interface {
	identityport.Resolver
	ConfirmMerge(context.Context, identityapp.ConfirmMergeCommand) (identityapp.LinkResult, error)
	RevertConfirmedMerge(context.Context, int64) (identityapp.MergeRecord, error)
}

type Config struct {
	UnitOfWork      platformport.UnitOfWork
	Authenticator   Authenticator
	CSRF            CSRFAuthorizer
	OneID           OneIDService
	SourceConflicts identityport.HXCSourceConflictManager
	Queries         query.Reader
	Audit           Auditor
}

type Handler struct {
	uow             platformport.UnitOfWork
	auth            Authenticator
	csrf            CSRFAuthorizer
	oneID           OneIDService
	sourceConflicts identityport.HXCSourceConflictManager
	queries         query.Reader
	audit           Auditor
}

func NewHandler(config Config) (*Handler, error) {
	if config.UnitOfWork == nil || config.Authenticator == nil || config.CSRF == nil || config.OneID == nil || config.SourceConflicts == nil || config.Queries == nil || config.Audit == nil {
		return nil, errors.New("oneid HTTP dependencies are required")
	}
	return &Handler{uow: config.UnitOfWork, auth: config.Authenticator, csrf: config.CSRF,
		oneID: config.OneID, sourceConflicts: config.SourceConflicts, queries: config.Queries, audit: config.Audit}, nil
}

func (handler *Handler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/admin/oneid/resolve", handler.resolve)
	mux.HandleFunc("GET /api/admin/oneid/customers/{customer_id}", handler.customer)
	mux.HandleFunc("GET /api/admin/oneid/conflicts", handler.conflicts)
	mux.HandleFunc("GET /api/admin/oneid/merge-candidates", handler.mergeCandidates)
	mux.HandleFunc("GET /api/admin/oneid/source-conflicts", handler.hxcSourceConflicts)
	mux.HandleFunc("POST /api/admin/oneid/source-conflicts/{id}/ignore", handler.ignoreHXCSourceConflict)
	mux.HandleFunc("POST /api/admin/oneid/merge-candidates/{id}/confirm", handler.confirmMerge)
	mux.HandleFunc("POST /api/admin/oneid/merges/{id}/reverse", handler.reverseMerge)
	return noStore(mux)
}

func (handler *Handler) hxcSourceConflicts(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	if request.URL.Query().Get("source") != "hxc" {
		handler.writeError(response, query.ErrInvalidQuery)
		return
	}
	options, err := sourceConflictListOptions(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var page query.HXCSourceConflictPage
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		page, queryErr = handler.queries.HXCSourceConflicts(txContext, options)
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, page)
}

func (handler *Handler) ignoreHXCSourceConflict(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	conflictID, err := positiveID(request.PathValue("id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	key, err := idempotency.Parse(request.Header.Get("Idempotency-Key"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var input struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err = decodeJSON(response, request, &input); err != nil || input.ExpectedVersion < 1 {
		if err == nil {
			err = query.ErrInvalidQuery
		}
		handler.writeError(response, err)
		return
	}
	var item identityport.HXCSourceConflict
	var replayed bool
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var commandErr error
		item, replayed, commandErr = handler.sourceConflicts.IgnoreHXCSourceConflict(txContext, identityport.IgnoreHXCSourceConflictCommand{
			ConflictID: conflictID, ExpectedVersion: input.ExpectedVersion, ReasonCode: input.Reason,
			Operator: principalOperator(principal), IdempotencyKey: string(key),
		})
		if commandErr != nil {
			return commandErr
		}
		payload, marshalErr := json.Marshal(map[string]any{"conflict_id": conflictID, "reason": input.Reason, "replayed": replayed})
		if marshalErr != nil {
			return marshalErr
		}
		auditKeyDigest := sha256.Sum256([]byte(key))
		_, auditErr := handler.audit.Append(txContext, platformaudit.Event{IdempotencyKey: idempotency.Key(fmt.Sprintf("identity:hxc-ignore:%d:%x", conflictID, auditKeyDigest)), Action: "identity.hxc_source_conflict_ignored", ActorType: string(principal.Kind), ActorID: strconv.FormatInt(principal.InternalID, 10), ResourceType: "identity_source_conflict", ResourceID: strconv.FormatInt(conflictID, 10), Payload: payload})
		return auditErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{"conflict": item, "replayed": replayed})
}

func (handler *Handler) resolve(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	var input struct {
		Kind  identitydomain.Kind `json:"kind"`
		Scope string              `json:"scope"`
		Value string              `json:"value"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		handler.writeError(response, err)
		return
	}
	reference := identitydomain.Reference{
		Kind: input.Kind, Scope: input.Scope, Value: input.Value,
		Assurance: identitydomain.AssuranceDeclared, Source: "oneid-admin-resolve",
	}
	if _, err := identitydomain.Normalize(reference); err != nil {
		handler.writeError(response, err)
		return
	}

	var result identityport.ResolveResult
	err := handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var resolveErr error
		result, resolveErr = handler.oneID.Resolve(txContext, reference)
		return resolveErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	switch result.Status {
	case identityport.ResolveFound:
		writeJSON(response, nethttp.StatusOK, map[string]any{
			"status": result.Status, "customer_id": result.CustomerID, "identity_id": result.IdentityID,
		})
	case identityport.ResolveNotFound, identityport.ResolveConflict:
		writeJSON(response, nethttp.StatusOK, map[string]any{"status": result.Status})
	default:
		handler.writeError(response, errors.New("invalid OneID resolve result"))
	}
}

func (handler *Handler) customer(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	customerID, err := positiveID(request.PathValue("customer_id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var detail query.CustomerDetail
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		detail, queryErr = handler.queries.Customer(txContext, customerdomain.CustomerID(customerID))
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, detail)
}

func (handler *Handler) conflicts(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	options, err := listOptions(request, map[string]struct{}{"open": {}, "resolved": {}, "ignored": {}})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var page query.ConflictPage
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		page, queryErr = handler.queries.Conflicts(txContext, options)
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, page)
}

func (handler *Handler) mergeCandidates(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.readPrincipal(request); err != nil {
		handler.writeError(response, err)
		return
	}
	options, err := listOptions(request, map[string]struct{}{"open": {}, "confirmed": {}, "rejected": {}})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var page query.MergeCandidatePage
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		page, queryErr = handler.queries.MergeCandidates(txContext, options)
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, page)
}

func (handler *Handler) confirmMerge(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	candidateID, err := positiveID(request.PathValue("id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var input struct {
		SurvivorCustomerID customerdomain.CustomerID `json:"survivor_customer_id"`
	}
	if err = decodeJSON(response, request, &input); err != nil || input.SurvivorCustomerID < 1 {
		if err == nil {
			err = query.ErrInvalidQuery
		}
		handler.writeError(response, err)
		return
	}

	var result identityapp.LinkResult
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var commandErr error
		result, commandErr = handler.oneID.ConfirmMerge(txContext, identityapp.ConfirmMergeCommand{
			CandidateID: candidateID, SurvivorCustomerID: input.SurvivorCustomerID,
			Operator: principalOperator(principal),
		})
		if commandErr != nil || result.Status == identityapp.LinkConflict {
			return commandErr
		}
		if result.Status == identityapp.LinkCandidateRejected {
			if result.Candidate == nil || result.Candidate.ID != candidateID || result.Candidate.Status != "rejected" || result.Merge != nil {
				return errors.New("invalid OneID candidate rejection result")
			}
			return nil
		}
		if result.Status != identityapp.LinkMerged || result.Merge == nil || result.Merge.ID < 1 || result.Merge.CandidateID != candidateID ||
			result.Merge.FromCustomerID < 1 || result.Merge.ToCustomerID < 1 || result.CustomerID != result.Merge.ToCustomerID {
			return errors.New("invalid OneID confirm result")
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"candidate_id": candidateID, "from_customer_id": result.Merge.FromCustomerID,
			"survivor_customer_id": result.Merge.ToCustomerID,
		})
		if marshalErr != nil {
			return marshalErr
		}
		_, auditErr := handler.audit.Append(txContext, platformaudit.Event{
			IdempotencyKey: idempotency.Key("identity:merge-confirmed:" + strconv.FormatInt(result.Merge.ID, 10)),
			Action:         "identity.merge_confirmed",
			ActorType:      string(principal.Kind),
			ActorID:        strconv.FormatInt(principal.InternalID, 10),
			ResourceType:   "customer_merge",
			ResourceID:     strconv.FormatInt(result.Merge.ID, 10),
			Payload:        payload,
		})
		return auditErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	if result.Status == identityapp.LinkConflict || result.Status == identityapp.LinkCandidateRejected {
		payload := map[string]any{"ok": false, "error": "identity_conflict"}
		if result.Candidate != nil {
			payload["candidate_id"] = result.Candidate.ID
		}
		if result.Conflict != nil {
			payload["conflict_id"] = result.Conflict.ID
		}
		writeJSON(response, nethttp.StatusConflict, payload)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{
		"ok": true, "status": result.Status, "merge_id": result.Merge.ID,
		"survivor_customer_id": result.CustomerID,
	})
}

func (handler *Handler) reverseMerge(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.writePrincipal(request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	mergeID, err := positiveID(request.PathValue("id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	if err = requireEmptyBody(response, request); err != nil {
		handler.writeError(response, err)
		return
	}
	var result identityapp.MergeRecord
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var commandErr error
		result, commandErr = handler.oneID.RevertConfirmedMerge(txContext, mergeID)
		if commandErr != nil {
			return commandErr
		}
		if result.ID != mergeID || result.CandidateID < 1 || result.FromCustomerID < 1 || result.ToCustomerID < 1 {
			return errors.New("invalid OneID reverse result")
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"candidate_id": result.CandidateID, "restored_customer_id": result.FromCustomerID,
			"canonical_customer_id": result.ToCustomerID,
		})
		if marshalErr != nil {
			return marshalErr
		}
		_, auditErr := handler.audit.Append(txContext, platformaudit.Event{
			IdempotencyKey: idempotency.Key("identity:merge-reversed:" + strconv.FormatInt(result.ID, 10)),
			Action:         "identity.merge_reversed",
			ActorType:      string(principal.Kind),
			ActorID:        strconv.FormatInt(principal.InternalID, 10),
			ResourceType:   "customer_merge",
			ResourceID:     strconv.FormatInt(result.ID, 10),
			Payload:        payload,
		})
		return auditErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{
		"ok": true, "status": "reversed", "merge_id": result.ID,
		"restored_customer_id": result.FromCustomerID, "canonical_customer_id": result.ToCustomerID,
	})
}

func (handler *Handler) readPrincipal(request *nethttp.Request) (accessdomain.Principal, error) {
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

func (handler *Handler) writePrincipal(request *nethttp.Request) (accessdomain.Principal, error) {
	principal, err := handler.csrf.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		return accessdomain.Principal{}, err
	}
	if err = principal.Validate(); err != nil {
		return accessdomain.Principal{}, accessdomain.ErrAuthentication
	}
	if !identityBusinessWriteRole(principal) {
		return accessdomain.Principal{}, accessdomain.ErrPermissionDenied
	}
	return principal, nil
}

func identityBusinessWriteRole(principal accessdomain.Principal) bool {
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

func (handler *Handler) writeError(response nethttp.ResponseWriter, err error) {
	status, code := nethttp.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, errRequestTooLarge):
		status, code = nethttp.StatusRequestEntityTooLarge, "invalid_request"
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = nethttp.StatusUnauthorized, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = nethttp.StatusForbidden, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = nethttp.StatusForbidden, "permission_denied"
	case errors.Is(err, query.ErrNotFound):
		status, code = nethttp.StatusNotFound, "identity_not_found"
	case errors.Is(err, identityapp.ErrMergeNotReversible):
		status, code = nethttp.StatusConflict, "merge_not_reversible"
	case errors.Is(err, identityapp.ErrConcurrentIdentityChange):
		status, code = nethttp.StatusConflict, "identity_conflict"
	case errors.Is(err, identityapp.ErrInsufficientLinkEvidence):
		status, code = nethttp.StatusConflict, "identity_conflict"
	case errors.Is(err, identityapp.ErrDeclaredPayloadMismatch):
		status, code = nethttp.StatusConflict, "idempotency_payload_mismatch"
	case errors.Is(err, query.ErrInvalidQuery), errors.Is(err, identitydomain.ErrInvalidReference),
		errors.Is(err, identityapp.ErrInvalidLinkCommand), errors.Is(err, identityapp.ErrInvalidMergeID),
		errors.Is(err, idempotency.ErrInvalidKey):
		status, code = nethttp.StatusBadRequest, "invalid_request"
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func listOptions(request *nethttp.Request, allowed map[string]struct{}) (query.ListOptions, error) {
	values := request.URL.Query()
	for key := range values {
		if key != "status" && key != "limit" && key != "offset" {
			return query.ListOptions{}, query.ErrInvalidQuery
		}
		if len(values[key]) != 1 {
			return query.ListOptions{}, query.ErrInvalidQuery
		}
	}
	status := values.Get("status")
	if status == "" {
		status = "open"
	}
	if _, ok := allowed[status]; !ok {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	limit, err := optionalInteger(values.Get("limit"), query.DefaultLimit)
	if err != nil || limit < 1 || limit > query.MaximumLimit {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	offset, err := optionalInteger(values.Get("offset"), 0)
	if err != nil || offset < 0 {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	return query.ListOptions{Status: status, Limit: limit, Offset: offset}, nil
}

func sourceConflictListOptions(request *nethttp.Request) (query.ListOptions, error) {
	values := request.URL.Query()
	for key := range values {
		if key != "source" && key != "status" && key != "limit" && key != "offset" {
			return query.ListOptions{}, query.ErrInvalidQuery
		}
		if len(values[key]) != 1 {
			return query.ListOptions{}, query.ErrInvalidQuery
		}
	}
	if values.Get("source") != "hxc" {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	status := values.Get("status")
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "resolved" && status != "ignored" {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	limit, err := optionalInteger(values.Get("limit"), query.DefaultLimit)
	if err != nil || limit < 1 || limit > query.MaximumLimit {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	offset, err := optionalInteger(values.Get("offset"), 0)
	if err != nil || offset < 0 {
		return query.ListOptions{}, query.ErrInvalidQuery
	}
	return query.ListOptions{Status: status, Limit: limit, Offset: offset}, nil
}

func optionalInteger(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || strconv.FormatInt(value, 10) != raw {
		return 0, query.ErrInvalidQuery
	}
	return int(value), nil
}

func positiveID(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || strconv.FormatInt(value, 10) != raw {
		return 0, query.ErrInvalidQuery
	}
	return value, nil
}

func decodeJSON(response nethttp.ResponseWriter, request *nethttp.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return query.ErrInvalidQuery
	}
	request.Body = nethttp.MaxBytesReader(response, request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return bodyError(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return bodyError(err)
	}
	return nil
}

func requireEmptyBody(response nethttp.ResponseWriter, request *nethttp.Request) error {
	request.Body = nethttp.MaxBytesReader(response, request.Body, maxRequestBodyBytes)
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		return bodyError(err)
	}
	if strings.TrimSpace(string(contents)) != "" {
		return query.ErrInvalidQuery
	}
	return nil
}

func bodyError(err error) error {
	var maximum *nethttp.MaxBytesError
	if errors.As(err, &maximum) {
		return errRequestTooLarge
	}
	return query.ErrInvalidQuery
}

func principalOperator(principal accessdomain.Principal) string {
	return fmt.Sprintf("%s:%d", principal.Kind, principal.InternalID)
}

func noStore(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(response nethttp.ResponseWriter, request *nethttp.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

func writeJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
