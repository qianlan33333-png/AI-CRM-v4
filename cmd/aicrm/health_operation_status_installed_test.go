package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Fixture seeds and completion simulation use isolated PG; all operation
// reads, token authentication and scope enforcement reach the installed app.
func TestHealthOperationStatusInstalled(t *testing.T) {
	if testconfig.InstalledSurvey().Binary == "" {
		t.Fatal("required operation snapshot journey needs exact installed package inputs")
	}
	f := newPublicSurveyPresentationChromiumFixture(t)
	runHealthOperationHTTPContract(t, healthOperationHTTPFixture{ctx: f.ctx, native: f.native, server: f.server}, "installed-health-corp")
}

type healthOperationHTTPFixture struct {
	ctx    context.Context
	native *pgxpool.Pool
	server *httptest.Server
}

func TestHealthOperationStatusCompositionPostgreSQL(t *testing.T) {
	f := newAdminOverviewFixture(t)
	runHealthOperationHTTPContract(t, healthOperationHTTPFixture{ctx: f.ctx, native: f.application.pool.Native(), server: f.server}, "")
}
func runHealthOperationHTTPContract(t *testing.T, f healthOperationHTTPFixture, corpID string) {
	t.Helper()
	wrapped, e := postgres.Wrap(f.native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := postgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	service, e := accessapp.NewMachineService(accessstore.NewPostgreSQL(), uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), CorpID: corpID})
	if e != nil {
		t.Fatal(e)
	}
	var adminID, customerID int64
	if e = f.native.QueryRow(f.ctx, `SELECT admin_user_id FROM admin_user_roles WHERE role_code='super_admin' LIMIT 1`).Scan(&adminID); e != nil {
		t.Fatal(e)
	}
	if e = f.native.QueryRow(f.ctx, `SELECT id FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); e != nil {
		t.Fatal(e)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	create := func(id string) accessapp.IssuedMachineClient {
		t.Helper()
		issued, e := service.CreateV1(f.ctx, admin, accessapp.CreateMachineClientInput{ClientID: id, DisplayName: "Synthetic installed snapshot", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"operation.read"}, OwnerScope: accessdomain.OwnerScope{"customer_id": []string{strconv.FormatInt(customerID, 10)}}})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = service.Activate(f.ctx, admin, id, issued.Secret, true); e != nil {
			t.Fatal(e)
		}
		return issued
	}
	issue := func(c accessapp.IssuedMachineClient) string {
		t.Helper()
		token, e := service.IssueClientCredentialsToken(f.ctx, accessapp.ClientCredentialsInput{ClientID: c.Client.ClientID, ClientSecret: c.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("127.0.0.1")})
		if e != nil {
			t.Fatal(e)
		}
		return token.AccessToken
	}
	owner := create("installed.snapshot.owner")
	other := create("installed.snapshot.other")
	token, foreign := issue(owner), issue(other)
	var planID int64
	if e = f.native.QueryRow(f.ctx, `INSERT INTO ai_assistant_plans(name,source_kind,source_digest,state,target_count,pending_count,approved_count,created_actor_kind,created_actor_ref) VALUES('installed snapshot','open_platform',decode(repeat('01',32),'hex'),'dispatching',1,0,1,'machine','machine:installed.snapshot.owner') RETURNING id`).Scan(&planID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.native.Exec(f.ctx, `INSERT INTO ai_assistant_plan_recipients(plan_id,customer_id,staff_id,review_state,execution_state) VALUES($1,$2,$3,'approved','queued')`, planID, customerID, adminID); e != nil {
		t.Fatal(e)
	}
	endpoint := fmt.Sprintf("/open/v1/operations/%s%d", v1AIReviewPlanOperationPrefix, planID)
	read := func(bearer string, want int) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(f.ctx, http.MethodGet, f.server.URL+endpoint, nil)
		if e != nil {
			t.Fatal(e)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		// The installed fixture's controlled loopback reverse proxy appends its
		// real peer. Include a documentation-only original client so the chain
		// contains an untrusted origin, not just trusted 127.0.0.1 proxy hops.
		// Production's rightmost-untrusted-source rule stays unchanged.
		r.Header.Set("X-Forwarded-For", "203.0.113.50")
		res, e := f.server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if e != nil || res.StatusCode != want {
			t.Fatalf("installed operation status=%d want=%d err=%v", res.StatusCode, want, e)
		}
		return raw
	}
	read("", http.StatusUnauthorized)
	read(foreign, http.StatusNotFound)
	assert := func(raw []byte) {
		t.Helper()
		var body struct {
			Data struct {
				State      string `json:"operation_state"`
				Unknown    int    `json:"outcome_unknown_count"`
				Recipients []struct {
					State     string `json:"execution_state"`
					Accepted  bool   `json:"provider_accepted"`
					Delivered bool   `json:"delivery_proven"`
				} `json:"recipients"`
			} `json:"data"`
		}
		if e = json.Unmarshal(raw, &body); e != nil {
			t.Fatal(e)
		}
		d := body.Data
		if len(d.Recipients) != 1 {
			t.Fatalf("installed recipients=%d", len(d.Recipients))
		}
		r := d.Recipients[0]
		if r.Accepted || r.Delivered {
			t.Fatal("synthetic unknown/queued fact represented as Provider success")
		}
		if !(d.State == "dispatching" && d.Unknown == 0 && r.State == "queued" || d.State == "outcome_unknown" && d.Unknown == 1 && r.State == "outcome_unknown") {
			t.Fatalf("installed mixed aggregate state=%s unknown=%d member=%s", d.State, d.Unknown, r.State)
		}
	}
	assert(read(token, http.StatusOK))
	start := make(chan struct{})
	completed := make(chan error, 1)
	go func() {
		<-start
		for i := 0; i < 24; i++ {
			state, member, count := "dispatching", "queued", 0
			if i%2 == 0 {
				state, member, count = "needs_attention", "outcome_unknown", 1
			}
			if e := uow.Within(f.ctx, func(ctx context.Context) error {
				tx, e := postgres.RequireTransaction(ctx)
				if e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `SELECT id FROM ai_assistant_plans WHERE id=$1 FOR UPDATE`, planID); e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `UPDATE ai_assistant_plans SET state=$2,needs_attention_count=$3,version=version+1 WHERE id=$1`, planID, state, count); e != nil {
					return e
				}
				_, e = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET execution_state=$2,version=version+1 WHERE plan_id=$1`, planID, member)
				return e
			}); e != nil {
				completed <- e
				return
			}
		}
		completed <- nil
	}()
	close(start)
	for i := 0; i < 24; i++ {
		assert(read(token, http.StatusOK))
	}
	if e = <-completed; e != nil {
		t.Fatal(e)
	}
	if _, e = service.PatchV1(f.ctx, admin, owner.Client.ClientID, accessapp.PatchMachineClientInput{OwnerScopeSet: true, OwnerScope: accessdomain.OwnerScope{"customer_id": []string{strconv.FormatInt(customerID+1000, 10)}}}); e != nil {
		t.Fatal(e)
	}
	read(token, http.StatusUnauthorized)
	read(issue(owner), http.StatusForbidden)
	mode := "source composition"
	if testconfig.InstalledSurvey().Binary != "" {
		mode = "installed package"
	}
	t.Log(mode + " operation-status-snapshot: 25 coherent reads during atomic completion simulation; anonymous/foreign actor/revoked token/current scope denied; no Provider writes")
}
