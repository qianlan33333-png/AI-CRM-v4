package media

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaUIRendersEveryFrozenWorkspaceWithStablePageKey(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"attach", "mpLib"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(`<template id="tpl"><section data-page="`+page+`"></section></template>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js", "material-save.js", "image-filter.js", "material-library.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","materialSaveHost":"assets/material-save.js","imageLibraryFilterHost":"assets/image-filter.js","materialLibraryHost":"assets/material-library.js"},"files":{"assets/tokens.css":{},"assets/labs.css":{},"assets/admin.js":{},"assets/material-save.js":{},"assets/image-filter.js":{},"assets/material-library.js":{}}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	handler := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, donor string, assets MediaAssets) error {
		got = append(got, page)
		if (page != "images" && donor == "") || (page == "images" && donor != "") || assets.AdminJS != "/media-assets/assets/admin.js" || assets.MaterialSaveHostJS != "/media-assets/assets/material-save.js" || assets.ImageLibraryFilterHostJS != "/media-assets/assets/image-filter.js" || assets.MaterialLibraryHostJS != "/media-assets/assets/material-library.js" {
			t.Fatalf("bad render input page=%q donor=%q assets=%+v", page, donor, assets)
		}
		w.WriteHeader(http.StatusOK)
		return nil
	})
	for _, legacy := range []struct{ path, want string }{
		{"/admin/image-library", "/admin/materials?tab=images"},
		{"/admin/miniprogram-library", "/admin/materials?tab=miniprograms"},
		{"/admin/attachment-library", "/admin/materials?tab=attachments"},
		{"/admin/images.html", "/admin/materials?tab=images"},
		{"/admin/mpLib.html", "/admin/materials?tab=miniprograms"},
		{"/admin/attach.html", "/admin/materials?tab=attachments"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, legacy.path, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != legacy.want {
			t.Fatalf("%s status=%d location=%q", legacy.path, response.Code, response.Header().Get("Location"))
		}
	}
	for _, path := range []string{"/admin/materials?tab=images", "/admin/materials?tab=miniprograms", "/admin/materials?tab=attachments"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
	want := []string{"images", "mpLib", "attach"}
	if len(got) != len(want) {
		t.Fatalf("pages=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pages=%v want=%v", got, want)
		}
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/admin/materials?tab=unknown", nil))
	if invalid.Code != http.StatusSeeOther || invalid.Header().Get("Location") != "/admin/materials?tab=images" {
		t.Fatalf("invalid tab status=%d location=%q", invalid.Code, invalid.Header().Get("Location"))
	}
}

func TestMediaUIExtractsCompleteNestedFrozenTemplates(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// These three forms mirror the lowered donor markup: the outer #tpl wraps
	// nested <template> nodes and the upload/create modal follows an earlier
	// nested loop. Regression here is deliberately page-by-page because a
	// partially extracted page appears to load but its toolbar cannot reveal the
	// later modal.
	pages := map[string]string{
		"images": `<template id="tpl"><button>上传图片</button><template data-sc-for="{{ rows.images }}"><span>{{ m.name }}</span></template><template data-sc-if="{{ imagesPage.uploadOpen }}"><input id="fImgUpFile"></template></template>`,
		"mpLib":  `<template id='tpl'><button>新建小程序卡片</button><template data-sc-if="{{ mpPage.empty }}"><p>空</p></template><template data-sc-if="{{ mpPage.createOpen }}"><input id="fMpAppid"></template></template>`,
		"attach": `<template data-unused="true"></template><!-- <template id=tpl>ignored</template> --><template id=tpl><button>上传附件</button><template data-sc-for="{{ rows.attachItems }}"><span>{{ a.name }}</span></template><template data-sc-if="{{ attachPage.uploadOpen }}"><input id="fAttUpFile" value=">"></template></template>`,
	}
	for page, raw := range pages {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ui := &mediaUI{dist: dist}
	assertContains := func(page, want string) {
		t.Helper()
		got, err := ui.template(page)
		if err != nil {
			t.Fatalf("%s: extract donor template: %v", page, err)
		}
		if !strings.Contains(got, want) {
			t.Fatalf("%s: extracted template lost later interaction %q: %q", page, want, got)
		}
	}
	assertContains("images", `id="fImgUpFile"`)
	assertContains("mpLib", `id="fMpAppid"`)
	assertContains("attach", `id="fAttUpFile"`)
}

func TestExtractDonorTemplateRejectsMissingOrUnclosedOuterTemplate(t *testing.T) {
	if _, err := extractDonorTemplate(`<template data-sc-if="{{ enabled }}"></template>`); err == nil || err.Error() != "donor template missing" {
		t.Fatalf("missing outer template error=%v", err)
	}
	if _, err := extractDonorTemplate(`<template id="tpl"><template data-sc-if="{{ enabled }}"></template>`); err == nil || err.Error() != "donor template incomplete" {
		t.Fatalf("unclosed outer template error=%v", err)
	}
}

func TestMediaGroupNavigationKeepsMaterialType(t *testing.T) {
	for _, item := range []struct{ tab, page string }{{"images", "images"}, {"attachments", "attach"}, {"miniprograms", "mpLib"}} {
		for _, group := range []string{"", "课程 A&B / 中文"} {
			u, _ := url.Parse("/admin/materials?tab=" + item.tab + "&material_group=" + url.QueryEscape(group))
			page, redirect, ok := mediaRequest(u)
			if !ok || page != item.page || redirect != "" {
				t.Fatalf("%s: page=%s redirect=%s", u, page, redirect)
			}
		}
	}
	for _, query := range []string{"tab=attachments&material_group=a&material_group=b", "tab=attachments&tab=images", "tab=attachments&unknown=1"} {
		u, _ := url.Parse("/admin/materials?" + query)
		_, redirect, _ := mediaRequest(u)
		if redirect == "" {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
}
