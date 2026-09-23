package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	radarstore "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/store"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// TestOpenPlatformV1ReadPortsPostgreSQLJourney composes the V1 read half with
// real, owner-owned PostgreSQL Ports. It proves REST and MCP both resolve the
// same trusted OneID customer, return the Customer projection, and merge all
// four activity streams without Host-side table access.
func TestOpenPlatformV1ReadPortsPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()

	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformV1ReadMigrate(ctx, native); err != nil {
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

	accessRepository := accessstore.NewPostgreSQL()
	machine, err := accessapp.NewMachineService(accessRepository, uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-read", Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	admin := openPlatformV1ReadAdmin(t, ctx, uow, accessRepository)
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:open-read", Value: "union-open-read", Source: "v1-read-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var provision identityport.ProvisionResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var provisionErr error
		provision, provisionErr = oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
		return provisionErr
	}); err != nil {
		t.Fatal(err)
	}
	customerID := provision.CustomerID
	if err = openPlatformV1ReadSeed(ctx, native, customerID, provision.IdentityID, admin.InternalID); err != nil {
		t.Fatal(err)
	}

	ordersRepository, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	payer, beneficiary := int64(customerID), int64(customerID)
	if _, err = orders.Create(ctx, orderport.CreateCommand{Actor: admin.InternalID, IdempotencyKey: "v1-read-order-0001", Input: orderdomain.NewOrderInput{
		Provider: orderdomain.ProviderWeChatPay, SourceSystem: "v1-read", SourceKey: "v1-read-order-0001", MerchantOrderNo: "V1-READ-ORDER-0001",
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 8800, Currency: "CNY"},
		Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "v1-read", ProductName: "V1 read", UnitAmountMinor: 8800, Quantity: 1, LineAmountMinor: 8800}}, RecordOrigin: orderdomain.RecordOriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	surveyRepository, err := surveystore.NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	survey := surveyapp.NewSubmissionService(uow, surveyRepository, cipher)
	radar, err := radarapp.NewQueryService(uow, radarstore.NewPostgres())
	if err != nil {
		t.Fatal(err)
	}
	identities := openPlatformV1ReadIdentities{uow: uow, resolver: oneID, query: identityquery.NewPostgreSQL()}
	archive := archiveapp.Service{ReadEnabled: true, Store: archivestore.NewPostgreSQL(), UOW: uow, Lineage: identityquery.NewPostgreSQL(), StaffDirectory: accessRepository}
	profiles := openPlatformV1ReadProfiles{uow: uow, store: customerstore.NewPostgreSQL()}
	owners := &openPlatformV1ReadOwners{}
	executor, err := newOpenPlatformExecutor(identities, orders, profiles, archive, &openPlatformTimelineStub{}, owners, configuredOpenPlatformScopes("open-read", []string{"wechat-open-platform:open-read"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatal(err)
	}
	watermark := time.Now().UTC().Add(time.Second)
	if page, readErr := archive.CustomerMessages(ctx, archiveport.CustomerQuery{CustomerID: customerID, Limit: 2, Watermark: watermark}); readErr != nil {
		t.Fatalf("archive owner read: %v page=%+v", readErr, page)
	}
	if page, readErr := survey.CustomerHistoryWindow(ctx, surveyport.CustomerHistoryQuery{CustomerID: int64(customerID), Limit: 2, Watermark: watermark}); readErr != nil {
		t.Fatalf("survey owner read: %v page=%+v", readErr, page)
	}
	if page, readErr := radar.CustomerActivities(ctx, radarport.CustomerActivityQuery{CustomerID: customerID, Limit: 2, Watermark: watermark}); readErr != nil {
		t.Fatalf("radar owner read: %v page=%+v", readErr, page)
	}
	if page, readErr := orders.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: int64(customerID), Limit: 2, Watermark: watermark}); readErr != nil {
		t.Fatalf("order owner read: %v page=%+v", readErr, page)
	}
	// The sidebar must read real owner facts, not the sparse generic sync projection.
	businessTimeline := sidebarBusinessTimeline{surveys: survey, orders: orders, radar: radar,
		channels: timelineChannelFixture{businessTimelineFixture{at: watermark.Add(-time.Minute)}}, uow: uow}
	businessPage, businessErr := businessTimeline.CustomerTimeline(ctx, customerID, customerport.PageQuery{Limit: 20, Watermark: watermark})
	if businessErr != nil {
		t.Fatal(businessErr)
	}
	businessTitles := ""
	for _, item := range businessPage.Items {
		businessTitles += item.Title + "|"
	}
	for _, want := range []string{"提交问卷：V1 Read Survey", "创建订单（待支付）：V1 read", "打开雷达内容：V1 Read Radar"} {
		if !strings.Contains(businessTitles, want) {
			t.Fatalf("sidebar missing %q in %q", want, businessTitles)
		}
	}
	if err = executor.BindV1OperationAudit(accessRepository, uow); err != nil {
		t.Fatal(err)
	}
	rateLimiter, err := accessapp.NewMachineRequestRateLimiter(accessRepository, uow, accessapp.MachineRequestRateLimitConfig{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := openplatformhttp.NewHandler(openplatformhttp.Config{MachineAuthentication: machine, RateLimiter: rateLimiter, AdminAuthentication: openPlatformMachineAdmin{}, Management: machine, Operations: executor, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test"})
	if err != nil {
		t.Fatal(err)
	}

	customerScope := accessdomain.OwnerScope{"customer_id": {fmt.Sprint(customerID)}}
	issued, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{ClientID: "v1.read-machine", DisplayName: "V1 read machine", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"platform.capabilities.read", "customer.resolve", "customer.read", "customer.activity.read"}, OwnerScope: customerScope})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, issued.Client.ClientID, issued.Secret, true); err != nil {
		t.Fatal(err)
	}
	token, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: issued.Client.ClientID, ClientSecret: issued.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.60")})
	if err != nil {
		t.Fatal(err)
	}

	capabilities := openPlatformV1ReadRequest(http.MethodGet, "https://crm.example.test/open/v1/capabilities", "", token.AccessToken)
	capabilitiesResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(capabilitiesResponse, capabilities)
	if capabilitiesResponse.Code != http.StatusOK || !strings.Contains(capabilitiesResponse.Body.String(), `"customer.activities.list"`) || strings.Contains(capabilitiesResponse.Body.String(), `"ai.review_plan.create"`) {
		t.Fatalf("capabilities status=%d body=%s", capabilitiesResponse.Code, capabilitiesResponse.Body.String())
	}

	resolve := openPlatformV1ReadRequest(http.MethodPost, "https://crm.example.test/open/v1/customers:resolve", `{"references":[{"kind":"unionid","scope":"wechat-open-platform:open-read","value":"union-open-read"}]}`, token.AccessToken)
	resolveResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(resolveResponse, resolve)
	if resolveResponse.Code != http.StatusOK || !strings.Contains(resolveResponse.Body.String(), `"status":"found"`) || !strings.Contains(resolveResponse.Body.String(), fmt.Sprintf(`"customer_id":%d`, customerID)) {
		t.Fatalf("resolve status=%d body=%s", resolveResponse.Code, resolveResponse.Body.String())
	}

	mcpContext := openPlatformV1ReadRequest(http.MethodPost, "https://crm.example.test/mcp", `{"jsonrpc":"2.0","id":"context-1","method":"tools/call","params":{"name":"get_customer_context","arguments":{"customer_id":`+fmt.Sprint(customerID)+`}}}`, token.AccessToken)
	mcpContext.Header.Set("Content-Type", "application/json")
	mcpContextResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpContextResponse, mcpContext)
	if mcpContextResponse.Code != http.StatusOK || !strings.Contains(mcpContextResponse.Body.String(), `"display_name":"V1 Read Customer"`) {
		t.Fatalf("MCP context status=%d body=%s", mcpContextResponse.Code, mcpContextResponse.Body.String())
	}

	activities := openPlatformV1ReadRequest(http.MethodGet, "https://crm.example.test/open/v1/customers/"+fmt.Sprint(customerID)+"/activities?types=message&types=survey&types=radar&types=order&limit=1", "", token.AccessToken)
	activitiesResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(activitiesResponse, activities)
	if activitiesResponse.Code != http.StatusOK {
		t.Fatalf("REST activities status=%d body=%s", activitiesResponse.Code, activitiesResponse.Body.String())
	}
	var activityPage struct {
		Data struct {
			Items []struct {
				ActivityID string `json:"activity_id"`
				Type       string `json:"type"`
				Source     string `json:"source"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err = json.Unmarshal(activitiesResponse.Body.Bytes(), &activityPage); err != nil || len(activityPage.Data.Items) != 1 || activityPage.Data.NextCursor == "" || activityPage.Data.Items[0].ActivityID == "" || activityPage.Data.Items[0].Type == "" || activityPage.Data.Items[0].Source == "" {
		t.Fatalf("REST activities decode=%v page=%s", err, activitiesResponse.Body.String())
	}
	mcpActivities := openPlatformV1ReadRequest(http.MethodPost, "https://crm.example.test/mcp", `{"jsonrpc":"2.0","id":"activities-2","method":"tools/call","params":{"name":"list_customer_activities","arguments":{"customer_id":`+fmt.Sprint(customerID)+`,"types":["message","survey","radar","order"],"limit":10,"cursor":`+mustJSON(t, activityPage.Data.NextCursor)+`}}}`, token.AccessToken)
	mcpActivities.Header.Set("Content-Type", "application/json")
	mcpActivitiesResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpActivitiesResponse, mcpActivities)
	if mcpActivitiesResponse.Code != http.StatusOK {
		t.Fatalf("MCP activities status=%d body=%s", mcpActivitiesResponse.Code, mcpActivitiesResponse.Body.String())
	}
	allActivities := activitiesResponse.Body.String() + mcpActivitiesResponse.Body.String()
	for _, kind := range []string{"message:", "survey:", "radar:", "order:"} {
		if !strings.Contains(allActivities, kind) {
			t.Fatalf("combined activities missing %s REST=%s MCP=%s", kind, activitiesResponse.Body.String(), mcpActivitiesResponse.Body.String())
		}
	}

	// The cursor includes customer/types/grant bindings: a caller cannot reuse
	// a signed page position to move to another canonical customer or stream.
	cursorReuse := openPlatformV1ReadRequest(http.MethodGet, "https://crm.example.test/open/v1/customers/"+fmt.Sprint(customerID)+"/activities?types=order&cursor="+activityPage.Data.NextCursor, "", token.AccessToken)
	cursorReuseResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(cursorReuseResponse, cursorReuse)
	if cursorReuseResponse.Code != http.StatusBadRequest || !strings.Contains(cursorReuseResponse.Body.String(), `"validation"`) {
		t.Fatalf("cursor reuse status=%d body=%s", cursorReuseResponse.Code, cursorReuseResponse.Body.String())
	}

	// An owner-scoped credential cannot make the Customer or activity readers
	// run when its owner Port is unavailable; this is a dependency error, not a
	// broadened read nor an out-of-scope 404.
	owners.err = errors.New("owner projection unavailable")
	ownerClient, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{ClientID: "v1.owner-machine", DisplayName: "V1 owner machine", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"customer.read"}, OwnerScope: accessdomain.OwnerScope{"owner_userid": {"owner-v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, ownerClient.Client.ClientID, ownerClient.Secret, true); err != nil {
		t.Fatal(err)
	}
	ownerToken, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: ownerClient.Client.ClientID, ClientSecret: ownerClient.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.60")})
	if err != nil {
		t.Fatal(err)
	}
	ownerFailure := openPlatformV1ReadRequest(http.MethodGet, "https://crm.example.test/open/v1/customers/"+fmt.Sprint(customerID), "", ownerToken.AccessToken)
	ownerFailureResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(ownerFailureResponse, ownerFailure)
	if ownerFailureResponse.Code != http.StatusServiceUnavailable || !strings.Contains(ownerFailureResponse.Body.String(), `"dependency_unavailable"`) || owners.calls != 1 {
		t.Fatalf("owner failure status=%d calls=%d body=%s", ownerFailureResponse.Code, owners.calls, ownerFailureResponse.Body.String())
	}
}

func openPlatformV1ReadAdmin(t *testing.T, ctx context.Context, uow platformport.UnitOfWork, repository *accessstore.PostgreSQL) accessdomain.Principal {
	t.Helper()
	hash, err := credential.PasswordHasher{}.Hash("open-platform-v1-read-admin")
	if err != nil {
		t.Fatal(err)
	}
	var user accessdomain.User
	if err = uow.Within(ctx, func(tx context.Context) error {
		var createErr error
		user, createErr = repository.CreateUser(tx, accessdomain.User{Username: "open-platform-v1-read-admin", PasswordHash: hash, DisplayName: "Open Platform V1 Read", Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: user.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
}

func openPlatformV1ReadSeed(ctx context.Context, native *pgxpool.Pool, customerID customerdomain.CustomerID, identityID, staffID int64) error {
	now := time.Now().UTC().Add(-time.Minute)
	if _, err := native.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,updated_at) VALUES($1,'active','V1 Read Customer','CID-v1-read','active','v1-read-fixture',$2)`, customerID, now); err != nil {
		return err
	}
	var wecomIdentityID, syncRunID int64
	if err := native.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:open-read','external-v1-read','verified','wecom-fixture',1,$2) RETURNING id`, customerID, now).Scan(&wecomIdentityID); err != nil {
		return err
	}
	if err := native.QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at)
		VALUES('open-platform-v1-read-completed','manual','succeeded','wecom-corp:open-read',$1) RETURNING id`, now).Scan(&syncRunID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id)
		VALUES($1,'wecom-corp:open-read',$2,'V1 Read Customer','active',$3,$4,$5,'owner-v1-read',$4)`, customerID, wecomIdentityID, bytes.Repeat([]byte{10}, 32), syncRunID, now); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,remark,relationship_status,last_seen_run_id,observed_at,primary_owner_userid)
		VALUES($1,'wecom-corp:open-read','owner-v1-read','已完成首次跟进','active',$2,$3,'owner-v1-read'),($1,'wecom-corp:open-read','follow-v1-read','二次跟进','active',$2,$3,'owner-v1-read')`, customerID, syncRunID, now); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:open-read')`); err != nil {
		return err
	}
	var messageID int64
	if err := native.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,msgtime_ms,occurred_at,content_text,normalized_payload) VALUES('wecom-corp:open-read',1,'v1-read-message','text','private',1,$1,'safe summary','{}') RETURNING id`, now.Add(-time.Hour)).Scan(&messageID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,customer_id_at_ingest,identity_id_at_ingest,resolution_status,resolved_at) VALUES($1,'sender','external_customer','external-read-customer',$2,$3,$4,'found',$5),($1,'recipient','staff','staff-v1-read',$6,NULL,NULL,'not_applicable',NULL)`, messageID, bytes.Repeat([]byte{1}, 32), customerID, identityID, now, bytes.Repeat([]byte{2}, 32)); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `UPDATE message_archive_participants SET staff_user_id=$2 WHERE message_id=$1 AND actor_type='staff'`, messageID, staffID); err != nil {
		return err
	}
	var questionnaireID, versionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('V1 Read Survey','V1 Read Survey','survey','all_in_one','v1-read-survey','published',$1,$1,$2,$2) RETURNING id`, staffID, now).Scan(&questionnaireID); err != nil {
		return err
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','V1 Read Survey',$2,true,$3,$4,$3) RETURNING id`, questionnaireID, bytes.Repeat([]byte{3}, 32), now, staffID).Scan(&versionID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,'v1-read-survey','V1 Read Survey','survey','{}',$6,$6)`, questionnaireID, versionID, customerID, bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 32), now.Add(-2*time.Hour)); err != nil {
		return err
	}
	var radarID, sessionID int64
	if err := native.QueryRow(ctx, `INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at) VALUES('rd_1234567890abcdef','V1 Read Radar','V1 Read Radar','link','https://example.test/v1-read','unionid_required','enabled',$1,$1,$2,$2) RETURNING id`, staffID, now).Scan(&radarID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',$2,$3)`, radarID, staffID, now); err != nil {
		return err
	}
	if err := native.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at) VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`, bytes.Repeat([]byte{6}, 32), radarID, identityID, customerID, bytes.Repeat([]byte{7}, 32), now.Add(time.Hour), now).Scan(&sessionID); err != nil {
		return err
	}
	if _, err := native.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at) VALUES('v1-read-radar-event',$1,1,$2,'content_opened','resolved',$3,$4,$5,$6,$7,$7)`, radarID, sessionID, identityID, customerID, bytes.Repeat([]byte{8}, 32), bytes.Repeat([]byte{9}, 32), now.Add(-3*time.Hour)); err != nil {
		return err
	}
	return nil
}

