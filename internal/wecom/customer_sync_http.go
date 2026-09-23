package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrContactDescriptionBackfillDisabled = errors.New("contact description backfill disabled")

type CustomerSyncAuthenticator interface {
	Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}
type CustomerSyncCSRF interface {
	AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

type CustomerSyncHTTPHandler struct {
	Service              CustomerSyncService
	Auth                 CustomerSyncAuthenticator
	CSRF                 CustomerSyncCSRF
	DescriptionEnabled   bool
	DescriptionStatus    outboundport.ContactDescriptionRunStatusReader
	DescriptionReadbacks outboundport.ContactDescriptionReadbackScheduler
	UOW                  platformport.UnitOfWork
}

// ContactDescriptionCoverage joins WeCom's directory field-presence facts
// with Outbound's submitted-effect count at the HTTP composition boundary.
// The two domains retain their separate stores; this response makes any gap
// explicit instead of treating an omitted Provider field as completion.
type ContactDescriptionCoverage struct {
	Observed     int64 `json:"observed"`
	Projected    int64 `json:"projected"`
	Omitted      int64 `json:"omitted"`
	Submitted    int64 `json:"submitted"`
	NotSubmitted int64 `json:"not_submitted"`
}

func (handler CustomerSyncHTTPHandler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/admin/customer-sync-runs", handler.create)
	mux.HandleFunc("GET /api/admin/customer-sync-runs", handler.list)
	mux.HandleFunc("GET /api/admin/customer-sync-runs/{run_id}", handler.get)
	mux.HandleFunc("POST /api/admin/wecom/contact-description-backfills", handler.createDescriptionBackfill)
	mux.HandleFunc("GET /api/admin/wecom/contact-description-backfills/{run_id}", handler.getDescriptionBackfill)
	mux.HandleFunc("GET /api/admin/wecom/contact-description-backfills/{run_id}/readback", handler.getDescriptionBackfill)
	mux.HandleFunc("POST /api/admin/wecom/contact-description-backfills/{run_id}/readback", handler.scheduleDescriptionReadback)
	return mux
}

func (handler CustomerSyncHTTPHandler) createDescriptionBackfill(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.CSRF.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if !customerSyncMayWrite(principal) {
		writeSyncError(response, accessdomain.ErrPermissionDenied)
		return
	}
	if !handler.descriptionReady() {
		writeSyncError(response, ErrContactDescriptionBackfillDisabled)
		return
	}
	handler.createAuthorized(response, request, principal)
}

func (handler CustomerSyncHTTPHandler) create(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.CSRF.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if !customerSyncMayWrite(principal) {
		writeSyncError(response, accessdomain.ErrPermissionDenied)
		return
	}
	handler.createAuthorized(response, request, principal)
}

func (handler CustomerSyncHTTPHandler) createAuthorized(response nethttp.ResponseWriter, request *nethttp.Request, principal accessdomain.Principal) {
	if request.Body != nil && request.ContentLength > 0 {
		writeSyncError(response, errors.New("body_not_allowed"))
		return
	}
	rawKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if _, err := idempotency.Parse(rawKey); err != nil {
		writeSyncError(response, err)
		return
	}
	digest := sha256.Sum256([]byte(rawKey))
	run, replay, err := handler.Service.Create(request.Context(), CreateCustomerSyncRun{RunKey: "manual:" + hex.EncodeToString(digest[:]), Trigger: "manual",
		CorpScope: "wecom-corp:" + handler.Service.CorpID, RequestedBy: principal.InternalID})
	if err != nil {
		writeSyncError(response, err)
		return
	}
	status := nethttp.StatusAccepted
	if replay {
		status = nethttp.StatusOK
	}
	writeSyncJSON(response, status, map[string]any{"run": run, "replayed": replay})
}

func customerSyncMayWrite(principal accessdomain.Principal) bool {
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

func (handler CustomerSyncHTTPHandler) list(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.Auth.Authenticate(request.Context(), request); err != nil {
		writeSyncError(response, err)
		return
	}
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(value) != raw || value < 1 || value > 100 {
			writeSyncError(response, errors.New("invalid_limit"))
			return
		}
		limit = value
	}
	for key, values := range request.URL.Query() {
		if key != "limit" || len(values) != 1 {
			writeSyncError(response, errors.New("invalid_query"))
			return
		}
	}
	runs, err := handler.Service.List(request.Context(), limit)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusOK, map[string]any{"items": runs})
}

