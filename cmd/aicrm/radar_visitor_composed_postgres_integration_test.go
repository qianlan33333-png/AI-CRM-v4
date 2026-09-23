package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
)

// TestPostgreSQLRadarVisitorCompositionKeepsHistoricalSessionsReadable uses
// the normal composed admin HTTP path and isolated PostgreSQL. It proves that
// historic pre-merge Radar sessions show current canonical customer data,
// while Radar itself keeps one row per session and one shared server-side
// filter contract for page and CSV reads. It creates no Provider intent.
func TestPostgreSQLRadarVisitorCompositionKeepsHistoricalSessionsReadable(t *testing.T) {
	fixture := newProductExternalPushChromiumFixture(t)
	radarID, rootID := seedComposedRadarVisitorHistory(t, fixture.ctx, fixture.application)
	var rootOneID string
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT public_number::text FROM customers WHERE id=$1`, rootID).Scan(&rootOneID); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")

	read := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
		request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
		response := httptest.NewRecorder()
		fixture.application.handler.ServeHTTP(response, request)
		return response
	}
	// The directory, profile and 360 view expose the same persisted number,
	// while URLs retain the canonical root used by historical links.
	for _, path := range []string{
		"/api/admin/customers?keyword=" + rootOneID,
		"/api/admin/customers/" + strconv.FormatInt(rootID, 10),
		"/api/admin/customers/" + strconv.FormatInt(rootID, 10) + "/360",
	} {
		response := read(path)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"customer_number":"`+rootOneID+`"`) {
			t.Fatalf("public number inconsistent for %s: status=%d", path, response.Code)
		}
	}
	assertPage := func(search string) {
		t.Helper()
		response := read("/api/admin/radar-links/" + strconv.FormatInt(radarID, 10) + "/visitors?limit=500&search=" + url.QueryEscape(search))
		var page struct {
			Items []struct {
				Nickname              *string   `json:"nickname"`
				ExternalContactID     *string   `json:"external_contact_id"`
				ExternalContactStatus string    `json:"external_contact_status"`
				OneID                 *string   `json:"oneid"`
				OpenedAt              time.Time `json:"opened_at"`
				AttributionStatus     string    `json:"attribution_status"`
			} `json:"items"`
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("visitor search=%q status=%d cache=%q decode=%v", search, response.Code, response.Header().Get("Cache-Control"), err)
		}
		if page.Total != 2 || len(page.Items) != 2 {
			t.Fatalf("visitor search=%q total=%d items=%d", search, page.Total, len(page.Items))
		}
		opened := make(map[time.Time]struct{}, len(page.Items))
		for _, item := range page.Items {
			if item.Nickname == nil || *item.Nickname != "历史根访客" || item.ExternalContactID == nil || *item.ExternalContactID != "external-radar-composed-001" || item.ExternalContactStatus != "available" || item.OneID == nil || *item.OneID != rootOneID || item.AttributionStatus != "resolved" {
				t.Fatalf("visitor search=%q did not project canonical root safely", search)
			}
			opened[item.OpenedAt] = struct{}{}
		}
		if len(opened) != 2 {
			t.Fatalf("visitor search=%q merged multiple sessions into one row", search)
		}
	}
	for _, search := range []string{"历史根访客", rootOneID, "CID-" + strconv.FormatInt(rootID, 10), "external-radar-composed-001"} {
		assertPage(search)
	}

	export := read("/api/admin/radar-links/" + strconv.FormatInt(radarID, 10) + "/visitors/export?search=" + url.QueryEscape("external-radar-composed-001"))
	if export.Code != http.StatusOK || export.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("visitor CSV status=%d cache=%q", export.Code, export.Header().Get("Cache-Control"))
	}
	records, err := csv.NewReader(strings.NewReader(export.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"昵称", "外部联系人ID", "外部联系人ID状态", "OneID", "打开时间", "身份状态"}
	if len(records) != 3 {
		t.Fatalf("visitor CSV rows=%d", len(records))
	}
	if strings.Join(records[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("visitor CSV header=%q", records[0])
	}
	opened := map[string]struct{}{"2026-09-13 09:02:03": {}, "2026-09-13 09:03:03": {}}
	for _, row := range records[1:] {
		if len(row) != 6 || row[0] != "历史根访客" || row[1] != "external-radar-composed-001" || row[2] != "可确认" || row[3] != rootOneID || row[5] != "已关联客户" {
			t.Fatalf("visitor CSV did not use the page filter and six-column contract")
		}
		if _, exists := opened[row[4]]; !exists {
			t.Fatalf("visitor CSV opened_at=%q is not the expected Shanghai time", row[4])
		}
		delete(opened, row[4])
	}
	if len(opened) != 0 {
		t.Fatalf("visitor CSV is missing expected Shanghai opened times")
	}
}

func seedComposedRadarVisitorHistory(t *testing.T, ctx context.Context, application *composedApplication) (int64, int64) {
	t.Helper()
	now := time.Date(2026, time.September, 13, 1, 2, 3, 0, time.UTC)
	var radarID, rootID, historicalID, identityID int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO radar_links(
		public_code,name,title,description,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at
	) VALUES(
		'rd_composedvisitorhistory','Composed visitor history','Composed visitor history','fixture',
		'link','https://example.com/composed-visitor-history','unionid_required','enabled',1,1,$1,$1
	) RETURNING id`, now).Scan(&radarID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}'::jsonb,1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status,merged_into_customer_id,merged_at) VALUES('merged',$1,$2) RETURNING id`, rootID, now).Scan(&historicalID); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:admin-layout-fixture-corp','external-radar-composed-001','verified','radar_composed_fixture',1,$2) RETURNING id`, rootID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	// This intentionally stale label proves the visitor read derives CID from
	// the canonical root rather than trusting directory cache content.
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','历史根访客','STALE-NONCANONICAL-LABEL','active','radar_composed_fixture',1,$2,$2)`, rootID, now); err != nil {
		t.Fatal(err)
	}
	for index, openedAt := range []time.Time{now, now.Add(time.Minute)} {
		var sessionID int64
		if err := application.pool.Native().QueryRow(ctx, `INSERT INTO radar_view_sessions(
			session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at
		) VALUES(
			decode(lpad(to_hex($1::integer),64,'0'),'hex'),$2,1,$3,$4,'resolved',decode(lpad(to_hex($1::integer+100),64,'0'),'hex'),$5::timestamptz + interval '1 hour',$5
		) RETURNING id`, index+1, radarID, identityID, historicalID, openedAt).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO radar_events(
			receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at
		) VALUES(
			$1,$2,1,$3,'content_opened','resolved',$4,$5,decode(lpad(to_hex($6::integer),64,'0'),'hex'),decode(lpad(to_hex($6::integer+100),64,'0'),'hex'),$7,$7
		)`, fmt.Sprintf("rre_composed_history_%032d", index+1), radarID, sessionID, identityID, historicalID, index+1, openedAt); err != nil {
			t.Fatal(err)
		}
	}
	return radarID, rootID
}
