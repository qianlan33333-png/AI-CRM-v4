package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublicContactPrefillThreeModes(t *testing.T) {
	pages := map[string]string{}
	for _, level := range []string{"none", "mobile", "shipping_address"} {
		p := enabledPublicProduct(7, "contact-course")
		p.LegacyAdminProjection = json.RawMessage(fmt.Sprintf(`{"schema_version":1,"status":"active","enabled":true,"contact_collection_level":%q,"slices":[]}`, level))
		h, e := NewPublicHandler(&testCatalog{product: p})
		if e != nil {
			t.Fatal(e)
		}
		h.SetPaymentMethods(true, true)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pay/contact-course", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		pages[level] = w.Body.String()
		if dir := os.Getenv("AICRM_CONTACT_QA_DIR"); dir != "" {
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, level+".html"), w.Body.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	raw, _ := json.Marshal(pages)
	cmd := exec.Command("node", "testdata/contact_prefill.mjs")
	cmd.Stdin = bytes.NewReader(raw)
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("prefill journey: %v\n%s", e, output)
	}
}
