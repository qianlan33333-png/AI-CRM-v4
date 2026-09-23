package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/groupops"
)

// This is an HTTP behavior test for the v3 host binding, not a donor UI test:
// the frozen browser module is allowed only after the regular admin session
// gate and must retain a JavaScript MIME type when loaded dynamically.
func TestGroupOpsHostServesAuthenticatedHistoryDynamicChunk(t *testing.T) {
	dist := t.TempDir()
	for relative, body := range map[string]string{
		"admin/groupops.html":                          `<template id="tpl"><section data-page="groupops"></section></template>`,
		"assets/tokens-test.css":                       "body{}",
		"assets/labs-test.css":                         "#stage{}",
		"assets/admin-test.js":                         `import("./chunks/groupOpsHistory-test.js")`,
		"assets/chunks/groupOpsHistory-test.js":        `export const mountGroupOpsHistory = () => {};`,
		"aiassistant/send_content_readonly_detail.css": `.send-readonly-detail{}`,
		"aiassistant/send_content_readonly_detail.js":  `window.AICRMSendContentReadonlyDetail={renderFull:()=>''};`,
		"asset-manifest.json":                          `{"entries":{"tokens":"assets/tokens-test.css","labs":"assets/labs-test.css","admin":"assets/admin-test.js"},"files":{"assets/tokens-test.css":{},"assets/labs-test.css":{},"assets/admin-test.js":{},"assets/chunks/groupOpsHistory-test.js":{},"aiassistant/send_content_readonly_detail.css":{},"aiassistant/send_content_readonly_detail.js":{}}}`,
	} {
		file := filepath.Join(dist, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ui := groupops.NewModuleRegistration().UIBinding(dist, func(writer http.ResponseWriter, _ *http.Request, page, donor string, assets groupops.GroupOpsAssets) error {
		if page != "groupops" || !strings.Contains(donor, `data-page="groupops"`) || assets.AdminJS != "/groupops-assets/assets/admin-test.js" || assets.ReadonlyCSS != "/groupops-assets/aiassistant/send_content_readonly_detail.css" || assets.ReadonlyJS != "/groupops-assets/aiassistant/send_content_readonly_detail.js" {
			return fmt.Errorf("unexpected Group Ops UI binding page=%q assets=%+v", page, assets)
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte("groupops carrier"))
		return nil
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, err: accessdomain.ErrAuthentication}
	handler := requireAdminSession(authentication, ui)
	chunkPath := "/groupops-assets/assets/chunks/groupOpsHistory-test.js"
	readonlyPath := "/groupops-assets/aiassistant/send_content_readonly_detail.js"

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, chunkPath, nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fgroupops-assets%2Fassets%2Fchunks%2FgroupOpsHistory-test.js" {
		t.Fatalf("anonymous dynamic chunk status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	authentication.err = nil

	request := httptest.NewRequest(http.MethodGet, chunkPath, nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || !strings.Contains(response.Body.String(), "mountGroupOpsHistory") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("authenticated dynamic chunk status=%d type=%q body=%q nosniff=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String(), response.Header().Get("X-Content-Type-Options"))
	}

	request = httptest.NewRequest(http.MethodGet, readonlyPath, nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || !strings.Contains(response.Body.String(), "AICRMSendContentReadonlyDetail") {
		t.Fatalf("authenticated read-only content asset status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/groupops.html?history=1", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "groupops carrier" {
		t.Fatalf("authenticated history carrier status=%d body=%q", response.Code, response.Body.String())
	}
}
