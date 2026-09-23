package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

const adminDistributionBodyLimit = 32 << 10

// AdminRequestSecurity deliberately reuses Access's employee session and CSRF
// boundary. Distribution browser sessions cannot satisfy this interface.
type AdminRequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type AdminConfig struct {
	Reader   distributionport.AdminDistributionReadModel
	Commands distributionport.AdminCommandService
	Security AdminRequestSecurity
}

type AdminHandler struct {
	reader   distributionport.AdminDistributionReadModel
	commands distributionport.AdminCommandService
	security AdminRequestSecurity
}

func NewAdminHandler(config AdminConfig) (*AdminHandler, error) {
	if config.Reader == nil || config.Commands == nil || config.Security == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &AdminHandler{reader: config.Reader, commands: config.Commands, security: config.Security}, nil
}

func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.reader == nil || h.commands == nil || h.security == nil {
		adminWriteError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	parts := adminPath(r.URL.Path)
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "admin" || parts[2] != "distribution" {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	principal, ok := h.authorize(w, r, write)
	if !ok {
		return
	}
	actorScope := "access:" + strconv.FormatInt(principal.InternalID, 10)
	switch {
	case r.Method == http.MethodGet && len(parts) == 5 && parts[3] == "distributors":
		h.distributorDetail(w, r, parts[4])
	case r.Method == http.MethodGet && len(parts) == 6 && parts[3] == "distributors" && parts[5] == "orders":
		h.distributorOrders(w, r, parts[4])
	case r.Method == http.MethodGet && len(parts) == 5 && parts[3] == "orders":
		h.orderDetail(w, r, parts[4])
	case r.Method == http.MethodGet && len(parts) == 5 && parts[3] == "exceptions":
		h.exceptionDetail(w, r, parts[4])
	case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "distributors":
		h.listDistributors(w, r)
	case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "orders":
		h.listOrders(w, r)
	case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "exceptions":
		h.listExceptions(w, r)
	case r.Method == http.MethodPost && len(parts) == 6 && parts[3] == "distributors" && (parts[5] == "disable" || parts[5] == "enable"):
		h.setDistributor(w, r, parts[4], parts[5] == "enable", actorScope)
	case r.Method == http.MethodPost && len(parts) == 6 && parts[3] == "exceptions" && parts[5] == "reconcile":
		h.reconcile(w, r, parts[4], actorScope)
	case r.Method == http.MethodPost && len(parts) == 6 && parts[3] == "exceptions" && parts[5] == "recoveries":
		h.recovery(w, r, parts[4], actorScope)
	case r.Method == http.MethodPost && len(parts) == 6 && parts[3] == "exceptions" && parts[5] == "merchant-liabilities":
		h.merchantLiability(w, r, parts[4], actorScope)
	default:
		adminWriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *AdminHandler) detailReader(w http.ResponseWriter) (distributionport.AdminDistributionDetailReadModel, bool) {
	reader, ok := h.reader.(distributionport.AdminDistributionDetailReadModel)
	if !ok {
		adminWriteError(w, http.StatusServiceUnavailable, "unavailable")
		return nil, false
	}
	return reader, true
}
func (h *AdminHandler) distributorDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.URL.RawQuery != "" {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	reader, ok := h.detailReader(w)
	if !ok {
		return
	}
	value, err := reader.ReadAdminDistributorDetail(r.Context(), id)
	if err != nil {
		adminResultError(w, err)
		return
	}
	d := value.Distributor
	e := value.Earnings
	adminWriteJSON(w, http.StatusOK, map[string]any{"distributor": distributorDetailJSON(d), "earnings": earningsJSON(e)})
}
func (h *AdminHandler) distributorOrders(w http.ResponseWriter, r *http.Request, rawID string) {
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	cursor, limit, ok := adminPage(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	reader, ok := h.detailReader(w)
	if !ok {
		return
	}
	page, err := reader.ListAdminOrdersByDistributor(r.Context(), id, cursor, limit)
	if err != nil {
		adminResultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, orderJSON(item))
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *AdminHandler) orderDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.URL.RawQuery != "" {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	reader, ok := h.detailReader(w)
	if !ok {
		return
	}
	value, err := reader.ReadAdminOrderDetail(r.Context(), id)
	if err != nil {
		adminResultError(w, err)
		return
	}
	response := map[string]any{"order": orderJSON(value.Order), "adjustments": adjustmentJSONs(value.Adjustments), "settlements": settlementJSONs(value.Settlements), "exceptions": exceptionJSONs(value.Exceptions)}
	if value.Commission != nil {
		response["commission"] = commissionDetailJSON(*value.Commission)
	}
	adminWriteJSON(w, http.StatusOK, response)
}
func (h *AdminHandler) exceptionDetail(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.URL.RawQuery != "" {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	reader, ok := h.detailReader(w)
	if !ok {
		return
	}
	value, err := reader.ReadAdminExceptionDetail(r.Context(), id)
	if err != nil {
		adminResultError(w, err)
		return
	}
	adminWriteJSON(w, http.StatusOK, exceptionDetailJSON(value))
}

func adminReceiverStatusLabel(ready bool, reason string) string {
	if ready {
		return "收款准备完成"
	}
	switch reason {
	case "receiver_accepted":
		return "收款申请已受理，等待支付侧确认"
	case "receiver_outcome_unknown":
		return "收款结果待支付侧核验"
	case "receiver_final_failed":
		return "收款准备失败，需管理员核验支付侧状态"
	case "receiver_provider_permission_denied":
		return "收款准备被支付侧拒绝，需核验分佣权限"
	case "receiver_unavailable":
		return "收款准备不可用，需管理员核验支付侧条件"
	default:
		return "收款准备待支付侧核验"
	}
}

func distributorListJSON(x distributionport.AdminDistributor) map[string]any {
	return map[string]any{
		"id":                    x.ID,
		"display_name":          x.DisplayName,
		"agreement_version":     x.AgreementVersion,
		"enabled":               x.Enabled,
		"receiver_ready":        x.ReceiverReady,
		"receiver_status":       x.ReceiverReason,
		"receiver_status_label": adminReceiverStatusLabel(x.ReceiverReady, x.ReceiverReason),
		"registered_at":         x.RegisteredAt.UTC(),
		"version":               x.Version,
	}
}
func distributorDetailJSON(x distributionport.AdminDistributor) map[string]any {
	value := distributorListJSON(x)
	// The opaque distribution number is only useful when staff have opened a
	// specific drawer. Customer IDs and synthetic customer references never
	// leave Distribution's read model.
	value["public_no"] = x.PublicNo
	return value
}
func orderJSON(x distributionport.AdminOrder) map[string]any {
	return map[string]any{"attribution_id": x.AttributionID, "order_reference": x.OrderReference, "item_line": x.ItemLine, "product_id": x.ProductID, "product_type": x.ProductType, "product_name": x.ProductName, "distributor_display_name": x.DistributorDisplayName, "qualification_state": x.QualificationState, "qualification_evidence_reference": x.QualificationEvidenceReference, "policy_version": x.PolicyVersion, "rate_basis_points": x.RateBasisPoints, "wait_days": x.WaitDays, "paid_minor": x.PaidMinor, "currency": x.Currency, "attributed_at": x.AttributedAt.UTC()}
}
func earningsJSON(x distributionport.Earnings) map[string]any {
	return map[string]any{"gross_paid_sales_minor": x.GrossPaidSalesMinor, "successful_refunds_minor": x.SuccessfulRefundsMinor, "initial_commission_minor": x.InitialCommissionMinor, "commission_adjustments_minor": x.CommissionAdjustmentsMinor, "unsettled_payable_minor": x.UnsettledPayableMinor, "paid_commission_minor": x.PaidCommissionMinor, "recovered_minor": x.RecoveredMinor, "currency": x.Currency}
}
func commissionJSON(x distributionport.CommissionListItem) map[string]any {
	return map[string]any{"commission_id": x.CommissionID, "order_reference": x.OrderReference, "product_name": x.ProductName, "initial_minor": x.InitialMinor, "current_payable_minor": x.CurrentPayableMinor, "paid_minor": x.PaidMinor, "status": x.Status, "hold_reason": x.HoldReason, "cancel_reason": x.CancelReason, "exception_reason": x.ExceptionReason, "paid_confirmed_at": x.PaidConfirmedAt.UTC(), "due_at": x.DueAt.UTC(), "created_at": x.CreatedAt.UTC(), "currency": x.Currency}
}
func commissionDetailJSON(x distributionport.AdminCommissionDetail) map[string]any {
	return map[string]any{"commission_id": x.CommissionID, "order_reference": x.OrderReference, "product_name": x.ProductName, "original_item_paid_minor": x.OriginalItemPaidMinor, "successful_refund_minor": x.SuccessfulRefundMinor, "initial_minor": x.InitialMinor, "current_payable_minor": x.CurrentPayableMinor, "paid_minor": x.PaidMinor, "status": x.Status, "hold_reason": x.HoldReason, "cancel_reason": x.CancelReason, "exception_reason": x.ExceptionReason, "paid_confirmed_at": x.PaidConfirmedAt.UTC(), "due_at": x.DueAt.UTC(), "created_at": x.CreatedAt.UTC(), "currency": x.Currency}
}
func adjustmentJSON(x distributionport.AdminCommissionAdjustment) map[string]any {
	return map[string]any{"id": x.ID, "kind": x.Kind, "delta_minor": x.DeltaMinor, "resulting_payable_minor": x.ResultingPayableMinor, "reason": x.Reason, "source_reference": x.SourceReference, "occurred_at": x.OccurredAt.UTC()}
}
func adjustmentJSONs(xs []distributionport.AdminCommissionAdjustment) []any {
	result := make([]any, 0, len(xs))
	for _, x := range xs {
		result = append(result, adjustmentJSON(x))
	}
	return result
}
func settlementJSON(x distributionport.AdminSettlement) map[string]any {
	return map[string]any{"id": x.ID, "reference": x.Reference, "amount_minor": x.AmountMinor, "currency": x.Currency, "state": x.State, "provider_deadline_at": x.ProviderDeadlineAt, "settlement_confirmed_at": x.SettlementConfirmedAt, "created_at": x.CreatedAt, "updated_at": x.UpdatedAt}
}
func settlementJSONs(xs []distributionport.AdminSettlement) []any {
	result := make([]any, 0, len(xs))
	for _, x := range xs {
		result = append(result, settlementJSON(x))
	}
	return result
}
func exceptionJSON(x distributionport.AdminException) map[string]any {
	return map[string]any{"exception_id": x.ExceptionID, "commission_id": x.CommissionID, "distributor_display_name": x.DistributorDisplayName, "order_reference": x.OrderReference, "kind": x.Kind, "status": x.Status, "unpaid_due_minor": x.UnpaidDueMinor, "already_paid_minor": x.AlreadyPaidMinor, "amount_minor": x.AmountMinor, "currency": x.Currency, "reason": x.Reason, "payment_instruction_reference": x.PaymentInstructionReference, "reconcile_target": x.ReconcileTarget, "created_at": x.CreatedAt.UTC(), "updated_at": x.UpdatedAt.UTC(), "version": x.Version, "can_reconcile": x.CanReconcile, "can_record_recovery": x.CanRecordRecovery, "can_record_merchant_liability": x.CanRecordMerchantLiability}
}
func exceptionDetailJSON(x distributionport.AdminException) map[string]any {
	value := exceptionJSON(x)
	value["evidence_reference"] = x.EvidenceReference
	value["actor_scope"] = x.ActorScope
	facts := make([]any, 0, len(x.Audit))
	for _, fact := range x.Audit {
		facts = append(facts, map[string]any{"event_type": fact.EventType, "actor_scope": fact.ActorScope, "reason": fact.Reason, "evidence_reference": fact.EvidenceReference, "amount_minor": fact.AmountMinor, "occurred_at": fact.OccurredAt.UTC()})
	}
	value["audit"] = facts
	return value
}
func exceptionJSONs(xs []distributionport.AdminException) []any {
	result := make([]any, 0, len(xs))
	for _, x := range xs {
		result = append(result, exceptionJSON(x))
	}
	return result
}

func (h *AdminHandler) listDistributors(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := adminPage(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.reader.ListAdminDistributors(r.Context(), cursor, limit)
	if err != nil {
		adminResultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, x := range page.Items {
		items = append(items, distributorListJSON(x))
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *AdminHandler) listOrders(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := adminPage(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.reader.ListAdminOrders(r.Context(), cursor, limit)
	if err != nil {
		adminResultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, x := range page.Items {
		items = append(items, orderJSON(x))
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}
func (h *AdminHandler) listExceptions(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := adminPage(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := h.reader.ListAdminExceptions(r.Context(), cursor, limit)
	if err != nil {
		adminResultError(w, err)
		return
	}
	items := make([]any, 0, len(page.Items))
	for _, x := range page.Items {
		items = append(items, exceptionJSON(x))
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (h *AdminHandler) setDistributor(w http.ResponseWriter, r *http.Request, rawID string, enabled bool, actorScope string) {
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Version int64  `json:"version"`
		Reason  string `json:"reason"`
	}
	if !adminDecode(w, r, &body) {
		return
	}
	key, ok := adminIdempotencyKey(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	err := h.commands.SetDistributorEnabled(r.Context(), distributionport.AdminDistributorCommand{DistributorID: id, ExpectedVersion: body.Version, ActorScope: actorScope, Reason: strings.TrimSpace(body.Reason), IdempotencyKey: key}, enabled)
	if err != nil {
		adminResultError(w, err)
		return
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (h *AdminHandler) reconcile(w http.ResponseWriter, r *http.Request, rawID, actorScope string) {
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if !adminDecode(w, r, &body) {
		return
	}
	key, ok := adminIdempotencyKey(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	err := h.commands.ReconcileException(r.Context(), distributionport.AdminExceptionCommand{ExceptionID: id, ExpectedVersion: body.Version, ActorScope: actorScope, IdempotencyKey: key})
	if err != nil {
		adminResultError(w, err)
		return
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (h *AdminHandler) recovery(w http.ResponseWriter, r *http.Request, rawID, actorScope string) {
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Version           int64  `json:"version"`
		AmountMinor       int64  `json:"amount_minor"`
		EvidenceReference string `json:"evidence_reference"`
	}
	if !adminDecode(w, r, &body) {
		return
	}
	key, ok := adminIdempotencyKey(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	err := h.commands.RecordRecovery(r.Context(), distributionport.AdminExceptionCommand{ExceptionID: id, ExpectedVersion: body.Version, AmountMinor: body.AmountMinor, ActorScope: actorScope, Reason: "manual_recovery", EvidenceReference: strings.TrimSpace(body.EvidenceReference), IdempotencyKey: key})
	if err != nil {
		adminResultError(w, err)
		return
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (h *AdminHandler) merchantLiability(w http.ResponseWriter, r *http.Request, rawID, actorScope string) {
	id, ok := adminID(rawID)
	if !ok {
		adminWriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Version     int64  `json:"version"`
		AmountMinor int64  `json:"amount_minor"`
		Reason      string `json:"reason"`
	}
	if !adminDecode(w, r, &body) {
		return
	}
	key, ok := adminIdempotencyKey(r)
	if !ok {
		adminWriteError(w, http.StatusBadRequest, "invalid_idempotency_key")
		return
	}
	err := h.commands.RecordMerchantLiability(r.Context(), distributionport.AdminExceptionCommand{ExceptionID: id, ExpectedVersion: body.Version, AmountMinor: body.AmountMinor, ActorScope: actorScope, Reason: strings.TrimSpace(body.Reason), IdempotencyKey: key})
	if err != nil {
		adminResultError(w, err)
		return
	}
	adminWriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *AdminHandler) authorize(w http.ResponseWriter, r *http.Request, write bool) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		adminWriteError(w, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !adminCanRead(principal) || (write && !adminCanWrite(principal)) {
		adminWriteError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if write {
		if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
			adminWriteError(w, http.StatusForbidden, "csrf_required")
			return accessdomain.Principal{}, false
		}
	}
	return principal, true
}
func adminCanRead(p accessdomain.Principal) bool {
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
func adminCanWrite(p accessdomain.Principal) bool {
	if !adminCanRead(p) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func adminPath(path string) []string {
	return strings.FieldsFunc(strings.Trim(path, "/"), func(r rune) bool { return r == '/' })
}
func adminID(value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
func adminPage(r *http.Request) (string, int32, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "cursor" && key != "limit") || len(values) != 1 {
			return "", 0, false
		}
	}
	cursor := query.Get("cursor")
	if len(cursor) > 100 {
		return "", 0, false
	}
	if cursor != "" {
		value, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || value < 1 || cursor != strconv.FormatInt(value, 10) {
			return "", 0, false
		}
	}
	limit := int64(50)
	if raw := query.Get("limit"); raw != "" {
		var err error
		limit, err = strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return "", 0, false
		}
	}
	return cursor, int32(limit), limit >= 1 && limit <= 100
}
func adminIdempotencyKey(r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	return key, key == r.Header.Get("Idempotency-Key") && len(key) >= 16 && len(key) <= 200
}
func adminDecode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, adminDistributionBodyLimit)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		adminWriteError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}
func adminWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func adminWriteError(w http.ResponseWriter, status int, code string) {
	adminWriteJSON(w, status, map[string]any{"error": code})
}
func adminResultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, distributionport.ErrUnauthorized):
		adminWriteError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, distributionport.ErrNotFound):
		adminWriteError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, distributionport.ErrConflict):
		adminWriteError(w, http.StatusConflict, "conflict")
	case errors.Is(err, distributionport.ErrUnavailable):
		adminWriteError(w, http.StatusServiceUnavailable, "unavailable")
	default:
		adminWriteError(w, http.StatusInternalServerError, "internal_error")
	}
}
