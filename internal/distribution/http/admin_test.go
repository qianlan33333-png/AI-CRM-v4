package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type adminHTTPSecurity struct {
	principal accessdomain.Principal
	csrfCalls int
	csrfErr   error
}

func (s *adminHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s *adminHTTPSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	s.csrfCalls++
	return s.principal, s.csrfErr
}

type adminHTTPReader struct{}

func (adminHTTPReader) ListAdminDistributors(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminDistributor], error) {
	return distributionport.AdminPage[distributionport.AdminDistributor]{Items: []distributionport.AdminDistributor{{ID: 9, PublicNo: "D-9", DisplayName: "管理员可见昵称", AgreementVersion: "v1", Enabled: true, Version: 3}}}, nil
}
func (adminHTTPReader) ListAdminOrders(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	return distributionport.AdminPage[distributionport.AdminOrder]{}, nil
}
func (adminHTTPReader) ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	return distributionport.AdminPage[distributionport.AdminException]{}, nil
}

type adminWarningReader struct{ adminHTTPReader }

type adminHTTPDetailReader struct{ adminHTTPReader }

func (adminHTTPDetailReader) ReadAdminDistributorDetail(context.Context, int64) (distributionport.AdminDistributorDetail, error) {
	return distributionport.AdminDistributorDetail{Distributor: distributionport.AdminDistributor{ID: 9, PublicNo: "D-9", DisplayName: "管理员可见昵称", Enabled: true, ReceiverReady: true}, Earnings: distributionport.Earnings{GrossPaidSalesMinor: 19900, UnsettledPayableMinor: 1200, Currency: "CNY"}}, nil
}
func (adminHTTPDetailReader) ListAdminOrdersByDistributor(context.Context, int64, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	return distributionport.AdminPage[distributionport.AdminOrder]{Items: []distributionport.AdminOrder{{AttributionID: 11, OrderReference: "order-8", ProductName: "增长课", DistributorPublicNo: "D-9", DistributorDisplayName: "管理员可见昵称", QualificationState: "eligible", QualificationEvidenceReference: "order:8:item:1", PolicyVersion: 2, RateBasisPoints: 3000, WaitDays: 7, PaidMinor: 19900, Currency: "CNY"}}}, nil
}
func (adminHTTPDetailReader) ReadAdminOrderDetail(context.Context, int64) (distributionport.AdminOrderDetail, error) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	deadline := now.AddDate(0, 0, 7)
	return distributionport.AdminOrderDetail{Order: distributionport.AdminOrder{AttributionID: 11, OrderReference: "order-8", ProductName: "增长课", DistributorPublicNo: "D-9", DistributorDisplayName: "管理员可见昵称", QualificationState: "eligible", QualificationEvidenceReference: "order:8:item:1", PolicyVersion: 2, RateBasisPoints: 3000, WaitDays: 7, PaidMinor: 19900, Currency: "CNY"}, Commission: &distributionport.AdminCommissionDetail{CommissionID: "22", OriginalItemPaidMinor: 19900, SuccessfulRefundMinor: 9900, InitialMinor: 5970, CurrentPayableMinor: 3000, PaidMinor: 0, Status: "held", PaidConfirmedAt: now, DueAt: deadline, Currency: "CNY"}, Adjustments: []distributionport.AdminCommissionAdjustment{{ID: 31, Kind: "buyer_refund", DeltaMinor: -2970, ResultingPayableMinor: 3000, Reason: "buyer_refund", SourceReference: "refund:8", OccurredAt: now}}, Settlements: []distributionport.AdminSettlement{{ID: 41, Reference: "settlement-41", AmountMinor: 3000, Currency: "CNY", State: "receiver_succeeded", ProviderDeadlineAt: &deadline, SettlementConfirmedAt: &now, CreatedAt: &now, UpdatedAt: &deadline}}}, nil
}
func (adminHTTPDetailReader) ReadAdminExceptionDetail(context.Context, int64) (distributionport.AdminException, error) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	return distributionport.AdminException{ExceptionID: 51, CommissionID: 22, OrderReference: "order-8", DistributorPublicNo: "D-9", DistributorDisplayName: "管理员可见昵称", Kind: "buyer_refund_after_paid", Status: "recovery_recorded", AmountMinor: 1200, Currency: "CNY", Reason: "buyer_refund_after_paid", EvidenceReference: "receipt:51", ActorScope: "access:7", Audit: []distributionport.AdminExceptionAuditFact{{EventType: "distribution.recovery_recorded.v1", ActorScope: "access:7", AmountMinor: 1200, EvidenceReference: "receipt:51", OccurredAt: now}}}, nil
}

