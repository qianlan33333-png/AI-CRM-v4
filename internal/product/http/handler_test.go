package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type testSecurity struct {
	mu        sync.Mutex
	principal accessdomain.Principal
	authErr   error
	csrfErr   error
	authCalls int
	csrfCalls int
}

func (security *testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.mu.Lock()
	defer security.mu.Unlock()
	security.authCalls++
	return security.principal, security.authErr
}

func (security *testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.mu.Lock()
	defer security.mu.Unlock()
	security.csrfCalls++
	return security.principal, security.csrfErr
}

type testCatalog struct {
	page       productport.Page
	product    productport.Product
	listCalls  int
	getCalls   int
	getID      productport.ID
	getCode    string
	createCall *productport.CreateCommand
	updateCall *productport.UpdateCommand
}

func (catalog *testCatalog) List(context.Context, string, int32) (productport.Page, error) {
	catalog.listCalls++
	return catalog.page, nil
}

func (catalog *testCatalog) Get(_ context.Context, id productport.ID) (productport.Product, error) {
	catalog.getCalls++
	catalog.getID = id
	return catalog.product, nil
}

func (catalog *testCatalog) GetByCode(_ context.Context, code string) (productport.Product, error) {
	catalog.getCode = code
	if code != catalog.product.ProductCode {
		return productport.Product{}, productapp.ErrNotFound
	}
	return catalog.product, nil
}

func (catalog *testCatalog) Create(_ context.Context, command productport.CreateCommand) (productport.Product, error) {
	catalog.createCall = &command
	return catalog.product, nil
}

func (catalog *testCatalog) Update(_ context.Context, command productport.UpdateCommand) (productport.Product, error) {
	catalog.updateCall = &command
	return catalog.product, nil
}

type testLifecycle struct {
	local       productport.LocalProduct
	share       productport.LocalProductShare
	shareErr    error
	archiveCall *productport.ArchiveLocalProductCommand
}

func (lifecycle *testLifecycle) SetLocalProductEnabled(context.Context, productport.SetLocalProductEnabledCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) CopyLocalProduct(context.Context, productport.CopyLocalProductCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) ArchiveLocalProduct(_ context.Context, command productport.ArchiveLocalProductCommand) (productport.LocalProduct, error) {
	lifecycle.archiveCall = &command
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) DeleteLocalProduct(context.Context, productport.DeleteLocalProductCommand) (productport.DeleteLocalProductResult, error) {
	return productport.DeleteLocalProductResult{}, nil
}

func (lifecycle *testLifecycle) ShareLocalProduct(context.Context, productport.ID) (productport.LocalProductShare, error) {
	return lifecycle.share, lifecycle.shareErr
}

type testServicePeriod struct {
	page       productport.ServicePeriodPage
	product    productport.ServicePeriodProduct
	createCall *productport.CreateServicePeriodProductCommand
	updateCall *productport.UpdateServicePeriodProductCommand
}

func (service *testServicePeriod) ListServicePeriodProducts(context.Context, int32, int32) (productport.ServicePeriodPage, error) {
	return service.page, nil
}

func (service *testServicePeriod) GetServicePeriodProduct(context.Context, productport.ID) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CreateServicePeriodProduct(_ context.Context, command productport.CreateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	service.createCall = &command
	return service.product, nil
}

func (service *testServicePeriod) UpdateServicePeriodProduct(_ context.Context, command productport.UpdateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	service.updateCall = &command
	return service.product, nil
}

func (service *testServicePeriod) SetServicePeriodProductEnabled(context.Context, productport.SetServicePeriodProductEnabledCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CopyServicePeriodProduct(context.Context, productport.CopyServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) ArchiveServicePeriodProduct(context.Context, productport.ArchiveServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

type testExternalPush struct {
	configuration productport.ExternalPushConfiguration
	test          productport.ExternalPushTest
	save          *productport.SaveExternalPushConfigurationCommand
	saveErr       error
	listCalls     int
	listProductID productport.ID
	listKind      productport.ExternalPushProductKind
	queue         *productport.QueueExternalPushTestCommand
}

type testMemberEntitlements struct {
	mu            sync.Mutex
	page          orderport.ServicePeriodMemberPage
	pages         []orderport.ServicePeriodMemberPage
	queries       []orderport.ServicePeriodMemberQuery
	remarkCmd     *orderport.RemarkCommand
	remarkCalls   int
	allianceCmd   *orderport.AllianceCommand
	allianceCalls int
}

func (stub *testMemberEntitlements) ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error) {
	return orderport.EntitlementPage{}, nil
}
func (stub *testMemberEntitlements) ListServicePeriodMembers(_ context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.queries = append(stub.queries, query)
	page := stub.page
	if len(stub.pages) > 0 {
		pageIndex := 0
		if query.Cursor != "" {
			if _, err := fmt.Sscanf(query.Cursor, "test-member-page-%d", &pageIndex); err != nil || pageIndex < 1 || pageIndex >= len(stub.pages) {
				return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
			}
		}
		page = stub.pages[pageIndex]
		if page.NextCursor == "" && pageIndex+1 < len(stub.pages) {
			page.NextCursor = fmt.Sprintf("test-member-page-%d", pageIndex+1)
		}
	}
	page.Items = append([]orderport.Entitlement(nil), page.Items...)
	if query.GroupByRemainingDays || len(query.GridGroups) > 0 {
		snapshot := query.SnapshotAt
		if snapshot.IsZero() {
			snapshot = time.Now().UTC()
		}
		groups := query.GridGroups
		if len(groups) == 0 {
			groups = []orderport.MemberGridOrder{{Field: "remaining_days", Direction: "asc"}}
		}
		for groupIndex, group := range groups {
			if group.Field != "remaining_days" {
				continue
			}
			counts := map[int]int64{}
			for _, item := range page.Items {
				counts[donorGridRemainingDays(item.EndAt, snapshot)]++
			}
			for index := range page.Items {
				for len(page.Items[index].MemberGridGroupCounts) <= groupIndex {
					page.Items[index].MemberGridGroupCounts = append(page.Items[index].MemberGridGroupCounts, 0)
				}
				page.Items[index].MemberGridGroupCounts[groupIndex] = counts[donorGridRemainingDays(page.Items[index].EndAt, snapshot)]
				if groupIndex == 0 {
					page.Items[index].MemberGridGroupCount = page.Items[index].MemberGridGroupCounts[groupIndex]
				}
			}
		}
	}
	if page.SnapshotAt.IsZero() {
		page.SnapshotAt = query.SnapshotAt
	}
	return page, nil
}
func (stub *testMemberEntitlements) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, nil
}
func (stub *testMemberEntitlements) UpdateEntitlementRemark(_ context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.remarkCalls++
	stub.remarkCmd = &command
	for index := range stub.page.Items {
		item := &stub.page.Items[index]
		if item.ID != command.EntitlementID || item.ServiceProductID != command.ServiceProductID || (command.CustomerID != 0 && item.CustomerID != command.CustomerID) {
			continue
		}
		if item.Version != command.ExpectedVersion {
			return orderport.Entitlement{}, orderport.ErrConflict
		}
		item.Remark = command.Remark
		item.Version++
		item.UpdatedAt = time.Now().UTC()
		return *item, nil
	}
	return orderport.Entitlement{}, orderport.ErrNotFound
}

