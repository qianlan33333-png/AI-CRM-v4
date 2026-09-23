package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	hxcdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// TestPostgreSQLMemberGridCompositionHTTP uses the same composition-root ports
// as the runtime. The only Product fake is its local workspace authorization;
// membership, customer display names, and the pinned HXC generation all read
// from their real PostgreSQL Owner stores. The matching HXC member is placed
// beyond Order's first 200-row candidate page.
func TestPostgreSQLMemberGridCompositionHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	entitlements, err := orderapp.NewEntitlementApplication(uow, orders)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	var targetCustomer, targetEntitlement int64
	for index := 0; index <= 200; index++ {
		var customerID int64
		if err = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("候选 %03d", index)
		endAt := snapshot.Add(30 * 24 * time.Hour)
		if index == 200 {
			name = "第二页 HXC 会员"
			endAt = snapshot.Add(24 * time.Hour)
			targetCustomer = customerID
		}
		if _, err = pool.Exec(ctx, `INSERT INTO customer_directory_projection(
			customer_id,customer_status,display_name,avatar_url,gender,contact_type,corp_name,
			oneid_label,phone_masked,activation_status,source,source_version,last_synced_at,updated_at
		) VALUES($1,'active',$2,'',0,0,'','','','active','member-grid-pg',1,$3,$3)`, customerID, name, snapshot); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("member-grid-composition-%d", index)))
		var entitlementID int64
		if err = pool.QueryRow(ctx, `INSERT INTO order_service_entitlements(
			source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at
		) VALUES('member-grid-pg',$1,$2,991,'真实组合会员','active',$3,$4,'',$5,$3,$3) RETURNING id`,
			fmt.Sprintf("member-grid-composition-%03d", index), customerID, snapshot.Add(-24*time.Hour), endAt, digest[:]).Scan(&entitlementID); err != nil {
			t.Fatal(err)
		}
		if index == 200 {
			targetEntitlement = entitlementID
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE order_service_entitlements SET alliance='联盟甲' WHERE id=$1`, targetEntitlement); err != nil {
		t.Fatal(err)
	}

	hxc := hxcstore.NewPostgreSQL(pool)
	memberGridPublishHXC(t, ctx, pool, uow, hxc, targetCustomer, snapshot)
	reader := &memberGridPGEntitlements{delegate: entitlements, targetID: targetEntitlement}
	handler, err := producthttp.NewHandler(memberGridPGCatalog{}, memberGridPGLifecycle{}, memberGridPGService{}, memberGridPGExternal{}, memberGridPGSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberWorkspace(memberGridPGWorkspace{}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberReaders(reader, orderCustomerDisplayNameAdapter{uow: uow, reader: customerstore.NewPostgreSQL()}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberSharedFacts(hxc); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/service-period-products/991/member-grid/query", strings.NewReader(`{
		"config":{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"formally_logged_in","operator":"in","value":["yes"]},{"field":"alliance","operator":"contains","value":"联盟"}]},"sorts":[{"field":"member","direction":"asc"}],"groups":[{"field":"formally_logged_in","direction":"asc"}]},
		"limit":20
	}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("member-grid HTTP=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Rows []struct {
			RecordID string `json:"record_id"`
			Values   struct {
				Member struct {
					Primary string `json:"primary"`
				} `json:"member"`
				Formal   string  `json:"formally_logged_in"`
				Alliance *string `json:"alliance"`
			} `json:"values"`
			GroupPath []struct {
				Count int64 `json:"count"`
			} `json:"group_path"`
		} `json:"rows"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Rows) != 1 || response.Rows[0].RecordID == "" || response.Rows[0].Values.Member.Primary != "第二页 HXC 会员" || response.Rows[0].Values.Formal != "yes" || response.Rows[0].Values.Alliance == nil || *response.Rows[0].Values.Alliance != "联盟甲" || len(response.Rows[0].GroupPath) != 1 || response.Rows[0].GroupPath[0].Count != 1 {
		t.Fatalf("member-grid response=%s", recorder.Body.String())
	}
	if reader.calls < 2 || reader.targetPage < 2 {
		t.Fatalf("full Order relation was not read before filtering: calls=%d targetPage=%d", reader.calls, reader.targetPage)
	}
}

func memberGridPublishHXC(t *testing.T, ctx context.Context, pool *pgxpool.Pool, uow *platformpostgres.UnitOfWork, store *hxcstore.PostgreSQL, customerID int64, asOf time.Time) {
	t.Helper()
	requestDigest := sha256.Sum256([]byte("member-grid-postgres-shared-facts"))
	var runID int64
	if err := pool.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(
		run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count
	) VALUES('member-grid-postgres-shared-facts',$1,'initial','apply','publishing',1,1,1) RETURNING id`, requestDigest[:]).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	current, total := int64(1), int64(2)
	projection := hxcdomain.Projection{
		AsOf: asOf, SharedFactsAvailable: true,
		Counts: hxcdomain.Counts{Total: 1, RegisteredNoActiveMembership: 1, Matched: 1, MatchedByUnionID: 1},
		Rows: []hxcdomain.ProjectionRow{{
			SubjectDigest: [32]byte{1}, UserRef: "HXC-000000000001", Stage: hxcdomain.RegisteredNoActiveMembership,
			SourceRow: hxcdomain.SourceRow{
				MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), SourceUpdatedAt: asOf,
				FormallyLoggedIn: true, HasTokenUsage: true, LearningPlanFound: true, LearningPlanCurrent: &current, LearningPlanTotal: &total,
			},
			CustomerID: customerdomain.CustomerID(customerID), IdentityState: hxcdomain.Matched, MatchedBy: "unionid", IdentityReasonCode: "matched_unionid",
		}},
	}
	if err := uow.Within(ctx, func(tx context.Context) error {
		_, publishErr := store.Publish(tx, runID, projection)
		return publishErr
	}); err != nil {
		t.Fatal(err)
	}
}

