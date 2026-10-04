package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Exercise the real authenticated HTTP and persisted Product contract used by
// the editor. Provider effects stay disabled in this composition fixture.
func TestPostgreSQLProductImageEditorClearAndCopy(t *testing.T) {
	fixture := newProductExternalPushChromiumFixture(t)
	session, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	for _, product := range []struct {
		id   int64
		path string
	}{{fixture.productID, fmt.Sprintf("/api/v1/products/%d", fixture.productID)}, {fixture.serviceProductID, fmt.Sprintf("/api/admin/service-period-products/%d", fixture.serviceProductID)}} {
		read := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodGet, product.path, "", session, csrf, "")
		var stored map[string]json.RawMessage
		if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &stored) != nil {
			t.Fatalf("read %s: %d %s", product.path, read.Code, read.Body.String())
		}
		if envelope, ok := stored["product"]; ok {
			if json.Unmarshal(envelope, &stored) != nil {
				t.Fatal("invalid product envelope")
			}
		}
		command := map[string]json.RawMessage{}
		for _, key := range []string{"name", "description", "price_minor", "currency", "stock_quantity", "admin_projection"} {
			command[key] = stored[key]
		}
		if product.id == fixture.serviceProductID {
			command["duration_days"] = stored["duration_days"]
		}
		command["expected_version"] = stored["version"]
		command["images"] = json.RawMessage(`[]`)
		body, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("editor-clear-images-%d", product.id)
		cleared := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPut, product.path, string(body), session, csrf, key)
		if cleared.Code != 200 {
			t.Fatalf("clear %s: %d %s", product.path, cleared.Code, cleared.Body.String())
		}
		replay := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPut, product.path, string(body), session, csrf, key)
		if replay.Code != 200 {
			t.Fatalf("clear replay: %d %s", replay.Code, replay.Body.String())
		}
		var images string
		var version int64
		if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT images::text,version FROM products WHERE id=$1`, product.id).Scan(&images, &version); err != nil || images != "[]" {
			t.Fatalf("persisted images=%s err=%v", images, err)
		}
		// Copy the cleared product through the same editor lifecycle endpoint.
		copyPath := product.path + "/copy"
		if product.id == fixture.productID {
			copyPath = fmt.Sprintf("/api/admin/wechat-pay/products/%d/copy", product.id)
		}
		copyBody := fmt.Sprintf(`{"expected_version":%d}`, version)
		copyKey := fmt.Sprintf("editor-copy-empty-images-%d", product.id)
		copied := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPost, copyPath, copyBody, session, csrf, copyKey)
		if copied.Code != 200 && copied.Code != 201 {
			t.Fatalf("copy %s: %d %s", copyPath, copied.Code, copied.Body.String())
		}
		copiedAgain := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPost, copyPath, copyBody, session, csrf, copyKey)
		if copiedAgain.Code != copied.Code {
			t.Fatalf("copy replay: %d %s", copiedAgain.Code, copiedAgain.Body.String())
		}
		after := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodGet, product.path, "", session, csrf, "")
		if after.Code != 200 {
			t.Fatalf("final read: %d", after.Code)
		}
		// The wire contract declares an array: len(nil)==0 must not hide a
		// null response after a successful persisted clear or empty copy.
		for _, result := range [][]byte{cleared.Body.Bytes(), replay.Body.Bytes(), copied.Body.Bytes(), copiedAgain.Body.Bytes(), after.Body.Bytes()} {
			var response map[string]json.RawMessage
			if err := json.Unmarshal(result, &response); err != nil {
				t.Fatal(err)
			}
			if envelope, ok := response["product"]; ok {
				if err := json.Unmarshal(envelope, &response); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(bytes.TrimSpace(response["images"]), []byte("[]")) {
				t.Fatalf("%s empty images wire contract: %s", product.path, result)
			}
		}
	}
}
