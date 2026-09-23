package wecom

import (
	"context"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type groupHTTPRefresh struct{ called bool }

func (g *groupHTTPRefresh) Refresh(context.Context, string) (wecomport.AudienceGroupMembership, error) {
	g.called = true
	return wecomport.AudienceGroupMembership{ProviderComplete: true, ExternalCount: 122, UnresolvedCount: 10, ExternalIdentityHashes: []string{"private-hash"}}, nil
}
func TestGroupRefreshHTTPRejectsCSRFBeforeProvider(t *testing.T) {
	g := &groupHTTPRefresh{}
	h := &CallbackAdminHandler{csrf: &callbackAdminTestSecurity{csrfErr: accessdomain.ErrAuthentication}, groups: g}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/wecom/group-membership/refresh", strings.NewReader(`{"chat_reference":"g"}`))
	r.Header.Set("Content-Type", "application/json")
	h.Routes().ServeHTTP(w, r)
	if w.Code < 400 || g.called {
		t.Fatal("unauthorized provider read")
	}
}

func TestGroupRefreshHTTPAcceptsAdminAndDoesNotExposeIDs(t *testing.T) {
	g := &groupHTTPRefresh{}
	h := &CallbackAdminHandler{csrf: &callbackAdminTestSecurity{csrfPrincipal: callbackAdminSuperAdmin()}, groups: g}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/wecom/group-membership/refresh", strings.NewReader(`{"chat_reference":"g"}`))
	r.Header.Set("Content-Type", "application/json")
	h.Routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !g.called || strings.Contains(w.Body.String(), "customer_ids") || strings.Contains(w.Body.String(), "private-hash") || !strings.Contains(w.Body.String(), `"provider_complete":true`) || !strings.Contains(w.Body.String(), `"complete":false`) {
		t.Fatalf("status=%d called=%v", w.Code, g.called)
	}
}
