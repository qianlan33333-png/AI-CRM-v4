package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestOpenPlatformV1CompositionPostgreSQLJourney applies every production
// migration, composes cmd/aicrm's real HTTP root, and invokes all six current
// V1 operations. The seed only creates trusted owner facts; the machine path
// itself reaches each owner through the actual Composition adapters and Ports.
func TestOpenPlatformV1CompositionPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	const signingKey = "01234567890123456789012345678901"
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://crm.example.test",
		ReleaseSHA:   "open-platform-v1-composition",
		WorkerOwner:  "open-platform-v1-composition",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "open-platform-v1-composition-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "open-read"},
		HXCDashboard: platformconfig.HXCDashboard{UnionIDScope: "wechat-open-platform:secondary"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: signingKey},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
			OAuthOpenPlatformID:  "open-read",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()

	adminUser, bootstrapped, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "open-v1-owner", Password: "open-v1-owner-password", DisplayName: "Open V1 Owner"})
	if err != nil || !bootstrapped {
		t.Fatalf("bootstrap user=%+v created=%t err=%v", adminUser, bootstrapped, err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}

	phoneVault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(dataKey))
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(phoneVault)}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:open-read", Value: "union-composed-v1", Source: "open-platform-v1-composition-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var provision struct {
		CustomerID customerdomain.CustomerID
		IdentityID int64
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		resolved, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
		if provisionErr != nil {
			return provisionErr
		}
		provision.CustomerID, provision.IdentityID = resolved.CustomerID, resolved.IdentityID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, attachErr := oneID.AttachDeclaredPhoneToCustomer(tx, identityport.DeclaredPhoneCommand{CustomerID: provision.CustomerID, Phone: "13800138000", Source: "hxc", SourceEventID: "open-platform-v1-composition-declared-phone", IdempotencyKey: "open-platform-v1-composition-declared-phone"})
		return attachErr
	}); err != nil {
		t.Fatal(err)
	}
	// These are provider-originated facts in the integration fixture. The HTTP
	// journey subsequently reads them only through Identity's composed Port.
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'phone','phone:e164','+8613800138001','verified','provider-fixture',1,CURRENT_TIMESTAMP),($1,'unionid','wechat-open-platform:secondary','union-composed-secondary','verified','provider-fixture',1,CURRENT_TIMESTAMP)`, provision.CustomerID); err != nil {
		t.Fatal(err)
	}
	if err = openPlatformV1ReadSeed(ctx, application.pool.Native(), provision.CustomerID, provision.IdentityID, adminUser.ID); err != nil {
		t.Fatal(err)
	}
	beneficiaryFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:open-read", Value: "union-composed-v1-beneficiary", Source: "open-platform-v1-composition-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var beneficiaryProvision customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		resolved, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, beneficiaryFact)
		if provisionErr != nil {
			return provisionErr
		}
		beneficiaryProvision = resolved.CustomerID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unionOnlyFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:open-read", Value: "union-composed-v1-only", Source: "open-platform-v1-composition-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var unionOnlyCustomerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		resolved, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, unionOnlyFact)
		if provisionErr != nil {
			return provisionErr
		}
		unionOnlyCustomerID = resolved.CustomerID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ordersRepository, err := orderstore.NewPostgreSQL(application.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	payer, beneficiary := int64(provision.CustomerID), int64(provision.CustomerID)
	knownOrder, err := orders.Create(ctx, orderport.CreateCommand{Actor: adminUser.ID, IdempotencyKey: "open-v1-composition-order-0001", Input: orderdomain.NewOrderInput{
		Provider: orderdomain.ProviderWeChatPay, SourceSystem: "open-v1-composition", SourceKey: "open-v1-composition-order-0001", MerchantOrderNo: "OPEN-V1-COMPOSITION-ORDER-0001",
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 8800, Currency: "CNY"},
		Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "open-v1-composition", ProductName: "Open V1 Composition", UnitAmountMinor: 8800, Quantity: 1, LineAmountMinor: 8800}}, RecordOrigin: orderdomain.RecordOriginNative,
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Seed Payment's completed-refund projection for exactly one order. The
	// public HTTP assertions below read it through the composed Payment Port;
	// the separate payer/beneficiary order deliberately has no summary.
	now := time.Now().UTC()
	var paymentID int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,$3,$4,$4,8800,'CNY','paid',1,$5,$5) RETURNING id`, knownOrder.ID, knownOrder.MerchantOrderNo, provision.IdentityID, provision.CustomerID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','open-v1-composition-refund-0001',2200,'composition fixture','completed',1,$2,$2)`, paymentID, now); err != nil {
		t.Fatal(err)
	}
	beneficiary = int64(beneficiaryProvision)
	if _, err = orders.Create(ctx, orderport.CreateCommand{Actor: adminUser.ID, IdempotencyKey: "open-v1-composition-order-payer-beneficiary", Input: orderdomain.NewOrderInput{
		Provider: orderdomain.ProviderWeChatPay, SourceSystem: "open-v1-composition", SourceKey: "open-v1-composition-order-payer-beneficiary", MerchantOrderNo: "OPEN-V1-COMPOSITION-ORDER-PAYER-BENEFICIARY",
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 6600, Currency: "CNY"},
		Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "open-v1-composition-beneficiary", ProductName: "Open V1 Payer and Beneficiary", UnitAmountMinor: 6600, Quantity: 1, LineAmountMinor: 6600}}, RecordOrigin: orderdomain.RecordOriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	machine := mustOpenPlatformV1CompositionMachine(t, uow, signingKey, "open-read")
	client, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "v1.composition-machine", DisplayName: "V1 Composition Machine", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"},
		Capabilities: []string{"platform.capabilities.read", "customer.resolve", "customer.read", "customer.activity.read", "customer.detail.read", "order.read", "identity.read", "questionnaire.read", "radar.click.read", "radar.link.read", "chat.read", "ai.review_plan.create", "operation.read"},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {fmt.Sprint(provision.CustomerID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, client.Client.ClientID, client.Secret, true); err != nil {
		t.Fatal(err)
	}
	readToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "read")
	writeToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "write")
	allToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "read write")

	capabilities := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/capabilities", "", allToken)
	capabilitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(capabilitiesResponse, capabilities)
	if capabilitiesResponse.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", capabilitiesResponse.Code, capabilitiesResponse.Body.String())
	}
	for _, operation := range []string{"platform.capabilities.list", "customer.resolve", "customer.context.get", "customer.activities.list", "customer.detail.get", "questionnaire.submissions.list", "radar.clicks.list", "radar.links.list", "chat.records.list", "ai.review_plan.create", "operation.get"} {
		if !strings.Contains(capabilitiesResponse.Body.String(), `"operation_id":"`+operation+`"`) {
			t.Fatalf("catalog omitted %s: %s", operation, capabilitiesResponse.Body.String())
		}
	}
	if !strings.Contains(capabilitiesResponse.Body.String(), `"operation_id":"order.list"`) || !strings.Contains(capabilitiesResponse.Body.String(), `"operation_id":"order.get"`) {
		t.Fatalf("catalog omitted composed order operations: %s", capabilitiesResponse.Body.String())
	}

	tools := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"catalog","method":"tools/list","params":{}}`, allToken)
	tools.Header.Set("Content-Type", "application/json")
	toolsResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(toolsResponse, tools)
	if toolsResponse.Code != http.StatusOK || !strings.Contains(toolsResponse.Body.String(), `"list_customer_activities"`) || !strings.Contains(toolsResponse.Body.String(), `"create_ai_review_plan"`) {
		t.Fatalf("MCP catalog status=%d body=%s", toolsResponse.Code, toolsResponse.Body.String())
	}

	resolve := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/customers:resolve", `{"references":[{"kind":"unionid","scope":"wechat-open-platform:open-read","value":"union-composed-v1"}]}`, readToken)
	resolve.Header.Set("Content-Type", "application/json")
	resolveResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(resolveResponse, resolve)
	if resolveResponse.Code != http.StatusOK || !strings.Contains(resolveResponse.Body.String(), `"customer_id":`+fmt.Sprint(provision.CustomerID)) || !strings.Contains(resolveResponse.Body.String(), `"status":"found"`) {
		t.Fatalf("resolve status=%d body=%s", resolveResponse.Code, resolveResponse.Body.String())
	}
	phoneResolve := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/customers:resolve", `{"references":[{"kind":"phone","scope":"phone:cn11","value":"13800138000"}]}`, readToken)
	phoneResolve.Header.Set("Content-Type", "application/json")
	phoneResolveResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(phoneResolveResponse, phoneResolve)
	if phoneResolveResponse.Code != http.StatusOK || !strings.Contains(phoneResolveResponse.Body.String(), `"customer_id":`+fmt.Sprint(provision.CustomerID)) || !strings.Contains(phoneResolveResponse.Body.String(), `"status":"found"`) {
		t.Fatalf("declared phone resolve status=%d body=%s", phoneResolveResponse.Code, phoneResolveResponse.Body.String())
	}
	identities := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID)+"/identities?unionid_scope=wechat-open-platform:open-read&unionid_scope=wechat-open-platform:secondary", "", readToken)
	identitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(identitiesResponse, identities)
	for _, expected := range []string{`"canonical_customer_id":"` + fmt.Sprint(provision.CustomerID) + `"`, `"scope":"phone:cn11"`, `"value":"13800138000"`, `"assurance":"declared"`, `"scope":"phone:e164"`, `"value":"+8613800138001"`, `"assurance":"verified"`, `"scope":"wechat-open-platform:open-read"`, `"scope":"wechat-open-platform:secondary"`} {
		if identitiesResponse.Code != http.StatusOK || !strings.Contains(identitiesResponse.Body.String(), expected) {
			t.Fatalf("identity expected=%s status=%d body=%s", expected, identitiesResponse.Code, identitiesResponse.Body.String())
		}
	}
	wrongIdentityScope := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID)+"/identities?unionid_scope=wechat-open-platform:not-granted", "", readToken)
	wrongIdentityScopeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(wrongIdentityScopeResponse, wrongIdentityScope)
	if wrongIdentityScopeResponse.Code != http.StatusForbidden {
		t.Fatalf("wrong identity scope status=%d body=%s", wrongIdentityScopeResponse.Code, wrongIdentityScopeResponse.Body.String())
	}
	unauthorizedIdentity := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(int64(provision.CustomerID)+1)+"/identities", "", readToken)
	unauthorizedIdentityResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(unauthorizedIdentityResponse, unauthorizedIdentity)
	if unauthorizedIdentityResponse.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope identity status=%d body=%s", unauthorizedIdentityResponse.Code, unauthorizedIdentityResponse.Body.String())
	}
	questionnaire := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/questionnaire-submissions?customer_id="+fmt.Sprint(provision.CustomerID)+"&limit=1", "", readToken)
	questionnaireResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(questionnaireResponse, questionnaire)
	if questionnaireResponse.Code != http.StatusOK || !strings.Contains(questionnaireResponse.Body.String(), `"source_system":"aicrm_v3"`) || !strings.Contains(questionnaireResponse.Body.String(), `"identity_status":"resolved"`) {
		t.Fatalf("questionnaire submissions status=%d body=%s", questionnaireResponse.Code, questionnaireResponse.Body.String())
	}
	chatRecords := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/chat-records?customer_id="+fmt.Sprint(provision.CustomerID)+"&staff_user_id="+fmt.Sprint(adminUser.ID)+"&limit=20", "", readToken)
	chatRecordsResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(chatRecordsResponse, chatRecords)
	if chatRecordsResponse.Code != http.StatusOK || !strings.Contains(chatRecordsResponse.Body.String(), `"message_id":"v1-read-message"`) || !strings.Contains(chatRecordsResponse.Body.String(), `"source_system":"message_archive"`) || !strings.Contains(chatRecordsResponse.Body.String(), `"media_availability":"not_applicable"`) {
		t.Fatalf("chat records status=%d body=%s", chatRecordsResponse.Code, chatRecordsResponse.Body.String())
	}
	chatMCP := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"chat","method":"tools/call","params":{"name":"list_chat_records","arguments":{"customer_id":`+fmt.Sprint(provision.CustomerID)+`,"staff_user_id":`+fmt.Sprint(adminUser.ID)+`,"message_id":"v1-read-message","limit":20}}}`, readToken)
	chatMCP.Header.Set("Content-Type", "application/json")
	chatMCPResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(chatMCPResponse, chatMCP)
	if chatMCPResponse.Code != http.StatusOK || !strings.Contains(chatMCPResponse.Body.String(), `"message_id":"v1-read-message"`) {
		t.Fatalf("MCP chat records status=%d body=%s", chatMCPResponse.Code, chatMCPResponse.Body.String())
	}
	radarClicks := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/radar/clicks?customer_id="+fmt.Sprint(provision.CustomerID)+"&limit=100", "", readToken)
	radarClicksResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(radarClicksResponse, radarClicks)
	if radarClicksResponse.Code != http.StatusOK || !strings.Contains(radarClicksResponse.Body.String(), `"source_system":"aicrm_v3"`) || !strings.Contains(radarClicksResponse.Body.String(), `"identity_status":"resolved"`) {
		t.Fatalf("radar clicks status=%d body=%s", radarClicksResponse.Code, radarClicksResponse.Body.String())
	}

	contextRequest := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID), "", readToken)
	contextResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(contextResponse, contextRequest)
	if contextResponse.Code != http.StatusOK || !strings.Contains(contextResponse.Body.String(), `"display_name":"V1 Read Customer"`) {
		t.Fatalf("customer context status=%d body=%s", contextResponse.Code, contextResponse.Body.String())
	}
	customerDetail := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID)+"/detail", "", readToken)
	customerDetailResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(customerDetailResponse, customerDetail)
	for _, expected := range []string{`"business_detail_availability":{"status":"available"}`, `"remark":"已完成首次跟进"`, `"owner":{"user_id":"owner-v1-read"}`, `"follow_users":[{"user_id":"follow-v1-read"},{"user_id":"owner-v1-read"}]`} {
		if customerDetailResponse.Code != http.StatusOK || !strings.Contains(customerDetailResponse.Body.String(), expected) {
			t.Fatalf("customer detail expected=%s status=%d body=%s", expected, customerDetailResponse.Code, customerDetailResponse.Body.String())
		}
	}
	detailMCP := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"customer-detail","method":"tools/call","params":{"name":"get_customer_detail","arguments":{"customer_id":`+fmt.Sprint(provision.CustomerID)+`}}}`, readToken)
	detailMCP.Header.Set("Content-Type", "application/json")
	detailMCPResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(detailMCPResponse, detailMCP)
	if detailMCPResponse.Code != http.StatusOK || !strings.Contains(detailMCPResponse.Body.String(), `"remark":"已完成首次跟进"`) {
		t.Fatalf("MCP customer detail status=%d body=%s", detailMCPResponse.Code, detailMCPResponse.Body.String())
	}
	deniedDetail := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(int64(provision.CustomerID)+1)+"/detail", "", readToken)
	deniedDetailResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(deniedDetailResponse, deniedDetail)
	if deniedDetailResponse.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope customer detail status=%d body=%s", deniedDetailResponse.Code, deniedDetailResponse.Body.String())
	}

	// A dedicated external-read client uses the existing Access grant model:
	// explicit corp scope plus a precise read-only capability list. It must not
	// acquire any AI/write operation and disabling it must revoke issued JWTs.
	readonlyClient, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "v1.composition-readonly", DisplayName: "V1 Composition Read Only", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read"},
		Capabilities: []string{"platform.capabilities.read", "customer.resolve", "customer.read", "customer.detail.read", "identity.read", "order.read", "questionnaire.read", "radar.click.read", "radar.link.read", "chat.read"},
		OwnerScope:   accessdomain.OwnerScope{"corp_id": {"open-read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, readonlyClient.Client.ClientID, readonlyClient.Secret, true); err != nil {
		t.Fatal(err)
	}
	readonlyToken := openPlatformV1CompositionOAuthToken(t, application.handler, readonlyClient.Client.ClientID, readonlyClient.Secret, "read")
	for _, path := range []string{"/open/v1/customers/" + fmt.Sprint(provision.CustomerID) + "/detail", "/open/v1/customers/" + fmt.Sprint(provision.CustomerID) + "/identities"} {
		request := openPlatformV1CompositionRequest(http.MethodGet, path, "", readonlyToken)
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("readonly client path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	// The export status reports Identity's complete customer state. The facts
	// array is a separate caller-requested projection: without a UnionID scope,
	// a UnionID-only customer remains found but cannot leak that UnionID.
	unionOnlyNoScope := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(unionOnlyCustomerID)+"/identities", "", readonlyToken)
	unionOnlyNoScopeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(unionOnlyNoScopeResponse, unionOnlyNoScope)
	var unionOnlyNoScopeBody struct {
		Data struct {
			Status     string `json:"status"`
			Identities []any  `json:"identities"`
		} `json:"data"`
	}
	if err = json.Unmarshal(unionOnlyNoScopeResponse.Body.Bytes(), &unionOnlyNoScopeBody); err != nil || unionOnlyNoScopeResponse.Code != http.StatusOK || unionOnlyNoScopeBody.Data.Status != "found" || len(unionOnlyNoScopeBody.Data.Identities) != 0 || strings.Contains(unionOnlyNoScopeResponse.Body.String(), "union-composed-v1-only") {
		t.Fatalf("union-only identity without requested scope status=%d err=%v body=%s", unionOnlyNoScopeResponse.Code, err, unionOnlyNoScopeResponse.Body.String())
	}
	unionOnlyRequested := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(unionOnlyCustomerID)+"/identities?unionid_scope=wechat-open-platform:open-read", "", readonlyToken)
	unionOnlyRequestedResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(unionOnlyRequestedResponse, unionOnlyRequested)
	if unionOnlyRequestedResponse.Code != http.StatusOK || !strings.Contains(unionOnlyRequestedResponse.Body.String(), `"value":"union-composed-v1-only"`) {
		t.Fatalf("union-only identity with requested scope status=%d body=%s", unionOnlyRequestedResponse.Code, unionOnlyRequestedResponse.Body.String())
	}
	readonlyRadarLinks := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/radar/links?limit=100", "", readonlyToken)
	readonlyRadarLinksResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(readonlyRadarLinksResponse, readonlyRadarLinks)
	if readonlyRadarLinksResponse.Code != http.StatusOK || !strings.Contains(readonlyRadarLinksResponse.Body.String(), `"source_system":"aicrm_v3"`) {
		t.Fatalf("readonly radar links status=%d body=%s", readonlyRadarLinksResponse.Code, readonlyRadarLinksResponse.Body.String())
	}
	readonlyWrite := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/ai/review-plans", `{}`, readonlyToken)
	readonlyWrite.Header.Set("Content-Type", "application/json")
	readonlyWrite.Header.Set("Idempotency-Key", "readonly-write-denied")
	readonlyWriteResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(readonlyWriteResponse, readonlyWrite)
	if readonlyWriteResponse.Code != http.StatusForbidden {
		t.Fatalf("readonly client write status=%d body=%s", readonlyWriteResponse.Code, readonlyWriteResponse.Body.String())
	}
	if _, err = machine.SetEnabled(ctx, admin, readonlyClient.Client.ClientID, false); err != nil {
		t.Fatal(err)
	}
	revokedRead := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/capabilities", "", readonlyToken)
	revokedReadResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(revokedReadResponse, revokedRead)
	if revokedReadResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked readonly token status=%d body=%s", revokedReadResponse.Code, revokedReadResponse.Body.String())
	}

	activities := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID)+"/activities?types=message&types=survey&types=radar&types=order&limit=1", "", readToken)
	activitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(activitiesResponse, activities)
	var firstPage struct {
		Data struct {
			Items []struct {
				ActivityID string `json:"activity_id"`
				Type       string `json:"type"`
				Source     string `json:"source"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err = json.Unmarshal(activitiesResponse.Body.Bytes(), &firstPage); err != nil || activitiesResponse.Code != http.StatusOK || len(firstPage.Data.Items) != 1 || firstPage.Data.Items[0].ActivityID == "" || firstPage.Data.Items[0].Source == "" || firstPage.Data.NextCursor == "" {
		t.Fatalf("activities status=%d err=%v body=%s", activitiesResponse.Code, err, activitiesResponse.Body.String())
	}
	mcpActivities := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"activities","method":"tools/call","params":{"name":"list_customer_activities","arguments":{"customer_id":`+fmt.Sprint(provision.CustomerID)+`,"types":["message","survey","radar","order"],"limit":100,"cursor":`+mustJSON(t, firstPage.Data.NextCursor)+`}}}`, readToken)
	mcpActivities.Header.Set("Content-Type", "application/json")
	mcpActivitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(mcpActivitiesResponse, mcpActivities)
	allActivities := activitiesResponse.Body.String() + mcpActivitiesResponse.Body.String()
	if mcpActivitiesResponse.Code != http.StatusOK {
		t.Fatalf("MCP activities status=%d body=%s", mcpActivitiesResponse.Code, mcpActivitiesResponse.Body.String())
	}
	for _, kind := range []string{"message:", "survey:", "radar:", "order:"} {
		if !strings.Contains(allActivities, kind) {
			t.Fatalf("activities missing %s REST=%s MCP=%s", kind, activitiesResponse.Body.String(), mcpActivitiesResponse.Body.String())
		}
	}

	// Create a real owner-owned page boundary. The HTTP API must ask the
	// Order Port for a look-ahead row, then advance with its signed cursor;
	// this catches a merely-composed route that never reads PostgreSQL.
	for index := 2; index <= 100; index++ {
		key := fmt.Sprintf("open-v1-composition-order-%04d", index)
		if _, err = orders.Create(ctx, orderport.CreateCommand{Actor: adminUser.ID, IdempotencyKey: key, Input: orderdomain.NewOrderInput{
			Provider: orderdomain.ProviderWeChatPay, SourceSystem: "open-v1-composition", SourceKey: key, MerchantOrderNo: strings.ToUpper(key),
			PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 8800, Currency: "CNY"},
			Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "open-v1-composition", ProductName: "Open V1 Composition", UnitAmountMinor: 8800, Quantity: 1, LineAmountMinor: 8800}}, RecordOrigin: orderdomain.RecordOriginNative,
		}}); err != nil {
			t.Fatalf("create order %d: %v", index, err)
		}
	}
	type orderItem struct {
		OrderID            string `json:"order_id"`
		CustomerID         string `json:"customer_id"`
		AmountYuan         string `json:"amount_yuan"`
		RefundStatus       string `json:"refund_status"`
		RefundAmountStatus string `json:"refund_amount_status"`
		RefundedMinor      int64  `json:"refunded_minor"`
		IsRefunded         bool   `json:"is_refunded"`
	}
	type orderPage struct {
		Data struct {
			Items      []orderItem `json:"items"`
			NextCursor string      `json:"next_cursor"`
		} `json:"data"`
	}
	list := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders?customer_id="+fmt.Sprint(provision.CustomerID)+"&limit=100", "", readToken)
	listResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(listResponse, list)
	var firstOrders orderPage
	if err = json.Unmarshal(listResponse.Body.Bytes(), &firstOrders); err != nil || listResponse.Code != http.StatusOK || len(firstOrders.Data.Items) != 100 || firstOrders.Data.NextCursor == "" {
		t.Fatalf("orders first page status=%d err=%v body=%s", listResponse.Code, err, listResponse.Body.String())
	}
	for _, item := range firstOrders.Data.Items {
		if item.OrderID == "" || item.CustomerID != fmt.Sprint(provision.CustomerID) || (item.AmountYuan != "88.00" && item.AmountYuan != "66.00") {
			t.Fatalf("orders first page item=%+v", item)
		}
	}
	payerBeneficiaryLookup := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders?source_system=open-v1-composition&source_record_id=open-v1-composition-order-payer-beneficiary", "", readToken)
	payerBeneficiaryResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(payerBeneficiaryResponse, payerBeneficiaryLookup)
	if payerBeneficiaryResponse.Code != http.StatusOK || !strings.Contains(payerBeneficiaryResponse.Body.String(), `"customer_id":"`+fmt.Sprint(provision.CustomerID)+`"`) || !strings.Contains(payerBeneficiaryResponse.Body.String(), `"payer_customer_id":"`+fmt.Sprint(provision.CustomerID)+`"`) || !strings.Contains(payerBeneficiaryResponse.Body.String(), `"beneficiary_customer_id":"`+fmt.Sprint(beneficiaryProvision)+`"`) || !strings.Contains(payerBeneficiaryResponse.Body.String(), `"refund_status":"unavailable"`) || !strings.Contains(payerBeneficiaryResponse.Body.String(), `"refund_amount_status":"unavailable"`) {
		t.Fatalf("orders payer/beneficiary projection status=%d body=%s", payerBeneficiaryResponse.Code, payerBeneficiaryResponse.Body.String())
	}
	sourceLookup := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders?source_system=open-v1-composition&source_record_id=open-v1-composition-order-0001", "", readToken)
	sourceLookupResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(sourceLookupResponse, sourceLookup)
	var sourceOrders orderPage
	if err = json.Unmarshal(sourceLookupResponse.Body.Bytes(), &sourceOrders); err != nil || sourceLookupResponse.Code != http.StatusOK || len(sourceOrders.Data.Items) != 1 || sourceOrders.Data.Items[0].OrderID != fmt.Sprint(knownOrder.ID) || sourceOrders.Data.Items[0].RefundStatus != "known" || sourceOrders.Data.Items[0].RefundAmountStatus != "known" || sourceOrders.Data.Items[0].RefundedMinor != 2200 || !sourceOrders.Data.Items[0].IsRefunded {
		t.Fatalf("orders source lookup status=%d err=%v body=%s", sourceLookupResponse.Code, err, sourceLookupResponse.Body.String())
	}
	second := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders?customer_id="+fmt.Sprint(provision.CustomerID)+"&limit=100&cursor="+firstOrders.Data.NextCursor, "", readToken)
	secondResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(secondResponse, second)
	var secondOrders orderPage
	if err = json.Unmarshal(secondResponse.Body.Bytes(), &secondOrders); err != nil || secondResponse.Code != http.StatusOK || len(secondOrders.Data.Items) != 1 || secondOrders.Data.NextCursor != "" {
		t.Fatalf("orders second page status=%d err=%v body=%s", secondResponse.Code, err, secondResponse.Body.String())
	}
	detail := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders/"+fmt.Sprint(knownOrder.ID), "", readToken)
	detailResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(detailResponse, detail)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"amount_yuan":"88.00"`) || !strings.Contains(detailResponse.Body.String(), `"line_no":1`) || !strings.Contains(detailResponse.Body.String(), `"product_code":"open-v1-composition"`) || !strings.Contains(detailResponse.Body.String(), `"refund_status":"known"`) || !strings.Contains(detailResponse.Body.String(), `"refund_amount_status":"known"`) || !strings.Contains(detailResponse.Body.String(), `"refunded_minor":2200`) || strings.Contains(detailResponse.Body.String(), `"LineNo"`) || strings.Contains(detailResponse.Body.String(), `"RefundID"`) {
		t.Fatalf("order detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	missing := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders/999999999", "", readToken)
	missingResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing order status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
	deniedCustomer := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders?customer_id="+fmt.Sprint(int64(provision.CustomerID)+1), "", readToken)
	deniedCustomerResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(deniedCustomerResponse, deniedCustomer)
	if deniedCustomerResponse.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope order list status=%d body=%s", deniedCustomerResponse.Code, deniedCustomerResponse.Body.String())
	}
	noOrderClient, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{ClientID: "v1.composition-no-orders", DisplayName: "No order scope", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"customer.read"}, OwnerScope: accessdomain.OwnerScope{"customer_id": {fmt.Sprint(provision.CustomerID)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, noOrderClient.Client.ClientID, noOrderClient.Secret, true); err != nil {
		t.Fatal(err)
	}
	noOrderToken := openPlatformV1CompositionOAuthToken(t, application.handler, noOrderClient.Client.ClientID, noOrderClient.Secret, "read")
	denied := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/orders", "", noOrderToken)
	deniedResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("missing order capability status=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}

	planBody, err := json.Marshal(map[string]any{
		"name": "Composed V1 review plan", "source_kind": "open_platform", "source_digest": effectport.Hash("open-v1-composition-plan"),
		"recipients": []any{map[string]any{"customer_id": provision.CustomerID, "staff_id": adminUser.ID, "content": []any{map[string]any{"kind": "text", "text": "review composed plan"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	create := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/ai/review-plans", string(planBody), writeToken)
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "open-v1-composition-plan-0001")
	createResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(createResponse, create)
	var created struct {
		Data struct {
			OperationID string `json:"operation_id"`
			ReviewState string `json:"review_state"`
		} `json:"data"`
	}
	if err = json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil || createResponse.Code != http.StatusCreated || created.Data.OperationID == "" || created.Data.ReviewState != "pending_review" {
		t.Fatalf("create review plan status=%d err=%v body=%s", createResponse.Code, err, createResponse.Body.String())
	}
	status := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"operation","method":"tools/call","params":{"name":"get_operation_status","arguments":{"operation_id":`+mustJSON(t, created.Data.OperationID)+`}}}`, readToken)
	status.Header.Set("Content-Type", "application/json")
	statusResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(statusResponse, status)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"review_state":"pending_review"`) || !strings.Contains(statusResponse.Body.String(), `"operation_state":"pending_review"`) {
		t.Fatalf("operation status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	coreAudienceOAuthJourney(t, ctx, application, machine, admin, int64(provision.CustomerID))

}

func mustOpenPlatformV1CompositionMachine(t *testing.T, uow *platformpostgres.UnitOfWork, signingKey, corpID string) *accessapp.MachineService {
	t.Helper()
	machine, err := accessapp.NewMachineService(accessstore.NewPostgreSQL(), uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte(signingKey), CorpID: corpID})
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func openPlatformV1CompositionOAuthToken(t *testing.T, handler http.Handler, clientID, secret, scope string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.test/oauth/token", strings.NewReader("grant_type=client_credentials&audience=external_integration&scope="+strings.ReplaceAll(scope, " ", "+")))
	request.TLS = &tlsState
	request.RemoteAddr = "203.0.113.101:443"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(clientID, secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var issued struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil || response.Code != http.StatusOK || issued.AccessToken == "" || issued.Scope != scope {
		t.Fatalf("oauth scope=%s status=%d err=%v body=%s", scope, response.Code, err, response.Body.String())
	}
	return issued.AccessToken
}

func openPlatformV1CompositionRequest(method, path, body, bearer string) *http.Request {
	request := httptest.NewRequest(method, "https://crm.example.test"+path, strings.NewReader(body))
	request.TLS = &tlsState
	request.RemoteAddr = "203.0.113.101:443"
	request.Header.Set("Authorization", "Bearer "+bearer)
	return request
}
