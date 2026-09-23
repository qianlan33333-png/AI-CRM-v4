package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/http"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type listStatisticsSecurity struct{}

func (listStatisticsSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (listStatisticsSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7}, nil
}

type listStatisticsPublic struct{}

func (listStatisticsPublic) Open(context.Context, radar.PublicCode, string) (radarport.PublicAccess, error) {
	return radarport.PublicAccess{}, radarport.ErrUnavailable
}
func (listStatisticsPublic) CompleteOAuth(context.Context, string, string) (string, string, error) {
	return "", "", radarport.ErrUnavailable
}
func (listStatisticsPublic) Content(context.Context, radar.PublicCode, string) (radarport.Content, error) {
	return radarport.Content{}, radarport.ErrUnavailable
}
func (listStatisticsPublic) Record(context.Context, radar.PublicCode, string, radarport.EventStage, string) (radarport.EventProjection, bool, error) {
	return radarport.EventProjection{}, false, radarport.ErrUnavailable
}

func TestPostgreSQLAudienceFirstClicksKeepsFirstResolvedAttribution(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	var customerID, identityID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:test','radar-audience-customer','verified','test-fixture',1,$2)
		RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	insertRadar := func(code string) int64 {
		t.Helper()
		var radarID int64
		if err := native.QueryRow(ctx, `
			INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
			VALUES($1,'Audience radar','Audience radar','link','https://example.test/radar','unionid_required','enabled',1,1,$2,$2)
			RETURNING id`, code, now).Scan(&radarID); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
			t.Fatal(err)
		}
		return radarID
	}
	radarOneID := insertRadar("rd_1234567890abcdef")
	radarTwoID := insertRadar("rd_fedcba0987654321")

	nextDigest := byte(1)
	insertSession := func(radarID int64, attribution string) int64 {
		t.Helper()
		digest := make([]byte, 32)
		digest[0] = nextDigest
		nextDigest++
		var sessionID int64
		if attribution == "resolved" {
			evidence := make([]byte, 32)
			evidence[0] = nextDigest
			nextDigest++
			if err := native.QueryRow(ctx, `
				INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
				VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`,
				digest, radarID, identityID, customerID, evidence, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
				t.Fatal(err)
			}
			return sessionID
		}
		if err := native.QueryRow(ctx, `
			INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at)
			VALUES($1,$2,1,$3,$4,$5) RETURNING id`, digest, radarID, attribution, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		return sessionID
	}
	insertEvent := func(radarID, sessionID int64, attribution, stage string, occurredAt time.Time) int64 {
		t.Helper()
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = nextDigest, nextDigest+1
		receiptID := "audience-radar-receipt-" + hex.EncodeToString([]byte{nextDigest})
		nextDigest += 2
		var identity, customer any
		if attribution == "resolved" {
			identity, customer = identityID, customerID
		}
		var eventID int64
		if err := native.QueryRow(ctx, `
			INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
			VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			receiptID, radarID, sessionID, stage, attribution, identity, customer, keyDigest, payloadDigest, occurredAt, now).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		return eventID
	}

	firstResolved := insertSession(radarOneID, "resolved")
	firstEventID := insertEvent(radarOneID, firstResolved, "resolved", "oauth_verified", now.Add(-48*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "resolved"), "resolved", "identity_resolved", now.Add(-48*time.Hour))
	insertEvent(radarOneID, firstResolved, "resolved", "content_opened", now.Add(-24*time.Hour))
	secondRadar := insertSession(radarTwoID, "resolved")
	insertEvent(radarTwoID, secondRadar, "resolved", "identity_resolved", now.Add(-36*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "anonymous"), "anonymous", "content_opened", now.Add(-60*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "pending"), "pending", "oauth_verified", now.Add(-60*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "conflict"), "conflict", "content_opened", now.Add(-60*time.Hour))
	insertEvent(radarTwoID, insertSession(radarTwoID, "resolved"), "resolved", "oauth_verified", now.Add(time.Hour))

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	var facts []radarport.AudienceFirstClick
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		facts, readErr = NewPostgres().AudienceFirstClicks(txCtx, now)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts=%+v", facts)
	}
	if int64(facts[0].CustomerID) != customerID || facts[0].RadarID != radarOneID || facts[0].FirstClickEventID != firstEventID || !facts[0].FirstClickedAt.Equal(now.Add(-48*time.Hour)) || facts[0].OwnerUserID != "" {
		t.Fatalf("first radar fact=%+v", facts[0])
	}
	if int64(facts[1].CustomerID) != customerID || facts[1].RadarID != radarTwoID || !facts[1].FirstClickedAt.Equal(now.Add(-36*time.Hour)) {
		t.Fatalf("second radar fact=%+v", facts[1])
	}
}

func radarIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Radar PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_radar_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	identifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	var native *pgxpool.Pool
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if native != nil {
				native.Close()
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if _, dropErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); dropErr != nil {
				t.Errorf("drop isolated Radar PostgreSQL schema %s: %v", schemaName, dropErr)
			}
			admin.Close(cleanupCtx)
		})
	}
	// Register cleanup before parsing a pool or applying migrations, so every
	// post-CREATE failure owns and removes only this random test schema.
	t.Cleanup(cleanup)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	native, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Radar integration test")
	}
	for _, migrationName := range []string{"0002_identity.sql", "0050_radar_core.sql", "0051_radar_sessions_events.sql", "0052_radar_legacy_import.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	return native, cleanup
}

func TestPostgreSQLListBatchesPageStatisticsAndKeepsNoViewLastNull(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)

	var customerID, identityID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:list-stats','radar-list-stats','verified','test-fixture',1,$2)
		RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	insertLink := func(code, title string, updatedAt time.Time) int64 {
		t.Helper()
		var id int64
		if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
			VALUES($1,$2,$2,'link','https://example.com/radar','unionid_required','enabled',1,1,$3,$3) RETURNING id`, code, title, updatedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, id, updatedAt); err != nil {
			t.Fatal(err)
		}
		return id
	}
	measuredID := insertLink("rd_1111111111111111", "Measured", now)
	emptyID := insertLink("rd_2222222222222222", "No events", now.Add(-time.Minute))

	next := byte(1)
	digest := func() []byte {
		value := make([]byte, 32)
		value[0] = next
		next++
		return value
	}
	newSession := func(attribution string) int64 {
		t.Helper()
		var id int64
		sessionDigest := digest()
		if attribution == "resolved" {
			evidence := digest()
			if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
				VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`, sessionDigest, measuredID, identityID, customerID, evidence, now.Add(time.Hour), now).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at)
			VALUES($1,$2,1,$3,$4,$5) RETURNING id`, sessionDigest, measuredID, attribution, now.Add(time.Hour), now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	receipt := 0
	insertEvent := func(sessionID int64, attribution, stage string, occurredAt time.Time) {
		t.Helper()
		receipt++
		keyDigest, payloadDigest := digest(), digest()
		var identity, customer any
		if attribution == "resolved" {
			identity, customer = identityID, customerID
		}
		if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
			VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, fmt.Sprintf("radar-list-stats-%d", receipt), measuredID, sessionID, stage, attribution, identity, customer, keyDigest, payloadDigest, occurredAt, now); err != nil {
			t.Fatal(err)
		}
	}
	anonymousLanding := newSession("anonymous")
	resolvedFirst := newSession("resolved")
	resolvedSecond := newSession("resolved")
	viewAnonymous := newSession("anonymous")
	insertEvent(anonymousLanding, "anonymous", "landing", now.Add(-5*time.Hour))
	insertEvent(resolvedFirst, "resolved", "landing", now.Add(-4*time.Hour))
	insertEvent(resolvedSecond, "resolved", "landing", now.Add(-3*time.Hour))
	insertEvent(resolvedFirst, "resolved", "content_opened", now.Add(-2*time.Hour))
	insertEvent(resolvedSecond, "resolved", "redirected", now.Add(-time.Hour))
	insertEvent(viewAnonymous, "anonymous", "pdf_opened", now.Add(time.Hour))
	insertEvent(viewAnonymous, "anonymous", "oauth_verified", now.Add(2*time.Hour))

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	var page radarport.LinkPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = NewPostgres().List(tx, radarport.ListQuery{Limit: 20})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("page=%+v", page)
	}
	items := make(map[int64]radarport.LinkSummary, len(page.Items))
	for _, item := range page.Items {
		items[int64(item.Link.ID)] = item
	}
	measured, ok := items[measuredID]
	if !ok || measured.StatisticsStatus != radarport.LinkStatisticsReady || measured.TotalLandings != 3 || measured.AuthorizedUsers != 1 || measured.AuthorizedViews != 2 || measured.ViewCount != 3 || measured.LastViewedAt == nil || !measured.LastViewedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("measured=%+v", measured)
	}
	empty, ok := items[emptyID]
	if !ok || empty.StatisticsStatus != radarport.LinkStatisticsReady || empty.TotalLandings != 0 || empty.AuthorizedUsers != 0 || empty.AuthorizedViews != 0 || empty.ViewCount != 0 || empty.LastViewedAt != nil {
		t.Fatalf("empty=%+v", empty)
	}

	// A real aggregate failure must roll back only its savepoint. The HTTP
	// request still commits the enclosing read transaction and exposes null,
	// unavailable statistics instead of synthetic zeroes or a stale timestamp.
	if _, err = native.Exec(ctx, `DROP TABLE radar_events`); err != nil {
		t.Fatal(err)
	}
	manager, err := radarapp.NewService(uow, NewPostgres(), NewPostgres())
	if err != nil {
		t.Fatal(err)
	}
	query, err := radarapp.NewQueryService(uow, NewPostgres())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := radarhttp.NewHandler(manager, query, listStatisticsPublic{}, listStatisticsSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("statistics degradation status=%d body=%s", response.Code, response.Body.String())
	}
	var degraded struct {
		Items []struct {
			StatisticsStatus string  `json:"statistics_status"`
			TotalLandings    *int64  `json:"total_landings"`
			AuthorizedUsers  *int64  `json:"authorized_users"`
			ViewCount        *int64  `json:"view_count"`
			LastViewedAt     *string `json:"last_viewed_at"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &degraded); err != nil {
		t.Fatal(err)
	}
	if len(degraded.Items) != 2 {
		t.Fatalf("degraded=%+v", degraded)
	}
	for _, item := range degraded.Items {
		if item.StatisticsStatus != "unavailable" || item.TotalLandings != nil || item.AuthorizedUsers != nil || item.ViewCount != nil || item.LastViewedAt != nil {
			t.Fatalf("statistics failure leaked synthetic values: %+v", item)
		}
	}
}

func TestPostgreSQLListPagesServerFiltersAndEscapesURLSearch(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	ids := make(map[int]int64, 21)
	for index := 1; index <= 21; index++ {
		name := fmt.Sprintf("Page %02d name", index)
		title := fmt.Sprintf("Page %02d title", index)
		destination := fmt.Sprintf("https://example.com/radar/%02d", index)
		contentType := "link"
		var mediaID any
		status := "enabled"
		switch index {
		case 1:
			name = "name needle"
		case 2:
			title = "title needle"
		case 3:
			destination = "https://example.com/url-needle"
		case 4:
			name = `escaped %_\ needle`
		case 5:
			contentType, destination, mediaID = "image", "", int64(8)
		case 6:
			status = "draft"
		case 7:
			status = "disabled"
		}
		publicCode := radar.PublicCode(fmt.Sprintf("rd_page_%016d", index))
		content := radar.Content{Type: radar.ContentType(contentType), DestinationURL: destination}
		if contentType == "image" {
			content.DestinationURL = ""
			content.MediaID = 8
		}
		updatedAt := now.Add(time.Duration(index) * time.Second)
		if err := (radar.Link{ID: 1, PublicCode: publicCode, Name: name, Title: title, Content: content, AuthPolicy: radar.AuthPolicyUnionIDRequired, Status: radar.Status(status), Version: 1, CreatedBy: 1, UpdatedBy: 1, CreatedAt: updatedAt, UpdatedAt: updatedAt}).Validate(); err != nil {
			t.Fatalf("fixture link %d violates Radar contract before SQL insert: %v", index, err)
		}
		var id int64
		if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,media_id,auth_policy,status,created_by,updated_by,created_at,updated_at)
			VALUES($1,$2,$3,$4,NULLIF($5,''),$6,'unionid_required',$7,1,1,$8,$8) RETURNING id`,
			publicCode, name, title, contentType, destination, mediaID, status, updatedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[index] = id
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	read := func(query radarport.ListQuery) radarport.LinkPage {
		t.Helper()
		var page radarport.LinkPage
		if err := uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			page, readErr = NewPostgres().List(tx, query)
			return readErr
		}); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := read(radarport.ListQuery{Limit: 20, Offset: 0})
	if first.Total != 21 || len(first.Items) != 20 || !first.HasMore || first.Offset != 0 || first.Limit != 20 || first.Items[0].Link.ID != radar.RadarID(ids[21]) || first.Items[19].Link.ID != radar.RadarID(ids[2]) {
		t.Fatalf("first page=%+v", first)
	}
	second := read(radarport.ListQuery{Limit: 20, Offset: 20})
	if second.Total != 21 || len(second.Items) != 1 || second.HasMore || second.Offset != 20 || second.Items[0].Link.ID != radar.RadarID(ids[1]) {
		t.Fatalf("second page=%+v", second)
	}
	for _, test := range []struct {
		name  string
		query radarport.ListQuery
		id    int64
	}{
		{name: "name", query: radarport.ListQuery{Search: "name needle", Limit: 20}, id: ids[1]},
		{name: "title", query: radarport.ListQuery{Search: "title needle", Limit: 20}, id: ids[2]},
		{name: "destination url", query: radarport.ListQuery{Search: "url-needle", Limit: 20}, id: ids[3]},
		{name: "escaped wildcard", query: radarport.ListQuery{Search: `%_\`, Limit: 20}, id: ids[4]},
		{name: "content type", query: radarport.ListQuery{ContentType: radar.ContentTypeImage, Limit: 20}, id: ids[5]},
		{name: "draft", query: radarport.ListQuery{Status: radar.StatusDraft, Limit: 20}, id: ids[6]},
		{name: "disabled", query: radarport.ListQuery{Status: radar.StatusDisabled, Limit: 20}, id: ids[7]},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := read(test.query)
			if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Link.ID != radar.RadarID(test.id) {
				t.Fatalf("query=%+v page=%+v", test.query, page)
			}
		})
	}
}

func TestPostgreSQLExternalLinkMappingsRetainDisabledAndUseDescendingKeyset(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	for _, link := range []struct {
		code, title, status string
	}{
		{"rd_1111111111111111", "Enabled mapping", "enabled"},
		{"rd_2222222222222222", "Disabled mapping", "disabled"},
		{"rd_3333333333333333", "Draft mapping", "draft"},
	} {
		if _, err := native.Exec(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,'link','https://example.test/external','anonymous',$4,1,1,$5,$5)`, link.code, link.title, link.title, link.status, now); err != nil {
			t.Fatal(err)
		}
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgres()
	var first radarport.ExternalLinkMappingPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		first, readErr = store.ExternalLinkMappings(tx, radarport.ExternalLinkMappingQuery{Limit: 2})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || !first.HasMore || len(first.Items) != 2 || first.Items[0].RadarID != 3 || first.Items[0].RadarCode != "rd_3333333333333333" || first.Items[0].Status != radar.StatusDraft || first.Items[1].RadarID != 2 || first.Items[1].Title != "Disabled mapping" || first.Items[1].Status != radar.StatusDisabled {
		t.Fatalf("first=%+v", first)
	}
	var second radarport.ExternalLinkMappingPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		second, readErr = store.ExternalLinkMappings(tx, radarport.ExternalLinkMappingQuery{BeforeRadarID: first.Items[1].RadarID, Limit: 2})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if second.Total != 3 || second.HasMore || len(second.Items) != 1 || second.Items[0].RadarID != 1 {
		t.Fatalf("second=%+v", second)
	}
	var filtered radarport.ExternalLinkMappingPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		filtered, readErr = store.ExternalLinkMappings(tx, radarport.ExternalLinkMappingQuery{RadarID: 2, RadarCode: "rd_2222222222222222", Limit: 100})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Title != "Disabled mapping" || filtered.Items[0].Status != radar.StatusDisabled {
		t.Fatalf("filtered=%+v", filtered)
	}
}

func TestPostgreSQLCustomerActivitiesAreCustomerScopedAndUseDescendingKeyset(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	insertCustomer := func(value string) (int64, int64) {
		t.Helper()
		var customerID, identityID int64
		if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if err := native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'wecom_external_userid','wecom-corp:activity',$2,'verified','radar-activity-test',1,$3) RETURNING id`, customerID, value, now).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		return customerID, identityID
	}
	customerID, identityID := insertCustomer("customer-activity")
	otherCustomerID, otherIdentityID := insertCustomer("other-activity")
	var radarID int64
	if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
		VALUES('rd_abcdefabcdefabcd','Activity radar','Activity radar','link','https://example.test/activity','unionid_required','enabled',1,1,$1,$1) RETURNING id`, now).Scan(&radarID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}

	next := byte(1)
	appendEvent := func(customer, identity int64, stage string, occurredAt time.Time) int64 {
		t.Helper()
		sessionDigest, evidence, key, payload := make([]byte, 32), make([]byte, 32), make([]byte, 32), make([]byte, 32)
		sessionDigest[0], evidence[0], key[0], payload[0] = next, next+1, next+2, next+3
		next += 4
		var sessionID int64
		if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
			VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`, sessionDigest, radarID, identity, customer, evidence, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		var eventID int64
		if err := native.QueryRow(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
			VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7,$8,$9,$10) RETURNING id`, "customer-activity-"+hex.EncodeToString(sessionDigest[:1]), radarID, sessionID, stage, identity, customer, key, payload, occurredAt, now).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		return eventID
	}
	oldID := appendEvent(customerID, identityID, "oauth_verified", now.Add(-2*time.Hour))
	latestID := appendEvent(customerID, identityID, "content_opened", now.Add(-time.Hour))
	_ = appendEvent(customerID, identityID, "redirected", now.Add(time.Hour))
	_ = appendEvent(otherCustomerID, otherIdentityID, "content_opened", now.Add(-30*time.Minute))

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgres()
	var first radarport.CustomerActivityPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		first, readErr = store.CustomerActivities(tx, radarport.CustomerActivityQuery{CustomerID: customerdomain.CustomerID(customerID), Limit: 1, Watermark: now})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].EventID != latestID || first.Items[0].Stage != radarport.EventContentOpened {
		t.Fatalf("first page=%+v", first)
	}
	var second radarport.CustomerActivityPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		second, readErr = store.CustomerActivities(tx, radarport.CustomerActivityQuery{CustomerID: customerdomain.CustomerID(customerID), Limit: 10, Watermark: now, AfterAt: first.Items[0].OccurredAt, AfterID: first.Items[0].EventID})
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].EventID != oldID || second.Items[0].Stage != radarport.EventOAuthVerified {
		t.Fatalf("second page=%+v", second)
	}
	var business radarport.CustomerActivityPage
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		business, e = store.CustomerActivities(tx, radarport.CustomerActivityQuery{BusinessOnly: true, CustomerID: customerdomain.CustomerID(customerID), Limit: 10, Watermark: now})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(business.Items) != 1 || business.Items[0].EventID != latestID || business.Items[0].Title == "" {
		t.Fatalf("business projection=%+v", business)
	}
}