func (adminWarningReader) ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	return distributionport.AdminPage[distributionport.AdminException]{Items: []distributionport.AdminException{{ExceptionID: 17, CommissionID: 9, Kind: "settlement_deadline_imminent", Status: "open", AmountMinor: 0, Currency: "CNY", Reason: "split_deadline_within_24h", Version: 1, CanReconcile: false, CanRecordRecovery: false, CanRecordMerchantLiability: false}}}, nil
}

type adminHTTPCommands struct {
	enable   *distributionport.AdminDistributorCommand
	recovery *distributionport.AdminExceptionCommand
}

func (c *adminHTTPCommands) SetDistributorEnabled(_ context.Context, command distributionport.AdminDistributorCommand, enabled bool) error {
	if enabled {
		return errors.New("not expected")
	}
	c.enable = &command
	return nil
}
func (c *adminHTTPCommands) ReconcileException(context.Context, distributionport.AdminExceptionCommand) error {
	return nil
}
func (c *adminHTTPCommands) RecordRecovery(_ context.Context, command distributionport.AdminExceptionCommand) error {
	c.recovery = &command
	return nil
}
func (c *adminHTTPCommands) RecordMerchantLiability(context.Context, distributionport.AdminExceptionCommand) error {
	return nil
}

func TestAdminHandlerUsesAccessCSRFAndNeverExposesManualPaidEndpoint(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	commands := &adminHTTPCommands{}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: commands, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/distributors", nil))
	if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" || !strings.Contains(get.Body.String(), `"display_name":"管理员可见昵称"`) || !strings.Contains(get.Body.String(), `"receiver_status_label":"收款准备待支付侧核验"`) || strings.Contains(get.Body.String(), `"public_no"`) || strings.Contains(get.Body.String(), "customer-") || security.csrfCalls != 0 {
		t.Fatalf("read=%d body=%s csrf=%d", get.Code, get.Body.String(), security.csrfCalls)
	}
	write := httptest.NewRequest(http.MethodPost, "/api/admin/distribution/distributors/9/disable", strings.NewReader(`{"version":3,"reason":"policy breach"}`))
	write.Header.Set("Idempotency-Key", "distribution-admin-disable-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, write)
	if response.Code != http.StatusOK || security.csrfCalls != 1 || commands.enable == nil || commands.enable.ActorScope != "access:7" || commands.enable.Reason != "policy breach" {
		t.Fatalf("write=%d command=%+v csrf=%d", response.Code, commands.enable, security.csrfCalls)
	}
	paid := httptest.NewRecorder()
	handler.ServeHTTP(paid, httptest.NewRequest(http.MethodPost, "/api/admin/distribution/exceptions/8/paid", nil))
	if paid.Code != http.StatusNotFound || paid.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("manual paid route=%d cache=%q", paid.Code, paid.Header().Get("Cache-Control"))
	}
}

