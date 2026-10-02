package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/crmcli"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/zalando/go-keyring"
)

// Real CLI -> TLS -> composition -> Access/Identity/Customer -> PostgreSQL.
// Only the OS keyring is replaced. The fixture has no enabled Provider writes.
func TestCLIPostgreSQLAuthenticationCustomerAndGrantRevocationJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.RawStdEncoding.EncodeToString(dataKey)
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin,
		ReleaseSHA: "cli-postgres-fixture", WorkerOwner: "cli-postgres-fixture", WorkerLimit: 1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "cli-postgres-fixture-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "cli-fixture"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: "01234567890123456789012345678901"},
		Survey:       platformconfig.Survey{DataKey: encodedKey, IdentityPhoneDataKey: encodedKey, OAuthOpenPlatformID: "cli-fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	_, created, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "cli-owner", Password: "cli-owner-fixture-password", DisplayName: "CLI Owner"})
	if err != nil || !created {
		t.Fatalf("bootstrap created=%t err=%v", created, err)
	}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := identitysecure.NewPhoneVault(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}
	customerIDs := make([]int64, 0, 2)
	for _, value := range []string{"cli-authorized-customer", "cli-other-customer"} {
		fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:cli-fixture", Value: value, Source: "cli-verified-fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			result, err := oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
			customerIDs = append(customerIDs, int64(result.CustomerID))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// An identity root alone is not a materialized customer profile. Seed the
	// synthetic read projection just as the directory-sync consumer does.
	for _, id := range customerIDs {
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,updated_at) VALUES($1,'active','CLI synthetic customer',$2,'active','cli-fixture',CURRENT_TIMESTAMP)`, id, fmt.Sprintf("CID-cli-%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	for _, name := range []string{"AICRM_CLI_TOKEN", "AICRM_CLI_PASSWORD", "AICRM_CLI_CLIENT_SECRET"} {
		t.Setenv(name, "")
	}
	var out, stderr bytes.Buffer
	run := func(want int, input string, args ...string) map[string]any {
		t.Helper()
		out.Reset()
		stderr.Reset()
		code := crmcli.Run(args, strings.NewReader(input), &out, &stderr)
		if code != want {
			t.Fatalf("command=%v code=%d want=%d output=%s", args, code, want, out.String())
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("command must return one JSON document: %v", err)
		}
		return result
	}
	run(0, "", "profile", "add", "synthetic", origin)
	run(0, "cli-owner-fixture-password", "auth", "login", "--username", "cli-owner", "--secret-stdin")
	run(0, "", "doctor")
	human, err := crmcli.LoadCredential(origin)
	if err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(t.TempDir(), "issued.json")
	payload := fmt.Sprintf(`{"client_id":"cli.machine","display_name":"CLI synthetic reader","purpose":"external_agent","audiences":["external_integration"],"scopes":["read"],"capabilities":["customer.read","platform.capabilities.read"],"owner_scope":{"customer_id":["%d"]},"allowed_cidrs":[],"token_ttl_seconds":1800}`, customerIDs[0])
	run(0, payload, "invoke", "admin.createOpenPlatformClient", "--input", "-", "--secret-output", secretFile)
	var issued struct {
		Secret string `json:"secret"`
	}
	raw, err := os.ReadFile(secretFile)
	if err != nil || json.Unmarshal(raw, &issued) != nil || issued.Secret == "" {
		t.Fatal("creation did not preserve its one-time secret in the protected file")
	}
	if strings.Contains(out.String(), issued.Secret) {
		t.Fatal("creation exposed secret on stdout")
	}
	activation, _ := json.Marshal(map[string]any{"client_secret": issued.Secret, "copied_confirmed": true})
	run(0, string(activation), "invoke", "admin.activateOpenPlatformClient", "--param", "client_id=cli.machine", "--input", "-")
	run(0, issued.Secret, "auth", "login", "--client-id", "cli.machine", "--secret-stdin")
	machine, err := crmcli.LoadCredential(origin)
	if err != nil {
		t.Fatal(err)
	}
	run(0, "", "doctor")
	resource := fmt.Sprintf(`{"customer_id":%d}`, customerIDs[0])
	run(0, resource, "auth", "can-i", "customer.context.get", "--resource", "-")
	profile := run(0, "", "invoke", "customer.context.get", "--param", fmt.Sprintf("customer_id=%d", customerIDs[0]))
	profileJSON, _ := json.Marshal(profile)
	if !strings.Contains(string(profileJSON), "CLI synthetic customer") {
		t.Fatal("CLI did not read back the authorized customer projection")
	}
	run(4, fmt.Sprintf(`{"customer_id":%d}`, customerIDs[1]), "auth", "can-i", "customer.context.get", "--resource", "-")
	outside := run(1, "", "invoke", "customer.context.get", "--param", fmt.Sprintf("customer_id=%d", customerIDs[1]))
	if outside["error"].(map[string]any)["http_status"] != float64(http.StatusNotFound) || outside["data"] != nil {
		t.Fatal("out-of-scope customer must preserve the Owner's existence-hiding 404")
	}
	// Querying can-i never converts an independently authenticated machine into
	// the human owner, even if that machine knows an admin operation ID.
	run(4, "", "invoke", "admin.listOpenPlatformClients")
	if err = crmcli.StoreCredential(human); err != nil {
		t.Fatal(err)
	}
	run(0, `{"capabilities":["platform.capabilities.read"]}`, "invoke", "admin.patchOpenPlatformClient", "--param", "client_id=cli.machine", "--input", "-")
	audit := run(0, "", "invoke", "admin.listOpenPlatformClientAudit", "--param", "client_id=cli.machine")
	auditJSON, _ := json.Marshal(audit)
	if !strings.Contains(string(auditJSON), "machine_client_grants_updated") {
		t.Fatal("grant change has no owner audit readback")
	}
	if err = crmcli.StoreCredential(machine); err != nil {
		t.Fatal(err)
	}
	run(3, "", "auth", "whoami") // Old bearer revoked after the real transaction.
	run(0, issued.Secret, "auth", "login", "--client-id", "cli.machine", "--secret-stdin")
	run(4, resource, "auth", "can-i", "customer.context.get", "--resource", "-")
	run(4, "", "invoke", "customer.context.get", "--param", fmt.Sprintf("customer_id=%d", customerIDs[0]))
	if err = crmcli.StoreCredential(human); err != nil {
		t.Fatal(err)
	}
	run(0, "", "auth", "logout")
	run(3, "", "auth", "whoami")
}
