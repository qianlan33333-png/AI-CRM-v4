package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type testSecurity struct {
	p       accessdomain.Principal
	err     error
	csrfErr error
}

func (s testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.p, s.err
}
func (s testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.csrfErr != nil {
		return s.p, s.csrfErr
	}
	return s.p, s.err
}

type testAgents struct {
	item   automationport.Agent
	create automationport.CreateCommand
	status automationport.AgentStatus
	err    error
}

func (s *testAgents) List(context.Context, automationport.AutomationType) (automationport.Page, error) {
	return automationport.Page{Items: []automationport.Agent{s.item}, Total: 1}, s.err
}
func (s *testAgents) Get(context.Context, automationport.AgentID) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Create(_ context.Context, c automationport.CreateCommand) (automationport.Agent, error) {
	s.create = c
	return s.item, s.err
}
func (s *testAgents) Update(context.Context, automationport.UpdateCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Copy(context.Context, automationport.MutationCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Publish(context.Context, automationport.MutationCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) SetStatus(_ context.Context, _ automationport.MutationCommand, status automationport.AgentStatus) (automationport.Agent, error) {
	s.status = status
	s.item.Status = status
	s.item.ExecutionEnabled = status == automationport.AgentStatusActive
	return s.item, s.err
}
func (s *testAgents) SaveFixedContent(context.Context, automationport.FixedContentCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) PublishedAgent(context.Context, automationport.AgentID) (automationport.PublishedAgent, bool, error) {
	return automationport.PublishedAgent{AgentID: s.item.ID, AutomationType: s.item.AutomationType, Status: s.item.Status, PublishedVersion: s.item.PublishedVersion}, s.item.PublishedVersion > 0, s.err
}
func principal() accessdomain.Principal {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}
func agent() automationport.Agent {
	return automationport.Agent{ID: 1, AgentName: "a", AgentCode: "a", AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusPaused, DraftVersion: 1, PublishedVersion: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: time.Now(), UpdatedAt: time.Now(), LegacyConfiguration: []byte(`{}`)}
}
func TestCreateUsesPrincipalAndIdempotencyHeader(t *testing.T) {
	s := &testAgents{item: agent()}
	h, e := NewHandler(s, testSecurity{p: principal()})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents", strings.NewReader(`{"agent_name":"a","agent_code":"a","automation_type":"agent"}`))
	r.Header.Set("Idempotency-Key", "1234567890123456")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || s.create.Actor != 7 || s.create.IdempotencyKey != "1234567890123456" {
		t.Fatalf("status=%d command=%+v", w.Code, s.create)
	}
}
func TestActivatePersistsLocalStatusAfterCSRF(t *testing.T) {
	s := &testAgents{item: agent()}
	h, _ := NewHandler(s, testSecurity{p: principal()})
	r := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents/1/activate", nil)
	r.Header.Set("Idempotency-Key", "1234567890123456")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || s.status != automationport.AgentStatusActive || !s.item.ExecutionEnabled {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

func TestActivateRequiresCSRF(t *testing.T) {
	s := &testAgents{item: agent()}
	h, _ := NewHandler(s, testSecurity{p: principal(), csrfErr: errors.New("csrf")})
	r := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents/1/activate", nil)
	r.Header.Set("Idempotency-Key", "1234567890123456")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || s.status != "" {
		t.Fatalf("status=%d transition=%q body=%s", w.Code, s.status, w.Body.String())
	}
}

func TestSummaryUsesPersistedMaterialCounts(t *testing.T) {
	a := agent()
	a.Status, a.ExecutionEnabled = automationport.AgentStatusActive, true
	a.FixedContentPackage.ImageLibraryIDs = []int64{1, 2}
	a.FixedContentPackage.AttachmentLibraryIDs = []int64{3}
	got := summary(a)
	counts := got["fixed_material_summary"].(map[string]int)
	if got["status"] != automationport.AgentStatusActive || got["execution_enabled"] != true || counts["image_count"] != 2 || counts["attachment_count"] != 1 {
		t.Fatalf("summary=%#v", got)
	}
}
func TestDetailExposesOnlyOpaquePublishedDigest(t *testing.T) {
	s := &testAgents{item: agent()}
	h, _ := NewHandler(s, testSecurity{p: principal()})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/automation-agents/1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"published_digest":"`) || strings.Contains(w.Body.String(), "content_digest") || strings.Contains(w.Body.String(), "materials_digest") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestErrorMapsNotFound(t *testing.T) {
	h, _ := NewHandler(&testAgents{err: automationapp.ErrAgentNotFound}, testSecurity{p: principal()})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/automation-agents/1", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
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

func TestPolicyCommandDoesNotAcceptClientSelectedApprover(t *testing.T) {
	var input policyInput
	if err := json.Unmarshal([]byte(`{"code":"p","name":"Policy","package_id":1,"trigger":"audience.member_entered.v1","action":"record","action_config":{},"quiet_hours":{},"single_run_limit":1,"approval_staff_id":999}`), &input); err != nil {
		t.Fatal(err)
	}
	command := policyCommand(input, 7, "1234567890123456")
	if command.ApprovalStaffID == nil || *command.ApprovalStaffID != 7 {
		t.Fatalf("approval actor=%v, want authenticated actor 7", command.ApprovalStaffID)
	}
	if command.Actor != 7 {
		t.Fatalf("actor=%d, want authenticated actor", command.Actor)
	}
}

var _ = errors.New
