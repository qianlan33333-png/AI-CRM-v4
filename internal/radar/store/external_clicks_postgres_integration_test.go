package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestPostgreSQLExternalClicksGroupSuccessfulStagesAndApplyScopeBeforePaging(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

	newCustomer := func(seed string) (int64, int64) {
		t.Helper()
		var customerID, identityID int64
		if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if err := native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'wecom_external_userid','wecom-corp:external-click-test',$2,'verified','external-click-test',1,$3) RETURNING id`, customerID, seed, now).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		return customerID, identityID
	}
	customerOne, identityOne := newCustomer("click-one")
	customerTwo, identityTwo := newCustomer("click-two")
	var radarID int64
	if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
		VALUES('rd_externalclick123','External click','External click','link','https://example.test/click','anonymous','enabled',1,1,$1,$1) RETURNING id`, now).Scan(&radarID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}

	sequence := 1
	insertSession := func(status string, customerID, identityID int64) int64 {
		t.Helper()
		digest := make([]byte, 32)
		digest[0] = byte(sequence)
		sequence++
		var sessionID int64
		if status == string(radarport.AttributionResolved) {
			evidence := make([]byte, 32)
			evidence[0] = byte(sequence)
			sequence++
			if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
				VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`, digest, radarID, identityID, customerID, evidence, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
				t.Fatal(err)
			}
			return sessionID
		}
		if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at)
			VALUES($1,$2,1,$3,$4,$5) RETURNING id`, digest, radarID, status, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		return sessionID
	}
	insertEvent := func(sessionID int64, status string, customerID, identityID int64, stage radarport.EventStage, at time.Time) int64 {
		t.Helper()
		key, payload := make([]byte, 32), make([]byte, 32)
		key[0], payload[0] = byte(sequence), byte(sequence+1)
		sequence += 2
		var eventID int64
		var customer, identity any
		if status == string(radarport.AttributionResolved) {
			customer, identity = customerID, identityID
		}
		if err := native.QueryRow(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
			VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, fmt.Sprintf("external-click-%d", sequence), radarID, sessionID, stage, status, identity, customer, key, payload, at, now).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		return eventID
	}

	// A normal OAuth visit persists several technical phases. Only the first
	// successful content-open stage (redirected here) is one logical click.
	resolved := insertSession(string(radarport.AttributionResolved), customerOne, identityOne)
	_ = insertEvent(resolved, string(radarport.AttributionResolved), customerOne, identityOne, radarport.EventLanding, now.Add(-6*time.Minute))
	_ = insertEvent(resolved, string(radarport.AttributionResolved), customerOne, identityOne, radarport.EventOAuthVerified, now.Add(-5*time.Minute))
	_ = insertEvent(resolved, string(radarport.AttributionResolved), customerOne, identityOne, radarport.EventIdentityResolved, now.Add(-4*time.Minute))
	firstResolvedEvent := insertEvent(resolved, string(radarport.AttributionResolved), customerOne, identityOne, radarport.EventRedirected, now.Add(-3*time.Minute))
	pending := insertSession(string(radarport.AttributionPending), 0, 0)
	pendingEvent := insertEvent(pending, string(radarport.AttributionPending), 0, 0, radarport.EventRedirected, now.Add(-2*time.Minute))
	conflict := insertSession(string(radarport.AttributionConflict), 0, 0)
	_ = insertEvent(conflict, string(radarport.AttributionConflict), 0, 0, radarport.EventContentOpened, now.Add(-time.Minute))
	anonymous := insertSession(string(radarport.AttributionAnonymous), 0, 0)
	_ = insertEvent(anonymous, string(radarport.AttributionAnonymous), 0, 0, radarport.EventContentOpened, now.Add(-30*time.Second))
	failed := insertSession(string(radarport.AttributionFailed), 0, 0)
	_ = insertEvent(failed, string(radarport.AttributionFailed), 0, 0, radarport.EventContentOpened, now.Add(-20*time.Second))
	other := insertSession(string(radarport.AttributionResolved), customerTwo, identityTwo)
	_ = insertEvent(other, string(radarport.AttributionResolved), customerTwo, identityTwo, radarport.EventContentOpened, now.Add(-10*time.Second))

	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := radarapp.NewQueryService(uow, NewPostgres())
	if err != nil {
		t.Fatal(err)
	}

	scoped, err := reader.ExternalClicks(ctx, radarport.ExternalClickQuery{RadarID: radar.RadarID(radarID), CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customerOne)}, FilterApplied: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Items) != 1 || scoped.Items[0].SessionID != resolved || scoped.Items[0].EventID != firstResolvedEvent || scoped.Items[0].CustomerID == nil || *scoped.Items[0].CustomerID != customerdomain.CustomerID(customerOne) || scoped.Items[0].OpenStage != radarport.EventRedirected || !scoped.Items[0].OpenedAt.Equal(now.Add(-3*time.Minute)) {
		t.Fatalf("scoped=%+v", scoped)
	}

	emptyScope, err := reader.ExternalClicks(ctx, radarport.ExternalClickQuery{RadarID: radar.RadarID(radarID), FilterApplied: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyScope.Items) != 0 {
		t.Fatalf("empty selected scope widened query: %+v", emptyScope)
	}

	broad, err := reader.ExternalClicks(ctx, radarport.ExternalClickQuery{RadarID: radar.RadarID(radarID), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	// The unbound client sees all auditable statuses, ordered by the first
	// successful opening. The newer resolved visit for another Customer must
	// not be hidden simply because this is not a customer-scoped query.
	if len(broad.Items) != 2 || broad.Items[0].SessionID != other || broad.Items[0].CustomerID == nil || *broad.Items[0].CustomerID != customerdomain.CustomerID(customerTwo) || broad.Items[0].AttributionStatus != radarport.AttributionResolved || broad.Items[1].SessionID != conflict || broad.Items[1].CustomerID != nil || broad.Items[1].AttributionStatus != radarport.AttributionConflict || !broad.HasMore {
		t.Fatalf("broad=%+v", broad)
	}

	second, err := reader.ExternalClicks(ctx, radarport.ExternalClickQuery{RadarID: radar.RadarID(radarID), Limit: 2, BeforeOpened: broad.Items[1].OpenedAt, BeforeEventID: broad.Items[1].EventID})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.Items[0].SessionID != pending || second.Items[0].EventID != pendingEvent || second.Items[0].AttributionStatus != radarport.AttributionPending || second.Items[1].SessionID != resolved || second.Items[1].EventID != firstResolvedEvent || second.Items[0].SessionID == anonymous || second.Items[1].SessionID == failed {
		t.Fatalf("second=%+v", second)
	}
}