type memberGridPGEntitlements struct {
	delegate   orderport.EntitlementService
	targetID   int64
	calls      int
	targetPage int
}

func (r *memberGridPGEntitlements) ListServicePeriodMembers(ctx context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	page, err := r.delegate.ListServicePeriodMembers(ctx, query)
	if err != nil {
		return page, err
	}
	r.calls++
	for _, item := range page.Items {
		if item.ID == r.targetID {
			r.targetPage = r.calls
		}
	}
	return page, nil
}
func (r *memberGridPGEntitlements) ListCustomerEntitlements(ctx context.Context, customerID int64, limit int32) (orderport.EntitlementPage, error) {
	return r.delegate.ListCustomerEntitlements(ctx, customerID, limit)
}
func (r *memberGridPGEntitlements) GetCustomerServicePeriodEntitlement(ctx context.Context, customerID, serviceProductID int64) (orderport.Entitlement, bool, error) {
	return r.delegate.GetCustomerServicePeriodEntitlement(ctx, customerID, serviceProductID)
}
func (r *memberGridPGEntitlements) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	return r.delegate.UpdateEntitlementRemark(ctx, command)
}
func (r *memberGridPGEntitlements) UpdateEntitlementAlliance(ctx context.Context, command orderport.AllianceCommand) (orderport.Entitlement, error) {
	return r.delegate.UpdateEntitlementAlliance(ctx, command)
}

type memberGridPGSecurity struct{}

