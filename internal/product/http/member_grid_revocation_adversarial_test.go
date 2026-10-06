package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// Revocation is committed while the bounded owner read is in flight. The
// legacy public capability must use the same response-time gate as scoped
// shares; returning the composed rows after that gate closes leaks one page.
func TestMemberGridPublicQueryRechecksRevocationBeforeResponding(t *testing.T) {
	for _, action := range []string{"revoke", "rotate"} {
		t.Run(action, func(t *testing.T) {
			handler, _, _, _ := newHandlerForTest(t)
			workspace := handler.workspace.(*testMemberWorkspace)
			workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Version: 1}
			token := workspace.share.PublicID
			handler.members = revokeDuringMemberGridRead{EntitlementService: handler.members, revoke: func() { closeAdversarialShare(workspace, action) }}
			request := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"default","limit":50}`))
			request.Header.Set("X-AICRM-Grid-Share-Token", token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusGone || strings.Contains(response.Body.String(), `"rows"`) || strings.Contains(response.Body.String(), `"member"`) {
				t.Fatalf("closed capability emitted member data: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMemberGridPublicBootstrapRechecksRevocationBeforeResponding(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	workspace := handler.workspace.(*testMemberWorkspace)
	workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Version: 1}
	token := workspace.share.PublicID
	handler.workspace = revokeDuringMemberGridBootstrap{MemberGridWorkspace: workspace, revoke: func() { closeAdversarialShare(workspace, "revoke") }}
	request := httptest.NewRequest(http.MethodGet, "/api/public/service-period-member-grid/bootstrap", nil)
	request.Header.Set("X-AICRM-Grid-Share-Token", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusGone || strings.Contains(response.Body.String(), `"views"`) || strings.Contains(response.Body.String(), `"product"`) {
		t.Fatalf("closed capability emitted workspace metadata: status=%d body=%s", response.Code, response.Body.String())
	}
}

func closeAdversarialShare(workspace *testMemberWorkspace, action string) {
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	workspace.share.Enabled = action == "rotate"
	workspace.share.PublicID = ""
	if action == "rotate" {
		workspace.share.PublicID = "mgshare1.abcdefghijklmnopqrstuv.BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	}
	workspace.share.Version++
}

type revokeDuringMemberGridBootstrap struct {
	productport.MemberGridWorkspace
	revoke func()
}

func (workspace revokeDuringMemberGridBootstrap) ListViews(ctx context.Context, id productport.ID) ([]productport.MemberGridView, error) {
	views, err := workspace.MemberGridWorkspace.ListViews(ctx, id)
	if err == nil {
		workspace.revoke()
	}
	return views, err
}

type revokeDuringMemberGridRead struct {
	orderport.EntitlementService
	revoke func()
}

func (reader revokeDuringMemberGridRead) ListServicePeriodMembers(ctx context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	page, err := reader.EntitlementService.ListServicePeriodMembers(ctx, query)
	if err == nil {
		reader.revoke()
	}
	return page, err
}
