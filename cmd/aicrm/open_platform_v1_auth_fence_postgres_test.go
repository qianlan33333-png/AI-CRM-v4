package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type auth02CoreFenceCanonicalCustomer struct{}

func (auth02CoreFenceCanonicalCustomer) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

type auth02CoreFenceGate struct {
	delegate openplatformport.OperationService
	entered  chan openplatformport.Invocation
	resume   chan struct{}
}

func (gate *auth02CoreFenceGate) Available(ctx context.Context, principal accessdomain.MachinePrincipal) ([]openplatformport.Descriptor, error) {
	return gate.delegate.Available(ctx, principal)
}

func (gate *auth02CoreFenceGate) Invoke(ctx context.Context, invocation openplatformport.Invocation) (openplatformport.Result, error) {
	descriptor, ok := openplatformport.DescriptorForOperation(invocation.Operation)
	if !ok {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "operation not found")
	}
	if !descriptor.Allows(invocation.Principal) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "operation is not granted")
	}
	select {
	case gate.entered <- invocation:
	case <-ctx.Done():
		return openplatformport.Result{}, ctx.Err()
	}
	select {
	case <-gate.resume:
	case <-ctx.Done():
		return openplatformport.Result{}, ctx.Err()
	}
	return gate.delegate.Invoke(ctx, invocation)
}

