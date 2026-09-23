package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestOpenPlatformMachineManagementPostgreSQLJourney exercises the exact
// composition boundary against an isolated, randomly named database. It proves
// the list read model can load grants after multi-row scans on one pgx Tx, and
// that lifecycle state and its audit are atomic in the owner store.
func TestOpenPlatformMachineManagementPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()

	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformMachineMigrate(ctx, native); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := accessstore.NewPostgreSQL()
	passwords := credential.PasswordHasher{}
	service, err := accessapp.NewMachineService(repository, unit, passwords, accessapp.MachineConfig{
		SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-platform-pg", Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := passwords.Hash("open-platform-postgres-admin")
	if err != nil {
		t.Fatal(err)
	}
	var adminUser accessdomain.User
	err = unit.Within(ctx, func(txContext context.Context) error {
		var createErr error
		adminUser, createErr = repository.CreateUser(txContext, accessdomain.User{Username: "open-platform-admin", PasswordHash: passwordHash, DisplayName: "Open Platform Admin", Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}})
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}

	external, err := service.Create(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "postgres.external", DisplayName: "PostgreSQL external", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"external_read", "external_write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := service.Create(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "postgres.mcp", DisplayName: "PostgreSQL MCP", Purpose: "mcp",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_read", "mcp_execute"},
	})
	if err != nil {
		t.Fatal(err)
	}

	clients, err := service.List(ctx, admin)
	if err != nil || len(clients) != 2 {
		t.Fatalf("machine list clients=%+v err=%v", clients, err)
	}
	assertMachineCapabilities(t, clients, external.Client.ClientID, []string{"external_read", "external_write"})
	assertMachineCapabilities(t, clients, mcp.Client.ClientID, []string{"mcp_execute", "mcp_read"})

	machineExecutor := &openPlatformMachineExecutor{}
	rateLimiter, err := accessapp.NewMachineRequestRateLimiter(repository, unit, accessapp.MachineRequestRateLimitConfig{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := openplatformhttp.NewHandler(openplatformhttp.Config{
		MachineAuthentication: service,
		RateLimiter:           rateLimiter, AdminAuthentication: openPlatformMachineAdmin{}, Management: service,
		Operations: machineExecutor, Executor: machineExecutor, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://crm.example.test/api/admin/open-platform/clients", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("V1 management list status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Items []struct {
			ClientID     string   `json:"client_id"`
			Capabilities []string `json:"capabilities"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("V1 management page=%s err=%v", response.Body.String(), err)
	}

	// The browser control plane rejects frozen donor route capabilities even if
	// a caller bypasses the UI catalog. Historical import remains a separate,
	// protected process and is not exercised through this HTTP endpoint.
	legacyGrantRequest := httptest.NewRequest(http.MethodPost, "https://crm.example.test/api/admin/open-platform/clients", strings.NewReader(`{"client_id":"postgres.legacy-grant","display_name":"Legacy grant","purpose":"external_agent","audiences":["external_integration"],"scopes":["read"],"capabilities":["external_read"],"token_ttl_seconds":1800}`))
	legacyGrantRequest.Header.Set("Content-Type", "application/json")
	legacyGrantResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(legacyGrantResponse, legacyGrantRequest)
	if legacyGrantResponse.Code != http.StatusBadRequest {
		t.Fatalf("V1 management accepted historical capability status=%d body=%s", legacyGrantResponse.Code, legacyGrantResponse.Body.String())
	}

	// The V1 management transport must persist a complete grant replacement,
	// distinguish explicit null clearing from omission, and invalidate a bearer
	// issued before the changed authorization boundary.
	expiresAt := time.Now().UTC().Add(time.Hour)
	v1Client, err := service.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "postgres.v1-control", DisplayName: "PostgreSQL V1 control", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"},
		Capabilities: []string{"customer.read", "customer.resolve"}, OwnerScope: accessdomain.OwnerScope{"customer_id": {"42"}}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAllowedCIDRsJSON(t, v1Client.Client, "create")
	activateRequest := httptest.NewRequest(http.MethodPost, "https://crm.example.test/api/admin/open-platform/clients/"+v1Client.Client.ClientID+"/activate", strings.NewReader(`{"client_secret":"`+v1Client.Secret+`","copied_confirmed":true}`))
	activateRequest.Header.Set("Content-Type", "application/json")
	activateResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(activateResponse, activateRequest)
	if activateResponse.Code != http.StatusOK || !strings.Contains(activateResponse.Body.String(), `"enabled":true`) {
		t.Fatalf("V1 management activate status=%d body=%s", activateResponse.Code, activateResponse.Body.String())
	}
	assertAllowedCIDRsJSON(t, activateResponse.Body.Bytes(), "activate")
	v1Bearer, err := service.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: v1Client.Client.ClientID, ClientSecret: v1Client.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: mustOpenPlatformAddr(t, "203.0.113.5")})
	if err != nil {
		t.Fatal(err)
	}
	patchRequest := httptest.NewRequest(http.MethodPatch, "https://crm.example.test/api/admin/open-platform/clients/"+v1Client.Client.ClientID, strings.NewReader(`{"capabilities":["customer.resolve"],"owner_scope":null,"expires_at":null}`))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK || !strings.Contains(patchResponse.Body.String(), `"customer.resolve"`) {
		t.Fatalf("V1 management patch status=%d body=%s", patchResponse.Code, patchResponse.Body.String())
	}
	if _, err = service.AuthenticateBearer(ctx, v1Bearer.AccessToken, "external_integration", mustOpenPlatformAddr(t, "203.0.113.5")); err == nil {
		t.Fatal("V1 grant PATCH left the prior bearer valid")
	}
	detailRequest := httptest.NewRequest(http.MethodGet, "https://crm.example.test/api/admin/open-platform/clients/"+v1Client.Client.ClientID, nil)
	detailResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || strings.Contains(detailResponse.Body.String(), `"owner_scope":{"customer_id"`) || strings.Contains(detailResponse.Body.String(), `"expires_at"`) {
		t.Fatalf("V1 management detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	assertAllowedCIDRsJSON(t, detailResponse.Body.Bytes(), "detail")
	auditRequest := httptest.NewRequest(http.MethodGet, "https://crm.example.test/api/admin/open-platform/clients/"+v1Client.Client.ClientID+"/audit?limit=10", nil)
	auditResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK || !strings.Contains(auditResponse.Body.String(), `"machine_client_grants_updated"`) || !strings.Contains(auditResponse.Body.String(), `"revoked_prior_bearers"`) {
		t.Fatalf("V1 management audit status=%d body=%s", auditResponse.Code, auditResponse.Body.String())
	}
	rotateRequest := httptest.NewRequest(http.MethodPost, "https://crm.example.test/api/admin/open-platform/clients/"+v1Client.Client.ClientID+"/rotate", strings.NewReader(`{}`))
	rotateRequest.Header.Set("Content-Type", "application/json")
	rotateResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rotateResponse, rotateRequest)
	if rotateResponse.Code != http.StatusOK {
		t.Fatalf("V1 management rotate status=%d body=%s", rotateResponse.Code, rotateResponse.Body.String())
	}
	assertAllowedCIDRsJSON(t, rotateResponse.Body.Bytes(), "rotate")

	if _, err = service.Activate(ctx, admin, external.Client.ClientID, external.Secret, true); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{
		ClientID: external.Client.ClientID, ClientSecret: external.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: mustOpenPlatformAddr(t, "203.0.113.5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Rate decisions use the existing Access locked row store. Eight concurrent
	// requests with the same authenticated caller and source must consume one
	// durable slot, and a fresh limiter instance must observe the blocked row.
	ratePrincipal, err := service.AuthenticateBearer(ctx, issued.AccessToken, "external_integration", mustOpenPlatformAddr(t, "203.0.113.77"))
	if err != nil {
		t.Fatal(err)
	}
	strictRateLimiter, err := accessapp.NewMachineRequestRateLimiter(repository, unit, accessapp.MachineRequestRateLimitConfig{Window: time.Minute, MaxClientCredentialChecks: 1, MaxMachineRequests: 1})
	if err != nil {
		t.Fatal(err)
	}
	rateStart := make(chan struct{})
	rateResults := make(chan error, 8)
	var rateWorkers sync.WaitGroup
	rateWorkers.Add(8)
	for worker := 0; worker < 8; worker++ {
		go func() {
			defer rateWorkers.Done()
			<-rateStart
			rateResults <- strictRateLimiter.AllowMachineRequest(ctx, ratePrincipal, mustOpenPlatformAddr(t, "203.0.113.77"))
		}()
	}
	close(rateStart)
	rateWorkers.Wait()
	close(rateResults)
	successes, limited := 0, 0
	for rateErr := range rateResults {
		switch {
		case rateErr == nil:
			successes++
		case errors.Is(rateErr, accessdomain.ErrRateLimited):
			limited++
		default:
			t.Fatalf("concurrent machine rate decision=%v", rateErr)
		}
	}
	if successes != 1 || limited != 7 {
		t.Fatalf("machine rate successes=%d limited=%d", successes, limited)
	}
	restartedRateLimiter, err := accessapp.NewMachineRequestRateLimiter(accessstore.NewPostgreSQL(), unit, accessapp.MachineRequestRateLimitConfig{Window: time.Minute, MaxClientCredentialChecks: 1, MaxMachineRequests: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = restartedRateLimiter.AllowMachineRequest(ctx, ratePrincipal, mustOpenPlatformAddr(t, "203.0.113.77")); !errors.Is(err, accessdomain.ErrRateLimited) {
		t.Fatalf("restarted limiter bypassed persisted machine limit: %v", err)
	}

	// Token credential checks and authenticated protocol calls have independent
	// persistent buckets. The latter is intentionally reached only after Access
	// verifies the JWT, so invalid credentials cannot exhaust a valid caller's
	// machine-request quota.
	strictHandler, err := openplatformhttp.NewHandler(openplatformhttp.Config{
		MachineAuthentication: service, RateLimiter: strictRateLimiter, AdminAuthentication: openPlatformMachineAdmin{}, Management: service,
		Operations: machineExecutor, Executor: machineExecutor, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var credentialBucketsBefore int
	if err = native.QueryRow(ctx, `SELECT COUNT(*) FROM admin_login_rate_limits`).Scan(&credentialBucketsBefore); err != nil {
		t.Fatal(err)
	}
	// Both unknown IDs are syntactically valid. The second must hit the same
	// source-only pre-auth bucket instead of allocating a row per guessed ID.
	for attempt, clientID := range []string{"unknown-client-one", "unknown-client-two"} {
		request := httptest.NewRequest(http.MethodPost, "https://crm.example.test/oauth/token", strings.NewReader("grant_type=client_credentials&client_id="+url.QueryEscape(clientID)+"&client_secret=wrong-secret&audience=external_integration"))
		request.TLS = &tls.ConnectionState{}
		request.RemoteAddr = "203.0.113.88:443"
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		strictHandler.Routes().ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == 1 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want || (attempt == 1 && !strings.Contains(response.Body.String(), `"error":"rate_limited"`)) {
			t.Fatalf("credential rate client=%q attempt=%d status=%d body=%s", clientID, attempt, response.Code, response.Body.String())
		}
	}
	var credentialBucketsAfter int
	if err = native.QueryRow(ctx, `SELECT COUNT(*) FROM admin_login_rate_limits`).Scan(&credentialBucketsAfter); err != nil {
		t.Fatal(err)
	}
	if credentialBucketsAfter != credentialBucketsBefore+1 {
		t.Fatalf("unknown OAuth IDs created %d rate rows, want one source bucket", credentialBucketsAfter-credentialBucketsBefore)
	}
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "https://crm.example.test/mcp", nil)
		request.TLS = &tls.ConnectionState{}
		request.RemoteAddr = "203.0.113.89:443"
		request.Header.Set("Authorization", "Bearer "+issued.AccessToken)
		response := httptest.NewRecorder()
		strictHandler.Routes().ServeHTTP(response, request)
		want := http.StatusOK
		if attempt == 1 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want || (attempt == 1 && !strings.Contains(response.Body.String(), `"code":"rate_limited"`)) {
			t.Fatalf("machine HTTP rate attempt=%d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}

	// This crosses the real PostgreSQL Access service and a freshly signed
	// machine JWT through a retained V1 transport endpoint. Retired /api
	// routes are not a compatibility surface.
	externalRequest := httptest.NewRequest(http.MethodGet, "https://crm.example.test/mcp", nil)
	externalRequest.RemoteAddr = "203.0.113.5:443"
	externalRequest.TLS = &tls.ConnectionState{}
	externalRequest.Header.Set("Authorization", "Bearer "+issued.AccessToken)
	externalResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(externalResponse, externalRequest)
	if externalResponse.Code != http.StatusOK || !strings.Contains(externalResponse.Body.String(), `"transport":"jsonrpc"`) {
		t.Fatalf("authenticated V1 MCP metadata status=%d body=%s", externalResponse.Code, externalResponse.Body.String())
	}
	retiredRequest := httptest.NewRequest(http.MethodPost, "https://crm.example.test/api/ai/audience/packages", nil)
	retiredRequest.RemoteAddr = "203.0.113.5:443"
	retiredRequest.TLS = &tls.ConnectionState{}
	retiredRequest.Header.Set("Authorization", "Bearer "+issued.AccessToken)
	retiredResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(retiredResponse, retiredRequest)
	if retiredResponse.Code != http.StatusNotFound {
		t.Fatalf("retired machine route status=%d body=%s", retiredResponse.Code, retiredResponse.Body.String())
	}

	// Both operations lock the same client. Whichever wins, rotation leaves a
	// regular client disabled and every old bearer must fail immediately.
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, rotateErr := service.Rotate(ctx, admin, external.Client.ClientID)
		results <- rotateErr
	}()
	go func() {
		defer workers.Done()
		<-start
		_, disableErr := service.SetEnabled(ctx, admin, external.Client.ClientID, false)
		results <- disableErr
	}()
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result != nil {
			t.Fatalf("concurrent rotate/disable=%v", result)
		}
	}
	if _, err = service.AuthenticateBearer(ctx, issued.AccessToken, "external_integration", mustOpenPlatformAddr(t, "203.0.113.5")); err == nil {
		t.Fatal("old bearer remained valid after concurrent lifecycle changes")
	}

	historicalBatch := accessport.HistoricalMachineImportBatch{ImportRunID: "open-platform:11111111111111111111111111111111", SourceSystem: "ai-crm", SourceRevision: "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f", ManifestDigest: [32]byte{1, 9}, SnapshotAt: time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC), ClientCount: 3, AuditCount: 1}
	if _, err = service.BeginHistoricalImport(ctx, historicalBatch); err != nil {
		t.Fatalf("begin historical batch=%v", err)
	}
	historical := accessapp.HistoricalMachineImportInput{ImportRunID: "open-platform:11111111111111111111111111111111", SourceSystem: "ai-crm", SourceScope: "auth_api_clients", SourceRowID: "legacy-identity-1", SourceClientID: "historic.identity", SourceRowDigest: [32]byte{9, 6}, SourceOwnerScopeDigest: [32]byte{9, 7}, OwnerScopeMappingStatus: "not_required", ClientID: "historic.identity", PrincipalID: "api_client:historic.identity", PrincipalType: "api_client", DisplayName: "Historic identity", Purpose: "identity", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"identity_resolve"}, CorpID: "historic-corp", SourceEnabled: true, SourceAuthVersion: 3, TokenTTLSeconds: 1800}
	imported, err := service.ImportHistorical(ctx, historical)
	if err != nil || imported.Replayed || imported.Client.Enabled || !imported.Client.ReissueRequired || imported.Client.Purpose != "identity" {
		t.Fatalf("historical import=%+v err=%v", imported, err)
	}
	// The donor direct-key identity is explicitly mapped before this Port:
	// source receipt retains its old client ID while the fixed V3 direct page
	// reads the disabled/reissue-required target record.
	directHistorical := historical
	directHistorical.SourceRowID = "legacy-direct-key"
	directHistorical.SourceClientID = "aicrm-direct-external-api-key"
	directHistorical.SourceRowDigest = [32]byte{9, 8}
	directHistorical.ClientID = accessapp.DirectExternalAPIKeyClientID
	directHistorical.PrincipalID = "api_client:aicrm-direct-external-api-key"
	directHistorical.DisplayName = "CRM 开放 API Key"
	directHistorical.Purpose = "direct_api_key"
	directHistorical.Capabilities = []string{"external_read"}
	directHistorical.SourceAuthVersion = 4
	directImported, err := service.ImportHistorical(ctx, directHistorical)
	if err != nil || directImported.Outcome != "reissue_required" || directImported.Client.Enabled || !directImported.Client.ReissueRequired || directImported.Client.ClientID != accessapp.DirectExternalAPIKeyClientID {
		t.Fatalf("historical direct import=%+v err=%v", directImported, err)
	}
	clients, err = service.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var directSummary *accessapp.MachineClientSummary
	for index := range clients {
		if clients[index].ClientID == accessapp.DirectExternalAPIKeyClientID {
			directSummary = &clients[index]
			break
		}
	}
	if directSummary == nil || directSummary.Enabled || !directSummary.ReissueRequired || directSummary.CredentialHint == "" {
		t.Fatalf("historical direct summary=%+v", directSummary)
	}
	directRequest := httptest.NewRequest(http.MethodGet, "https://crm.example.test/api/admin/config/api-key", nil)
	directResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(directResponse, directRequest)
	if directResponse.Code != http.StatusNotFound {
		t.Fatalf("retired direct-key page status=%d body=%s", directResponse.Code, directResponse.Body.String())
	}

	// A pre-existing normal V3 caller with the same target client_id is not
	// overwritten. Its exact donor row receives a durable, verifiable exclusion.
	conflictedHistorical := historical
	conflictedHistorical.SourceRowID = "legacy-existing-external"
	conflictedHistorical.SourceClientID = "historic.external"
	conflictedHistorical.SourceRowDigest = [32]byte{9, 10}
	conflictedHistorical.SourceOwnerScopeDigest = [32]byte{9, 11}
	conflictedHistorical.ClientID = external.Client.ClientID
	conflictedHistorical.PrincipalID = "api_client:historic.external"
	conflictedHistorical.DisplayName = "Historic external"
	conflictedHistorical.Purpose = "external_agent"
	conflictedHistorical.Scopes = []string{"read", "write"}
	conflictedHistorical.Capabilities = []string{"external_read", "external_write"}
	conflictOutcome, err := service.ImportHistorical(ctx, conflictedHistorical)
	if err != nil || conflictOutcome.Outcome != "excluded" || conflictOutcome.ReasonCode != "target_client_id_conflict" {
		t.Fatalf("target client conflict outcome=%+v err=%v", conflictOutcome, err)
	}
	verifiedConflict, err := service.VerifyHistorical(ctx, conflictedHistorical)
	if err != nil || verifiedConflict.Outcome != "excluded" || verifiedConflict.ReasonCode != "target_client_id_conflict" {
		t.Fatalf("target client conflict verify=%+v err=%v", verifiedConflict, err)
	}
	var existingTargetCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_clients WHERE client_id=$1 AND display_name='PostgreSQL external'`, external.Client.ClientID).Scan(&existingTargetCount); err != nil || existingTargetCount != 1 {
		t.Fatalf("target client overwritten count=%d err=%v", existingTargetCount, err)
	}
	driftedConflict := conflictedHistorical
	driftedConflict.SourceRowDigest = [32]byte{9, 12}
	if _, err = service.VerifyHistorical(ctx, driftedConflict); !errors.Is(err, accessdomain.ErrConflict) {
		t.Fatalf("conflict receipt source drift=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE access_machine_import_receipts SET reason_code='tampered_reason' WHERE source_system='ai-crm' AND source_scope='auth_api_clients' AND source_row_id='legacy-existing-external'`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.VerifyHistorical(ctx, conflictedHistorical); !errors.Is(err, accessdomain.ErrConflict) {
		t.Fatalf("conflict receipt reason mutation=%v", err)
	}
	verified, err := service.VerifyHistorical(ctx, historical)
	if err != nil || verified.Outcome != "reissue_required" || verified.Client.Enabled || !verified.Client.ReissueRequired {
		t.Fatalf("historical verification=%+v err=%v", verified, err)
	}
	replayed, err := service.ImportHistorical(ctx, historical)
	if err != nil || !replayed.Replayed || replayed.Outcome != "replayed" {
		t.Fatalf("historical replay=%+v err=%v", replayed, err)
	}
	var historicalAudits int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_audit WHERE action='machine_client_imported'`).Scan(&historicalAudits); err != nil || historicalAudits != 2 {
		t.Fatalf("historical import audit count=%d err=%v", historicalAudits, err)
	}
	historicalSourceAudit := accessport.HistoricalMachineAuditInput{ImportRunID: "open-platform:11111111111111111111111111111111", SourceSystem: "ai-crm", SourceScope: "admin_operation_logs:api_client", SourceAuditID: 77, SourceRowDigest: [32]byte{7, 7}, Operator: "crm_console", Action: "api_client_secret_rotated", TargetType: "api_client", TargetID: "historic.identity", BeforeDigest: [32]byte{8, 8}, AfterDigest: [32]byte{9, 9}, OccurredAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)}
	auditImported, err := service.ImportHistoricalAudit(ctx, historicalSourceAudit)
	if err != nil || auditImported.Outcome != "imported" || auditImported.Replayed {
		t.Fatalf("historical source audit=%+v err=%v", auditImported, err)
	}
	auditReplay, err := service.ImportHistoricalAudit(ctx, historicalSourceAudit)
	if err != nil || auditReplay.Outcome != "replayed" || !auditReplay.Replayed {
		t.Fatalf("historical source audit replay=%+v err=%v", auditReplay, err)
	}
	if err = service.VerifyHistoricalAudit(ctx, historicalSourceAudit); err != nil {
		t.Fatalf("verify historical source audit=%v", err)
	}
	var sourceAuditFacts int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_historical_audit_facts WHERE import_run_id='open-platform:11111111111111111111111111111111'`).Scan(&sourceAuditFacts); err != nil || sourceAuditFacts != 1 {
		t.Fatalf("historical source audit facts=%d err=%v", sourceAuditFacts, err)
	}
	clients, err = service.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.ClientID == external.Client.ClientID && client.Enabled {
			t.Fatal("rotated external client remained enabled")
		}
	}

	rollback := errors.New("machine audit rollback")
	err = unit.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := repository.MachineClientByID(txContext, external.Client.ClientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		if auditErr := repository.AppendMachineAudit(txContext, accessdomain.MachineAudit{MachineClientID: client.ID, Action: "machine_audit_rollback", Outcome: "failed", Details: []byte(`{}`), CreatedAt: time.Now().UTC()}); auditErr != nil {
			return auditErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("audit rollback error=%v", err)
	}
	var rolledBack int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_audit WHERE action='machine_audit_rollback'`).Scan(&rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("rolled-back audit count=%d err=%v", rolledBack, err)
	}
}

type openPlatformMachineAdmin struct{}

func (openPlatformMachineAdmin) Authenticate(context.Context, string) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (openPlatformMachineAdmin) AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error) {
	return openPlatformMachineAdmin{}.Authenticate(context.Background(), "")
}

type openPlatformMachineExecutor struct {
	requests []openplatformport.Request
}

func (executor *openPlatformMachineExecutor) Execute(_ context.Context, request openplatformport.Request) (openplatformport.Response, error) {
	executor.requests = append(executor.requests, request)
	return openplatformport.Response{Status: http.StatusOK, Body: map[string]any{"ok": true}}, nil
}

func (*openPlatformMachineExecutor) Available(context.Context, accessdomain.MachinePrincipal) ([]openplatformport.Descriptor, error) {
	return []openplatformport.Descriptor{}, nil
}

func (*openPlatformMachineExecutor) Invoke(context.Context, openplatformport.Invocation) (openplatformport.Result, error) {
	return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "test operation service is not composed")
}

func assertAllowedCIDRsJSON(t *testing.T, source any, operation string) {
	t.Helper()
	var raw []byte
	if value, ok := source.([]byte); ok {
		raw = value
	} else {
		var err error
		raw, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if client, ok := payload["client"]; ok {
		if err := json.Unmarshal(client, &payload); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := payload["allowed_cidrs"]; !ok || string(got) != "[]" {
		t.Fatalf("%s allowed_cidrs=%s want []", operation, got)
	}
}

func assertMachineCapabilities(t *testing.T, clients []accessapp.MachineClientSummary, clientID string, want []string) {
	t.Helper()
	for _, client := range clients {
		if client.ClientID == clientID {
			if len(client.Capabilities) != len(want) {
				t.Fatalf("client=%s capabilities=%v want=%v", clientID, client.Capabilities, want)
			}
			for index := range want {
				if client.Capabilities[index] != want[index] {
					t.Fatalf("client=%s capabilities=%v want=%v", clientID, client.Capabilities, want)
				}
			}
			return
		}
	}
	t.Fatalf("missing listed client %s", clientID)
}

func mustOpenPlatformAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	address, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func openPlatformMachineTestDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Open Platform PostgreSQL journey")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	database := "aicrm_open_platform_" + hex.EncodeToString(random[:])
	adminURL := *parsed
	adminURL.Path = "/postgres"
	adminURL.RawPath = ""
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + database
	testURL.RawPath = ""
	return testURL.String(), func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)")
		admin.Close(cleanup)
	}
}

func openPlatformMachineMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0003_access.sql", "0096_open_platform.sql"} {
		sql, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}
	return ensureAccessLoginFixtureSchema(ctx, pool)
}