func openPlatformV1ReadMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0009_customer_activation.sql", "0018_survey.sql", "0038_survey_oauth_phone_vault.sql", "0020_order.sql", "0022_customer_profile_sections.sql", "0024_order_product_version.sql", "0050_radar_core.sql", "0051_radar_sessions_events.sql", "0071_message_archive_core.sql", "0086_wecom_profile_primary_owner.sql", "0096_open_platform.sql", "0098_message_archive_historical_projection.sql", "0103_sidebar_customer_profile_annotations.sql", "0153_wecom_customer_detail_projection.sql"} {
		sql, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return ensureAccessLoginFixtureSchema(ctx, pool)
}

func openPlatformV1ReadRequest(method, target, body, bearer string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.RemoteAddr = "203.0.113.60:443"
	request.TLS = &tlsState
	return request
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

type openPlatformV1ReadIdentities struct {
	uow      platformport.UnitOfWork
	resolver identityport.Resolver
	query    identityquery.PostgreSQL
}

func (adapter openPlatformV1ReadIdentities) Resolve(ctx context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	var result identityport.ResolveResult
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var resolveErr error
		result, resolveErr = adapter.resolver.Resolve(tx, reference)
		return resolveErr
	})
	return result, err
}
func (adapter openPlatformV1ReadIdentities) VerifiedExternalUserID(ctx context.Context, customerID customerdomain.CustomerID, scope string) (string, bool, error) {
	var value string
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		value, found, readErr = adapter.query.VerifiedExternalIdentityValue(tx, customerID, identitydomain.KindWeComExternalUserID, scope)
		return readErr
	})
	return value, found, err
}

type openPlatformV1ReadProfiles struct {
	uow   platformport.UnitOfWork
	store customerstore.PostgreSQL
}

func (adapter openPlatformV1ReadProfiles) ReadSidebarProfile(ctx context.Context, customerID customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	var profile customerport.SidebarProfile
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		profile, readErr = adapter.store.ReadSidebarProfile(tx, customerID)
		return readErr
	})
	return profile, err
}
func (openPlatformV1ReadProfiles) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{}, errors.New("read-only V1 customer profile adapter")
}
func (openPlatformV1ReadProfiles) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{}, errors.New("read-only V1 customer profile adapter")
}

type openPlatformV1ReadOwners struct {
	err   error
	calls int
}

func (reader *openPlatformV1ReadOwners) AudiencePrimaryOwners(_ context.Context, _ []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	reader.calls++
	if reader.err != nil {
		return nil, reader.err
	}
	return []wecomport.AudiencePrimaryOwner{}, nil
}
