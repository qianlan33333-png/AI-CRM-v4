package http

import (
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
)

func TestDirectoryFailureResponseUsesClosedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ stage, reason, code, message string }{
		{"detail", "provider_permission_denied", "group_directory_detail_provider_permission_denied", "读取群详情失败：企微权限不足"},
		{"list", "provider_timeout", "group_directory_list_provider_timeout", "读取群列表失败：请求超时"},
		{"secret-token", "raw-provider-secret", "group_directory_list_provider_unavailable", "读取群列表失败：企微服务暂不可用"},
	} {
		w := httptest.NewRecorder()
		(&Handler{}).respondStatus(w, stdhttp.StatusOK, nil, groupopsapp.NewGroupDirectoryReadError(tc.stage, tc.reason))
		if w.Code != stdhttp.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unsafe response: %s", w.Body.String())
		}
		var body struct {
			Message string `json:"message"`
			Error   struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != tc.code || !strings.HasPrefix(body.Message, tc.message) {
			t.Fatalf("response=%s", w.Body.String())
		}
	}
}
