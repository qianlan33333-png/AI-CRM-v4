package store

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestPostgreSQLVisitorsGroupsFirstOpeningAndHonorsEmptyAppliedSearch(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC)
	var radarID int64
	if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
		VALUES('rd_visitor_projection_0001','Visitors','Visitors','link','https://example.test/visitors','unionid_required','enabled',1,1,$1,$1) RETURNING id`, now).Scan(&radarID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,2,'{}',1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}
	insertCustomer := func(external string) (int64, int64) {
		t.Helper()
		var customerID, identityID int64
		if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if err := native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'wecom_external_userid','wecom-corp:visitor-test',$2,'verified','fixture',1,$3) RETURNING id`, customerID, external, now).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		return customerID, identityID
	}
	insertSession := func(customerID, identityID *int64, attribution string) int64 {
		t.Helper()
		var sessionID int64
		if attribution == "resolved" {
			if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
				VALUES(decode(lpad(to_hex(nextval('radar_view_sessions_id_seq')),64,'0'),'hex'),$1,1,$2,$3,'resolved',decode(repeat('03',32),'hex'),$4,$5) RETURNING id`, radarID, identityID, customerID, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
				t.Fatal(err)
			}
			return sessionID
		}
		if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at)
			VALUES(decode(lpad(to_hex(nextval('radar_view_sessions_id_seq')),64,'0'),'hex'),$1,1,$2,$3,$4) RETURNING id`, radarID, attribution, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		return sessionID
	}
	insertEvent := func(sessionID int64, stage string, attribution string, customerID, identityID *int64, at time.Time) {
		t.Helper()
		if attribution == "resolved" {
			if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
				VALUES('rre_'||lpad(nextval('radar_events_id_seq')::text,32,'0'),$1,1,$2,$3,'resolved',$4,$5,decode(lpad(to_hex(nextval('radar_events_id_seq')),64,'0'),'hex'),decode(lpad(to_hex(nextval('radar_events_id_seq')),64,'0'),'hex'),$6,$7)`, radarID, sessionID, stage, identityID, customerID, at, now); err != nil {
				t.Fatal(err)
			}
			return
		}
		if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,key_digest,payload_digest,occurred_at,created_at)
			VALUES('rre_'||lpad(nextval('radar_events_id_seq')::text,32,'0'),$1,1,$2,$3,$4,decode(lpad(to_hex(nextval('radar_events_id_seq')),64,'0'),'hex'),decode(lpad(to_hex(nextval('radar_events_id_seq')),64,'0'),'hex'),$5,$6)`, radarID, sessionID, stage, attribution, at, now); err != nil {
			t.Fatal(err)
		}
	}
	customerOne, identityOne := insertCustomer("visitor-external-one")
	customerTwo, identityTwo := insertCustomer("visitor-external-two")
	first := insertSession(&customerOne, &identityOne, "resolved")
	insertEvent(first, "content_opened", "resolved", &customerOne, &identityOne, now.Add(-5*time.Minute))
	insertEvent(first, "image_loaded", "resolved", &customerOne, &identityOne, now.Add(-2*time.Minute))
	second := insertSession(&customerOne, &identityOne, "resolved")
	insertEvent(second, "redirected", "resolved", &customerOne, &identityOne, now.Add(-3*time.Minute))
	third := insertSession(&customerTwo, &identityTwo, "resolved")
	insertEvent(third, "pdf_opened", "resolved", &customerTwo, &identityTwo, now.Add(-4*time.Minute))
	anonymous := insertSession(nil, nil, "anonymous")
	insertEvent(anonymous, "content_opened", "anonymous", nil, nil, now.Add(-1*time.Minute))
	// A receipt with a mismatched version must not attach to this visit session.
	if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,key_digest,payload_digest,occurred_at,created_at)
		VALUES('rre_version_mismatch',$1,2,$2,'pdf_opened','anonymous',decode(repeat('01',32),'hex'),decode(repeat('02',32),'hex'),$3,$4)`, radarID, anonymous, now, now); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	read := func(query radarport.VisitorQuery) radarport.VisitorSessionPage {
		t.Helper()
		var page radarport.VisitorSessionPage
		if err := uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			page, readErr = NewPostgres().Visitors(tx, query)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read(radarport.VisitorQuery{RadarID: radar.RadarID(radarID), Limit: 10})
	if page.Total != 4 || len(page.Items) != 4 || page.Items[0].SessionID != anonymous || page.Items[1].SessionID != second || page.Items[2].SessionID != third || page.Items[3].SessionID != first {
		t.Fatalf("unexpected visitor page=%+v", page)
	}
	if !page.Items[3].OpenedAt.Equal(now.Add(-5*time.Minute)) || page.Items[3].Version != 1 {
		t.Fatalf("same visit stages did not retain first opening: %+v", page.Items[3])
	}
	start := now.Add(-4 * time.Minute)
	filtered := read(radarport.VisitorQuery{RadarID: radar.RadarID(radarID), Start: &start, Limit: 10})
	if filtered.Total != 3 || len(filtered.Items) != 3 || filtered.Items[2].SessionID != third {
		t.Fatalf("[start,end) first-opening filter=%+v", filtered)
	}
	empty := read(radarport.VisitorQuery{RadarID: radar.RadarID(radarID), FilterApplied: true, CustomerIDs: []customerdomain.CustomerID{}, Limit: 10})
	if empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("applied zero-match search returned visitors=%+v", empty)
	}
	one := read(radarport.VisitorQuery{RadarID: radar.RadarID(radarID), FilterApplied: true, CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerOne)}, Limit: 10})
	if one.Total != 2 || len(one.Items) != 2 || one.Items[0].SessionID != second || one.Items[1].SessionID != first {
		t.Fatalf("customer search did not preserve separate sessions=%+v", one)
	}
}