// auth02CoreFencePausingUOW holds the actual PostgreSQL transaction after its
// callback has written Segment rows but before commit. It provides a stable
// point to prove that Access revocation cannot finish ahead of the owner UOW.
type auth02CoreFencePausingUOW struct {
	delegate    platformport.UnitOfWork
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (uow *auth02CoreFencePausingUOW) releaseWrite() {
	uow.releaseOnce.Do(func() { close(uow.release) })
}

func (uow *auth02CoreFencePausingUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return uow.delegate.Within(ctx, func(tx context.Context) error {
		if err := callback(tx); err != nil {
			return err
		}
		uow.once.Do(func() { close(uow.entered) })
		select {
		case <-uow.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

type auth02CoreFenceRig struct {
	ctx        context.Context
	native     *pgxpool.Pool
	uow        *platformpostgres.UnitOfWork
	machine    *accessapp.MachineService
	admin      accessdomain.Principal
	clientID   string
	write      string
	read       string
	packageID  int64
	occurredAt time.Time
	executor   *openPlatformExecutor
	gate       *auth02CoreFenceGate
	handler    *openplatformhttp.Handler
	writeUOW   *auth02CoreFencePausingUOW
}

func newAUTH02CoreFenceRig(t *testing.T, gated, pauseWrite bool) *auth02CoreFenceRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	if err = openPlatformV1CoreFenceMigrate(ctx, native); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}

	accessRepository := accessstore.NewPostgreSQL()
	machine, err := accessapp.NewMachineService(accessRepository, uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-platform-v1", Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := (credential.PasswordHasher{}).Hash("auth02-core-fence-admin")
	if err != nil {
		t.Fatal(err)
	}
	var adminUser accessdomain.User
	if err = uow.Within(ctx, func(tx context.Context) error {
		var createErr error
		adminUser, createErr = accessRepository.CreateUser(tx, accessdomain.User{Username: "auth02-core-fence-admin", PasswordHash: passwordHash, DisplayName: "AUTH02 Core Fence", Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}

	segmentRepository, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	segmentService := segmentapp.NewService(uow, segmentRepository)
	segmentPackage, err := segmentService.CreatePackage(ctx, segmentapp.PackageCreateCommand{Name: "AUTH02 Core Fence", TemplateKey: "active_contacts", Actor: adminUser.ID, IdempotencyKey: "auth02-core-fence-package-0001"})
	if err != nil {
		t.Fatal(err)
	}
	canonicalCustomers := segmentadapter.CanonicalCustomers{UoW: uow, Resolver: auth02CoreFenceCanonicalCustomer{}}
	coreOperations := segmentapp.NewCoreOperations(segmentService, segmentRepository, canonicalCustomers, nil)

	clientID := "auth02-core-fence-machine"
	client, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: clientID, DisplayName: "AUTH02 Core Fence synthetic machine", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"},
		Capabilities: []string{"platform.capabilities.read", "audience.push.write"},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {"7"}, "package_id": {fmt.Sprint(segmentPackage.ID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, clientID, client.Secret, true); err != nil {
		t.Fatal(err)
	}
	writeToken, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: clientID, ClientSecret: client.Secret, Audience: "external_integration", RequestedScopes: []string{"write"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}
	readToken, err := machine.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{ClientID: clientID, ClientSecret: client.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}

	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("open-platform-v1", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1OperationAudit(accessRepository, uow); err != nil {
		t.Fatal(err)
	}
	rig := &auth02CoreFenceRig{ctx: ctx, native: native, uow: uow, machine: machine, admin: admin, clientID: clientID, write: writeToken.AccessToken, read: readToken.AccessToken, packageID: segmentPackage.ID, occurredAt: time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC), executor: executor}
	var writeUOW platformport.UnitOfWork = uow
	if pauseWrite {
		rig.writeUOW = &auth02CoreFencePausingUOW{delegate: uow, entered: make(chan struct{}), release: make(chan struct{})}
		writeUOW = rig.writeUOW
	}
	if err = executor.BindV1MachineMutationFence(machine, writeUOW); err != nil {
		t.Fatal(err)
	}
	executor.coreAudience = coreOperations

	rateLimiter, err := accessapp.NewMachineRequestRateLimiter(accessRepository, uow, accessapp.MachineRequestRateLimitConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var operations openplatformport.OperationService = executor
	var gate *auth02CoreFenceGate
	if gated {
		gate = &auth02CoreFenceGate{delegate: executor, entered: make(chan openplatformport.Invocation, 1), resume: make(chan struct{})}
		operations = gate
	}
	handler, err := openplatformhttp.NewHandler(openplatformhttp.Config{MachineAuthentication: machine, RateLimiter: rateLimiter, AdminAuthentication: openPlatformMachineAdmin{}, Management: machine, Operations: operations, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	rig.gate, rig.handler = gate, handler
	return rig
}

func openPlatformV1CoreFenceMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := openPlatformMachineMigrate(ctx, pool); err != nil {
		return err
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0039_segment_audience_configuration.sql", "0040_segment_audience_snapshots.sql", "0041_segment_audience_webhooks.sql", "0042_segment_audience_execution_bindings.sql", "0045_segment_audience_member_events.sql", "0048_segment_audience_schedule_state.sql", "0053_segment_audience_member_event_fact_kinds.sql", "0083_segment_audience_refresh_modes.sql", "0085_segment_audience_refresh_kind.sql", "0097_segment_audience_mutation_actor.sql", "0183_segment_core_operations.sql", "0214_segment_member_paid_fact.sql"} {
		path := filepath.Join(root, "migrations", name)
		sql, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply Segment migration %s: %w", name, err)
		}
	}
	return nil
}

func (rig *auth02CoreFenceRig) request(method, token, key, pushID string) *httptest.ResponseRecorder {
	push := map[string]any{
		"push_id": pushID, "customer_id": 7, "package_id": rig.packageID,
		"materials":   []any{map[string]any{"kind": "image", "id": 1}},
		"occurred_at": rig.occurredAt.Format(time.RFC3339Nano), "status": "reported", "status_version": 1,
	}
	body, _ := json.Marshal(push)
	var request *http.Request
	if method == "MCP" {
		arguments := string(body)
		request = httptest.NewRequest(http.MethodPost, "https://crm.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"auth02-core-push","method":"tools/call","params":{"name":"record_audience_push","arguments":`+arguments+`}}`))
	} else {
		request = httptest.NewRequest(http.MethodPost, "https://crm.example.test/open/v1/audience/push-records", strings.NewReader(string(body)))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.RemoteAddr = "203.0.113.50:443"
	request.TLS = &tlsState
	response := httptest.NewRecorder()
	rig.handler.Routes().ServeHTTP(response, request)
	return response
}

func (rig *auth02CoreFenceRig) revokePushGrant() error {
	remaining := []string{"platform.capabilities.read"}
	_, err := rig.machine.PatchV1(rig.ctx, rig.admin, rig.clientID, accessapp.PatchMachineClientInput{Capabilities: &remaining})
	return err
}

type auth02CoreFenceCounts struct {
	pushes, receipts, facts, outbox int64
}

func (rig *auth02CoreFenceRig) counts(t *testing.T) auth02CoreFenceCounts {
	t.Helper()
	var counts auth02CoreFenceCounts
	err := rig.native.QueryRow(rig.ctx, `SELECT
		(SELECT count(*) FROM segment_core_pushes),
		(SELECT count(*) FROM segment_audience_operation_receipts WHERE operation='push_recorded' AND actor_scope=$1),
		(SELECT count(*) FROM segment_audience_audit_events WHERE resource_kind='core_operations' AND operation='push_recorded'),
		(SELECT count(*) FROM segment_audience_outbox WHERE aggregate_kind='core_operations' AND event_type='audience.core.push_recorded.v1')`, "machine:"+rig.clientID).Scan(&counts.pushes, &counts.receipts, &counts.facts, &counts.outbox)
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func waitForAUTH02MachineRowLockWaits(t *testing.T, rig *auth02CoreFenceRig, minimum int) {
	waitForAUTH02ClientRowLockWaits(t, rig.native, rig.clientID, minimum)
}

func waitForAUTH02ClientRowLockWaits(t *testing.T, native *pgxpool.Pool, clientID string, minimum int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		err := native.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND cardinality(pg_blocking_pids(pid)) > 0
			AND query LIKE '%access_machine_clients%' AND query LIKE '%FOR UPDATE OF c%'`).Scan(&blocked)
		if err == nil && blocked >= minimum {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var blocked int
	_ = native.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
		WHERE datname=current_database() AND wait_event_type='Lock'
		AND cardinality(pg_blocking_pids(pid)) > 0
		AND query LIKE '%access_machine_clients%' AND query LIKE '%FOR UPDATE OF c%'`).Scan(&blocked)
	t.Fatalf("waiting for %d Access client-row lock waits for client %q, observed %d", minimum, clientID, blocked)
}

func TestAUTH02CorePushRevocationDeniesOldRESTAndMCPIdempotentReplayPostgreSQL(t *testing.T) {
	rig := newAUTH02CoreFenceRig(t, false, false)
	created := rig.request(http.MethodPost, rig.write, "auth02-core-push-replay-0001", "business-push-replay")
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), "business-push-replay") {
		t.Fatalf("initial CorePush REST status=%d body=%s", created.Code, created.Body.String())
	}
	baseline := rig.counts(t)
	if baseline != (auth02CoreFenceCounts{pushes: 1, receipts: 1, facts: 1, outbox: 1}) {
		t.Fatalf("initial Segment owner effects=%+v", baseline)
	}
	if err := rig.revokePushGrant(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPost, "MCP"} {
		replay := rig.request(method, rig.write, "auth02-core-push-replay-0001", "business-push-replay")
		if method == http.MethodPost {
			if replay.Code != http.StatusUnauthorized || !strings.Contains(replay.Body.String(), `"authentication"`) {
				t.Fatalf("revoked REST idempotent replay status=%d body=%s", replay.Code, replay.Body.String())
			}
		} else if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"authentication"`) || strings.Contains(replay.Body.String(), `"isError":false`) {
			t.Fatalf("revoked MCP idempotent replay status=%d body=%s", replay.Code, replay.Body.String())
		}
		if after := rig.counts(t); after != baseline {
			t.Fatalf("revoked %s replay mutated Segment owner: before=%+v after=%+v", method, baseline, after)
		}
	}
}

func TestAUTH02CorePushRevocationWinsAfterBearerAdmissionBeforeOwnerFencePostgreSQL(t *testing.T) {
	rig := newAUTH02CoreFenceRig(t, true, false)
	responseChannel := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responseChannel <- rig.request(http.MethodPost, rig.write, "auth02-core-push-race-0001", "business-push-race")
	}()
	select {
	case invocation := <-rig.gate.entered:
		if invocation.Operation != openplatformport.OperationCorePushRecord || !invocation.Principal.HasCapability("audience.push.write") {
			t.Fatalf("request was not admitted with pre-revoke grant: %+v", invocation)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CorePush did not reach the post-auth operation gate")
	}
	if got := rig.counts(t); got != (auth02CoreFenceCounts{}) {
		t.Fatalf("pre-fence Segment effects=%+v", got)
	}
	if err := rig.revokePushGrant(); err != nil {
		t.Fatal(err)
	}
	close(rig.gate.resume)
	select {
	case response := <-responseChannel:
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication"`) {
			t.Fatalf("in-flight stale CorePush status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight CorePush did not finish after release")
	}
	if got := rig.counts(t); got != (auth02CoreFenceCounts{}) {
		t.Fatalf("revocation-first CorePush left Segment owner effects=%+v", got)
	}
}

func TestAUTH02CorePushOwnerFenceMakesRevocationWaitUntilCommitPostgreSQL(t *testing.T) {
	rig := newAUTH02CoreFenceRig(t, false, true)
	defer rig.writeUOW.releaseWrite()
	requestChannel := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		requestChannel <- rig.request(http.MethodPost, rig.write, "auth02-core-push-lock-0001", "business-push-lock")
	}()
	select {
	case <-rig.writeUOW.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("CorePush did not finish owner writes before its transaction boundary")
	}
	if uncommitted := rig.counts(t); uncommitted != (auth02CoreFenceCounts{}) {
		t.Fatalf("uncommitted Segment writes leaked to another connection: %+v", uncommitted)
	}

	revokeStarted := make(chan struct{})
	revokeDone := make(chan error, 1)
	go func() { close(revokeStarted); revokeDone <- rig.revokePushGrant() }()
	<-revokeStarted
	waitForAUTH02MachineRowLockWaits(t, rig, 1)
	select {
	case err := <-revokeDone:
		t.Fatalf("Access revocation completed while Segment owner transaction held the client row: %v", err)
	default:
	}

	// PostgreSQL's ordinary SELECT remains compatible with FOR SHARE while the
	// write transaction is open. The HTTP machine GET path uses Access's
	// existing FOR UPDATE/last_used write and may wait on this fence.
	readCtx, cancelRead := context.WithTimeout(rig.ctx, time.Second)
	var enabled bool
	if err := rig.native.QueryRow(readCtx, `SELECT enabled FROM access_machine_clients WHERE client_id=$1`, rig.clientID).Scan(&enabled); err != nil || !enabled {
		cancelRead()
		t.Fatalf("ordinary credential SELECT blocked or failed: enabled=%v err=%v", enabled, err)
	}
	cancelRead()
	getDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "https://crm.example.test/open/v1/capabilities", nil)
		request.Header.Set("Authorization", "Bearer "+rig.read)
		request.RemoteAddr = "203.0.113.50:443"
		request.TLS = &tlsState
		response := httptest.NewRecorder()
		rig.handler.Routes().ServeHTTP(response, request)
		getDone <- response
	}()
	waitForAUTH02MachineRowLockWaits(t, rig, 2)
	select {
	case response := <-getDone:
		t.Fatalf("machine GET unexpectedly passed Access's conflicting FOR UPDATE before the writer released its fence: status=%d body=%s", response.Code, response.Body.String())
	default:
	}

	rig.writeUOW.releaseWrite()
	select {
	case response := <-requestChannel:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "business-push-lock") {
			t.Fatalf("write-first CorePush status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CorePush did not commit after fence release")
	}
	select {
	case err := <-revokeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Access revocation did not finish after CorePush committed")
	}
	select {
	case response := <-getDone:
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusOK {
			t.Fatalf("machine GET status after fence release=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("machine GET did not leave the existing Access row-lock wait")
	}
	if committed := rig.counts(t); committed != (auth02CoreFenceCounts{pushes: 1, receipts: 1, facts: 1, outbox: 1}) {
		t.Fatalf("write-first committed Segment owner effects=%+v", committed)
	}
}

func TestAUTH02WorkbenchAIReplayRevalidatesAccessInOwnerUnitOfWorkPostgreSQL(t *testing.T) {
	rig := newAUTH02CoreFenceRig(t, false, false)
	clientID := "auth02-workbench-fence"
	issued, err := rig.machine.CreateV1(rig.ctx, rig.admin, accessapp.CreateMachineClientInput{
		ClientID: clientID, DisplayName: "AUTH02 Workbench synthetic machine", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"write"},
		Capabilities: []string{"ai.review_plan.create", "ai.workbench.package.create"},
		OwnerScope:   accessdomain.OwnerScope{"corp_id": {"open-platform-v1"}, "customer_id": {"7"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rig.machine.Activate(rig.ctx, rig.admin, clientID, issued.Secret, true); err != nil {
		t.Fatal(err)
	}
	token, err := rig.machine.IssueClientCredentialsToken(rig.ctx, accessapp.ClientCredentialsInput{ClientID: clientID, ClientSecret: issued.Secret, Audience: "external_integration", RequestedScopes: []string{"write"}, SourceIP: netip.MustParseAddr("203.0.113.50")})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := rig.machine.AuthenticateBearer(rig.ctx, token.AccessToken, "external_integration", netip.MustParseAddr("203.0.113.50"))
	if err != nil {
		t.Fatal(err)
	}

	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 7}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("open-platform-v1", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.scopes.SurveyUnionScopes = []string{"wechat-open-platform:shared"}
	executor.contacts = workbenchContactStub{rows: []wecomport.MachineContactRow{{CustomerID: 7, OwnerUserID: "staff-1", IdentityStatus: "resolved", BindingStatus: "bound"}}}
	executor.contactStatuses = workbenchStatusStub{}
	executor.contactStaff = workbenchStaffStub{}
	executor.workbenchUnions = &workbenchUnionStub{verified: true}
	ai := &v1AIMachineStub{create: aiassistantport.MachineCreatePlanResult{Plan: aiassistantport.MachinePlan{ID: 42, ReviewState: aiassistantport.ReviewPending}}}
	if err = executor.BindV1AI(ai, ai, rig.uow); err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1OperationAudit(accessstore.NewPostgreSQL(), rig.uow); err != nil {
		t.Fatal(err)
	}
	if err = executor.BindV1MachineMutationFence(rig.machine, rig.uow); err != nil {
		t.Fatal(err)
	}
	pkg := aiassistantport.MachinePackageMetadata{AudiencePackageID: "aud-1", AudienceVersion: "v1", CopyPackageID: "copy-1", CopyVersion: "v1", StrategyVersion: "v1", ProductFactVersion: "v1", SourceFingerprint: string(effectport.Hash("auth02-workbench")), ApprovalRevision: "rev-1", ClientReference: "auth02-1", MemberCount: 1}
	raw, err := json.Marshal(map[string]any{
		"name": "synthetic workbench review", "package": pkg,
		"members": []any{map[string]any{"union_id": "synthetic-union-7", "owner_userid": "staff-1"}},
		"content": []any{map[string]any{"kind": "text", "text": "review only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	invocation := openplatformport.Invocation{Operation: openplatformport.OperationAIReviewPlanCreate, Principal: principal, IdempotencyKey: "auth02-workbench-replay-0001", RequestID: "auth02-workbench-first", Input: raw}
	if _, err = executor.Invoke(rig.ctx, invocation); err != nil || ai.createCalls != 1 {
		t.Fatalf("initial workbench create calls=%d err=%v", ai.createCalls, err)
	}

	remaining := []string{"operation.read"}
	if _, err = rig.machine.PatchV1(rig.ctx, rig.admin, clientID, accessapp.PatchMachineClientInput{Capabilities: &remaining}); err != nil {
		t.Fatal(err)
	}
	// Reuse the exact admitted bearer principal and the same idempotency key to
	// reach the transactional Access fence after revocation has committed.
	invocation.RequestID = "auth02-workbench-replay-after-revoke"
	_, err = executor.Invoke(rig.ctx, invocation)
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorAuthentication || ai.createCalls != 1 {
		t.Fatalf("revoked workbench replay err=%v intake_calls=%d", err, ai.createCalls)
	}
}
