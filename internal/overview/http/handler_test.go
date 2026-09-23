package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type overviewSecurityStub struct {
	principal accessdomain.Principal
	err       error
}

func (stub overviewSecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, stub.err
}

type overviewReaderStub struct {
	calls             int
	query             overviewapp.Query
	response          overviewapp.Response
	err               error
	paidRecordsCalls  int
	paidRecordsQuery  overviewapp.PaidRecordsQuery
	paidRecordsResult overviewapp.PaidRecordsResponse
	paidRecordsErr    error
}

func (stub *overviewReaderStub) Read(_ context.Context, query overviewapp.Query) (overviewapp.Response, error) {
	stub.calls++
	stub.query = query
	if stub.err != nil {
		return overviewapp.Response{}, stub.err
	}
	response := stub.response
	if response.Range.Period == "" {
		response.Range = query.Range
	}
	return response, nil
}

func (stub *overviewReaderStub) ReadPaidRecords(_ context.Context, query overviewapp.PaidRecordsQuery) (overviewapp.PaidRecordsResponse, error) {
	stub.paidRecordsCalls++
	stub.paidRecordsQuery = query
	if stub.paidRecordsErr != nil {
		return overviewapp.PaidRecordsResponse{}, stub.paidRecordsErr
	}
	response := stub.paidRecordsResult
	if response.Range.Period == "" {
		response.Range = query.Range
	}
	return response, nil
}

func TestHandlerOmitsUnknownCanonicalPayerCountFromJSON(t *testing.T) {
	asOf := time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC)
	reader := &overviewReaderStub{response: overviewapp.Response{Paid: overviewapp.Paid{
		Section:    overviewapp.Section{Status: overviewapp.StatusDataMissing, AsOf: asOf, Scope: "admin_authorized_global", ReasonCode: "canonical_payer_unavailable"},
		Gross:      []overviewapp.Money{{AmountMinor: 120, Currency: "CNY"}},
		OrderCount: 1,
	}}}
	handler, err := NewHandler(Config{
		Reader:   reader,
		Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
		Now:      func() time.Time { return asOf },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+"?period=today", nil))
	var body map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	paid, ok := body["paid"].(map[string]any)
	if response.Code != http.StatusOK || !ok || paid["order_count"] != float64(1) || paid["reason_code"] != "canonical_payer_unavailable" {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	if _, exists := paid["distinct_canonical_payers"]; exists {
		t.Fatalf("unknown canonical payer count must be omitted: %s", response.Body.String())
	}
}

func TestHandlerStopsBeforeAnyAggregateForUnauthenticatedOrUnauthorizedCaller(t *testing.T) {
	for _, row := range []struct {
		name     string
		security overviewSecurityStub
		wantCode int
	}{
		{name: "unauthenticated", security: overviewSecurityStub{err: errors.New("no session")}, wantCode: http.StatusUnauthorized},
		{name: "customer", security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, wantCode: http.StatusForbidden},
		{name: "staff without role", security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 9}}, wantCode: http.StatusForbidden},
	} {
		t.Run(row.name, func(t *testing.T) {
			reader := &overviewReaderStub{}
			handler, err := NewHandler(Config{Reader: reader, Security: row.security, Now: func() time.Time { return time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC) }})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+"?period=today", nil))
			if response.Code != row.wantCode || reader.calls != 0 {
				t.Fatalf("status=%d calls=%d", response.Code, reader.calls)
			}
		})
	}
}

func TestHandlerUsesBeijingHalfOpenWindowForAdminViewer(t *testing.T) {
	reader := &overviewReaderStub{}
	now := time.Date(2026, 9, 14, 16, 30, 0, 0, time.UTC) // 00:30 in Beijing on the 15th.
	handler, err := NewHandler(Config{
		Reader:   reader,
		Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+"?period=today", nil))
	if response.Code != http.StatusOK || reader.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.calls, response.Body.String())
	}
	wantStart := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)
	if reader.query.Range.Period != "today" || reader.query.Range.Timezone != "Asia/Shanghai" || !reader.query.Range.Start.Equal(wantStart) || !reader.query.Range.End.Equal(wantEnd) {
		t.Fatalf("window=%+v", reader.query.Range)
	}
}

func TestHandlerRejectsInvalidCustomWindowAndDuplicateQueryBeforeRead(t *testing.T) {
	reader := &overviewReaderStub{}
	handler, err := NewHandler(Config{
		Reader:   reader,
		Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}},
		Now:      time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		overviewPath + "?period=custom&from=2026-09-15",
		overviewPath + "?period=custom&from=2026-09-16&to=2026-09-15",
		overviewPath + "?period=today&period=7d",
		overviewPath + "?period=7d&from=2026-09-15",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d", target, response.Code)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid query reached reader %d times", reader.calls)
	}
}