func (stub *testMemberEntitlements) UpdateEntitlementAlliance(_ context.Context, command orderport.AllianceCommand) (orderport.Entitlement, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.allianceCalls++
	stub.allianceCmd = &command
	for index := range stub.page.Items {
		item := &stub.page.Items[index]
		if item.ID != command.EntitlementID || item.ServiceProductID != command.ServiceProductID {
			continue
		}
		if item.Version != command.ExpectedVersion {
			return orderport.Entitlement{}, orderport.ErrConflict
		}
		alliance := command.Alliance
		item.Alliance = &alliance
		item.Version++
		item.UpdatedAt = time.Now().UTC()
		return *item, nil
	}
	return orderport.Entitlement{}, orderport.ErrNotFound
}

func (stub *testMemberEntitlements) snapshot() (orderport.RemarkCommand, bool, int, []orderport.ServicePeriodMemberQuery) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	queries := append([]orderport.ServicePeriodMemberQuery(nil), stub.queries...)
	if stub.remarkCmd == nil {
		return orderport.RemarkCommand{}, false, stub.remarkCalls, queries
	}
	return *stub.remarkCmd, true, stub.remarkCalls, queries
}

func (stub *testMemberEntitlements) allianceSnapshot() (orderport.AllianceCommand, bool, int) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.allianceCmd == nil {
		return orderport.AllianceCommand{}, false, stub.allianceCalls
	}
	return *stub.allianceCmd, true, stub.allianceCalls
}

type testMemberNames struct{}

func (testMemberNames) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}

// This directory is an Access projection, as production composition supplies.
// The old page submits the stable WeCom user id, never an internal numeric id.
type testMemberStaffDirectory struct{ staff productport.MemberGridStaff }

func (directory testMemberStaffDirectory) ActiveMemberGridStaff(_ context.Context, id int64) (bool, error) {
	return directory.staff.Active && id == directory.staff.AdminUserID, nil
}
func (directory testMemberStaffDirectory) MemberGridStaffByWeComUserID(_ context.Context, value string) (productport.MemberGridStaff, bool, error) {
	return directory.staff, directory.staff.WeComUserID == value, nil
}
func (directory testMemberStaffDirectory) MemberGridStaffByID(_ context.Context, id int64) (productport.MemberGridStaff, bool, error) {
	return directory.staff, directory.staff.AdminUserID == id, nil
}
func (directory testMemberStaffDirectory) ListActiveMemberGridStaff(context.Context) ([]productport.MemberGridStaff, error) {
	if !directory.staff.Active {
		return []productport.MemberGridStaff{}, nil
	}
	return []productport.MemberGridStaff{directory.staff}, nil
}

// The HTTP fixture models the Product-owned workspace port.  It keeps the
// handler test on the real HttpApi surface and deliberately has no Order or
// Customer store access.
type testMemberWorkspace struct {
	mu                      sync.Mutex
	access                  productport.MemberGridAccess
	deniedID                int64
	views                   []productport.MemberGridView
	collaborators           []productport.MemberGridCollaborator
	share                   productport.MemberGridShare
	createViewCalls         int
	updateViewCalls         int
	deleteViewCalls         int
	createCollaboratorCalls int
	updateCollaboratorCalls int
	deleteCollaboratorCalls int
	setShareCalls           int
}

func (s *testMemberWorkspace) Access(_ context.Context, _ productport.ID, actor productport.MemberGridActor) (productport.MemberGridAccess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor.AdminUserID == s.deniedID {
		return productport.MemberGridAccess{}, productapp.ErrNotFound
	}
	return s.access, nil
}
func (s *testMemberWorkspace) ListViews(context.Context, productport.ID) ([]productport.MemberGridView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productport.MemberGridView(nil), s.views...), nil
}
func (s *testMemberWorkspace) CreateView(_ context.Context, c productport.CreateMemberGridViewCommand) (productport.MemberGridView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createViewCalls++
	id := productport.ID(13)
	for _, current := range s.views {
		if current.ID >= id {
			id = current.ID + 1
		}
	}
	v := productport.MemberGridView{ID: id, ProductID: c.ProductID, Name: c.Name, Config: c.Config, Position: int32(len(s.views) + 1), Version: 1, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.views = append(s.views, v)
	return v, nil
}
func (s *testMemberWorkspace) UpdateView(_ context.Context, c productport.UpdateMemberGridViewCommand) (productport.MemberGridView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateViewCalls++
	for index := range s.views {
		view := &s.views[index]
		if view.ID != c.ViewID || view.ProductID != c.ProductID {
			continue
		}
		if view.Version != c.ExpectedVersion {
			return productport.MemberGridView{}, productapp.ErrConflict
		}
		view.Name, view.Config, view.Version, view.UpdatedBy, view.UpdatedAt = c.Name, c.Config, view.Version+1, c.Actor.AdminUserID, time.Now()
		return *view, nil
	}
	return productport.MemberGridView{}, productapp.ErrNotFound
}
func (s *testMemberWorkspace) DeleteView(_ context.Context, c productport.DeleteMemberGridViewCommand) (productport.MemberGridView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteViewCalls++
	for index, view := range s.views {
		if view.ID != c.ViewID || view.ProductID != c.ProductID {
			continue
		}
		if view.Version != c.ExpectedVersion {
			return productport.MemberGridView{}, productapp.ErrConflict
		}
		s.views = append(s.views[:index], s.views[index+1:]...)
		return view, nil
	}
	return productport.MemberGridView{}, productapp.ErrNotFound
}
func (s *testMemberWorkspace) ListCollaborators(context.Context, productport.ID) ([]productport.MemberGridCollaborator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productport.MemberGridCollaborator(nil), s.collaborators...), nil
}
func (s *testMemberWorkspace) CreateCollaborator(_ context.Context, c productport.CreateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCollaboratorCalls++
	id := productport.ID(14)
	for _, current := range s.collaborators {
		if current.ID >= id {
			id = current.ID + 1
		}
	}
	v := productport.MemberGridCollaborator{ID: id, ProductID: c.ProductID, AdminUserID: c.AdminUserID, Permission: c.Permission, Version: 1, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.collaborators = append(s.collaborators, v)
	return v, nil
}
func (s *testMemberWorkspace) UpdateCollaborator(_ context.Context, c productport.UpdateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCollaboratorCalls++
	for index := range s.collaborators {
		item := &s.collaborators[index]
		if item.ID != c.CollaboratorID || item.ProductID != c.ProductID {
			continue
		}
		if item.Version != c.ExpectedVersion {
			return productport.MemberGridCollaborator{}, productapp.ErrConflict
		}
		item.Permission, item.Version, item.UpdatedBy, item.UpdatedAt = c.Permission, item.Version+1, c.Actor.AdminUserID, time.Now()
		return *item, nil
	}
	return productport.MemberGridCollaborator{}, productapp.ErrNotFound
}
func (s *testMemberWorkspace) DeleteCollaborator(_ context.Context, c productport.DeleteMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCollaboratorCalls++
	for index, item := range s.collaborators {
		if item.ID != c.CollaboratorID || item.ProductID != c.ProductID {
			continue
		}
		if item.Version != c.ExpectedVersion {
			return productport.MemberGridCollaborator{}, productapp.ErrConflict
		}
		s.collaborators = append(s.collaborators[:index], s.collaborators[index+1:]...)
		return item, nil
	}
	return productport.MemberGridCollaborator{}, productapp.ErrNotFound
}
func (s *testMemberWorkspace) Share(context.Context, productport.ID) (productport.MemberGridShare, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.share, nil
}
func (s *testMemberWorkspace) SetShare(_ context.Context, c productport.SetMemberGridShareCommand) (productport.MemberGridShare, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setShareCalls++
	s.share = productport.MemberGridShare{ProductID: c.ProductID, Enabled: c.Enabled, Version: c.ExpectedVersion + 1}
	if c.Enabled {
		s.share.PublicID = "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return s.share, c.Enabled, nil
}
func (s *testMemberWorkspace) ResolveShare(_ context.Context, token string) (productport.MemberGridShare, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.share.Enabled || token != s.share.PublicID {
		return productport.MemberGridShare{}, productapp.ErrNotFound
	}
	return s.share, nil
}

type testMemberWorkspaceSnapshot struct {
	Views, Collaborators                                          int
	Share                                                         productport.MemberGridShare
	CreateViews, UpdateViews, DeleteViews                         int
	CreateCollaborators, UpdateCollaborators, DeleteCollaborators int
	SetShares                                                     int
	ViewConfigs                                                   []json.RawMessage
}

func (s *testMemberWorkspace) snapshot() testMemberWorkspaceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	configs := make([]json.RawMessage, 0, len(s.views))
	for _, view := range s.views {
		configs = append(configs, append(json.RawMessage(nil), view.Config...))
	}
	return testMemberWorkspaceSnapshot{
		Views: len(s.views), Collaborators: len(s.collaborators), Share: s.share,
		CreateViews: s.createViewCalls, UpdateViews: s.updateViewCalls, DeleteViews: s.deleteViewCalls,
		CreateCollaborators: s.createCollaboratorCalls, UpdateCollaborators: s.updateCollaboratorCalls, DeleteCollaborators: s.deleteCollaboratorCalls, SetShares: s.setShareCalls,
		ViewConfigs: configs,
	}
}

func (external *testExternalPush) GetExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return external.configuration, nil
}

