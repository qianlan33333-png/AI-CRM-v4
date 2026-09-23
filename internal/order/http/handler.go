// Package http exposes the authenticated transaction-management read and
// export surface. It returns canonical internal customer references only.
package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

const maxBody = 64 << 10

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Application interface {
	orderport.Query
	orderport.Exporter
}

type Handler struct {
	app             Application
	security        RequestSecurity
	customers       customerport.DirectoryContactDisplayReader
	customerFilters orderport.CustomerFilterResolver
	distribution    distributionport.OrderDistributionReader
	contactReader   checkoutContactReader
}

type checkoutContactReader interface {
	orderport.CheckoutContactReader
}

// SetCustomerFilterResolver installs the composition-owned OneID read bridge
// for the optional phone/external-contact list filters.  Absence of this
// bridge fails closed for those filters; it never turns them into an
// unfiltered order query.
// SetDistributionReader installs Distribution's Order-ID batch read port. Order
// authorization remains the gate before this projection is requested.
func (h *Handler) SetDistributionReader(reader distributionport.OrderDistributionReader) error {
	if h == nil || reader == nil {
		return errors.New("order distribution reader is required")
	}
	h.distribution = reader
	return nil
}

func (h *Handler) SetCustomerFilterResolver(resolver orderport.CustomerFilterResolver) error {
	if h == nil || resolver == nil {
		return errors.New("order customer filter resolver is required")
	}
	h.customerFilters = resolver
	return nil
}

// SetCheckoutContactReader installs the Order-owned immutable checkout contact
// read seam used by the transaction detail projection.
func (h *Handler) SetCheckoutContactReader(reader checkoutContactReader) error {
	if h == nil || reader == nil {
		return errors.New("order checkout contact reader is required")
	}
	h.contactReader = reader
	return nil
}

func NewHandler(app Application, security RequestSecurity, customers ...customerport.DirectoryContactDisplayReader) (*Handler, error) {
	if app == nil || security == nil {
		return nil, errors.New("order HTTP dependencies are required")
	}
	var directory customerport.DirectoryContactDisplayReader
	if len(customers) > 0 {
		directory = customers[0]
	}
	return &Handler{app: app, security: security, customers: directory}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/admin/orders" || path == "/api/admin/wechat-pay/orders" || path == "/api/admin/alipay/transactions":
		h.list(w, r, path)
	case strings.HasPrefix(path, "/api/admin/orders/"):
		h.orderTail(w, r, strings.TrimPrefix(path, "/api/admin/orders/"))
	case path == "/api/admin/refunds":
		h.emptyRefunds(w, r)
	case path == "/api/admin/exports/preview":
		h.preview(w, r)
	case path == "/api/admin/exports" || path == "/api/admin/wechat-pay/order-exports":
		h.export(w, r, path)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/external-push-deliveries"):
		h.emptyEffects(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	query, ok := parseListQuery(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query, resolved := h.resolveCustomerFilter(w, r, query)
	if !resolved {
		return
	}
	if path == "/api/admin/wechat-pay/orders" {
		query.Provider = domain.ProviderWeChatPay
	}
	if path == "/api/admin/alipay/transactions" {
		query.Provider = domain.ProviderAlipay
	}
	page, err := h.app.List(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]orderResponse, 0, len(page.Items))
	names := h.payerDisplays(r.Context(), page.Items)
	distribution, distributionState := h.distributionFor(r.Context(), page.Items)
	for _, item := range page.Items {
		response := responseFrom(item, names)
		response.DistributionReadState = distributionState
		response.Distribution = orderDistributionJSON(distribution[item.ID])
		items = append(items, response)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "orders": items, "total": page.Total, "limit": query.Limit, "offset": query.Offset, "has_more": page.NextCursor != "", "next_cursor": page.NextCursor})
}

