package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMediaHTTPManualImageLimitAndOriginalBytes(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	repo, closeRepo, _ := newHTTPIntegrationRepository(t, url)
	defer closeRepo()
	service, err := mediaapp.NewHTTPFacade(repo)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, handlerTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		req.Header.Set("X-CSRF-Token", "test-csrf")
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	png := httpPNG(t)
	padded := func(size int) []byte { return append(append([]byte{}, png...), make([]byte, size-len(png))...) }
	large := padded(mediaport.ManualImageUploadMaxBytes + 1)
	if got := serve(multipartImageRequest(t, "/api/admin/image-library/upload", "image", "large.png", large)); got.Code != 413 {
		t.Fatalf("multipart oversize=%d", got.Code)
	}
	body, _ := json.Marshal(map[string]any{"data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(large), "file_name": "large.png"})
	if got := serve(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", bytes.NewReader(body))); got.Code != 413 {
		t.Fatalf("JSON oversize=%d", got.Code)
	}
	exact := padded(mediaport.ManualImageUploadMaxBytes)
	req := multipartImageRequest(t, "/api/admin/image-library/upload", "image", "exact.png", exact)
	req.Header.Set("Idempotency-Key", "http-exact-image-size-0001")
	result := responseJSON(t, serve(req), 200)
	id := jsonID(int64(result["item_id"].(float64)))
	get := httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+id+"/variants/original", nil)
	original := serve(get)
	if original.Code != 200 || !bytes.Equal(original.Body.Bytes(), exact) {
		t.Fatal("allowed original bytes changed")
	}
	if original.Header().Get("ETag") == "" {
		t.Fatal("original digest missing")
	}
	req = multipartImageRequest(t, "/api/admin/image-library/upload", "image", "exact.png", exact)
	req.Header.Set("Idempotency-Key", "http-exact-image-size-0001")
	replay := responseJSON(t, serve(req), 200)
	if replay["item_id"] != result["item_id"] {
		t.Fatal("HTTP replay created a different image")
	}
}
