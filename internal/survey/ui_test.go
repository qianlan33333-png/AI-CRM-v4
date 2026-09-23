package survey

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func surveyTestDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	for _, dir := range []string{"admin", "h5", "assets"} {
		if err := os.MkdirAll(filepath.Join(dist, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js", "editor.js", "editor.css", "h5.js", "standard-host.js", "survey-host.js", "survey-operations.js", "survey-operations.css"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets", "standard-components"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "standard-components", "wecom_tag_picker.css"), []byte("tag"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","questionnaireEditor":"assets/editor.js","questionnaireEditorStyles":"assets/editor.css","h5":"assets/h5.js","standardComponentsHost":"assets/standard-host.js","surveyHost":"assets/survey-host.js","surveyOperationsHost":"assets/survey-operations.js","surveyOperationsStyles":"assets/survey-operations.css"},"files":{"assets/standard-components/wecom_tag_picker.css":{}}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"questionnaires", "questionnaireDetail", "questionnaireOps"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(`<html><template id="tpl"><section>`+page+`</section></template></html>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "admin", "questionnaireDetail.html"), []byte(`<!doctype html><html><body data-page="questionnaireDetail"><section id="editor">questionnaireDetail</section><div id="questionnaire-editor-config" hidden>{}</div><script type="module" src="../assets/questionnaireEditor-TEST.js"></script></body></html>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "h5", "all.html"), []byte(`<link href="../assets/tokens.css"><script src="../assets/h5.js"></script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dist
}

func TestSurveyUIUsesFrozenFragmentAndV3Assets(t *testing.T) {
	dist := surveyTestDist(t)
	var gotPage, gotBody string
	h := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, body string, assets UIAssets) error {
		gotPage, gotBody = page, body
		if assets.EditorJS != "/survey-assets/editor.js" {
			t.Fatalf("editor=%q", assets.EditorJS)
		}
		if assets.SurveyHostJS != "/survey-assets/survey-host.js" {
			t.Fatalf("survey host=%q", assets.SurveyHostJS)
		}
		if assets.OperationsHostJS != "/survey-assets/survey-operations.js" || assets.OperationsCSS != "/survey-assets/survey-operations.css" {
			t.Fatalf("survey operations host=%q css=%q", assets.OperationsHostJS, assets.OperationsCSS)
		}
		w.WriteHeader(200)
		return nil
	})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/questionnaireDetail.html?id=12", nil))
	if response.Code != 200 || gotPage != "questionnaireDetail" || gotBody != `<section id="editor">questionnaireDetail</section><div id="questionnaire-editor-config" hidden>{}</div>` {
		t.Fatalf("code=%d page=%q body=%q", response.Code, gotPage, gotBody)
	}
	for _, path := range []string{"/admin/questionnaireDetail.html?id=01", "/admin/questionnaireOps.html", "/admin/questionnaires.html?x=1"} {
		response = httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 404 {
			t.Fatalf("%s=%d", path, response.Code)
		}
	}
}

func TestSurveyPublicUIServesOnlyH5AndImmutableAssets(t *testing.T) {
	h := NewModuleRegistration().PublicUIBinding(surveyTestDist(t))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/h5/all.html", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "../survey-assets/h5.js") || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("html response=%d %q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/survey-assets/h5.js", nil))
	if response.Code != 200 || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset response=%d", response.Code)
	}
	response = httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/survey-assets/../asset-manifest.json", nil))
	if response.Code != 404 {
		t.Fatalf("traversal=%d", response.Code)
	}
}

func TestSurveyEditorAcceptsGeneratedPresentationMarker(t *testing.T) {
	dist := surveyTestDist(t)
	file := filepath.Join(dist, "admin", "questionnaireDetail.html")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	decorated := strings.Replace(string(raw), `<body data-page=`, `<body data-ui-surface="admin" data-page=`, 1)
	if err := os.WriteFile(file, []byte(decorated), 0600); err != nil {
		t.Fatal(err)
	}
	rendered := false
	h := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, body string, _ UIAssets) error {
		rendered = page == "questionnaireDetail" && strings.Contains(body, `id="questionnaire-editor-config"`) && !strings.Contains(body, "<script")
		w.WriteHeader(http.StatusOK)
		return nil
	})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/questionnaireDetail.html", nil))
	if response.Code != http.StatusOK || !rendered {
		t.Fatalf("presentation-marked editor: status=%d rendered=%t", response.Code, rendered)
	}
	if _, err := extractQuestionnaireEditor(strings.Replace(decorated, `data-ui-surface="admin"`, `data-ui-surface="other"`, 1)); err == nil {
		t.Fatal("unrecognized body variant was accepted")
	}
}