func (h *Handler) resolveCustomerFilter(w http.ResponseWriter, r *http.Request, query orderport.ListQuery) (orderport.ListQuery, bool) {
	filter := orderport.CustomerFilter{Phone: r.URL.Query().Get("phone"), ExternalUserID: r.URL.Query().Get("external_userid")}
	if filter.Phone == "" && filter.ExternalUserID == "" {
		return query, true
	}
	if filter.Phone != "" && filter.ExternalUserID != "" || query.CustomerID != 0 || h.customerFilters == nil {
		if h.customerFilters == nil && filter.Phone != "" || h.customerFilters == nil && filter.ExternalUserID != "" {
			writeError(w, http.StatusServiceUnavailable, "identity_filter_unavailable")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request")
		}
		return orderport.ListQuery{}, false
	}
	result, err := h.customerFilters.ResolveOrderCustomerFilter(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "identity_filter_unavailable")
		return orderport.ListQuery{}, false
	}
	switch result.Status {
	case orderport.CustomerFilterFound:
		if result.CustomerID < 1 {
			writeError(w, http.StatusServiceUnavailable, "identity_filter_unavailable")
			return orderport.ListQuery{}, false
		}
		query.CustomerID = int64(result.CustomerID)
		return query, true
	case orderport.CustomerFilterNotFound:
		query.NoCustomerMatch = true
		return query, true
	case orderport.CustomerFilterConflict:
		writeError(w, http.StatusConflict, "identity_filter_conflict")
	case orderport.CustomerFilterInvalid:
		writeError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeError(w, http.StatusServiceUnavailable, "identity_filter_unavailable")
	}
	return orderport.ListQuery{}, false
}

func (h *Handler) orderTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	ref, err := url.PathUnescape(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	provider, scoped, valid := detailProvider(r.URL.Query())
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var order domain.Snapshot
	if scoped {
		reader, ok := h.app.(orderport.ProviderScopedQuery)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "order_detail_unavailable")
			return
		}
		order, err = reader.GetByReferenceForProvider(r.Context(), provider, ref)
	} else {
		order, err = h.app.GetByReference(r.Context(), ref)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	if len(parts) == 1 {
		response := responseFrom(order, h.payerDisplays(r.Context(), []domain.Snapshot{order}))
		if err := h.populateCheckoutContact(r.Context(), order.ID, &response); err != nil {
			writeError(w, http.StatusServiceUnavailable, "order_detail_unavailable")
			return
		}
		distribution, state := h.distributionFor(r.Context(), []domain.Snapshot{order})
		response.DistributionReadState = state
		response.Distribution = orderDistributionJSON(distribution[order.ID])
		response.RefundableAmountTotal = order.Amount.AmountMinor - order.RefundedMinor
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 2 && parts[1] == "items" {
		items := make([]map[string]any, 0, len(order.Items))
		for _, item := range order.Items {
			items = append(items, map[string]any{"line_no": item.LineNo, "product_id": item.ProductID, "product_version": item.ProductVersion, "product_code": item.ProductCode, "name": item.ProductName, "unit_amount_minor": item.UnitAmountMinor, "quantity": item.Quantity, "line_amount_minor": item.LineAmountMinor, "status": order.Status, "created_at": order.CreatedAt})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func detailProvider(values url.Values) (domain.Provider, bool, bool) {
	if len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 || len(values["provider"]) != 1 {
		return "", false, false
	}
	switch values.Get("provider") {
	case "wechat", "wechat_pay":
		return domain.ProviderWeChatPay, true, true
	case "wechat_shop":
		return domain.ProviderWeChatShop, true, true
	case "alipay":
		return domain.ProviderAlipay, true, true
	default:
		return "", false, false
	}
}

func (h *Handler) emptyRefunds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "refunds": []any{}, "total": 0, "limit": 50, "has_more": false})
}

func (h *Handler) emptyEffects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "effects": []any{}, "total": 0})
}