func (memberGridPGSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (memberGridPGSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return memberGridPGSecurity{}.Authenticate(context.Background(), nil)
}

type memberGridPGCatalog struct{}

func (memberGridPGCatalog) List(context.Context, string, int32) (productport.Page, error) {
	return productport.Page{}, nil
}
func (memberGridPGCatalog) Get(context.Context, productport.ID) (productport.Product, error) {
	return productport.Product{}, nil
}
func (memberGridPGCatalog) Create(context.Context, productport.CreateCommand) (productport.Product, error) {
	return productport.Product{}, nil
}
func (memberGridPGCatalog) Update(context.Context, productport.UpdateCommand) (productport.Product, error) {
	return productport.Product{}, nil
}

type memberGridPGLifecycle struct{}

func (memberGridPGLifecycle) SetLocalProductEnabled(context.Context, productport.SetLocalProductEnabledCommand) (productport.LocalProduct, error) {
	return productport.LocalProduct{}, nil
}
func (memberGridPGLifecycle) CopyLocalProduct(context.Context, productport.CopyLocalProductCommand) (productport.LocalProduct, error) {
	return productport.LocalProduct{}, nil
}
func (memberGridPGLifecycle) ArchiveLocalProduct(context.Context, productport.ArchiveLocalProductCommand) (productport.LocalProduct, error) {
	return productport.LocalProduct{}, nil
}
func (memberGridPGLifecycle) DeleteLocalProduct(context.Context, productport.DeleteLocalProductCommand) (productport.DeleteLocalProductResult, error) {
	return productport.DeleteLocalProductResult{}, nil
}
func (memberGridPGLifecycle) ShareLocalProduct(context.Context, productport.ID) (productport.LocalProductShare, error) {
	return productport.LocalProductShare{}, nil
}

type memberGridPGService struct{}

func (memberGridPGService) ListServicePeriodProducts(context.Context, int32, int32) (productport.ServicePeriodPage, error) {
	return productport.ServicePeriodPage{}, nil
}
func (memberGridPGService) GetServicePeriodProduct(context.Context, productport.ID) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}
func (memberGridPGService) CreateServicePeriodProduct(context.Context, productport.CreateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}
func (memberGridPGService) UpdateServicePeriodProduct(context.Context, productport.UpdateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}
func (memberGridPGService) SetServicePeriodProductEnabled(context.Context, productport.SetServicePeriodProductEnabledCommand) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}
func (memberGridPGService) CopyServicePeriodProduct(context.Context, productport.CopyServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}
func (memberGridPGService) ArchiveServicePeriodProduct(context.Context, productport.ArchiveServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return productport.ServicePeriodProduct{}, nil
}

type memberGridPGExternal struct{}

func (memberGridPGExternal) GetExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return productport.ExternalPushConfiguration{}, nil
}
func (memberGridPGExternal) SaveExternalPushConfiguration(context.Context, productport.SaveExternalPushConfigurationCommand) (productport.ExternalPushConfiguration, error) {
	return productport.ExternalPushConfiguration{}, nil
}
func (memberGridPGExternal) QueueExternalPushTest(context.Context, productport.QueueExternalPushTestCommand) (productport.ExternalPushTest, error) {
	return productport.ExternalPushTest{}, nil
}
func (memberGridPGExternal) ListExternalPushTests(context.Context, productport.ID, productport.ExternalPushProductKind) ([]productport.ExternalPushTest, error) {
	return nil, nil
}

type memberGridPGWorkspace struct{}

func (memberGridPGWorkspace) Access(context.Context, productport.ID, productport.MemberGridActor) (productport.MemberGridAccess, error) {
	return productport.MemberGridAccess{CanView: true}, nil
}
func (memberGridPGWorkspace) ListViews(context.Context, productport.ID) ([]productport.MemberGridView, error) {
	return nil, nil
}
func (memberGridPGWorkspace) CreateView(context.Context, productport.CreateMemberGridViewCommand) (productport.MemberGridView, error) {
	return productport.MemberGridView{}, nil
}
func (memberGridPGWorkspace) UpdateView(context.Context, productport.UpdateMemberGridViewCommand) (productport.MemberGridView, error) {
	return productport.MemberGridView{}, nil
}
func (memberGridPGWorkspace) DeleteView(context.Context, productport.DeleteMemberGridViewCommand) (productport.MemberGridView, error) {
	return productport.MemberGridView{}, nil
}
func (memberGridPGWorkspace) ListCollaborators(context.Context, productport.ID) ([]productport.MemberGridCollaborator, error) {
	return nil, nil
}
func (memberGridPGWorkspace) CreateCollaborator(context.Context, productport.CreateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	return productport.MemberGridCollaborator{}, nil
}
func (memberGridPGWorkspace) UpdateCollaborator(context.Context, productport.UpdateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	return productport.MemberGridCollaborator{}, nil
}
func (memberGridPGWorkspace) DeleteCollaborator(context.Context, productport.DeleteMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	return productport.MemberGridCollaborator{}, nil
}
func (memberGridPGWorkspace) Share(context.Context, productport.ID) (productport.MemberGridShare, error) {
	return productport.MemberGridShare{}, nil
}
func (memberGridPGWorkspace) SetShare(context.Context, productport.SetMemberGridShareCommand) (productport.MemberGridShare, bool, error) {
	return productport.MemberGridShare{}, false, nil
}
func (memberGridPGWorkspace) ResolveShare(context.Context, string) (productport.MemberGridShare, error) {
	return productport.MemberGridShare{}, nil
}