func TestAdminHandlerRecoveryRequiresAccessCSRFAndForwardsOnlyEvidence(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	commands := &adminHTTPCommands{}
	handler, _ := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: commands, Security: security})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/distribution/exceptions/8/recoveries", strings.NewReader(`{"version":4,"amount_minor":12,"evidence_reference":"receipt:9"}`))
	request.Header.Set("Idempotency-Key", "distribution-admin-recovery-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || commands.recovery == nil || commands.recovery.Reason != "manual_recovery" || commands.recovery.EvidenceReference != "receipt:9" || commands.recovery.AmountMinor != 12 {
		t.Fatalf("recovery=%d command=%+v", response.Code, commands.recovery)
	}
	security.csrfErr = errors.New("csrf")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("csrf=%d", denied.Code)
	}
}

func TestAdminHandlerDeadlineWarningIsInformationalInDTO(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminWarningReader{}, Commands: &adminHTTPCommands{}, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, `"kind":"settlement_deadline_imminent"`) || !strings.Contains(body, `"can_reconcile":false`) || !strings.Contains(body, `"can_record_recovery":false`) || !strings.Contains(body, `"can_record_merchant_liability":false`) {
		t.Fatalf("informational warning dto status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), body)
	}
}

func TestAdminReceiverStatusLabelUsesSafeProviderPermissionExplanation(t *testing.T) {
	if got := adminReceiverStatusLabel(false, "receiver_provider_permission_denied"); got != "收款准备被支付侧拒绝，需核验分佣权限" {
		t.Fatalf("label=%q", got)
	}
}

func TestAdminHandlerRejectsMalformedCursorWithNoStoreRead(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: &adminHTTPCommands{}, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"abc", "0", "01", "+1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/distributors?cursor="+cursor, nil))
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cursor=%q status=%d cache=%q", cursor, response.Code, response.Header().Get("Cache-Control"))
		}
	}
}

func TestAdminHandlerDetailReadsRemainServerFilteredAndExposeFrozenFacts(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminHTTPDetailReader{}, Commands: &adminHTTPCommands{}, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/api/admin/distribution/distributors/9":                 `"unsettled_payable_minor":1200`,
		"/api/admin/distribution/distributors/9/orders?limit=10": `"attribution_id":11`,
		"/api/admin/distribution/orders/11":                      `"delta_minor":-2970`,
		"/api/admin/distribution/exceptions/51":                  `"evidence_reference":"receipt:51"`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("detail %s status=%d cache=%q body=%s", path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/distributors/9", nil))
	if detailBody := response.Body.String(); !strings.Contains(detailBody, `"display_name":"管理员可见昵称"`) || !strings.Contains(detailBody, `"public_no":"D-9"`) || !strings.Contains(detailBody, `"receiver_status_label":"收款准备完成"`) || strings.Contains(detailBody, "customer-") {
		t.Fatalf("distributor detail nickname/auxiliary number DTO=%s", detailBody)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/orders/11", nil))
	body := response.Body.String()
	for _, want := range []string{`"resulting_payable_minor":3000`, `"provider_deadline_at":"2026-09-21T10:00:00Z"`, `"settlement_confirmed_at":"2026-09-14T10:00:00Z"`, `"created_at":"2026-09-14T10:00:00Z"`, `"currency":"CNY"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail DTO omitted %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"DeltaMinor"`) || strings.Contains(body, `"CreatedAt"`) {
		t.Fatalf("detail DTO exposed Go field names: %s", body)
	}
	if !strings.Contains(body, `"distributor_display_name":"管理员可见昵称"`) || strings.Contains(body, `"distributor_public_no"`) || strings.Contains(body, "customer-") {
		t.Fatalf("order detail did not expose only the distributor display name: %s", body)
	}
	exceptionResponse := httptest.NewRecorder()
	handler.ServeHTTP(exceptionResponse, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions/51", nil))
	if exceptionBody := exceptionResponse.Body.String(); !strings.Contains(exceptionBody, `"distributor_display_name":"管理员可见昵称"`) || strings.Contains(exceptionBody, `"distributor_public_no"`) || strings.Contains(exceptionBody, "customer-") {
		t.Fatalf("exception detail did not expose only the distributor display name: %s", exceptionBody)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/orders/11?cursor=1", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("detail must reject unowned cursor query, got %d", response.Code)
	}
}
