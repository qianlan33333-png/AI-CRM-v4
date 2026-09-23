package webshell

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DistReferralAdminAssets only accepts complete, manifest-declared release assets.
func DistReferralAdminAssets(distRoot string) (DistributionAssets, bool) {
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(distRoot, "asset-manifest.json"))
	if distRoot == "" || err != nil || json.Unmarshal(raw, &manifest) != nil {
		return DistributionAssets{}, false
	}
	asset := func(name, suffix string) (string, bool) {
		entry := manifest.Entries[name]
		if !strings.HasPrefix(entry, "assets/") || path.Clean(entry) != entry || !strings.HasSuffix(entry, suffix) || manifest.Files[entry] == nil {
			return "", false
		}
		info, err := os.Stat(filepath.Join(distRoot, filepath.FromSlash(entry)))
		if err != nil || info.IsDir() {
			return "", false
		}
		return "/" + entry, true
	}
	css, ok := asset("referralStyles", ".css")
	if !ok {
		return DistributionAssets{}, false
	}
	js, ok := asset("referralAdmin", ".js")
	if !ok {
		return DistributionAssets{}, false
	}
	drawer, ok := asset("sharedDetailDrawerStyles", ".css")
	if !ok {
		return DistributionAssets{}, false
	}
	return DistributionAssets{CSS: css, AdminJS: js, DetailDrawerCSS: drawer}, true
}

// RenderReferral shares the same CRM staff shell and its asset mounting contract.
func (renderer *Renderer) RenderReferral(writer http.ResponseWriter, data AdminPageData, assets DistributionAssets) error {
	if renderer == nil || renderer.templates == nil || assets.CSS == "" || assets.AdminJS == "" {
		return errors.New("referral shell assets are required")
	}
	normalizeAdminPage(&data)
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(`<section id="referral-admin-root" aria-live="polite"></section>`), Distribution: true, DistributionAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

func (handler *Handler) serveReferral(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	if handler.distDir == "" {
		http.Error(writer, "referral page unavailable", http.StatusServiceUnavailable)
		return
	}
	file := filepath.Join(handler.distDir, "referral", "index.html")
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		http.Error(writer, "referral page unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Referrer-Policy", "no-referrer")
	serveDistAdminPage(writer, request, file)
}
