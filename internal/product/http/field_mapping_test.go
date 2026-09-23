package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestFieldMappingPreviewAuthenticatedSyntheticNoMutation(t *testing.T) {
	for _, base := range []string{"/api/admin/wechat-pay/products/7", "/api/admin/service-period-products/7"} {
		h, sec, _, _ := newHandlerForTest(t)
		body := `{"field_mapping":{"version":1,"fields":[{"key":"phone","source":"variable","value_type":"string","variable":"order.mobile"},{"key":"amount","source":"fixed","value_type":"number","value":9007199254740993}]}}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, base+"/external-push/preview", strings.NewReader(body)))
		if w.Code != 200 || sec.csrfCalls != 1 {
			t.Fatalf("preview %d %s", w.Code, w.Body.String())
		}
		var result struct {
			PayloadJSON string `json:"payload_json"`
			Synthetic   bool   `json:"synthetic"`
			Real        bool   `json:"real_external_call_executed"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Synthetic || result.Real || result.PayloadJSON != `{"amount":9007199254740993,"phone":"+8613800000000"}` {
			t.Fatalf("payload %s", w.Body.String())
		}
		ext := h.external.(*testExternalPush)
		if ext.save != nil || ext.queue != nil {
			t.Fatal("preview performed mutation")
		}
		sec.csrfErr = errors.New("denied")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, base+"/external-push/preview", strings.NewReader(body)))
		if w.Code < 400 {
			t.Fatal("csrf bypass")
		}
	}
}
func TestFieldMappingSaveOmittedNullAndTyped(t *testing.T) {
	for _, suffix := range []string{"", `,"field_mapping":null`, `,"field_mapping":{"version":1,"fields":[{"key":"a","source":"fixed","value_type":"number","value":9007199254740993}]}`} {
		h, _, _, _ := newHandlerForTest(t)
		body := `{"enabled":false,"expected_revision":0,"type":"","day":null,"frequency":null,"expires_at_ts":null,"remark":"","custom_params":{}` + suffix + `}`
		r := httptest.NewRequest(http.MethodPut, "/api/admin/wechat-pay/products/7/external-push", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", "mapping-http-save-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("save %d %s", w.Code, w.Body.String())
		}
		command := h.external.(*testExternalPush).save
		if command == nil || command.FieldMappingSet != (suffix != "") {
			t.Fatal("presence lost")
		}
		if strings.Contains(suffix, "900719") {
			raw, e := productport.CompileFieldMapping(command.FieldMapping, nil)
			if e != nil || string(raw) != `{"a":9007199254740993}` {
				t.Fatal("fixed precision lost")
			}
		}
	}
}
