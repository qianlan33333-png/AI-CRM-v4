package webshell

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PresentationAssets is browser presentation only, shared by server-rendered
// pages and generated documents. It introduces no domain/API dependencies.
type PresentationAssets struct {
	Script                   string
	Styles                   []string
	AdminDateTimeScript      string
	SelectionDialogCSS       string
	ConfirmationDialogCSS    string
	ConfirmationDialogScript string
}

var emptyPresentationStage = regexp.MustCompile(`(<main\b[^>]*\bid="stage"[^>]*>)\s*(</main>)`)

func presentationFunctions(distDir string) (template.FuncMap, error) {
	var assets PresentationAssets
	if distDir != "" {
		contents, err := os.ReadFile(filepath.Join(distDir, "asset-manifest.json"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			var manifest struct {
				Entries      map[string]string `json:"entries"`
				ReleaseFiles map[string]any    `json:"release_files"`
			}
			if err := json.Unmarshal(contents, &manifest); err != nil {
				return nil, err
			}
			// A raw donor build is also used by older fixtures. Only enable the
			// presentation seam once its complete Host closure has been built.
			validEntry := func(key string) (string, error) {
				entry := manifest.Entries[key]
				if !strings.HasPrefix(entry, "assets/") || strings.Contains(entry, "..") || strings.ContainsAny(entry, "\\?#") {
					return "", fmt.Errorf("invalid presentation asset: %s", key)
				}
				if _, err := os.Stat(filepath.Join(distDir, filepath.FromSlash(entry))); err != nil {
					return "", fmt.Errorf("missing presentation asset: %s", key)
				}
				return "/" + entry, nil
			}
			if manifest.Entries["adminDateTimeHost"] == "" && len(manifest.ReleaseFiles) > 0 {
				return nil, fmt.Errorf("required production presentation asset is absent: adminDateTimeHost")
			}
			if manifest.Entries["adminDateTimeHost"] != "" {
				script, err := validEntry("adminDateTimeHost")
				if err != nil {
					return nil, err
				}
				assets.AdminDateTimeScript = script
			}
			if manifest.Entries["surfaceFeedbackHost"] != "" {
				for _, key := range []string{"surfaceFeedbackHost", "surfaceFeedbackStyles", "actionFeedbackStyles", "presentationStyles"} {
					entry, err := validEntry(key)
					if err != nil {
						return nil, err
					}
					if key == "surfaceFeedbackHost" {
						assets.Script = entry
					} else {
						assets.Styles = append(assets.Styles, entry)
					}
				}
			}
			if manifest.Entries["selectionDialogStyles"] != "" {
				selectionDialogCSS, err := validEntry("selectionDialogStyles")
				if err != nil {
					return nil, err
				}
				assets.SelectionDialogCSS = selectionDialogCSS
			}
			if manifest.Entries["confirmationDialogHost"] != "" || manifest.Entries["confirmationDialogStyles"] != "" {
				confirmationDialogCSS, err := validEntry("confirmationDialogStyles")
				if err != nil {
					return nil, err
				}
				confirmationDialogScript, err := validEntry("confirmationDialogHost")
				if err != nil {
					return nil, err
				}
				assets.ConfirmationDialogCSS = confirmationDialogCSS
				assets.ConfirmationDialogScript = confirmationDialogScript
			}
		}
	}
	return template.FuncMap{
		"presentationAssets": func() PresentationAssets { return assets },
		"presentationContent": func(content template.HTML) template.HTML {
			if assets.Script == "" {
				return content
			}
			const loading = `<div class="surface-feedback__busy surface-feedback__busy--initial" data-surface-placeholder role="status" aria-live="polite"><span class="surface-feedback__spinner" aria-hidden="true"></span><span>正在加载页面…</span></div>`
			return template.HTML(emptyPresentationStage.ReplaceAllString(string(content), "${1}"+loading+"${2}"))
		},
	}, nil
}
