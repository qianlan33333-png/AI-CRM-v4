package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestOpenPlatformV1AIReviewPlanPostgreSQLJourney is the protocol-level proof
// that the machine caller, Access audit and AI owner rows share one database
// Unit of Work. Its fresh handler/service instances emulate a restart before
// MCP reads the persisted operation state.
func TestOpenPlatformV1AIReviewPlanPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()

	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformV1AIMigrate(ctx, native); err != nil {
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
	machine, err := accessapp.NewMachineService(accessRepository, uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-platform-v1", Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	adminHash, err := credential.PasswordHasher{}.Hash("open-platform-v1-ai-admin")
	if err != nil {
		t.Fatal(err)
	}
	var adminUser accessdomain.User
	if err = uow.Within(ctx, func(tx context.Context) error {
		var createErr error
		adminUser, createErr = accessRepository.CreateUser(tx, accessdomain.User{Username: "open-platform-v1-ai-admin", PasswordHash: adminHash, DisplayName: "Open Platform V1 AI", Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	issuedClient, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "v1.ai-machine", DisplayName: "V1 AI machine", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"},
		Capabilities: []string{"ai.review_plan.create", "operation.read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, issuedClient.Client.ClientID, issuedClient.Secret, true); err != nil {
		t.Fatal(err)
	}
	writeToken, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: issuedClient.Client.ClientID, ClientSecret: issuedClient.Secret, Audience: "external_integration", RequestedScopes: []string{"write"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}
	readToken, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: issuedClient.Client.ClientID, ClientSecret: issuedClient.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}

	audit := &openPlatformV1AIAuditWriter{delegate: accessRepository}
	handler := newOpenPlatformV1AIHandler(t, native, uow, machine, audit)
	requestBody := openPlatformV1AIRequestBody(t, "v1-ai-rest-source")
	create := httptest.NewRequest(http.MethodPost, "https://crm.example.test/open/v1/ai/review-plans", strings.NewReader(requestBody))
	create.Header.Set("Authorization", "Bearer "+writeToken.AccessToken)
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "v1-ai-rest-key-0001")
	create.Header.Set("X-Request-ID", "v1-ai-rest-request")
	create.RemoteAddr = "203.0.113.50:443"
	create.TLS = &tlsState
	createdResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(createdResponse, create)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("REST create status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created struct {
		Data struct {
			OperationID string `json:"operation_id"`
			ReviewState string `json:"review_state"`
		} `json:"data"`
	}
	if err = json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || created.Data.OperationID == "" || created.Data.ReviewState != string(aiassistantport.ReviewPending) {
		t.Fatalf("REST create body=%s err=%v", createdResponse.Body.String(), err)
	}
	assertOpenPlatformV1AICounts(t, native, 1, 1, 1, 1, 1, 1)

	// The REST and MCP transports share AI's client-scoped idempotency receipt.
	// Replaying the same payload from the same machine must return the original
	// operation, while a changed payload with that key is a conflict.
	mcpReplay := httptest.NewRequest(http.MethodPost, "https://crm.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"replay-1","method":"tools/call","params":{"name":"create_ai_review_plan","arguments":`+requestBody+`}}`))
	mcpReplay.Header.Set("Authorization", "Bearer "+writeToken.AccessToken)
	mcpReplay.Header.Set("Content-Type", "application/json")
	mcpReplay.Header.Set("Idempotency-Key", "v1-ai-rest-key-0001")
	mcpReplay.RemoteAddr = "203.0.113.50:443"
	mcpReplay.TLS = &tlsState
	mcpReplayResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpReplayResponse, mcpReplay)
	if mcpReplayResponse.Code != http.StatusOK || !strings.Contains(mcpReplayResponse.Body.String(), `"operation_id":"`+created.Data.OperationID+`"`) || !strings.Contains(mcpReplayResponse.Body.String(), `"replayed":true`) {
		t.Fatalf("MCP replay status=%d body=%s", mcpReplayResponse.Code, mcpReplayResponse.Body.String())
	}
	changedBody := strings.Replace(requestBody, "V1 review plan", "V1 review plan changed", 1)
	mcpConflict := httptest.NewRequest(http.MethodPost, "https://crm.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"replay-conflict","method":"tools/call","params":{"name":"create_ai_review_plan","arguments":`+changedBody+`}}`))
	mcpConflict.Header.Set("Authorization", "Bearer "+writeToken.AccessToken)
	mcpConflict.Header.Set("Content-Type", "application/json")
	mcpConflict.Header.Set("Idempotency-Key", "v1-ai-rest-key-0001")
	mcpConflict.RemoteAddr = "203.0.113.50:443"
	mcpConflict.TLS = &tlsState
	mcpConflictResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mcpConflictResponse, mcpConflict)
	if mcpConflictResponse.Code != http.StatusOK || !strings.Contains(mcpConflictResponse.Body.String(), `"category":"conflict"`) {
		t.Fatalf("MCP replay conflict status=%d body=%s", mcpConflictResponse.Code, mcpConflictResponse.Body.String())
	}
	assertOpenPlatformV1AICounts(t, native, 1, 1, 1, 1, 1, 3)

	// Reconstruct Access, AI and the V1 handler over a new pool. MCP must read
	// the durable AI state and preserve the creator-scoped machine boundary.
	restartedNative, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedNative.Close()
	restartedPool, err := platformpostgres.Wrap(restartedNative, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedPool.Close()
	restartedUOW, err := platformpostgres.NewUnitOfWork(restartedPool)
	if err != nil {
		t.Fatal(err)
	}
	restartedMachine, err := accessapp.NewMachineService(accessstore.NewPostgreSQL(), restartedUOW, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-platform-v1", Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	restartedAudit := &openPlatformV1AIAuditWriter{delegate: accessstore.NewPostgreSQL()}
	restartedHandler := newOpenPlatformV1AIHandler(t, restartedNative, restartedUOW, restartedMachine, restartedAudit)
	mcp := httptest.NewRequest(http.MethodPost, "https://crm.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"read-1","method":"tools/call","params":{"name":"get_operation_status","arguments":{"operation_id":"`+created.Data.OperationID+`"}}}`))
	mcp.Header.Set("Authorization", "Bearer "+readToken.AccessToken)
	mcp.Header.Set("Content-Type", "application/json")
	mcp.RemoteAddr = "203.0.113.50:443"
	mcp.TLS = &tlsState
	mcpResponse := httptest.NewRecorder()
	restartedHandler.Routes().ServeHTTP(mcpResponse, mcp)
	if mcpResponse.Code != http.StatusOK || !strings.Contains(mcpResponse.Body.String(), `"operation_state":"pending_review"`) || !strings.Contains(mcpResponse.Body.String(), `"review_state":"pending_review"`) {
		t.Fatalf("MCP restarted status=%d body=%s", mcpResponse.Code, mcpResponse.Body.String())
	}

	other, err := restartedMachine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{ClientID: "v1.ai-other", DisplayName: "V1 AI other", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"operation.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restartedMachine.Activate(ctx, admin, other.Client.ClientID, other.Secret, true); err != nil {
		t.Fatal(err)
	}
	otherToken, err := restartedMachine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: other.Client.ClientID, ClientSecret: other.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}
	otherStatus := httptest.NewRequest(http.MethodGet, "https://crm.example.test/open/v1/operations/"+created.Data.OperationID, nil)
	otherStatus.Header.Set("Authorization", "Bearer "+otherToken.AccessToken)
	otherStatus.RemoteAddr = "203.0.113.50:443"
	otherStatus.TLS = &tlsState
	otherStatusResponse := httptest.NewRecorder()
	restartedHandler.Routes().ServeHTTP(otherStatusResponse, otherStatus)
	if otherStatusResponse.Code != http.StatusNotFound || !strings.Contains(otherStatusResponse.Body.String(), `"not_found"`) {
		t.Fatalf("cross-machine status status=%d body=%s", otherStatusResponse.Code, otherStatusResponse.Body.String())
	}

	// The audit failure occurs after AI has reserved and created every owner
	// row. Returning it from the same transaction must leave no second receipt,
	// plan, event, outbox or Access operation audit behind.
	audit.fail = true
	failed := httptest.NewRequest(http.MethodPost, "https://crm.example.test/open/v1/ai/review-plans", strings.NewReader(openPlatformV1AIRequestBody(t, "v1-ai-rollback-source")))
	failed.Header.Set("Authorization", "Bearer "+writeToken.AccessToken)
	failed.Header.Set("Content-Type", "application/json")
	failed.Header.Set("Idempotency-Key", "v1-ai-rollback-key-0002")
	failed.RemoteAddr = "203.0.113.50:443"
	failed.TLS = &tlsState
	failedResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(failedResponse, failed)
	if failedResponse.Code != http.StatusServiceUnavailable || !strings.Contains(failedResponse.Body.String(), `"dependency_unavailable"`) {
		t.Fatalf("audit failure create status=%d body=%s", failedResponse.Code, failedResponse.Body.String())
	}
	// A grant update must invalidate the old bearer, and a freshly issued token
	// without this capability cannot replay an existing receipt. The receipt is
	// never an authorization bypass.
	noAI := []string{"operation.read"}
	if _, err = restartedMachine.PatchV1(ctx, admin, issuedClient.Client.ClientID, accessapp.PatchMachineClientInput{Capabilities: &noAI}); err != nil {
		t.Fatal(err)
	}
	staleReplay := httptest.NewRequest(http.MethodPost, "https://crm.example.test/open/v1/ai/review-plans", strings.NewReader(requestBody))
	staleReplay.Header.Set("Authorization", "Bearer "+writeToken.AccessToken)
	staleReplay.Header.Set("Content-Type", "application/json")
	staleReplay.Header.Set("Idempotency-Key", "v1-ai-rest-key-0001")
	staleReplay.RemoteAddr = "203.0.113.50:443"
	staleReplay.TLS = &tlsState
	staleReplayResponse := httptest.NewRecorder()
	restartedHandler.Routes().ServeHTTP(staleReplayResponse, staleReplay)
	if staleReplayResponse.Code != http.StatusUnauthorized || !strings.Contains(staleReplayResponse.Body.String(), `"authentication"`) {
		t.Fatalf("stale grant replay status=%d body=%s", staleReplayResponse.Code, staleReplayResponse.Body.String())
	}
	narrowedToken, err := restartedMachine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: issuedClient.Client.ClientID, ClientSecret: issuedClient.Secret, Audience: "external_integration", RequestedScopes: []string{"write"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}
	narrowedReplay := httptest.NewRequest(http.MethodPost, "https://crm.example.test/open/v1/ai/review-plans", strings.NewReader(requestBody))
	narrowedReplay.Header.Set("Authorization", "Bearer "+narrowedToken.AccessToken)
	narrowedReplay.Header.Set("Content-Type", "application/json")
	narrowedReplay.Header.Set("Idempotency-Key", "v1-ai-rest-key-0001")
	narrowedReplay.RemoteAddr = "203.0.113.50:443"
	narrowedReplay.TLS = &tlsState
	narrowedReplayResponse := httptest.NewRecorder()
	restartedHandler.Routes().ServeHTTP(narrowedReplayResponse, narrowedReplay)
	if narrowedReplayResponse.Code != http.StatusForbidden || !strings.Contains(narrowedReplayResponse.Body.String(), `"permission"`) {
		t.Fatalf("narrowed grant replay status=%d body=%s", narrowedReplayResponse.Code, narrowedReplayResponse.Body.String())
	}

	// REST create, MCP replay/conflict, restarted MCP status, denied status and
	// the narrowed-grant denial append six operation audits. The failed create
	// and stale-token attempt append none.
	assertOpenPlatformV1AICounts(t, native, 1, 1, 1, 1, 1, 6)
}

var tlsState = tls.ConnectionState{}

type openPlatformV1AIAuditWriter struct {
	delegate interface {
		AppendMachineAudit(context.Context, accessdomain.MachineAudit) error
	}
	fail bool
}

func (writer *openPlatformV1AIAuditWriter) AppendMachineAudit(ctx context.Context, audit accessdomain.MachineAudit) error {
	if writer.fail {
		return errors.New("injected open platform audit failure")
	}
	return writer.delegate.AppendMachineAudit(ctx, audit)
}

func newOpenPlatformV1AIHandler(t *testing.T, native *pgxpool.Pool, uow *platformpostgres.UnitOfWork, machine *accessapp.MachineService, audit *openPlatformV1AIAuditWriter) *openplatformhttp.Handler {
	t.Helper()
	aiRepository, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	aiService, err := aiassistantapp.NewService(uow, aiRepository, openPlatformV1AICustomers{}, openPlatformV1AIStaff{}, openPlatformV1AIMaterials{}, openPlatformV1AIIdentities{}, openPlatformV1AIIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("open-platform-v1", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1OperationAudit(audit, uow); err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1AI(aiService, aiService, uow); err != nil {
		t.Fatal(err)
	}
	rateLimiter, err := accessapp.NewMachineRequestRateLimiter(accessstore.NewPostgreSQL(), uow, accessapp.MachineRequestRateLimitConfig{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := openplatformhttp.NewHandler(openplatformhttp.Config{MachineAuthentication: machine, RateLimiter: rateLimiter, AdminAuthentication: openPlatformMachineAdmin{}, Management: machine, Operations: executor, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type openPlatformV1AICustomers struct{}

func (openPlatformV1AICustomers) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "V1 customer", OneIDLabel: "OneID"}, nil
}

type openPlatformV1AIStaff struct{}

func (openPlatformV1AIStaff) StaffSnapshot(_ context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{ID: id, DisplayName: "V1 staff", Active: true}, nil
}
func (openPlatformV1AIStaff) StaffByWeComUserID(context.Context, string) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{}, errors.New("staff lookup is not used by machine recipient candidates")
}

type openPlatformV1AIMaterials struct{}

func (openPlatformV1AIMaterials) ResolveMaterial(_ context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	return block, nil
}
func (openPlatformV1AIMaterials) RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error {
	return nil
}

type openPlatformV1AIIdentities struct{}

func (openPlatformV1AIIdentities) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}
func (openPlatformV1AIIdentities) VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error) {
	return "", false, nil
}

func openPlatformV1AIRequestBody(t *testing.T, source string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":          "V1 review plan",
		"source_kind":   "open_platform",
		"source_digest": effectport.Hash(source),
		"recipients": []any{map[string]any{
			"customer_id": 42,
			"staff_id":    8,
			"content":     []any{map[string]any{"kind": "text", "text": "review this plan"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func assertOpenPlatformV1AICounts(t *testing.T, native *pgxpool.Pool, plans, recipients, contents, receipts, events, operationAudits int) {
	t.Helper()
	var gotPlans, gotRecipients, gotContents, gotReceipts, gotEvents, gotOutbox, gotOperationAudits int
	err := native.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM ai_assistant_plans),
		(SELECT count(*) FROM ai_assistant_plan_recipients),
		(SELECT count(*) FROM ai_assistant_content_versions),
		(SELECT count(*) FROM ai_assistant_operation_receipts),
		(SELECT count(*) FROM ai_assistant_audit_events),
		(SELECT count(*) FROM ai_assistant_outbox),
		(SELECT count(*) FROM access_machine_audit WHERE action='open_platform_operation')`).Scan(&gotPlans, &gotRecipients, &gotContents, &gotReceipts, &gotEvents, &gotOutbox, &gotOperationAudits)
	if err != nil || gotPlans != plans || gotRecipients != recipients || gotContents != contents || gotReceipts != receipts || gotEvents != events || gotOutbox != events || gotOperationAudits != operationAudits {
		t.Fatalf("plans=%d recipients=%d contents=%d receipts=%d events=%d outbox=%d operation_audits=%d err=%v", gotPlans, gotRecipients, gotContents, gotReceipts, gotEvents, gotOutbox, gotOperationAudits, err)
	}
}

func openPlatformV1AIMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := openPlatformMachineMigrate(ctx, pool); err != nil {
		return err
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0036_ai_assistant_review.sql", "0100_ai_assistant_machine_actor.sql", "0120_excel_batches.sql", "0124_operation_excel_batch_lifecycle.sql"} {
		sql, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}
	return nil
}
