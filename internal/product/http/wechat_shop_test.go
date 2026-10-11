package http

import (
	"context"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
	"net/http"
	"net/http/httptest"
	"testing"
)

type refreshStub struct{ calls int }

func (s *refreshStub) SubmitProductRefresh(context.Context) (shopport.RefreshStatus, error) {
	s.calls++
	return shopport.RefreshStatus{ID: 7, State: "queued"}, nil
}
func (s *refreshStub) ProductRefreshStatus(context.Context) (shopport.RefreshStatus, error) {
	return shopport.RefreshStatus{ID: 7, State: "running"}, nil
}
func TestShopRefreshRequiresProductEditAndCSRF(t *testing.T) {
	h, security, _, _ := newHandlerForTest(t)
	service := &refreshStub{}
	h.SetShopRefresh(service)
	for _, c := range []struct {
		method, path string
		role         accessdomain.Role
		csrf         error
		status       int
	}{{"GET", "refresh-status", accessdomain.RoleViewer, nil, 403}, {"POST", "refresh", accessdomain.RoleAdmin, errors.New("CSRF"), 403}, {"POST", "refresh", accessdomain.RoleAdmin, nil, 202}, {"GET", "refresh-status", accessdomain.RoleAdmin, nil, 200}} {
		security.principal.Roles = []accessdomain.Role{c.role}
		security.csrfErr = c.csrf
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, "/api/admin/products/wechat-shop/"+c.path, nil))
		if w.Code != c.status {
			t.Fatal(c, w.Code, w.Body.String())
		}
	}
	if service.calls != 1 {
		t.Fatal("unauthorized refresh accepted")
	}
	_ = http.MethodGet
}
