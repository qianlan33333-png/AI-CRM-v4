package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type securityStub struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (s securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.authErr
}
func (s securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.csrfErr
}

type appStub struct {
	page        orderport.Page
	detail      domain.Snapshot
	scoped      map[domain.Provider]domain.Snapshot
	scopedCalls []domain.Provider
	getErr      error
	query       orderport.ListQuery
	exports     int
}

type customerDisplaysStub map[customerdomain.CustomerID]customerport.DirectoryContactDisplay

func (displays customerDisplaysStub) ContactDisplays(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryContactDisplay, error) {
	return displays, nil
}

type customerFilterStub struct {
	result orderport.CustomerFilterResolution
	err    error
	input  orderport.CustomerFilter
}

func (stub *customerFilterStub) ResolveOrderCustomerFilter(_ context.Context, input orderport.CustomerFilter) (orderport.CustomerFilterResolution, error) {
	stub.input = input
	return stub.result, stub.err
}

func (a *appStub) Get(context.Context, int64) (domain.Snapshot, error) { return a.detail, a.getErr }
func (a *appStub) GetByReference(context.Context, string) (domain.Snapshot, error) {
	return a.detail, a.getErr
}
func (a *appStub) GetByReferenceForProvider(_ context.Context, provider domain.Provider, _ string) (domain.Snapshot, error) {
	a.scopedCalls = append(a.scopedCalls, provider)
	if order, ok := a.scoped[provider]; ok {
		return order, a.getErr
	}
	return domain.Snapshot{}, orderport.ErrNotFound
}

