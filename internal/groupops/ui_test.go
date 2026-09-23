package groupops

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivePageUsesStandardHostAndManifestBoundAssets(t *testing.T) {
	dist := t.TempDir()
	files := map[string]string{
		"assets/tokens.css": "", "assets/labs.css": "", "assets/admin.js": "", "assets/groupops.css": "", "assets/groupops-host.js": "", "assets/selection-dialog.css": "",
		"assets/standard-components/operation_member_picker.js": "", "assets/standard-components/group_chat_picker.css": "", "assets/standard-components/group_chat_picker.js": "", "assets/standard-components/material_picker.css": "", "assets/standard-components/material_picker.js": "", "assets/standard-components/send_content_composer.css": "", "assets/standard-components/send_content_composer.js": "",
		"aiassistant/send_content_readonly_detail.css": "", "aiassistant/send_content_readonly_detail.js": "",
	}
	for relative, content := range files {
		path := filepath.Join(dist, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","groupopsStyles":"assets/groupops.css","groupopsHost":"assets/groupops-host.js","selectionDialogStyles":"assets/selection-dialog.css"},"files":{`
	parts := make([]string, 0, len(files))
	for relative := range files {
		parts = append(parts, fmt.Sprintf("%q:{}", relative))
	}
	manifest += strings.Join(parts, ",") + `}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotBody string
	var gotAssets GroupOpsAssets
	h := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, body string, assets GroupOpsAssets) error {
		if page != "groupopsDetail" {
			return fmt.Errorf("page=%q", page)
		}
		gotBody, gotAssets = body, assets
		w.WriteHeader(http.StatusNoContent)
		return nil
	})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/automation-conversion/group-ops/plans/9", nil))
	if response.Code != http.StatusNoContent || !strings.Contains(gotBody, `id="group-ops-app"`) || !strings.Contains(gotBody, `data-page-mode="detail"`) || !strings.Contains(gotBody, `data-plan-id="9"`) || gotAssets.HostJS != "/groupops-assets/assets/groupops-host.js" || gotAssets.ComposerJS != "/groupops-assets/assets/standard-components/send_content_composer.js" || gotAssets.OperationPickerJS != "/groupops-assets/assets/standard-components/operation_member_picker.js" {
		t.Fatalf("host=%q assets=%+v status=%d", gotBody, gotAssets, response.Code)
	}
}
