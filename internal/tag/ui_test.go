package tag

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTagsUIBindingExtractsFrozenTemplateAndVerifiedAssets(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js", "page-header-action-host.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "admin", "tags.html"), []byte(`<template id="tpl"><section data-page="tags"><template><span title=">">nested</span></template></section></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","pageHeaderActionHost":"assets/page-header-action-host.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotBody string
	var gotAssets TagsAssets
	h := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, body string, assets TagsAssets) error {
		gotBody, gotAssets = body, assets
		w.WriteHeader(http.StatusOK)
		return nil
	})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/wecom-tags", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(gotBody, `data-page="tags"`) {
		t.Fatalf("status/body = %d/%q", recorder.Code, gotBody)
	}
	if gotAssets != (TagsAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", PageHeaderActionHostJS: "/assets/page-header-action-host.js"}) {
		t.Fatalf("assets = %#v", gotAssets)
	}
}

func TestTagsUIBindingRejectsQueryAndWrongRoute(t *testing.T) {
	h := NewModuleRegistration().UIBinding(t.TempDir(), func(http.ResponseWriter, *http.Request, string, TagsAssets) error { return nil })
	query := httptest.NewRecorder()
	h.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/admin/wecom-tags?unexpected=1", nil))
	if query.Code != http.StatusSeeOther || query.Header().Get("Location") != "/admin/wecom-tags" {
		t.Fatalf("query response = %d location=%q", query.Code, query.Header().Get("Location"))
	}
	if !validTagsQuery(httptest.NewRequest(http.MethodGet, "/admin/wecom-tags?id=42", nil)) || validTagsQuery(httptest.NewRequest(http.MethodGet, "/admin/wecom-tags?id=042", nil)) {
		t.Fatal("detail query validation did not preserve only canonical positive id")
	}
	wrong := httptest.NewRecorder()
	h.ServeHTTP(wrong, httptest.NewRequest(http.MethodGet, "/admin/tags", nil))
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong route status = %d", wrong.Code)
	}
}
