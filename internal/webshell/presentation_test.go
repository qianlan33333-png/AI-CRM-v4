package webshell

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationAssetsAndInitialServerStage(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, name := range []string{"surfaceFeedbackHost", "surfaceFeedbackStyles", "actionFeedbackStyles", "presentationStyles"} {
		entries[name] = "assets/" + name + ".test"
		if err := os.WriteFile(filepath.Join(dir, entries[name]), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries["adminDateTimeHost"] = "assets/adminDateTimeHost.test"
	if err := os.WriteFile(filepath.Join(dir, entries["adminDateTimeHost"]), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	entries["selectionDialogStyles"] = "assets/selectionDialogStyles.test"
	if err := os.WriteFile(filepath.Join(dir, entries["selectionDialogStyles"]), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"confirmationDialogHost", "confirmationDialogStyles"} {
		entries[name] = "assets/" + name + ".test"
		if err := os.WriteFile(filepath.Join(dir, entries[name]), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(map[string]any{"entries": entries})
	if err := os.WriteFile(filepath.Join(dir, "asset-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	functions, err := presentationFunctions(dir)
	if err != nil {
		t.Fatal(err)
	}
	assets := functions["presentationAssets"].(func() PresentationAssets)()
	if len(assets.Styles) != 3 || assets.Script != "/assets/surfaceFeedbackHost.test" || assets.AdminDateTimeScript != "/assets/adminDateTimeHost.test" || assets.SelectionDialogCSS != "/assets/selectionDialogStyles.test" || assets.ConfirmationDialogCSS != "/assets/confirmationDialogStyles.test" || assets.ConfirmationDialogScript != "/assets/confirmationDialogHost.test" {
		t.Fatalf("unexpected assets: %#v", assets)
	}
	content := functions["presentationContent"].(func(template.HTML) template.HTML)
	initial := content(`<main id="stage" class="stage"></main>`)
	if !strings.Contains(string(initial), "data-surface-placeholder") {
		t.Fatal("empty server stage has no accessible initial loading")
	}
	ready := template.HTML(`<main id="stage"><p>ready</p></main>`)
	if content(ready) != ready {
		t.Fatal("presentation changed already-rendered content")
	}
	if _, err := NewRenderer(dir); err != nil {
		t.Fatal(err)
	}
	delete(entries, "confirmationDialogHost")
	encoded, _ = json.Marshal(map[string]any{"entries": entries})
	if err := os.WriteFile(filepath.Join(dir, "asset-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRenderer(dir); err == nil {
		t.Fatal("incomplete confirmation dialog asset pair was accepted")
	}
	entries["confirmationDialogHost"] = "assets/confirmationDialogHost.test"
	delete(entries, "adminDateTimeHost")
	encoded, _ = json.Marshal(map[string]any{"entries": entries, "release_files": map[string]any{"assets/surfaceFeedbackHost.test": map[string]any{}}})
	if err := os.WriteFile(filepath.Join(dir, "asset-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRenderer(dir); err == nil {
		t.Fatal("production release manifest without admin date/time Host was accepted")
	}
	entries["adminDateTimeHost"] = "assets/adminDateTimeHost.test"
	entries["presentationStyles"] = "assets/../escape.css"
	encoded, _ = json.Marshal(map[string]any{"entries": entries})
	if err := os.WriteFile(filepath.Join(dir, "asset-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRenderer(dir); err == nil {
		t.Fatal("invalid asset manifest was accepted")
	}
}