func TestListUsesCanonicalCustomerDisplayName(t *testing.T) {
	application := &appStub{page: orderport.Page{Items: []domain.Snapshot{sampleOrder()}}}
	handler, _ := NewHandler(application, adminSecurity(), customerDisplaysStub{11: {DisplayName: "付款客户", PhoneMasked: "138****5678"}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"payer_name":"付款客户"`) || !strings.Contains(response.Body.String(), `"payer_id":"customer:11"`) || !strings.Contains(response.Body.String(), `"payer_phone_masked":"138****5678"`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}
func (a *appStub) List(_ context.Context, q orderport.ListQuery) (orderport.Page, error) {
	a.query = q
	return a.page, nil
}
func (a *appStub) PreviewExport(context.Context, orderport.ListQuery) (orderport.ExportPreview, error) {
	return orderport.ExportPreview{Rows: 1}, nil
}
func (a *appStub) ExportCSV(context.Context, orderport.ListQuery, int64, string) (orderport.ExportResult, error) {
	a.exports++
	return orderport.ExportResult{ReceiptID: 7, Rows: 1, Content: []byte("a,b\r\n"), ContentDigest: [32]byte{1}}, nil
}

func adminSecurity() securityStub {
	return securityStub{principal: accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
}
func sampleOrder() domain.Snapshot {
	payer, beneficiary := int64(11), int64(22)
	return domain.Snapshot{ID: 1, Provider: domain.ProviderWeChatPay, SourceSystem: "v3", SourceKey: "source-1", MerchantOrderNo: "M-1", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: domain.Money{AmountMinor: 1099, Currency: "CNY"}, Status: domain.StatusPaid, Items: []domain.ItemSnapshot{{LineNo: 1, ProductCode: "P-1", ProductName: "课程", UnitAmountMinor: 1099, Quantity: 1, LineAmountMinor: 1099}}, RecordOrigin: domain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func TestListUsesSafeServerFiltersAndNoStore(t *testing.T) {
	application := &appStub{page: orderport.Page{Items: []domain.Snapshot{sampleOrder()}}}
	handler, _ := NewHandler(application, adminSecurity())
	request := httptest.NewRequest(http.MethodGet, "/api/admin/orders?customer_id=11&status=paid&product=%E8%AF%BE%E7%A8%8B&limit=20", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || application.query.CustomerID != 11 || application.query.Status != domain.StatusPaid || application.query.Product != "课程" {
		t.Fatalf("code=%d headers=%v query=%+v body=%s", response.Code, response.Header(), application.query, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "external_userid") || strings.Contains(response.Body.String(), "mobile") || !strings.Contains(response.Body.String(), "customer:11") {
		t.Fatalf("unsafe or missing response: %s", response.Body.String())
	}
}

func TestListResolvesCustomerIdentityBeforeUsingTheSamePagedFilter(t *testing.T) {
	application := &appStub{page: orderport.Page{}}
	handler, _ := NewHandler(application, adminSecurity())
	resolver := &customerFilterStub{result: orderport.CustomerFilterResolution{Status: orderport.CustomerFilterFound, CustomerID: 51}}
	if err := handler.SetCustomerFilterResolver(resolver); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders?phone=13800138000&limit=20&offset=40", nil))
	if response.Code != http.StatusOK || resolver.input.Phone != "13800138000" || resolver.input.ExternalUserID != "" || application.query.CustomerID != 51 || application.query.NoCustomerMatch || application.query.Limit != 20 || application.query.Offset != 40 {
		t.Fatalf("code=%d input=%+v query=%+v body=%s", response.Code, resolver.input, application.query, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "13800138000") {
		t.Fatalf("identity leaked into response: %s", response.Body.String())
	}
}

func TestListIdentityNotFoundKeepsAnEmptyCustomerPredicate(t *testing.T) {
	application := &appStub{page: orderport.Page{}}
	handler, _ := NewHandler(application, adminSecurity())
	if err := handler.SetCustomerFilterResolver(&customerFilterStub{result: orderport.CustomerFilterResolution{Status: orderport.CustomerFilterNotFound}}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders?external_userid=external-missing&limit=20&offset=40", nil))
	if response.Code != http.StatusOK || !application.query.NoCustomerMatch || application.query.CustomerID != 0 || application.query.Limit != 20 || application.query.Offset != 40 {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, application.query, response.Body.String())
	}
}

func TestListRejectsAmbiguousOrUncomposedIdentityFilter(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		stub *customerFilterStub
		want int
		code string
	}{
		{name: "conflict", url: "/api/admin/orders?external_userid=ambiguous", stub: &customerFilterStub{result: orderport.CustomerFilterResolution{Status: orderport.CustomerFilterConflict}}, want: http.StatusConflict, code: "identity_filter_conflict"},
		{name: "both dimensions", url: "/api/admin/orders?phone=13800138000&external_userid=external", stub: &customerFilterStub{}, want: http.StatusBadRequest, code: "invalid_request"},
		{name: "uncomposed", url: "/api/admin/orders?phone=13800138000", want: http.StatusServiceUnavailable, code: "identity_filter_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &appStub{}
			handler, _ := NewHandler(application, adminSecurity())
			if test.stub != nil {
				if err := handler.SetCustomerFilterResolver(test.stub); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.url, nil))
			if response.Code != test.want || !strings.Contains(response.Body.String(), `"error":"`+test.code+`"`) || application.query != (orderport.ListQuery{}) {
				t.Fatalf("code=%d query=%+v body=%s", response.Code, application.query, response.Body.String())
			}
		})
	}
}

func TestAmbiguousReferenceReturnsConflict(t *testing.T) {
	app := &appStub{getErr: orderport.ErrConflict}
	handler, _ := NewHandler(app, adminSecurity())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/shared-ref", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProviderScopedOrderDetailKeepsSameMerchantNumbersSeparate(t *testing.T) {
	wechat := sampleOrder()
	wechat.ID, wechat.MerchantOrderNo, wechat.Provider = 101, "shared-reference", domain.ProviderWeChatPay
	alipay := sampleOrder()
	alipay.ID, alipay.MerchantOrderNo, alipay.Provider = 202, wechat.MerchantOrderNo, domain.ProviderAlipay
	application := &appStub{detail: wechat, scoped: map[domain.Provider]domain.Snapshot{domain.ProviderWeChatPay: wechat, domain.ProviderAlipay: alipay}}
	handler, err := NewHandler(application, adminSecurity())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		provider string
		wantID   int64
	}{{provider: "wechat", wantID: wechat.ID}, {provider: "alipay", wantID: alipay.ID}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/shared-reference?provider="+test.provider, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":`+strconv.FormatInt(test.wantID, 10)) {
			t.Fatalf("provider=%s code=%d body=%s", test.provider, response.Code, response.Body.String())
		}
	}
	if got := application.scopedCalls; len(got) != 2 || got[0] != domain.ProviderWeChatPay || got[1] != domain.ProviderAlipay {
		t.Fatalf("scoped calls=%v", got)
	}
	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/api/admin/orders/shared-reference", nil))
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), `"id":101`) {
		t.Fatalf("legacy detail code=%d body=%s", legacy.Code, legacy.Body.String())
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/admin/orders/shared-reference?provider=unknown", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider code=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestExportRequiresAdminCSRFAndRejectsUnresolvedIdentityFilters(t *testing.T) {
	body := `{"resource":"orders","format":"csv","filters":{"provider":"wechat","identity":"raw-openid"}}`
	for _, test := range []struct {
		name     string
		security securityStub
		want     int
	}{{"viewer", securityStub{principal: accessdomain.Principal{InternalID: 2, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, http.StatusForbidden}, {"csrf", securityStub{principal: adminSecurity().principal, csrfErr: errors.New("csrf")}, http.StatusForbidden}, {"raw identity", adminSecurity(), http.StatusBadRequest}} {
		t.Run(test.name, func(t *testing.T) {
			app := &appStub{}
			handler, _ := NewHandler(app, test.security)
			request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/order-exports", strings.NewReader(body))
			request.Header.Set("Idempotency-Key", "order-export-key-0001")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || app.exports != 0 {
				t.Fatalf("code=%d exports=%d body=%s", response.Code, app.exports, response.Body.String())
			}
		})
	}
	for _, field := range []string{"phone", "external_userid"} {
		t.Run("list-only "+field, func(t *testing.T) {
			app := &appStub{}
			handler, _ := NewHandler(app, adminSecurity())
			request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/order-exports", strings.NewReader(`{"resource":"orders","format":"csv","filters":{"`+field+`":"filter-value"}}`))
			request.Header.Set("Idempotency-Key", "order-export-key-0001")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || app.exports != 0 {
				t.Fatalf("field=%s code=%d exports=%d body=%s", field, response.Code, app.exports, response.Body.String())
			}
		})
	}
}

func TestExportReturnsReceiptBackedCSV(t *testing.T) {
	app := &appStub{}
	handler, _ := NewHandler(app, adminSecurity())
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/order-exports", strings.NewReader(`{"resource":"orders","format":"csv","filters":{"provider":"wechat"}}`))
	request.Header.Set("Idempotency-Key", "order-export-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-AICRM-Export-Receipt") != "7" || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" || app.exports != 1 {
		t.Fatalf("code=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

type distributionReaderStub struct {
	values map[int64][]distributionport.OrderDistributionLine
	err    error
	calls  [][]int64
}

func (s *distributionReaderStub) ReadOrderDistribution(_ context.Context, ids []int64) (map[int64][]distributionport.OrderDistributionLine, error) {
	s.calls = append(s.calls, append([]int64(nil), ids...))
	return s.values, s.err
}

func TestOrderResponsesBatchDistributionSnapshotsAfterOrderAuthorization(t *testing.T) {
	order := sampleOrder()
	reader := &distributionReaderStub{values: map[int64][]distributionport.OrderDistributionLine{order.ID: {{
		OrderID: order.ID, AttributionID: 71, CommissionID: 81, ItemLine: 1, ProductName: "冻结商品", DistributorCustomerID: 66, DistributorDisplayName: "分销员甲",
		RateBasisPoints: 1234, WaitDays: 7, PolicyVersion: 3, HasCommission: true, InitialMinor: 123, CurrentPayableMinor: 99, PaidMinor: 50, Currency: "CNY", Status: "exception", HoldReason: "退款复核中",
		Adjustments: []distributionport.OrderDistributionAdjustment{{Kind: "buyer_refund", DeltaMinor: -24, ResultingPayableMinor: 99, Reason: "部分退款", OccurredAt: time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC)}},
		Settlements: []distributionport.OrderDistributionSettlement{{Reference: "dstl_1", AmountMinor: 50, Currency: "CNY", State: "receiver_succeeded"}},
		Exceptions:  []distributionport.OrderDistributionException{{Kind: "buyer_refund_after_paid", Status: "open", AmountMinor: 50, Reason: "退款后待处理", EvidenceReference: "refund_1"}},
	}}}}
	handler, err := NewHandler(&appStub{page: orderport.Page{Items: []domain.Snapshot{order}}, detail: order}, adminSecurity())
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetDistributionReader(reader); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/orders", "/api/admin/orders/M-1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, `"distribution_read_state":"available"`) || !strings.Contains(body, `"distributor_display_name":"分销员甲"`) || !strings.Contains(body, `"settlement_confirmed_at":null`) || !strings.Contains(body, `"buyer_refund_after_paid"`) {
			t.Fatalf("path=%s code=%d body=%s", path, response.Code, body)
		}
		if strings.Contains(body, `"distributor_customer_id"`) || strings.Contains(body, `"attribution_id"`) || strings.Contains(body, `"commission_id"`) {
			t.Fatalf("Order response leaked Distribution internals: %s", body)
		}
	}
	if len(reader.calls) != 2 || len(reader.calls[0]) != 1 || reader.calls[0][0] != order.ID || len(reader.calls[1]) != 1 || reader.calls[1][0] != order.ID {
		t.Fatalf("expected one batch read per authorized response, calls=%v", reader.calls)
	}
}

func TestOrderResponseMakesDistributionReadFailureExplicitWithoutFailingOrder(t *testing.T) {
	order := sampleOrder()
	reader := &distributionReaderStub{err: errors.New("distribution unavailable")}
	handler, err := NewHandler(&appStub{detail: order}, adminSecurity())
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetDistributionReader(reader); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/M-1", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"distribution_read_state":"unavailable"`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOrderDistributionReadUsesSameRolesAsDistributionAdmin(t *testing.T) {
	order := sampleOrder()
	reader := &distributionReaderStub{}
	for _, principal := range []accessdomain.Principal{
		{InternalID: 9, Kind: accessdomain.KindStaff, Roles: []accessdomain.Role{accessdomain.RoleViewer}},
		{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}},
	} {
		handler, err := NewHandler(&appStub{detail: order}, securityStub{principal: principal})
		if err != nil {
			t.Fatal(err)
		}
		if err = handler.SetDistributionReader(reader); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/M-1", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("principal=%+v code=%d", principal, response.Code)
		}
	}
	before := len(reader.calls)
	denied, err := NewHandler(&appStub{detail: order}, securityStub{principal: accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindStaff}})
	if err != nil {
		t.Fatal(err)
	}
	if err = denied.SetDistributionReader(reader); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	denied.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/M-1", nil))
	if response.Code != http.StatusForbidden || len(reader.calls) != before {
		t.Fatalf("unauthorized read code=%d calls=%v", response.Code, reader.calls)
	}
}
