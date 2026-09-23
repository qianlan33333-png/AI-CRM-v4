package automation

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAgentUIExtractsPrivateTemplateAndPreservesAliases(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"agents", "agentEdit"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", p+".html"), []byte(`<div class="shell"><aside class="side"></aside><template id="tpl"><section data-page="`+p+`">frozen</section></template></div>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"tokens.css", "labs.css", "admin.js", "automation-lifecycle.js", "presentation.css", "automation-content.css", "selection-dialog.css", "automation-content.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", p), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets", "standard-components"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"material_picker.css", "material_picker.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", "standard-components", p), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","automationLifecycleHost":"assets/automation-lifecycle.js","presentationStyles":"assets/presentation.css","automationContentStyles":"assets/automation-content.css","selectionDialogStyles":"assets/selection-dialog.css","automationContentHost":"assets/automation-content.js"},"files":{"assets/tokens.css":{},"assets/labs.css":{},"assets/admin.js":{},"assets/automation-lifecycle.js":{},"assets/presentation.css":{},"assets/automation-content.css":{},"assets/selection-dialog.css":{},"assets/automation-content.js":{},"assets/standard-components/material_picker.css":{},"assets/standard-components/material_picker.js":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPage, gotTemplate string
	var gotBootstrap AgentPageBootstrap
	h := NewModuleRegistration().UIBinding(dist, func(_ http.ResponseWriter, _ *http.Request, page, tpl string, assets AgentAssets, bootstrap AgentPageBootstrap) error {
		if page == "agents" && assets.AdminJS != "/assets/automation-lifecycle.js" {
			t.Fatalf("list must mount lifecycle host, got %q", assets.AdminJS)
		}
		gotPage, gotTemplate = page, tpl
		gotBootstrap = bootstrap
		return nil
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/agents.html?type=agent", nil))
	if w.Code != 200 || gotPage != "agents" || gotTemplate != `<section data-page="agents">frozen</section>` {
		t.Fatalf("code=%d page=%q template=%q", w.Code, gotPage, gotTemplate)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?id=7", nil))
	if w.Code != 200 || gotPage != "agentEdit" || gotBootstrap.CreateCode != "" {
		t.Fatalf("detail alias code=%d page=%q bootstrap=%q", w.Code, gotPage, gotBootstrap.CreateCode)
	}
	codePattern := regexp.MustCompile(`^agent_[a-f0-9]{32}$`)
	seen := map[string]bool{}
	for _, target := range []string{"/admin/agentEdit.html", "/admin/agentEdit.html?type=agent", "/admin/agentEdit.html?type=fixed_script", "/admin/agentEdit.html?id=7&saved=1"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK || gotPage != "agentEdit" {
			t.Fatalf("editor target %s code=%d page=%q", target, w.Code, gotPage)
		}
		if target == "/admin/agentEdit.html?id=7&saved=1" {
			if gotBootstrap.CreateCode != "" {
				t.Fatalf("existing editor received create code %q", gotBootstrap.CreateCode)
			}
			continue
		}
		if !codePattern.MatchString(gotBootstrap.CreateCode) || seen[gotBootstrap.CreateCode] {
			t.Fatalf("create editor target %s received invalid or repeated code %q", target, gotBootstrap.CreateCode)
		}
		seen[gotBootstrap.CreateCode] = true
	}
	for _, target := range []string{"/admin/agentEdit.html?type=unsupported", "/admin/agentEdit.html?type=agent&type=fixed_script", "/admin/agentEdit.html?type=agent&id=7"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("unsupported editor target %s code=%d", target, w.Code)
		}
	}
}
