package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type UIAssets struct {
	TokensCSS, LabsCSS, AdminJS, StandardHostJS string
	StandardCSS                                 []string
}
type ChannelPageRenderer func(http.ResponseWriter, *http.Request, string, string, string, UIAssets) error
type channelUI struct {
	dist   string
	render ChannelPageRenderer
}

func (module *ModuleRegistration) UIBinding(dist string, render ChannelPageRenderer) http.Handler {
	if module == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &channelUI{dist: dist, render: render}
}

func (handler *channelUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, resourceID, ok := channelPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(handler.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "channel UI unavailable", http.StatusServiceUnavailable)
		return
	}
	body, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "channel UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := channelAssets(handler.dist)
	if err != nil {
		http.Error(w, "channel UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = handler.render(w, r, page, resourceID, body, assets); err != nil {
		http.Error(w, "channel UI unavailable", http.StatusInternalServerError)
	}
}

func channelPage(r *http.Request) (string, string, bool) {
	if r.URL.Path == "/admin/channels" || r.URL.Path == "/admin/channels.html" {
		return "channels", "", len(r.URL.Query()) == 0
	}
	if r.URL.Path == "/admin/channels/new" {
		return "channelForm", "", len(r.URL.Query()) == 0
	}
	if r.URL.Path == "/admin/channelForm.html" {
		values := r.URL.Query()
		if len(values) == 0 {
			return "channelForm", "", true
		}
		ids, ok := values["id"]
		if !ok || len(values) != 1 || len(ids) != 1 {
			return "", "", false
		}
		id, err := strconv.ParseInt(ids[0], 10, 64)
		return "channelForm", ids[0], err == nil && id > 0 && strconv.FormatInt(id, 10) == ids[0]
	}
	const prefix = "/admin/channels/"
	const suffix = "/edit"
	if strings.HasPrefix(r.URL.Path, prefix) && strings.HasSuffix(r.URL.Path, suffix) && len(r.URL.Query()) == 0 {
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && id > 0 && strconv.FormatInt(id, 10) == raw {
			return "channelForm", raw, true
		}
	}
	return "", "", false
}

func channelAssets(dist string) (UIAssets, error) {
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
			return "", errors.New("channel bundle asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(dist, value)); statErr != nil {
			return "", statErr
		}
		return "/" + value, nil
	}
	tokens, err := get("tokens")
	if err != nil {
		return UIAssets{}, err
	}
	labs, err := get("labs")
	if err != nil {
		return UIAssets{}, err
	}
	admin, err := get("channelCenterHost")
	if err != nil {
		return UIAssets{}, err
	}
	standardHost, err := get("standardComponentsHost")
	if err != nil {
		return UIAssets{}, err
	}
	selectionDialogCSS, err := get("selectionDialogStyles")
	if err != nil {
		return UIAssets{}, err
	}
	standardCSS := []string{
		"assets/standard-components/group_chat_picker.css",
		"assets/standard-components/material_picker.css",
		"assets/standard-components/send_content_composer.css",
		"assets/standard-components/wecom_tag_picker.css",
		"assets/channelAdmissionStandard.css",
		strings.TrimPrefix(selectionDialogCSS, "/"),
	}
	for _, value := range standardCSS {
		if _, ok := manifest.Files[value]; !ok {
			return UIAssets{}, errors.New("channel standard component asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(dist, value)); statErr != nil {
			return UIAssets{}, statErr
		}
	}
	return UIAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin, StandardHostJS: standardHost, StandardCSS: standardCSS}, nil
}

var _ http.Handler = (*channelUI)(nil)
