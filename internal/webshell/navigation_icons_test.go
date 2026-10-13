package webshell

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNavigationEveryDirectIconIsAnEmbeddedLoadableSVG(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, group := range NavItems("api.admin_products_page") {
		for _, item := range group.Items {
			count++
			path := "/static/admin_console/nav-icons/" + item.IconKey + ".svg"
			response := httptest.NewRecorder()
			renderer.ServeStatic(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "image/svg+xml") {
				t.Fatalf("navigation %s resource status=%d type=%s", item.Key, response.Code, response.Header().Get("Content-Type"))
			}
			var svg struct{ XMLName xml.Name }
			if xml.Unmarshal(response.Body.Bytes(), &svg) != nil || svg.XMLName.Local != "svg" {
				t.Fatalf("navigation %s resource is not SVG", item.Key)
			}
			if !strings.HasPrefix(item.Href, "/admin") {
				t.Fatalf("navigation route changed: %s", item.Key)
			}
		}
	}
	if count != 24 {
		t.Fatalf("direct navigation coverage changed: %d", count)
	}
	response := httptest.NewRecorder()
	if err = renderer.RenderAdmin(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin", nil), "总览", "", "api.admin_operating_overview")); err != nil {
		t.Fatal(err)
	}
	for key, icon := range map[string]string{"group_invitations": "group_ops", "referral": "distribution", "ops_governance": "config"} {
		if strings.Contains(response.Body.String(), "/nav-icons/"+key+".svg") || !strings.Contains(response.Body.String(), "/nav-icons/"+icon+".svg") {
			t.Fatalf("shell did not use explicit icon binding: %s", key)
		}
	}
}

func TestNavigationIconRejectsMissingForeignAndTraversalBindings(t *testing.T) {
	for _, icon := range []string{"missing", "../config", "/config", "config.svg", "https://foreign.invalid/icon", "config?x", "config\x00", "Config"} {
		if _, err := navigationIconKey("overview", icon); err == nil {
			t.Fatalf("unsafe/unavailable icon accepted: %q", icon)
		}
	}
	if icon, err := navigationIconKey("overview", ""); err != nil || icon != "overview" {
		t.Fatal("default existing icon changed")
	}
	if _, err := navigationIconKey("group_invitations", ""); err == nil {
		t.Fatal("missing implicit icon accepted")
	}
}