type exportRequest struct {
	Resource string         `json:"resource"`
	Format   string         `json:"format"`
	Filter   map[string]any `json:"filter"`
	Filters  map[string]any `json:"filters"`
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if _, ok := h.write(w, r); !ok {
		return
	}
	query, ok := decodeExport(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	preview, err := h.app.PreviewExport(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resource": "orders", "format": "csv", "total": preview.Rows, "truncated": preview.Truncated})
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	query, ok := decodeExport(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	result, err := h.app.ExportCSV(r.Context(), query, principal.InternalID, key)
	if err != nil {
		resultError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="orders.csv"`)
	w.Header().Set("X-AICRM-Export-Receipt", strconv.FormatInt(result.ReceiptID, 10))
	w.Header().Set("Digest", "sha-256="+hex.EncodeToString(result.ContentDigest[:]))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
	_ = path
}

func decodeExport(r *http.Request) (orderport.ListQuery, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	decoder.DisallowUnknownFields()
	var body exportRequest
	if decoder.Decode(&body) != nil || body.Resource != "orders" || body.Format != "csv" {
		return orderport.ListQuery{}, false
	}
	filter := body.Filter
	if filter == nil {
		filter = body.Filters
	}
	values := url.Values{}
	for key, value := range filter {
		if value != nil {
			values.Set(key, fmt.Sprint(value))
		}
	}
	// Export has no composition-owned Identity resolver.  Do not let a list-only
	// identity predicate parse successfully and then disappear from an export.
	if values.Get("identity") != "" || values.Get("mobile") != "" || values.Get("phone") != "" || values.Get("external_userid") != "" {
		return orderport.ListQuery{}, false
	}
	if values.Get("transaction_id") != "" {
		values.Set("order_ref", values.Get("transaction_id"))
	}
	if values.Get("product_code") != "" {
		values.Set("product", values.Get("product_code"))
	}
	return parseListQuery(values)
}

func parseListQuery(values url.Values) (orderport.ListQuery, bool) {
	allowed := map[string]bool{"cursor": true, "limit": true, "offset": true, "provider": true, "status": true, "payment_status": true, "order_ref": true, "customer_id": true, "product": true, "created_from": true, "created_to": true, "transaction_id": true, "product_code": true, "phone": true, "external_userid": true}
	for key := range values {
		if !allowed[key] || len(values[key]) != 1 {
			return orderport.ListQuery{}, false
		}
	}
	query := orderport.ListQuery{Cursor: values.Get("cursor"), Limit: 50, OrderRef: values.Get("order_ref"), Product: values.Get("product")}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 1 || n > 100 {
			return query, false
		}
		query.Limit = int32(n)
	}
	if raw := values.Get("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 0 || n > 1000000 {
			return query, false
		}
		query.Offset = int32(n)
	}
	if raw := values.Get("customer_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			return query, false
		}
		query.CustomerID = n
	}
	if raw := values.Get("provider"); raw != "" {
		switch raw {
		case "wechat", "wechat_pay":
			query.Provider = domain.ProviderWeChatPay
		case "wechat_shop":
			query.Provider = domain.ProviderWeChatShop
		case "alipay":
			query.Provider = domain.ProviderAlipay
		default:
			return query, false
		}
	}
	status := values.Get("status")
	if status == "" {
		status = values.Get("payment_status")
	}
	if status != "" {
		if status == "unpaid" {
			status = string(domain.StatusPendingPayment)
		}
		if status == "refunding" {
			status = string(domain.StatusPartiallyRefunded)
		}
		query.Status = domain.Status(status)
	}
	if raw := values.Get("created_from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return query, false
		}
		query.CreatedFrom = &parsed
	}
	if raw := values.Get("created_to"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return query, false
		}
		parsed = parsed.AddDate(0, 0, 1)
		query.CreatedTo = &parsed
	}
	return query, true
}

type orderResponse struct {
	PayerCustomerNumber    string           `json:"payer_customer_number,omitempty"`
	ID                     int64            `json:"id"`
	RecordOrigin           string           `json:"record_origin"`
	CreatedAt              time.Time        `json:"created_at"`
	MerchantOrderNo        string           `json:"merchant_order_no"`
	OutTradeNo             string           `json:"out_trade_no"`
	OrderNo                string           `json:"order_no"`
	PlatformTransactionNo  string           `json:"platform_transaction_no"`
	TransactionID          string           `json:"transaction_id"`
	PayerName              string           `json:"payer_name"`
	PayerID                string           `json:"payer_id"`
	PayerPhoneMasked       string           `json:"payer_phone_masked"`
	ProductCode            string           `json:"product_code"`
	ProductName            string           `json:"product_name"`
	AmountYuan             string           `json:"amount_yuan"`
	Currency               string           `json:"currency"`
	Status                 domain.Status    `json:"status"`
	StatusLabel            string           `json:"status_label"`
	Provider               string           `json:"provider"`
	ProviderLabel          string           `json:"provider_label"`
	DetailURL              string           `json:"detail_url"`
	RefundableAmountTotal  int64            `json:"refundable_amount_total"`
	DistributionReadState  string           `json:"distribution_read_state,omitempty"`
	Distribution           []map[string]any `json:"distribution,omitempty"`
	ContactCollectionLevel string           `json:"contact_collection_level"`
	ShippingMobileMasked   string           `json:"shipping_mobile_masked,omitempty"`
	RecipientName          string           `json:"recipient_name,omitempty"`
	ProvinceCode           string           `json:"province_code,omitempty"`
	ProvinceName           string           `json:"province_name,omitempty"`
	CityCode               string           `json:"city_code,omitempty"`
	CityName               string           `json:"city_name,omitempty"`
	DistrictCode           string           `json:"district_code,omitempty"`
	DistrictName           string           `json:"district_name,omitempty"`
	DetailAddress          string           `json:"detail_address,omitempty"`
}

func (h *Handler) populateCheckoutContact(ctx context.Context, orderID int64, response *orderResponse) error {
	if h == nil || h.contactReader == nil || response == nil || orderID < 1 {
		return nil
	}
	response.ContactCollectionLevel = "none"
	contact, err := h.contactReader.ReadCheckoutContact(ctx, orderID)
	if err != nil {
		return err
	}
	address := contact.ShippingAddress
	if contact.AddressCollected {
		response.ContactCollectionLevel = "shipping_address"
		response.RecipientName, response.ProvinceCode, response.ProvinceName = address.RecipientName, address.ProvinceCode, address.ProvinceName
		response.CityCode, response.CityName = address.CityCode, address.CityName
		response.DistrictCode, response.DistrictName = address.DistrictCode, address.DistrictName
		response.DetailAddress = address.DetailAddress
	}
	if contact.MobileCollected {
		if response.ContactCollectionLevel == "none" {
			response.ContactCollectionLevel = "mobile"
		}
		response.ShippingMobileMasked = maskCheckoutMobile(contact.MobileE164)
	}
	return nil
}

func maskCheckoutMobile(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "+86") {
		value = strings.TrimPrefix(value, "+86")
	}
	if len(value) == 11 {
		return value[:3] + "****" + value[7:]
	}
	if len(value) > 7 {
		return value[:3] + "****" + value[len(value)-4:]
	}
	return "****"
}

func (h *Handler) distributionFor(ctx context.Context, orders []domain.Snapshot) (map[int64][]distributionport.OrderDistributionLine, string) {
	if h == nil || h.distribution == nil {
		return nil, "unavailable"
	}
	ids := make([]int64, 0, len(orders))
	for _, order := range orders {
		if order.ID > 0 {
			ids = append(ids, order.ID)
		}
	}
	values, err := h.distribution.ReadOrderDistribution(ctx, ids)
	if err != nil {
		return nil, "unavailable"
	}
	return values, "available"
}

func orderDistributionJSON(lines []distributionport.OrderDistributionLine) []map[string]any {
	if len(lines) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		value := map[string]any{
			"item_line": line.ItemLine, "product_name": line.ProductName,
			"distributor_display_name": line.DistributorDisplayName,
			"rate_basis_points":        line.RateBasisPoints, "wait_days": line.WaitDays,
			"policy_version": line.PolicyVersion, "has_commission": line.HasCommission,
			"initial_minor": line.InitialMinor, "current_payable_minor": line.CurrentPayableMinor,
			"paid_minor": line.PaidMinor, "currency": line.Currency, "status": line.Status,
			"hold_reason": line.HoldReason, "cancel_reason": line.CancelReason,
			"exception_reason": line.ExceptionReason, "due_at": line.DueAt,
			"settlement_confirmed_at": line.SettlementConfirmedAt,
			"adjustments":             orderDistributionAdjustmentsJSON(line.Adjustments),
			"settlements":             orderDistributionSettlementsJSON(line.Settlements),
			"exceptions":              orderDistributionExceptionsJSON(line.Exceptions),
		}
		result = append(result, value)
	}
	return result
}
func orderDistributionAdjustmentsJSON(items []distributionport.OrderDistributionAdjustment) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"kind": item.Kind, "delta_minor": item.DeltaMinor, "resulting_payable_minor": item.ResultingPayableMinor, "reason": item.Reason, "occurred_at": item.OccurredAt.UTC()})
	}
	return result
}
func orderDistributionSettlementsJSON(items []distributionport.OrderDistributionSettlement) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"reference": item.Reference, "amount_minor": item.AmountMinor, "currency": item.Currency, "state": item.State, "provider_deadline_at": item.ProviderDeadlineAt, "settlement_confirmed_at": item.SettlementConfirmedAt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt})
	}
	return result
}
func orderDistributionExceptionsJSON(items []distributionport.OrderDistributionException) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"kind": item.Kind, "status": item.Status, "amount_minor": item.AmountMinor, "reason": item.Reason, "evidence_reference": item.EvidenceReference, "created_at": item.CreatedAt.UTC(), "updated_at": item.UpdatedAt.UTC()})
	}
	return result
}

func responseFrom(order domain.Snapshot, customers map[customerdomain.CustomerID]customerport.DirectoryContactDisplay) orderResponse {
	provider, label := string(order.Provider), string(order.Provider)
	if order.Provider == domain.ProviderWeChatPay {
		provider, label = "wechat", "微信支付"
	} else if order.Provider == domain.ProviderWeChatShop {
		label = "微信小店"
	} else if order.Provider == domain.ProviderAlipay {
		label = "支付宝"
	}
	origin := string(order.RecordOrigin)
	if order.RecordOrigin == domain.RecordOriginHistory {
		origin = "v1_history"
	}
	productCode, productName := "", ""
	if len(order.Items) > 0 {
		productCode, productName = order.Items[0].ProductCode, order.Items[0].ProductName
	}
	payer := ""
	payerName := "未归属"
	payerPhoneMasked := ""
	payerNumber := ""
	if order.PayerCustomerID != nil {
		payer = "customer:" + strconv.FormatInt(*order.PayerCustomerID, 10)
		payerName = "客户 #" + strconv.FormatInt(*order.PayerCustomerID, 10)
		if display, exists := customers[customerdomain.CustomerID(*order.PayerCustomerID)]; exists {
			if display.DisplayName != "" {
				payerName = display.DisplayName
			}
			payerPhoneMasked = display.PhoneMasked
			payerNumber = display.CustomerNumber
		}
	}
	return orderResponse{PayerCustomerNumber: payerNumber, ID: order.ID, RecordOrigin: origin, CreatedAt: order.CreatedAt, MerchantOrderNo: order.MerchantOrderNo, OutTradeNo: order.MerchantOrderNo, OrderNo: order.SourceKey, PlatformTransactionNo: order.ProviderTransactionNo, TransactionID: order.ProviderTransactionNo, PayerName: payerName, PayerID: payer, PayerPhoneMasked: payerPhoneMasked, ProductCode: productCode, ProductName: productName, AmountYuan: fmt.Sprintf("%d.%02d", order.Amount.AmountMinor/100, order.Amount.AmountMinor%100), Currency: order.Amount.Currency, Status: order.Status, StatusLabel: string(order.Status), Provider: provider, ProviderLabel: label, DetailURL: "/admin/orderDetail.html?" + url.Values{"id": {order.MerchantOrderNo}, "provider": {provider}}.Encode()}
}

func (h *Handler) payerDisplays(ctx context.Context, orders []domain.Snapshot) map[customerdomain.CustomerID]customerport.DirectoryContactDisplay {
	if h.customers == nil {
		return nil
	}
	ids := make([]customerdomain.CustomerID, 0, len(orders))
	seen := make(map[customerdomain.CustomerID]struct{}, len(orders))
	for _, order := range orders {
		if order.PayerCustomerID == nil {
			continue
		}
		id := customerdomain.CustomerID(*order.PayerCustomerID)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	contacts, err := h.customers.ContactDisplays(ctx, ids)
	if err != nil {
		return nil
	}
	return contacts
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !canRead(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, http.StatusForbidden, "csrf_required")
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
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orderport.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, orderport.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
