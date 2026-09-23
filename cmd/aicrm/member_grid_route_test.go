package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
)

func TestProductDataEntryIsServedByTheShell(t *testing.T) {
	marker := http.NotFoundHandler()
	productUI := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Product-UI", "bound")
		writer.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{
		Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin},
	}}
	handler, err := routeApplicationWithProducts(
		marker, marker, marker, marker, marker, marker, marker, marker, marker, marker,
		marker, productUI, marker, marker, webshell.MustHandler(), authentication, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	handler = mountMemberGridUI(handler, producthttp.NewMemberGridUI())

	// The member-grid data page must retain the bound Product UI. A generic
	// shell is insufficient because it would hide the grid's real views,
	// collaborator and share operations.
	request := httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Product-UI") != "bound" {
		t.Fatalf("member-grid entry did not reach Product UI status=%d marker=%q", response.Code, response.Header().Get("X-Product-UI"))
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/static/service-period/icons/funnel.svg", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "<svg") {
		t.Fatalf("embedded member-grid icon status=%d body=%s", asset.Code, asset.Body.String())
	}

	authentication.err = accessdomain.ErrAuthentication
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil))
	if unauthenticated.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated member-grid entry status=%d", unauthenticated.Code)
	}
}
