package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
)

// TestComposedV1RouterRetiresEveryInventoryPathBeforeOtherOwners uses the same
// outer mount ordering as composeApplication. The inner router deliberately
// answers every request so this proves retired machine paths cannot slip into
// an existing V3 GroupOps or OperationCycle owner after Open's route change.
func TestComposedV1RouterRetiresEveryInventoryPathBeforeOtherOwners(t *testing.T) {
	owner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Test-Owner", "existing-v3")
		writer.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{}
	base, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		owner, owner, owner, owner, owner, owner,
		owner, owner, owner, owner, owner, owner, owner, owner,
		owner, owner, owner, owner, owner, owner, owner, owner,
		owner, owner, authentication, "https://crm.example.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	machine := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Test-Owner", "machine")
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := openplatformhttp.Mount(base, machine)
	handler = mountMemberGridUI(handler, owner)
	handler, err = mountSegmentAPI(handler, owner)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = mountAutomationRuntimeAPI(handler, owner)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = mountSegmentWebhook(handler, owner)
	if err != nil {
		t.Fatal(err)
	}
	handler = mountAIAssistant(handler, owner, owner, authentication, true, "https://crm.example.test")
	handler = mountSurveyUI(handler, owner, owner, authentication)
	handler = mountOrderUI(handler, owner, authentication)
	handler = mountHXCUI(handler, owner, authentication)
	handler = mountChannelUI(handler, owner, authentication)
	handler = mountRadar(handler, owner, owner, authentication)
	handler = mountPublicProduct(handler, owner)
	handler = mountPublicServicePeriod(handler, owner)
	handler = mountPublicCoupon(handler, owner)
	handler = securityHeaders(handler)
	handler, err = mountMessageArchive(handler, owner)
	if err != nil {
		t.Fatal(err)
	}

	validJWT := issuedOpenPlatformJWT(t)
	for _, route := range openplatformhttp.Inventory {
		if route.Path == "/mcp" { // retained MCP transport is not a retired path.
			continue
		}
		path := concreteInventoryPath(route.Path)
		for _, authorization := range []string{"", "Bearer retained-legacy-credential", "Bearer " + validJWT} {
			request := httptest.NewRequest(route.Method, "https://crm.example.test"+path, nil)
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound || response.Header().Get("X-Test-Owner") != "" {
				t.Fatalf("%s %s authorization=%q code=%d owner=%q body=%q", route.Method, path, authorization, response.Code, response.Header().Get("X-Test-Owner"), response.Body.String())
			}
		}
	}
}

func TestPublicProductMediaRouteReachesProductOwner(t *testing.T) {
	owner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Test-Owner", "product")
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := mountPublicProduct(http.NotFoundHandler(), owner)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/product-images/course-9/88/variants/original", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("X-Test-Owner") != "product" {
		t.Fatalf("status=%d owner=%q", response.Code, response.Header().Get("X-Test-Owner"))
	}
}

func concreteInventoryPath(path string) string {
	replacements := strings.NewReplacer(
		"{package_key}", "package-1",
		"{order_no}", "order-1",
		"{campaign_code}", "campaign-1",
		"{preparation_id}", "preparation-1",
		"{package_id}", "1",
		"{subscription_id}", "1",
		"{request_id}", "request-1",
		"{strategy_key}", "strategy-1",
	)
	return replacements.Replace(path)
}

// issuedOpenPlatformJWT uses the Access issuer and verifier, rather than a
// JWT-shaped marker. Retired paths are denied before this credential is
// consumed, which is exactly the boundary this outer-composition test proves.
func issuedOpenPlatformJWT(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	repository := &v1RouteMachineRepository{clients: map[string]accessdomain.MachineClient{}}
	service, err := accessapp.NewMachineService(repository, v1RouteUnitOfWork{}, credential.PasswordHasher{}, accessapp.MachineConfig{
		SigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	issuedClient, err := service.Create(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "route-proof", DisplayName: "Route proof", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"external_read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Activate(ctx, admin, issuedClient.Client.ClientID, issuedClient.Secret, true); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{
		ClientID: issuedClient.Client.ClientID, ClientSecret: issuedClient.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.9"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateBearer(ctx, issued.AccessToken, "external_integration", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("issued test bearer must verify through Access: %v", err)
	}
	return issued.AccessToken
}

type v1RouteUnitOfWork struct{}

func (v1RouteUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type v1RouteMachineRepository struct {
	clients map[string]accessdomain.MachineClient
	audits  []accessdomain.MachineAudit
}

func (repository *v1RouteMachineRepository) MachineClientByID(_ context.Context, clientID string, _ bool) (accessdomain.MachineClient, error) {
	client, ok := repository.clients[clientID]
	if !ok {
		return accessdomain.MachineClient{}, accessdomain.ErrNotFound
	}
	return client, nil
}
func (repository *v1RouteMachineRepository) ListMachineClients(context.Context) ([]accessdomain.MachineClient, error) {
	clients := make([]accessdomain.MachineClient, 0, len(repository.clients))
	for _, client := range repository.clients {
		clients = append(clients, client)
	}
	return clients, nil
}
func (repository *v1RouteMachineRepository) CreateMachineClient(_ context.Context, client accessdomain.MachineClient) (accessdomain.MachineClient, error) {
	if _, exists := repository.clients[client.ClientID]; exists {
		return accessdomain.MachineClient{}, accessdomain.ErrConflict
	}
	client.ID = int64(len(repository.clients) + 1)
	repository.clients[client.ClientID] = client
	return client, nil
}
func (repository *v1RouteMachineRepository) ReplaceMachineClient(_ context.Context, client accessdomain.MachineClient) error {
	if _, exists := repository.clients[client.ClientID]; !exists {
		return accessdomain.ErrNotFound
	}
	repository.clients[client.ClientID] = client
	return nil
}
func (repository *v1RouteMachineRepository) SetMachineClientLastUsed(_ context.Context, clientID int64, used time.Time) error {
	for key, client := range repository.clients {
		if client.ID == clientID {
			client.LastUsedAt = &used
			repository.clients[key] = client
			return nil
		}
	}
	return accessdomain.ErrNotFound
}
func (repository *v1RouteMachineRepository) AppendMachineAudit(_ context.Context, audit accessdomain.MachineAudit) error {
	repository.audits = append(repository.audits, audit)
	return nil
}

func (repository *v1RouteMachineRepository) ListMachineAudit(_ context.Context, clientID int64, limit int) ([]accessport.MachineAuditEntry, error) {
	items := make([]accessport.MachineAuditEntry, 0)
	for index := len(repository.audits) - 1; index >= 0 && len(items) < limit; index-- {
		audit := repository.audits[index]
		if audit.MachineClientID != clientID {
			continue
		}
		items = append(items, accessport.MachineAuditEntry{ActorAdminUserID: audit.ActorAdminID, Action: audit.Action, Outcome: audit.Outcome, Details: append([]byte(nil), audit.Details...), CreatedAt: audit.CreatedAt})
	}
	return items, nil
}

var _ accessport.MachineRepository = (*v1RouteMachineRepository)(nil)
