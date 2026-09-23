package survey

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type UIAssets struct {
	TokensCSS, LabsCSS, AdminJS, EditorJS, EditorCSS, StandardHostJS, SurveyHostJS string
	OperationsHostJS, OperationsCSS                                                string
	StandardCSS                                                                    []string
}

type PageRenderer func(http.ResponseWriter, *http.Request, string, string, UIAssets) error

type adminUI struct {
	dist   string
	render PageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &adminUI{dist: dist, render: render}
}

func (h *adminUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, ok := surveyPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "survey UI unavailable", http.StatusServiceUnavailable)
		return
	}
	body := string(raw)
	if page == "questionnaireDetail" {
		body, err = extractQuestionnaireEditor(body)
		if err != nil {
			http.Error(w, "survey UI unavailable", http.StatusServiceUnavailable)
			return
		}
	} else {
		body, err = donortemplate.Extract(body)
		if err != nil {
			http.Error(w, "survey UI unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	assets, err := surveyAssets(h.dist)
	if err != nil {
		http.Error(w, "survey UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, body, assets); err != nil {
		http.Error(w, "survey UI unavailable", http.StatusInternalServerError)
	}
}

func extractQuestionnaireEditor(raw string) (string, error) {
	const bodyOpen = `<body data-page="questionnaireDetail">`
	// The V3 build adds only this presentation marker to the frozen document.
	// Normalize that known variant without relaxing the editor/body contract.
	raw = strings.Replace(raw, `<body data-ui-surface="admin" data-page="questionnaireDetail">`, bodyOpen, 1)
	start := strings.Index(raw, bodyOpen)
	end := strings.LastIndex(raw, `</body>`)
	if start < 0 || end <= start+len(bodyOpen) {
		return "", errors.New("survey editor body missing")
	}
	body := strings.TrimSpace(raw[start+len(bodyOpen) : end])
	scriptStart := strings.LastIndex(body, `<script type="module" src="../assets/questionnaireEditor-`)
	if scriptStart < 1 {
		return "", errors.New("survey editor entry missing")
	}
	script := body[scriptStart:]
	if !strings.HasSuffix(script, `.js"></script>`) || strings.Contains(script, "\n") {
		return "", errors.New("survey editor entry invalid")
	}
	body = strings.TrimSpace(body[:scriptStart])
	if body == "" || !strings.Contains(body, `id="questionnaire-editor-config"`) {
		return "", errors.New("survey editor configuration missing")
	}
	return body, nil
}

func surveyPage(r *http.Request) (string, bool) {
	values := r.URL.Query()
	switch r.URL.Path {
	case "/admin/questionnaires", "/admin/questionnaires.html":
		if len(values) == 0 {
			return "questionnaires", true
		}
		if len(values) > 2 || (values.Get("unresolved_history") != "1" && values.Get("history_id") == "") {
			return "", false
		}
		if id := values.Get("history_id"); id != "" && !positiveID(id) {
			return "", false
		}
		return "questionnaires", true
	case "/admin/questionnaireDetail.html":
		if len(values) == 0 {
			return "questionnaireDetail", true
		}
		if len(values) == 1 && positiveID(values.Get("id")) {
			return "questionnaireDetail", true
		}
		if len(values) == 1 && values.Get("mode") == "assessment" {
			return "questionnaireDetail", true
		}
	case "/admin/questionnaireOps.html":
		if len(values) == 1 && positiveID(values.Get("id")) {
			return "questionnaireOps", true
		}
	}
	return "", false
}

func positiveID(raw string) bool {
	id, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
}

func surveyAssets(dist string) (UIAssets, error) {
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil {
		return UIAssets{}, err
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return UIAssets{}, err
	}
	get := func(key string) (string, error) {
		value := manifest.Entries[key]
		if value == "" || !strings.HasPrefix(value, "assets/") || strings.Contains(value, "..") {
			return "", errors.New("survey bundle asset missing")
		}
		if _, err := os.Stat(filepath.Join(dist, value)); err != nil {
			return "", err
		}
		return "/survey-assets/" + strings.TrimPrefix(value, "assets/"), nil
	}
	assets := UIAssets{}
	if assets.TokensCSS, err = get("tokens"); err != nil {
		return UIAssets{}, err
	}
	if assets.LabsCSS, err = get("labs"); err != nil {
		return UIAssets{}, err
	}
	if assets.SurveyHostJS, err = get("surveyHost"); err != nil {
		return UIAssets{}, err
	}
	if assets.OperationsHostJS, err = get("surveyOperationsHost"); err != nil {
		return UIAssets{}, err
	}
	if assets.OperationsCSS, err = get("surveyOperationsStyles"); err != nil {
		return UIAssets{}, err
	}
	if assets.EditorJS, err = get("questionnaireEditor"); err != nil {
		return UIAssets{}, err
	}
	if assets.EditorCSS, err = get("questionnaireEditorStyles"); err != nil {
		return UIAssets{}, err
	}
	if assets.StandardHostJS, err = get("standardComponentsHost"); err != nil {
		return UIAssets{}, err
	}
	for _, relative := range []string{"assets/standard-components/wecom_tag_picker.css"} {
		if _, ok := manifest.Files[relative]; !ok {
			return UIAssets{}, errors.New("survey standard component asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(dist, relative)); statErr != nil {
			return UIAssets{}, statErr
		}
		assets.StandardCSS = append(assets.StandardCSS, "/survey-assets/"+strings.TrimPrefix(relative, "assets/"))
	}
	return assets, nil
}

type publicUI struct{ dist string }

func (m *ModuleRegistration) PublicUIBinding(dist string) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" {
		return http.NotFoundHandler()
	}
	return &publicUI{dist: dist}
}

func (h *publicUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	var root, relative string
	if strings.HasPrefix(path, "/h5/") {
		root, relative = filepath.Join(h.dist, "h5"), strings.TrimPrefix(path, "/h5/")
		if relative == "" {
			relative = "index.html"
		}
		if !strings.HasSuffix(relative, ".html") {
			http.NotFound(w, r)
			return
		}
	} else if strings.HasPrefix(path, "/survey-assets/") {
		root, relative = filepath.Join(h.dist, "assets"), strings.TrimPrefix(path, "/survey-assets/")
	} else {
		http.NotFound(w, r)
		return
	}
	if relative == "" || strings.Contains(relative, "..") || filepath.IsAbs(relative) {
		http.NotFound(w, r)
		return
	}
	filename := filepath.Join(root, filepath.FromSlash(relative))
	content, err := os.ReadFile(filename)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(relative, ".html") {
		content = []byte(strings.ReplaceAll(string(content), "../assets/", "../survey-assets/"))
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if kind := mime.TypeByExtension(filepath.Ext(relative)); kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}