func (external *testExternalPush) SaveExternalPushConfiguration(_ context.Context, command productport.SaveExternalPushConfigurationCommand) (productport.ExternalPushConfiguration, error) {
	external.save = &command
	if external.saveErr != nil {
		return productport.ExternalPushConfiguration{}, external.saveErr
	}
	configuration := external.configuration
	configuration.ProductID, configuration.ProductKind = command.ProductID, command.ProductKind
	configuration.Enabled, configuration.ConfigurationReference = command.Enabled, command.ConfigurationReference
	if command.BusinessParametersSet {
		configuration.PushType, configuration.Day, configuration.Frequency, configuration.Remark, configuration.CustomParams = command.PushType, command.Day, command.Frequency, command.Remark, command.CustomParams
		configuration.Revision = command.ExpectedRevision + 1
	}
	if configuration.Revision < 1 {
		configuration.Revision = 1
	}
	external.configuration = configuration
	return configuration, nil
}

func (external *testExternalPush) QueueExternalPushTest(_ context.Context, command productport.QueueExternalPushTestCommand) (productport.ExternalPushTest, error) {
	external.queue = &command
	return external.test, nil
}
func (external *testExternalPush) ListExternalPushTests(_ context.Context, id productport.ID, kind productport.ExternalPushProductKind) ([]productport.ExternalPushTest, error) {
	external.listCalls++
	external.listProductID, external.listKind = id, kind
	return []productport.ExternalPushTest{external.test}, nil
}

type testPolicyReader struct {
	policy distributiondomain.Policy
	err    error
	calls  int
}

func (reader *testPolicyReader) ReadProductPolicy(_ context.Context, _ int64, _ distributiondomain.ProductType) (distributiondomain.Policy, error) {
	reader.calls++
	return reader.policy, reader.err
}
func (reader *testPolicyReader) ReadProductPolicyWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error) {
	return reader.policy, reader.err
}