func TestPaidRecordsHandlerUsesFrozenCursorWindowAndDoesNotExposePaymentID(t *testing.T) {
	now := time.Date(2026, 9, 14, 16, 30, 0, 0, time.UTC)
	lastPaidAt := time.Date(2026, 9, 14, 17, 0, 0, 123000000, time.UTC)
	reader := &overviewReaderStub{paidRecordsResult: overviewapp.PaidRecordsResponse{
		Items:      []paymentport.PaidOverviewRecord{{Provider: "wechat_pay", OrderReference: "M-paid-1", AmountMinor: 123, Currency: "CNY", PaidConfirmedAt: lastPaidAt}},
		NextCursor: &paymentport.PaidOverviewRecordCursor{PaidConfirmedAt: lastPaidAt, PaymentID: 91},
	}}
	handler, err := NewHandler(Config{Reader: reader, Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, paidRecordsPath+"?period=today", nil))
	var firstBody struct {
		Range struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"range"`
		Items []struct {
			Provider        string `json:"provider"`
			OrderReference  string `json:"order_reference"`
			PayerCustomerID *int64 `json:"payer_customer_id"`
			AmountMinor     int64  `json:"amount_minor"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)
	if first.Code != http.StatusOK || reader.paidRecordsCalls != 1 || !reader.paidRecordsQuery.Range.Start.Equal(wantStart) || !reader.paidRecordsQuery.Range.End.Equal(wantEnd) || len(firstBody.Items) != 1 || firstBody.Items[0].Provider != "wechat_pay" || firstBody.Items[0].OrderReference != "M-paid-1" || firstBody.Items[0].PayerCustomerID != nil || firstBody.Items[0].AmountMinor != 123 || firstBody.NextCursor == "" || strings.Contains(first.Body.String(), "payment_id") {
		t.Fatalf("first response=%d query=%+v body=%s", first.Code, reader.paidRecordsQuery, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.now = func() time.Time { return now.AddDate(0, 0, 1) }
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, paidRecordsPath+"?cursor="+url.QueryEscape(firstBody.NextCursor), nil))
	if second.Code != http.StatusOK || reader.paidRecordsCalls != 2 || reader.paidRecordsQuery.Cursor == nil || reader.paidRecordsQuery.Cursor.PaymentID != 91 || !reader.paidRecordsQuery.Cursor.PaidConfirmedAt.Equal(lastPaidAt) || !reader.paidRecordsQuery.Range.Start.Equal(wantStart) || !reader.paidRecordsQuery.Range.End.Equal(wantEnd) {
		t.Fatalf("second response=%d query=%+v body=%s", second.Code, reader.paidRecordsQuery, second.Body.String())
	}
}

func TestPaidRecordsHandlerRejectsMixedOrMalformedCursorBeforeRead(t *testing.T) {
	reader := &overviewReaderStub{}
	handler, err := NewHandler(Config{Reader: reader, Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		paidRecordsPath + "?cursor=not-base64",
		paidRecordsPath + "?cursor=abc&period=today",
		paidRecordsPath + "?period=today&period=7d",
		paidRecordsPath + "?limit=100&period=today",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	if reader.paidRecordsCalls != 0 {
		t.Fatalf("invalid records request reached reader %d times", reader.paidRecordsCalls)
	}
}

func TestPaidRecordsHandlerRejectsCursorOutsideFirstPageReportingShape(t *testing.T) {
	reader := &overviewReaderStub{}
	handler, err := NewHandler(Config{Reader: reader, Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	for _, value := range []paidRecordsCursor{
		{Version: paidRecordsCursorV1, Period: "today", Start: start.Format(time.RFC3339Nano), End: start.AddDate(0, 0, 1).Format(time.RFC3339Nano), PaidConfirmedAt: start.AddDate(0, 0, 1).Format(time.RFC3339Nano), PaymentID: 1},
		{Version: paidRecordsCursorV1, Period: "today", Start: start.Add(30 * time.Minute).Format(time.RFC3339Nano), End: start.AddDate(0, 0, 1).Add(30 * time.Minute).Format(time.RFC3339Nano), PaidConfirmedAt: start.Add(time.Hour).Format(time.RFC3339Nano), PaymentID: 2},
		{Version: paidRecordsCursorV1, Period: "7d", Start: start.Format(time.RFC3339Nano), End: start.AddDate(0, 0, 6).Format(time.RFC3339Nano), PaidConfirmedAt: start.Add(time.Hour).Format(time.RFC3339Nano), PaymentID: 3},
	} {
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, paidRecordsPath+"?cursor="+url.QueryEscape(base64.RawURLEncoding.EncodeToString(payload)), nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("cursor=%+v status=%d body=%s", value, response.Code, response.Body.String())
		}
	}
	if reader.paidRecordsCalls != 0 {
		t.Fatalf("invalid cursor reached records reader %d times", reader.paidRecordsCalls)
	}
}
