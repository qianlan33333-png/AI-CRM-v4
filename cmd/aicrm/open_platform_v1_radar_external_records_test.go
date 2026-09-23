package main

import (
	"context"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type v1RadarExternalStub struct {
	clickQueries []radarport.ExternalClickQuery
	clickPages   []radarport.ExternalClickPage
	clickErr     error
	linkQueries  []radarport.ExternalLinkMappingQuery
	linkPages    []radarport.ExternalLinkMappingPage
	linkErr      error
}

func (s *v1RadarExternalStub) ExternalClicks(_ context.Context, query radarport.ExternalClickQuery) (radarport.ExternalClickPage, error) {
	s.clickQueries = append(s.clickQueries, query)
	if s.clickErr != nil {
		return radarport.ExternalClickPage{}, s.clickErr
	}
	if len(s.clickPages) == 0 {
		return radarport.ExternalClickPage{}, nil
	}
	page := s.clickPages[0]
	s.clickPages = s.clickPages[1:]
	return page, nil
}

func (s *v1RadarExternalStub) ExternalLinkMappings(_ context.Context, query radarport.ExternalLinkMappingQuery) (radarport.ExternalLinkMappingPage, error) {
	s.linkQueries = append(s.linkQueries, query)
	if s.linkErr != nil {
		return radarport.ExternalLinkMappingPage{}, s.linkErr
	}
	if len(s.linkPages) == 0 {
		return radarport.ExternalLinkMappingPage{}, nil
	}
	page := s.linkPages[0]
	s.linkPages = s.linkPages[1:]
	return page, nil
}

func TestV1RadarClicksScopesBeforePagingAndKeepsPendingExplicit(t *testing.T) {
	firstAt := time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(-time.Minute)
	customerID := customerdomain.CustomerID(7)
	stub := &v1RadarExternalStub{clickPages: []radarport.ExternalClickPage{
		{Items: []radarport.ExternalClick{{SessionID: 18, EventID: 31, RadarID: 5, RadarCode: "rd_1234567890abcdef", OpenedAt: firstAt, OpenStage: radarport.EventRedirected, AttributionStatus: radarport.AttributionResolved, CustomerID: &customerID}}, HasMore: true},
		{Items: []radarport.ExternalClick{{SessionID: 17, EventID: 30, RadarID: 5, RadarCode: "rd_1234567890abcdef", OpenedAt: secondAt, OpenStage: radarport.EventRedirected, AttributionStatus: radarport.AttributionResolved, CustomerID: &customerID}}},
		{Items: []radarport.ExternalClick{{SessionID: 16, EventID: 29, RadarID: 5, RadarCode: "rd_1234567890abcdef", OpenedAt: secondAt, OpenStage: radarport.EventRedirected, AttributionStatus: radarport.AttributionPending}}},
	}}
	executor := &openPlatformExecutor{radarClicks: stub, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{ClientID: "radar-client", ClientRecord: 8, Audience: "external_integration", AuthVersion: 3}
	input := []byte(`{"customer_id":7,"radar_id":5,"radar_code":"rd_1234567890abcdef","clicked_to":1789286400,"limit":1}`)
	first, err := executor.v1RadarClicks(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	data := first.Data.(map[string]any)
	items := data["items"].([]map[string]any)
	if len(items) != 1 || items[0]["click_id"] != "31" || items[0]["session_id"] != "18" || items[0]["radar_id"] != "5" || items[0]["source_record_id"] != "18" || items[0]["customer_id"] != "7" || items[0]["identity_status"] != "resolved" || items[0]["attribution_status"] != "resolved" {
		t.Fatalf("first=%#v", first.Data)
	}
	next, _ := data["next_cursor"].(string)
	if next == "" || len(stub.clickQueries) != 1 || !stub.clickQueries[0].FilterApplied || len(stub.clickQueries[0].CustomerIDs) != 1 || stub.clickQueries[0].CustomerIDs[0] != customerID || stub.clickQueries[0].Limit != 1 {
		t.Fatalf("first query=%+v result=%#v", stub.clickQueries, first.Data)
	}
	secondInput := []byte(`{"customer_id":7,"radar_id":5,"radar_code":"rd_1234567890abcdef","clicked_to":1789286400,"limit":1,"cursor":` + mustJSON(t, next) + `}`)
	second, err := executor.v1RadarClicks(context.Background(), principal, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.clickQueries) != 2 || !stub.clickQueries[1].BeforeOpened.Equal(firstAt) || stub.clickQueries[1].BeforeEventID != 31 || len(second.Data.(map[string]any)["items"].([]map[string]any)) != 1 {
		t.Fatalf("second=%#v queries=%+v", second.Data, stub.clickQueries)
	}
	if _, err = executor.v1RadarClicks(context.Background(), principal, []byte(`{"customer_id":7,"radar_id":5,"radar_code":"rd_1234567890abcdef","clicked_to":1789286401,"limit":1,"cursor":`+mustJSON(t, next)+`}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("changed watermark error=%v", err)
	}
	full, err := executor.v1RadarClicks(context.Background(), principal, []byte(`{"radar_id":5,"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	fullItem := full.Data.(map[string]any)["items"].([]map[string]any)[0]
	if fullItem["customer_id"] != nil || fullItem["identity_status"] != "pending" || fullItem["attribution_status"] != "pending" {
		t.Fatalf("pending identity lost=%#v", fullItem)
	}
}

func TestV1RadarClicksRejectsUnboundReadForSelectedClient(t *testing.T) {
	stub := &v1RadarExternalStub{}
	executor := &openPlatformExecutor{radarClicks: stub, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{OwnerScope: accessdomain.OwnerScope{"customer_id": {"7"}}}
	_, err := executor.v1RadarClicks(context.Background(), principal, []byte(`{"radar_id":5}`))
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission || len(stub.clickQueries) != 0 {
		t.Fatalf("scope error=%v queries=%+v", err, stub.clickQueries)
	}
}

func TestV1RadarLinksAreUnboundContentAndRetainDisabledStatus(t *testing.T) {
	stub := &v1RadarExternalStub{linkPages: []radarport.ExternalLinkMappingPage{{Items: []radarport.ExternalLinkMapping{{RadarID: 12, RadarCode: "rd_1234567890abcdef", Title: "Archived link", Status: radar.StatusDisabled}}, HasMore: true}}}
	executor := &openPlatformExecutor{radarLinks: stub, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	first, err := executor.v1RadarLinks(context.Background(), accessdomain.MachinePrincipal{ClientID: "radar-links"}, []byte(`{"radar_id":12,"radar_code":"rd_1234567890abcdef","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	data := first.Data.(map[string]any)
	item := data["items"].([]map[string]any)[0]
	if item["link_id"] != "12" || item["source_record_id"] != "12" || item["status"] != "disabled" || item["is_enabled"] != false || item["customer_id"] != nil {
		t.Fatalf("link result=%#v", item)
	}
	next, _ := data["next_cursor"].(string)
	if next == "" || len(stub.linkQueries) != 1 || stub.linkQueries[0].RadarID != 12 || stub.linkQueries[0].Limit != 1 {
		t.Fatalf("link query=%+v", stub.linkQueries)
	}
	selected := accessdomain.MachinePrincipal{OwnerScope: accessdomain.OwnerScope{"customer_id": {"7"}}}
	if _, err = executor.v1RadarLinks(context.Background(), selected, []byte(`{}`)); openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission {
		t.Fatalf("selected link scope error=%v", err)
	}
}

func TestV1RadarClicksCarriesImplicitEndAcrossCursor(t *testing.T) {
	end := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	firstAt := end.Add(-time.Minute)
	secondAt := firstAt.Add(-time.Minute)
	customerID := customerdomain.CustomerID(7)
	stub := &v1RadarExternalStub{clickPages: []radarport.ExternalClickPage{
		{Items: []radarport.ExternalClick{{SessionID: 2, EventID: 2, RadarID: 5, RadarCode: "rd_1234567890abcdef", OpenedAt: firstAt, OpenStage: radarport.EventRedirected, AttributionStatus: radarport.AttributionResolved, CustomerID: &customerID}}, HasMore: true},
		{Items: []radarport.ExternalClick{{SessionID: 1, EventID: 1, RadarID: 5, RadarCode: "rd_1234567890abcdef", OpenedAt: secondAt, OpenStage: radarport.EventRedirected, AttributionStatus: radarport.AttributionResolved, CustomerID: &customerID}}},
	}}
	executor := &openPlatformExecutor{radarClicks: stub, activityNow: func() time.Time { return end }, v1ExternalCursorKey: []byte("external-records-test-key-32bytes")}
	principal := accessdomain.MachinePrincipal{}
	first, err := executor.v1RadarClicks(context.Background(), principal, []byte(`{"customer_id":7,"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	next := first.Data.(map[string]any)["next_cursor"].(string)
	second, err := executor.v1RadarClicks(context.Background(), principal, []byte(`{"customer_id":7,"limit":1,"cursor":`+mustJSON(t, next)+`}`))
	if err != nil || len(second.Data.(map[string]any)["items"].([]map[string]any)) != 1 || len(stub.clickQueries) != 2 || stub.clickQueries[0].End == nil || stub.clickQueries[1].End == nil || !stub.clickQueries[0].End.Equal(end) || !stub.clickQueries[1].End.Equal(end) || !stub.clickQueries[1].BeforeOpened.Equal(firstAt) || stub.clickQueries[1].BeforeEventID != 2 {
		t.Fatalf("second=%#v error=%v queries=%+v", second.Data, err, stub.clickQueries)
	}
}