func (handler CustomerSyncHTTPHandler) get(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.Auth.Authenticate(request.Context(), request); err != nil {
		writeSyncError(response, err)
		return
	}
	id, err := strconv.ParseInt(request.PathValue("run_id"), 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != request.PathValue("run_id") {
		writeSyncError(response, errors.New("invalid_id"))
		return
	}
	run, err := handler.Service.Get(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusOK, run)
}

func (handler CustomerSyncHTTPHandler) getDescriptionBackfill(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.Auth.Authenticate(request.Context(), request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if !customerSyncMayWrite(principal) {
		writeSyncError(response, accessdomain.ErrPermissionDenied)
		return
	}
	if !handler.descriptionReady() {
		writeSyncError(response, ErrContactDescriptionBackfillDisabled)
		return
	}
	id, err := parseCustomerSyncRunID(request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	run, err := handler.Service.Get(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	stats, err := handler.DescriptionStatus.ContactDescriptionRunStats(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	source, err := handler.Service.ContactDescriptionSourceStats(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	coverage, err := contactDescriptionCoverage(source, stats)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusOK, map[string]any{"run": run, "description_backfill": stats, "description_source_coverage": coverage})
}

func (handler CustomerSyncHTTPHandler) scheduleDescriptionReadback(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.CSRF.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if !customerSyncMayWrite(principal) {
		writeSyncError(response, accessdomain.ErrPermissionDenied)
		return
	}
	if !handler.descriptionReady() {
		writeSyncError(response, ErrContactDescriptionBackfillDisabled)
		return
	}
	if request.Body != nil && request.ContentLength > 0 {
		writeSyncError(response, errors.New("body_not_allowed"))
		return
	}
	id, err := parseCustomerSyncRunID(request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if _, err = handler.Service.Get(request.Context(), id); err != nil {
		writeSyncError(response, err)
		return
	}
	var scheduled int64
	if err = handler.UOW.Within(request.Context(), func(txContext context.Context) error {
		var scheduleErr error
		scheduled, scheduleErr = handler.DescriptionReadbacks.ScheduleContactDescriptionReadbacksWithin(txContext, id)
		return scheduleErr
	}); err != nil {
		writeSyncError(response, err)
		return
	}
	stats, err := handler.DescriptionStatus.ContactDescriptionRunStats(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusAccepted, map[string]any{"run_id": id, "readback_scheduled": scheduled, "description_backfill": stats})
}

func (handler CustomerSyncHTTPHandler) descriptionReady() bool {
	return handler.DescriptionEnabled && handler.DescriptionStatus != nil && handler.DescriptionReadbacks != nil && handler.UOW != nil
}

func contactDescriptionCoverage(source ContactDescriptionSourceCoverage, stats outboundport.ContactDescriptionRunStats) (ContactDescriptionCoverage, error) {
	if source.Observed < 0 || source.Projected < 0 || source.Omitted < 0 || source.Observed != source.Projected+source.Omitted || stats.Discovered < 0 || stats.Discovered > source.Projected {
		return ContactDescriptionCoverage{}, ErrSyncCAS
	}
	return ContactDescriptionCoverage{Observed: source.Observed, Projected: source.Projected, Omitted: source.Omitted, Submitted: stats.Discovered, NotSubmitted: source.Projected - stats.Discovered}, nil
}

func parseCustomerSyncRunID(request *nethttp.Request) (int64, error) {
	id, err := strconv.ParseInt(request.PathValue("run_id"), 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != request.PathValue("run_id") {
		return 0, errors.New("invalid_id")
	}
	return id, nil
}

func writeSyncError(response nethttp.ResponseWriter, err error) {
	status, code := nethttp.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = 401, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = 403, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, ErrSyncConflict):
		status, code = 409, "sync_already_active"
	case errors.Is(err, ErrSyncNotFound):
		status, code = 404, "sync_run_not_found"
	case errors.Is(err, ErrSyncNotReady):
		status, code = 503, "provider_disabled"
	case errors.Is(err, ErrContactDescriptionBackfillDisabled):
		status, code = 503, "contact_description_backfill_disabled"
	case errors.Is(err, idempotency.ErrInvalidKey), strings.HasPrefix(err.Error(), "invalid_") || err.Error() == "body_not_allowed":
		status, code = 400, "invalid_request"
	}
	writeSyncJSON(response, status, map[string]any{"ok": false, "error": code})
}
func writeSyncJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