func newHandlerForTest(t *testing.T) (*Handler, *testSecurity, *testCatalog, *testLifecycle) {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	projection := json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	product := productport.Product{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Version: 2, LegacyAdminProjection: projection}
	security := &testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	catalog := &testCatalog{product: product, page: productport.Page{Items: []productport.Product{product}}}
	lifecycle := &testLifecycle{local: productport.LocalProduct{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Lifecycle: productport.LocalProductDraft, Enabled: false, Version: 2}, share: productport.LocalProductShare{ProductID: 7, ProductCode: "p-7", Lifecycle: productport.LocalProductDraft, Available: false, Reason: productappUnavailableReason}}
	service := &testServicePeriod{page: productport.ServicePeriodPage{OK: true, Items: []productport.ServicePeriodProduct{}, Limit: 50, Offset: 0}, product: productport.ServicePeriodProduct{ServiceProductID: 7, ProductCode: "sp-7", Name: "周期七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, AdminProjection: json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`), Lifecycle: productport.ServicePeriodEnabled, Enabled: true, Version: 2, CreatedAt: now, UpdatedAt: now}}
	external := &testExternalPush{configuration: productport.ExternalPushConfiguration{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, Enabled: false, UpdatedAt: now}, test: productport.ExternalPushTest{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_1", State: "accepted", CreatedAt: now}}
	handler, err := NewHandler(catalog, lifecycle, service, external, security)
	if err != nil {
		t.Fatal(err)
	}
	members := &testMemberEntitlements{page: orderport.ServicePeriodMemberPage{Items: []orderport.Entitlement{{ID: 41, CustomerID: 77, ServiceProductID: 7, ProductName: "周期七", Status: "active", StartAt: now.Add(-24 * time.Hour), EndAt: time.Now().UTC().Add(23 * time.Hour), RenewalCount: 0, RenewalCountAvailable: true, Version: 5, UpdatedAt: now}}}}
	if err = handler.SetServicePeriodMemberReaders(members, testMemberNames{}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberWorkspace(&testMemberWorkspace{access: productport.MemberGridAccess{CanView: true, CanEdit: true, CanManageViews: true, CanShare: true}, deniedID: 22}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberStaffDirectory(testMemberStaffDirectory{staff: productport.MemberGridStaff{AdminUserID: 5, WeComUserID: "zhangsan", DisplayName: "张三", Active: true}}); err != nil {
		t.Fatal(err)
	}
	return handler, security, catalog, lifecycle
}

func TestExternalPushTestTimelineReadsStatusAndDoesNotClaimDelivery(t *testing.T) {
	handler, security, _, _ := newHandlerForTest(t)
	external, ok := handler.external.(*testExternalPush)
	if !ok {
		t.Fatal("unexpected external test fixture")
	}
	external.test = productport.ExternalPushTest{
		ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_7", State: "outcome_unknown",
		AttemptCount: 1, ProviderAccepted: false, DeliveryProven: false, RealExternalCallExecuted: true, AutoRetryAllowed: false,
		CreatedAt: time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 6, 4, 1, 0, 0, time.UTC),
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/external-push/test", nil))
	if read.Code != http.StatusOK || security.authCalls != 1 || security.csrfCalls != 0 || external.listCalls != 1 || external.listProductID != 7 || external.listKind != productport.ExternalPushWeChatPay {
		t.Fatalf("read status=%d auth=%d csrf=%d list=%d product=%d kind=%s body=%s", read.Code, security.authCalls, security.csrfCalls, external.listCalls, external.listProductID, external.listKind, read.Body.String())
	}
	var response struct {
		Items []productport.ExternalPushTest `json:"items"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil || len(response.Items) != 1 {
		t.Fatalf("decode=%v body=%s", err, read.Body.String())
	}
	item := response.Items[0]
	if item.State != "outcome_unknown" || item.AttemptCount != 1 || item.ProviderAccepted || item.DeliveryProven || !item.RealExternalCallExecuted || item.AutoRetryAllowed {
		t.Fatalf("unsafe timeline item=%#v", item)
	}

	write := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/products/7/external-push/test", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "external-push-test-http-0001")
	handler.ServeHTTP(write, request)
	if write.Code != http.StatusAccepted || security.csrfCalls != 1 || external.queue == nil || external.queue.ProductID != 7 || external.queue.ProductKind != productport.ExternalPushWeChatPay || external.queue.Actor != 9 || external.queue.IdempotencyKey != "external-push-test-http-0001" {
		t.Fatalf("write status=%d csrf=%d command=%#v body=%s", write.Code, security.csrfCalls, external.queue, write.Body.String())
	}
}

func TestExternalPushConfigurationHTTPPreservesLegacyBusinessJSONAndCAS(t *testing.T) {
	handler, security, _, _ := newHandlerForTest(t)
	external, ok := handler.external.(*testExternalPush)
	if !ok {
		t.Fatal("unexpected external fixture")
	}
	external.configuration = productport.ExternalPushConfiguration{
		ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-7",
		PushType: "member_open", Day: pointerInt64(30), Frequency: pointerInt64(1), Remark: "旧备注", CustomParams: map[string]any{"old": true}, Revision: 3,
		UpdatedAt: time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC),
	}
	body := `{"enabled":true,"webhook_url":"https://push.example.test/legacy","configuration_reference":"product-push-7","type":"ignored_compat_value","push_type":"member_renew","day":45,"frequency":2,"expires_at_ts":2147483647,"remark":"保留业务备注","custom_params":{"count":9007199254740993,"flag":false,"nil":null,"nested":[" 空白 ",{"k":true}]},"expected_revision":3}`
	write := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "product-external-push-http-0001")
	handler.ServeHTTP(write, request)
	if write.Code != http.StatusOK || security.csrfCalls != 1 || external.save == nil {
		t.Fatalf("status=%d csrf=%d command=%#v body=%s", write.Code, security.csrfCalls, external.save, write.Body.String())
	}
	command := *external.save
	want := map[string]any{"count": json.Number("9007199254740993"), "flag": false, "nil": nil, "nested": []any{" 空白 ", map[string]any{"k": true}}}
	if command.URL == nil || *command.URL != "https://push.example.test/legacy" || !command.BusinessParametersSet || command.ExpectedRevision != 3 || command.PushType != "member_renew" || command.Day == nil || *command.Day != 45 || command.Frequency == nil || *command.Frequency != 2 || command.ExpiresAtTS == nil || *command.ExpiresAtTS != 2147483647 || command.Remark != "保留业务备注" || !reflect.DeepEqual(command.CustomParams, want) {
		t.Fatalf("business command=%#v", command)
	}
	var response productport.ExternalPushConfiguration
	decoder := json.NewDecoder(bytes.NewReader(write.Body.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil || response.Revision != 4 || !reflect.DeepEqual(response.CustomParams, want) {
		t.Fatalf("response=%s decoded=%#v err=%v", write.Body.String(), response, err)
	}
	var rawResponse struct {
		CustomParamsJSON string `json:"custom_params_json"`
		PushType         string `json:"push_type"`
	}
	if err := json.Unmarshal(write.Body.Bytes(), &rawResponse); err != nil || rawResponse.CustomParamsJSON == "" || rawResponse.PushType != "member_renew" {
		t.Fatalf("custom_params_json response=%s err=%v", write.Body.String(), err)
	}
	var rawParams map[string]any
	rawDecoder := json.NewDecoder(strings.NewReader(rawResponse.CustomParamsJSON))
	rawDecoder.UseNumber()
	if err := rawDecoder.Decode(&rawParams); err != nil || !reflect.DeepEqual(rawParams, want) {
		t.Fatalf("custom_params_json=%q decoded=%#v err=%v", rawResponse.CustomParamsJSON, rawParams, err)
	}

	legacy := httptest.NewRecorder()
	legacyRequest := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(`{"enabled":false,"configuration_reference":""}`))
	legacyRequest.Header.Set("Content-Type", "application/json")
	legacyRequest.Header.Set("Idempotency-Key", "product-external-push-http-0002")
	handler.ServeHTTP(legacy, legacyRequest)
	if legacy.Code != http.StatusOK || external.save == nil || external.save.BusinessParametersSet || external.save.ExpectedRevision != 0 {
		t.Fatalf("legacy status=%d command=%#v body=%s", legacy.Code, external.save, legacy.Body.String())
	}

	// The frozen editor also submits the key/value-list form. It must preserve
	// a number beyond IEEE-754's safe integer range and accept revision 0 for
	// the first persisted business configuration.
	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"product-push-7","type":"member_open","day":null,"frequency":null,"expires_at_ts":null,"remark":"","custom_params":[{"key":"big","value":9007199254740993},{"key":"nested","value":[1,{"inner":9007199254740993}]}],"expected_revision":0}`))
	firstRequest.Header.Set("Content-Type", "application/json")
	firstRequest.Header.Set("Idempotency-Key", "product-external-push-http-0003")
	handler.ServeHTTP(first, firstRequest)
	wantList := map[string]any{"big": json.Number("9007199254740993"), "nested": []any{json.Number("1"), map[string]any{"inner": json.Number("9007199254740993")}}}
	if first.Code != http.StatusOK || external.save == nil || !external.save.BusinessParametersSet || external.save.ExpectedRevision != 0 || !reflect.DeepEqual(external.save.CustomParams, wantList) {
		t.Fatalf("first-save status=%d command=%#v body=%s", first.Code, external.save, first.Body.String())
	}

	external.saveErr = productapp.ErrConflict
	stale := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"product-push-7","type":"member_open","day":null,"frequency":null,"expires_at_ts":null,"remark":"","custom_params":{},"expected_revision":0}`))
	staleRequest.Header.Set("Content-Type", "application/json")
	staleRequest.Header.Set("Idempotency-Key", "product-external-push-http-0004")
	handler.ServeHTTP(stale, staleRequest)
	external.saveErr = nil
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale first-save status=%d body=%s", stale.Code, stale.Body.String())
	}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"product-push-7","type":"member_open","day":null,"frequency":null,"expires_at_ts":null,"remark":"","custom_params":"not json","expected_revision":4}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidRequest.Header.Set("Idempotency-Key", "product-external-push-http-0005")
	handler.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest || security.csrfCalls != 5 {
		t.Fatalf("invalid status=%d csrf=%d body=%s", invalid.Code, security.csrfCalls, invalid.Body.String())
	}
}

func pointerInt64(value int64) *int64 { return &value }

// Keep the test fixture independent from the application package's internal
// constant while asserting the public blocked sharing contract.
const productappUnavailableReason = "no_authoritative_public_purchase_route"

func TestHandlerUsesOneToOneLimitAndProductListDTO(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=100", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || catalog.listCalls != 1 || security.authCalls != 1 {
		t.Fatalf("status=%d listCalls=%d authCalls=%d body=%s", recorder.Code, catalog.listCalls, security.authCalls, recorder.Body.String())
	}
	var response struct {
		Items      []productport.Product `json:"items"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.NextCursor != "" {
		t.Fatalf("response=%s err=%v", recorder.Body.String(), err)
	}
	tooLarge := httptest.NewRecorder()
	handler.ServeHTTP(tooLarge, httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=101", nil))
	if tooLarge.Code != http.StatusBadRequest || catalog.listCalls != 1 {
		t.Fatalf("limit gate status=%d calls=%d body=%s", tooLarge.Code, catalog.listCalls, tooLarge.Body.String())
	}
}

func TestHandlerDeleteCompatibilityPathReachesLifecycle(t *testing.T) {
	handler, security, _, lifecycle := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/wechat-pay/products/7", strings.NewReader(`{"expected_version":2}`))
	request.Header.Set("Idempotency-Key", "product-delete-00000001")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || lifecycle.archiveCall == nil || lifecycle.archiveCall.ID != 7 || lifecycle.archiveCall.ExpectedVersion != 2 || security.csrfCalls != 1 {
		t.Fatalf("status=%d archive=%+v csrfCalls=%d body=%s", recorder.Code, lifecycle.archiveCall, security.csrfCalls, recorder.Body.String())
	}
}

func TestHandlerShareReturnsPublicRouteOrProductNotEnabled(t *testing.T) {
	handler, _, _, lifecycle := newHandlerForTest(t)
	lifecycle.share = productport.LocalProductShare{ProductID: 7, ProductCode: "p-7", Lifecycle: productport.LocalProductEnabled, Available: true, PurchaseURL: "/p/7"}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/share", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"available":true`) || !strings.Contains(recorder.Body.String(), `"purchase_url":"/p/7"`) {
		t.Fatalf("share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	lifecycle.shareErr = productapp.ErrLocalProductNotEnabled
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/share", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"product_not_enabled"`) {
		t.Fatalf("disabled share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerWriteRequiresAdminCSRFAndMintsCompatibilityKey(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	security.csrfErr = errors.New("missing csrf")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || catalog.createCall != nil || security.csrfCalls != 1 {
		t.Fatalf("status=%d create=%+v csrfCalls=%d", recorder.Code, catalog.createCall, security.csrfCalls)
	}
	security.csrfErr = nil
	request = httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || catalog.createCall == nil || len(catalog.createCall.IdempotencyKey) < 16 {
		t.Fatalf("status=%d create=%+v body=%s", recorder.Code, catalog.createCall, recorder.Body.String())
	}
	projected, err := productapp.ProjectLocalProduct(productport.Product{ID: 8, ProductCode: "p-8", Name: "商品八", Currency: "CNY", Version: 1, CreatedBy: 9, CreatedAt: time.Now(), UpdatedAt: time.Now(), LegacyAdminProjection: catalog.createCall.LegacyAdminProjection})
	if err != nil || projected.Lifecycle != productport.LocalProductEnabled || !projected.Enabled {
		t.Fatalf("new Product default projection=%s local=%+v err=%v", catalog.createCall.LegacyAdminProjection, projected, err)
	}
}

func TestHandlerReturnsTruthfulCompatibilityReads(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	paths := []string{
		"/api/v1/products/7/local-entitlements",
		"/api/admin/service-period-products/7/members?state=all&source=paid_order&limit=100&cursor=opaque",
		"/api/admin/service-period-products/7/member-grid/access",
		"/api/admin/service-period-products/7/member-grid/schema",
		"/api/admin/service-period-products/7/member-views",
		"/api/admin/service-period-products/7/member-grid/share-settings",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/query", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("grid query method gate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFrozenMemberGridHTTPAPISavedViewCollaboratorShareAndRemarkJourney(t *testing.T) {
	handler, security, _, _ := newHandlerForTest(t)
	requestNumber := 0
	adminRequest := func(method, path, body string) *httptest.ResponseRecorder {
		requestNumber++
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method != http.MethodGet {
			r.Header.Set("Idempotency-Key", fmt.Sprintf("grid-http-api-key-%04d", requestNumber))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	viewConfig := `{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"gte","value":1}]},"sorts":[],"groups":[{"field":"remaining_days","direction":"asc"}]}`

	access := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/access", "")
	if access.Code != http.StatusOK || !strings.Contains(access.Body.String(), `"can_manage_views":true`) || !strings.Contains(access.Body.String(), `"can_manage_share":true`) {
		t.Fatalf("old access contract=%d %s", access.Code, access.Body.String())
	}
	schema := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/schema", "")
	if schema.Code != http.StatusOK || !strings.Contains(schema.Body.String(), `"schema_version":1`) || !strings.Contains(schema.Body.String(), `"remaining_days"`) {
		t.Fatalf("old schema contract=%d %s", schema.Code, schema.Body.String())
	}

	createView := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-views", `{"name":"本周","config":`+viewConfig+`}`)
	if createView.Code != http.StatusCreated || !strings.Contains(createView.Body.String(), `"id":"13"`) || !strings.Contains(createView.Body.String(), `"version":1`) || !strings.Contains(createView.Body.String(), `"config"`) {
		t.Fatalf("view create=%d %s", createView.Code, createView.Body.String())
	}
	updateView := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-views/13", `{"name":"本周更新","version":1,"config":`+viewConfig+`}`)
	if updateView.Code != http.StatusOK || !strings.Contains(updateView.Body.String(), `"version":2`) || !strings.Contains(updateView.Body.String(), "本周更新") {
		t.Fatalf("view update=%d %s", updateView.Code, updateView.Body.String())
	}
	query := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/query", `{"config":`+viewConfig+`,"limit":50}`)
	if query.Code != http.StatusOK || !strings.Contains(query.Body.String(), `"record_id":"spm_`) || !strings.Contains(query.Body.String(), `"group_path"`) || !strings.Contains(query.Body.String(), " 天") || !strings.Contains(query.Body.String(), `"count":1`) || !strings.Contains(query.Body.String(), `"renewal_count":0`) || !strings.Contains(query.Body.String(), `"renewal_count_unavailable":false`) || !strings.Contains(query.Body.String(), `"alliance":null`) || !strings.Contains(query.Body.String(), `"alliance_unavailable":true`) {
		t.Fatalf("saved view query=%d %s", query.Code, query.Body.String())
	}
	members := handler.members.(*testMemberEntitlements)
	members.mu.Lock()
	members.page.Items[0].RenewalCountAvailable = false
	members.page.Items[0].RenewalCount = 0
	members.mu.Unlock()
	unavailableRenewal := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/query", `{"config":`+viewConfig+`,"limit":50}`)
	if unavailableRenewal.Code != http.StatusOK || !strings.Contains(unavailableRenewal.Body.String(), `"renewal_count":null`) || !strings.Contains(unavailableRenewal.Body.String(), `"renewal_count_unavailable":true`) || strings.Contains(unavailableRenewal.Body.String(), `"renewal_count":0`) {
		t.Fatalf("unavailable renewal must not be rendered as zero: %d %s", unavailableRenewal.Code, unavailableRenewal.Body.String())
	}
	_, _, _, queries := members.snapshot()
	if len(queries) == 0 || queries[len(queries)-1].ServiceProductID != 7 || queries[len(queries)-1].SnapshotAt.IsZero() || len(queries[len(queries)-1].GridFilters) != 0 {
		t.Fatalf("Product composition did not scan the complete Order relation: %+v", queries)
	}
	deleteView := adminRequest(http.MethodDelete, "/api/admin/service-period-products/7/member-views/13", `{"version":2}`)
	if deleteView.Code != http.StatusOK || !strings.Contains(deleteView.Body.String(), `"deleted":true`) || !strings.Contains(deleteView.Body.String(), `"id":"13"`) {
		t.Fatalf("view delete=%d %s", deleteView.Code, deleteView.Body.String())
	}

	numericCollaborator := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/collaborators", `{"staff_id":5,"permission":"edit"}`)
	if numericCollaborator.Code != http.StatusBadRequest {
		t.Fatalf("numeric collaborator id bypass=%d %s", numericCollaborator.Code, numericCollaborator.Body.String())
	}
	staff := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/staff", "")
	if staff.Code != http.StatusOK || !strings.Contains(staff.Body.String(), `"user_id":"zhangsan"`) {
		t.Fatalf("staff picker=%d %s", staff.Code, staff.Body.String())
	}
	createCollaborator := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/collaborators", `{"wecom_userid":"zhangsan","permission":"edit"}`)
	if createCollaborator.Code != http.StatusCreated || !strings.Contains(createCollaborator.Body.String(), `"id":"14"`) || !strings.Contains(createCollaborator.Body.String(), `"wecom_userid":"zhangsan"`) || !strings.Contains(createCollaborator.Body.String(), `"version":1`) {
		t.Fatalf("collaborator create=%d %s", createCollaborator.Code, createCollaborator.Body.String())
	}
	updateCollaborator := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"permission":"read","version":1}`)
	if updateCollaborator.Code != http.StatusOK || !strings.Contains(updateCollaborator.Body.String(), `"version":2`) {
		t.Fatalf("collaborator update=%d %s", updateCollaborator.Code, updateCollaborator.Body.String())
	}
	deleteCollaborator := adminRequest(http.MethodDelete, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"version":2}`)
	if deleteCollaborator.Code != http.StatusOK || !strings.Contains(deleteCollaborator.Body.String(), `"deleted":true`) || !strings.Contains(deleteCollaborator.Body.String(), `"version":2`) {
		t.Fatalf("collaborator delete=%d %s", deleteCollaborator.Code, deleteCollaborator.Body.String())
	}

	enableShare := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/external-share", `{"enabled":true,"version":0}`)
	if enableShare.Code != http.StatusOK || strings.Contains(enableShare.Body.String(), `"token"`) || !strings.Contains(enableShare.Body.String(), "/shared/service-period-member-grid#mgshare1.") {
		t.Fatalf("share enable=%d %s", enableShare.Code, enableShare.Body.String())
	}
	workspace := handler.workspace.(*testMemberWorkspace)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/public/service-period-member-grid/bootstrap", nil)
	bootstrap.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	bootResponse := httptest.NewRecorder()
	handler.ServeHTTP(bootResponse, bootstrap)
	if bootResponse.Code != http.StatusOK || strings.Contains(bootResponse.Body.String(), workspace.share.PublicID) {
		t.Fatalf("public bootstrap=%d %s", bootResponse.Code, bootResponse.Body.String())
	}
	publicQuery := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"default","limit":50}`))
	publicQuery.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	publicResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicResponse, publicQuery)
	if publicResponse.Code != http.StatusOK || strings.Contains(publicResponse.Body.String(), workspace.share.PublicID) || !strings.Contains(publicResponse.Body.String(), `"rows"`) {
		t.Fatalf("public query=%d %s", publicResponse.Code, publicResponse.Body.String())
	}
	revokeShare := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/external-share", `{"enabled":false,"version":1}`)
	if revokeShare.Code != http.StatusOK || strings.Contains(revokeShare.Body.String(), "mgshare1.") || !strings.Contains(revokeShare.Body.String(), `"enabled":false`) {
		t.Fatalf("share revoke=%d %s", revokeShare.Code, revokeShare.Body.String())
	}
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, bootstrap)
	if revoked.Code != http.StatusGone || strings.Contains(revoked.Body.String(), `"rows"`) {
		t.Fatalf("revoked public token=%d %s", revoked.Code, revoked.Body.String())
	}

	remark := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/members/"+memberGridMemberRef(41)+"/remark", `{"remark":"已联系","version":5}`)
	if remark.Code != http.StatusOK || !strings.Contains(remark.Body.String(), `"version":6`) {
		t.Fatalf("remark=%d %s", remark.Code, remark.Body.String())
	}
	remarkCommand, foundRemark, _, _ := members.snapshot()
	if !foundRemark || remarkCommand.CustomerID != 0 || remarkCommand.ServiceProductID != 7 || remarkCommand.EntitlementID != 41 {
		t.Fatalf("opaque remark did not carry only product scope: %+v", remarkCommand)
	}

	security.principal = accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 22, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/members", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("revoked/no workspace read=%d %s", denied.Code, denied.Body.String())
	}
}

func TestReadOnlyMemberGridCollaboratorGetsExplicitForbiddenAndNoWrites(t *testing.T) {
	handler, security, _, _ := newHandlerForTest(t)
	workspace := handler.workspace.(*testMemberWorkspace)
	security.principal = accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	// Simulate a stale/local collaborator projection which would otherwise
	// grant edit rights. The global viewer role must still be read-only.
	workspace.access = productport.MemberGridAccess{CanView: true, CanEdit: true, CanManageViews: true, CanShare: true}
	workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Version: 1}
	config := `{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[],"groups":[]}`

	requestNumber := 0
	request := func(method, path, body string) *httptest.ResponseRecorder {
		requestNumber++
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method != http.MethodGet {
			r.Header.Set("Idempotency-Key", fmt.Sprintf("member-grid-read-only-%04d", requestNumber))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	assertForbidden := func(name string, response *httptest.ResponseRecorder) {
		t.Helper()
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"FORBIDDEN"`) {
			t.Fatalf("%s status=%d body=%s", name, response.Code, response.Body.String())
		}
	}

	// Read access keeps the settings metadata readable but never reveals the
	// opaque public link to a collaborator who cannot manage sharing.
	settings := request(http.MethodGet, "/api/admin/service-period-products/7/member-grid/share-settings", "")
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"url":""`) || strings.Contains(settings.Body.String(), workspace.share.PublicID) {
		t.Fatalf("read-only share settings status=%d body=%s", settings.Code, settings.Body.String())
	}
	access, _, ok := handler.memberGridAuthorize(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/share-settings", nil), 7, false)
	if !ok || !access.CanView || access.CanEdit || access.CanManageViews || access.CanShare {
		t.Fatalf("viewer access=%+v ok=%v", access, ok)
	}

	assertForbidden("create view", request(http.MethodPost, "/api/admin/service-period-products/7/member-views", `{"name":"不可保存","config":`+config+`}`))
	assertForbidden("update view", request(http.MethodPut, "/api/admin/service-period-products/7/member-views/13", `{"name":"不可更新","version":1,"config":`+config+`}`))
	assertForbidden("delete view", request(http.MethodDelete, "/api/admin/service-period-products/7/member-views/13", `{"version":1}`))
	assertForbidden("edit remark", request(http.MethodPut, "/api/admin/service-period-products/7/members/"+memberGridMemberRef(41)+"/remark", `{"remark":"不可写","version":5}`))
	assertForbidden("edit alliance", request(http.MethodPut, "/api/admin/service-period-products/7/members/"+memberGridMemberRef(41)+"/alliance", `{"alliance":"不可写","version":5}`))
	assertForbidden("enable external share", request(http.MethodPut, "/api/admin/service-period-products/7/member-grid/external-share", `{"enabled":false,"version":1}`))
	assertForbidden("create collaborator", request(http.MethodPost, "/api/admin/service-period-products/7/member-grid/collaborators", `{"wecom_userid":"zhangsan","permission":"edit"}`))
	assertForbidden("update collaborator", request(http.MethodPut, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"permission":"read","version":1}`))
	assertForbidden("delete collaborator", request(http.MethodDelete, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"version":1}`))

	members := handler.members.(*testMemberEntitlements)
	_, _, remarkCalls, _ := members.snapshot()
	_, _, allianceCalls := members.allianceSnapshot()
	workspaceSnapshot := workspace.snapshot()
	if remarkCalls != 0 || allianceCalls != 0 || workspaceSnapshot.CreateViews != 0 || workspaceSnapshot.UpdateViews != 0 || workspaceSnapshot.DeleteViews != 0 || workspaceSnapshot.CreateCollaborators != 0 || workspaceSnapshot.UpdateCollaborators != 0 || workspaceSnapshot.DeleteCollaborators != 0 || workspaceSnapshot.SetShares != 0 {
		t.Fatalf("read-only collaborator reached an owner write: remarks=%d alliances=%d views=%d/%d/%d collaborators=%d/%d/%d share=%d", remarkCalls, allianceCalls, workspaceSnapshot.CreateViews, workspaceSnapshot.UpdateViews, workspaceSnapshot.DeleteViews, workspaceSnapshot.CreateCollaborators, workspaceSnapshot.UpdateCollaborators, workspaceSnapshot.DeleteCollaborators, workspaceSnapshot.SetShares)
	}
}

// This drives the byte-frozen dd8 template plus its state/share/grid scripts
// through the V3 Host. The browser issues actual requests to Handler over an
// httptest server; the in-memory workspace only implements the stable Product
// port, while PostgreSQL CRUD/CAS is covered by member_grid_integration_test.
func TestFrozenMemberGridBrowserJourneyUsesActualHTTPAPI(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for frozen member-grid browser journey")
	}
	handler, _, _, _ := newHandlerForTest(t)
	members := handler.members.(*testMemberEntitlements)
	workspace := handler.workspace.(*testMemberWorkspace)
	filterConfig := json.RawMessage(`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"gte","value":1}]},"sorts":[],"groups":[]}`)
	workspace.views = []productport.MemberGridView{{ID: 19, ProductID: 7, Name: "筛选视图", Position: 1, Config: filterConfig, Version: 1, CreatedBy: 9, UpdatedBy: 9, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}

	ui := NewMemberGridUI()
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/spProductData.html", func(w http.ResponseWriter, r *http.Request) {
		if err := RenderMemberGridInternal(w, r, r.URL.Query().Get("id")); err != nil {
			http.NotFound(w, r)
		}
	})
	mux.Handle("/shared/service-period-member-grid", ui)
	mux.Handle("/service-period-member-grid-assets/", ui)
	mux.Handle("/static/service-period/icons/", ui)
	mux.HandleFunc("/assets/standard-components/operation_member_picker.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("..", "..", "webshell", "static", "admin_console", "operation_member_picker_dd8d60d.js"))
	})
	mux.Handle("/", handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate member-grid journey")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	journey := filepath.Join(root, "internal", "product", "http", "member_grid_host", "member_grid_journey.mjs")
	command := exec.Command("node", journey)
	command.Dir = root
	command.Env = append(os.Environ(), "AICRM_MEMBER_GRID_JOURNEY_BASE_URL="+server.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("frozen member-grid browser journey: %v\n%s", err, output)
	}

	remarkCommand, foundRemark, _, queries := members.snapshot()
	if !foundRemark || remarkCommand.Remark != "第二次备注" || remarkCommand.ExpectedVersion != 6 || remarkCommand.CustomerID != 0 {
		t.Fatalf("opaque remark CAS command=%+v", remarkCommand)
	}
	allianceCommand, foundAlliance, _ := members.allianceSnapshot()
	if !foundAlliance || allianceCommand.Alliance != "" || allianceCommand.ExpectedVersion != 8 || allianceCommand.CustomerID != 0 {
		t.Fatalf("opaque alliance CAS command=%+v", allianceCommand)
	}
	var scanned bool
	for _, query := range queries {
		scanned = scanned || (query.ServiceProductID == 7 && !query.SnapshotAt.IsZero() && len(query.GridFilters) == 0 && len(query.GridSorts) == 0 && len(query.GridGroups) == 0)
	}
	if !scanned {
		t.Fatalf("frozen browser did not reach the complete Product member composition: %+v", queries)
	}
	var savedGroup, savedSort bool
	workspaceSnapshot := workspace.snapshot()
	for _, configBytes := range workspaceSnapshot.ViewConfigs {
		var config donorGridConfig
		if json.Unmarshal(configBytes, &config) != nil {
			t.Fatalf("saved view config=%s", configBytes)
		}
		savedGroup = savedGroup || len(config.Groups) == 1
		savedSort = savedSort || len(config.Sorts) == 1
	}
	if !savedGroup || !savedSort || workspaceSnapshot.Collaborators != 0 || workspaceSnapshot.Share.Enabled || workspaceSnapshot.Share.PublicID != "" {
		t.Fatalf("frozen UI persistence group=%t sort=%t collaborators=%d share=%+v", savedGroup, savedSort, workspaceSnapshot.Collaborators, workspaceSnapshot.Share)
	}
}

func TestFrozenMemberGridPublicSummaryBrowserJourneyUsesCursorAndGroups(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for frozen member-grid public summary journey")
	}
	handler, _, _, _ := newHandlerForTest(t)
	members := handler.members.(*testMemberEntitlements)
	now := time.Now().UTC()
	members.pages = []orderport.ServicePeriodMemberPage{
		{Items: append([]orderport.Entitlement(nil), members.page.Items...)},
		{Items: []orderport.Entitlement{
			{ID: 42, CustomerID: 78, ServiceProductID: 7, ProductName: "周期七", Status: "active", StartAt: now.Add(-48 * time.Hour), EndAt: now.Add(23 * time.Hour), RenewalCountAvailable: true, Version: 1, UpdatedAt: now},
			{ID: 43, CustomerID: 79, ServiceProductID: 7, ProductName: "周期七", Status: "active", StartAt: now.Add(-72 * time.Hour), EndAt: now.Add(23 * time.Hour), RenewalCountAvailable: true, Version: 1, UpdatedAt: now},
		}},
	}
	workspace := handler.workspace.(*testMemberWorkspace)
	const shareToken = "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: shareToken, Version: 1}
	workspace.views = []productport.MemberGridView{{
		ID: 31, ProductID: 7, Name: "分组末页视图", Position: 1,
		Config:  json.RawMessage(`{"schema_version":1,"filter":{"logic":"and","conditions":[]},"sorts":[],"groups":[{"field":"remaining_days","direction":"asc"}]}`),
		Version: 1, CreatedBy: 9, UpdatedBy: 9, CreatedAt: now, UpdatedAt: now,
	}}

	ui := NewMemberGridUI()
	mux := http.NewServeMux()
	mux.Handle("/shared/service-period-member-grid", ui)
	mux.Handle("/service-period-member-grid-assets/", ui)
	mux.Handle("/static/service-period/icons/", ui)
	mux.Handle("/", handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate member-grid public summary journey")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	journey := filepath.Join(root, "internal", "product", "http", "member_grid_host", "member_grid_public_summary_journey.mjs")
	command := exec.Command("node", journey)
	command.Dir = root
	command.Env = append(os.Environ(), "AICRM_MEMBER_GRID_JOURNEY_BASE_URL="+server.URL, "AICRM_MEMBER_GRID_JOURNEY_SHARE_TOKEN="+shareToken)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("frozen member-grid public summary journey: %v\n%s", err, output)
	}
}

func TestMemberGridPublicHttpAPIOnlyReadsEnabledShareAndSavedViews(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	workspace := handler.workspace.(*testMemberWorkspace)
	workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Version: 1}
	workspace.views = []productport.MemberGridView{{ID: 19, ProductID: 7, Name: "续费备注", Position: 1, Config: json.RawMessage(`{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"renewal_count","operator":"is_not_empty"},{"field":"remark","operator":"is_empty"}]},"sorts":[{"field":"renewal_count","direction":"desc"},{"field":"remark","direction":"asc"}],"groups":[{"field":"remaining_days","direction":"asc"}]}`), Version: 1}}
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/public/service-period-member-grid/bootstrap", nil)
	bootstrap.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	bootResponse := httptest.NewRecorder()
	handler.ServeHTTP(bootResponse, bootstrap)
	if bootResponse.Code != http.StatusOK || !strings.Contains(bootResponse.Body.String(), `"views"`) || strings.Contains(bootResponse.Body.String(), workspace.share.PublicID) {
		t.Fatalf("bootstrap=%d %s", bootResponse.Code, bootResponse.Body.String())
	}
	query := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"default","limit":50}`))
	query.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	queryResponse := httptest.NewRecorder()
	handler.ServeHTTP(queryResponse, query)
	if queryResponse.Code != http.StatusOK || !strings.Contains(queryResponse.Body.String(), `"rows"`) {
		t.Fatalf("query=%d %s", queryResponse.Code, queryResponse.Body.String())
	}
	saved := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"19","limit":200}`))
	saved.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	savedResponse := httptest.NewRecorder()
	handler.ServeHTTP(savedResponse, saved)
	if savedResponse.Code != http.StatusOK || !strings.Contains(savedResponse.Body.String(), `"rows"`) {
		t.Fatalf("saved view query=%d %s", savedResponse.Code, savedResponse.Body.String())
	}
	_, _, _, queries := handler.members.(*testMemberEntitlements).snapshot()
	last := queries[len(queries)-1]
	if last.ServiceProductID != 7 || last.SnapshotAt.IsZero() || len(last.GridFilters) != 0 || len(last.GridSorts) != 0 || len(last.GridGroups) != 0 {
		t.Fatalf("public saved view did not use the complete Product member composition: %+v", last)
	}
	workspace.share.Enabled = false
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, bootstrap)
	if revoked.Code != http.StatusGone || strings.Contains(revoked.Body.String(), `"rows"`) {
		t.Fatalf("revoked=%d %s", revoked.Code, revoked.Body.String())
	}
}

func TestHandlerRejectsMalformedIDWithoutApplicationCall(t *testing.T) {
	handler, _, catalog, _ := newHandlerForTest(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/products/07", nil))
	if recorder.Code != http.StatusNotFound || catalog.getCalls != 0 {
		t.Fatalf("status=%d getCalls=%d", recorder.Code, catalog.getCalls)
	}
}

func TestCompatibilityIdempotencyKeyFailsClosedWhenRandomReadFails(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	key, err := compatibilityIdempotencyKey(func([]byte) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) || key != "" {
		t.Fatalf("key=%q err=%v, want empty key and entropy error", key, err)
	}
}

func TestExternalPushConfigurationHTTPReadsDisabledBindingWithCompleteFrozenShape(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	external, ok := handler.external.(*testExternalPush)
	if !ok {
		t.Fatal("unexpected external fixture")
	}
	external.configuration = productport.ExternalPushConfiguration{
		ProductID: 7, ProductKind: productport.ExternalPushWeChatPay,
		Enabled: false, ConfigurationReference: "", PushType: "", Remark: "", Revision: 0,
		UpdatedAt: time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC),
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/external-push", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("disabled configuration status=%d body=%s", response.Code, response.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["configuration_reference"]) != `""` || string(raw["expires_at_ts"]) != `null` || string(raw["custom_params"]) != `{}` || string(raw["custom_params_json"]) != `"{}"` {
		t.Fatalf("incomplete disabled frozen configuration response=%s", response.Body.String())
	}
}

func TestDistributionPolicyDTOUsesActualReadAndKeepsLegacyUpdateCompatible(t *testing.T) {
	handler, _, catalog, _ := newHandlerForTest(t)
	reader := &testPolicyReader{policy: distributiondomain.Policy{ProductID: 7, ProductType: distributiondomain.ProductTypeStandard, Enabled: true, CommissionRateBasisPoints: 1234, WaitDays: 8, Version: 3, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	if err := handler.SetDistributionPolicyReader(reader); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/products/7", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"distribution_policy":{"enabled":true,"commission_rate_basis_points":1234,"wait_days":8,"version":3}`) {
		t.Fatalf("actual policy missing: %d %s", get.Code, get.Body.String())
	}
	create := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/products", strings.NewReader(`{"product_code":"distribution-http","name":"分销商品","description":"","price_minor":1,"currency":"CNY","stock_quantity":1,"images":[],"distribution_policy":{"enabled":true,"commission_rate_basis_points":1234,"wait_days":8,"version":0}}`))
	create.Header.Set("Idempotency-Key", "distribution-http-create-0001")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || catalog.createCall == nil || catalog.createCall.DistributionPolicy == nil || !catalog.createCall.DistributionPolicy.Enabled || catalog.createCall.DistributionPolicy.CommissionRateBasisPoints != 1234 {
		t.Fatalf("policy create=%+v status=%d body=%s", catalog.createCall, created.Code, created.Body.String())
	}
	legacy := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7", strings.NewReader(`{"expected_version":2,"name":"商品七","description":"描述","price_minor":1200,"currency":"CNY","stock_quantity":4,"images":[]}`))
	legacy.Header.Set("Idempotency-Key", "distribution-http-update-0001")
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, legacy)
	if updated.Code != http.StatusOK || catalog.updateCall == nil || catalog.updateCall.DistributionPolicy != nil {
		t.Fatalf("legacy update policy=%+v status=%d body=%s", catalog.updateCall, updated.Code, updated.Body.String())
	}
	period := handler.service.(*testServicePeriod)
	periodCreate := httptest.NewRequest(http.MethodPost, "/api/admin/service-period-products", strings.NewReader(`{"product_code":"distribution-period","name":"周期分销商品","description":"","price_minor":1,"currency":"CNY","duration_days":7,"stock_quantity":1,"images":[],"distribution_policy":{"enabled":true,"commission_rate_basis_points":3000,"wait_days":0,"version":0}}`))
	periodCreate.Header.Set("Idempotency-Key", "distribution-period-create-0001")
	periodResponse := httptest.NewRecorder()
	handler.ServeHTTP(periodResponse, periodCreate)
	if periodResponse.Code != http.StatusCreated || period.createCall == nil || period.createCall.DistributionPolicy == nil || period.createCall.DistributionPolicy.CommissionRateBasisPoints != 3000 {
		t.Fatalf("period create=%+v status=%d body=%s", period.createCall, periodResponse.Code, periodResponse.Body.String())
	}
	periodUpdate := httptest.NewRequest(http.MethodPut, "/api/admin/service-period-products/7", strings.NewReader(`{"expected_version":2,"name":"周期分销商品更新","description":"","price_minor":1,"currency":"CNY","duration_days":7,"stock_quantity":1,"images":[],"admin_projection":{},"distribution_policy":{"enabled":true,"commission_rate_basis_points":1234,"wait_days":8,"version":1}}`))
	periodUpdate.Header.Set("Idempotency-Key", "distribution-period-update-0001")
	periodUpdated := httptest.NewRecorder()
	handler.ServeHTTP(periodUpdated, periodUpdate)
	if periodUpdated.Code != http.StatusOK || period.updateCall == nil || period.updateCall.DistributionPolicy == nil || !period.updateCall.DistributionPolicy.Enabled || period.updateCall.DistributionPolicy.CommissionRateBasisPoints != 1234 || period.updateCall.DistributionPolicy.WaitDays != 8 || period.updateCall.DistributionPolicy.ExpectedVersion != 1 {
		t.Fatalf("period update=%+v status=%d body=%s", period.updateCall, periodUpdated.Code, periodUpdated.Body.String())
	}
}
