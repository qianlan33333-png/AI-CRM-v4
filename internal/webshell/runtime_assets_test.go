package webshell

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeAssetsHandlerCachesOnlyManifestContentHashedAssets(t *testing.T) {
	fixture := runtimeAssetsFixture(t)
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Runtime-Asset-Fallback", "true")
		writer.WriteHeader(http.StatusOK)
	})
	handler := NewRuntimeAssetsHandler(fixture.dist, fallback)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/admin-ABCD1234.js", nil))
	if response.Code != http.StatusOK || response.Body.String() != "export const admin = true;" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("cache-control=%q", got)
	}
	if got := response.Header().Get("ETag"); got != `"`+fixture.assetSHA256+`"` {
		t.Fatalf("etag=%q", got)
	}
	if got := response.Header().Get("Vary"); got != "" {
		t.Fatalf("unexpected vary=%q without a compression middleware", got)
	}
	if response.Header().Get("X-Runtime-Asset-Fallback") != "" {
		t.Fatal("hashed manifest asset reached fallback")
	}

	notModified := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/admin-ABCD1234.js", nil)
	request.Header.Set("If-None-Match", `"`+fixture.assetSHA256+`"`)
	handler.ServeHTTP(notModified, request)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional status=%d body=%q", notModified.Code, notModified.Body.String())
	}
	if notModified.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("conditional cache-control=%q", notModified.Header().Get("Cache-Control"))
	}

	for _, requestPath := range []string{
		"/assets/admin.js",
		"/assets/standard-components/material_picker.js",
		"/assets/chunks/chunk-NOTINMAN.js",
		"/assets/asset-manifest.json",
	} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != http.StatusOK || response.Header().Get("X-Runtime-Asset-Fallback") != "true" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("path=%s status=%d fallback=%q cache=%q", requestPath, response.Code, response.Header().Get("X-Runtime-Asset-Fallback"), response.Header().Get("Cache-Control"))
		}
	}
}

func TestRuntimeAssetsHandlerPreservesHeadAndInvalidAssetFallback(t *testing.T) {
	fixture := runtimeAssetsFixture(t)
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNotFound)
	})
	handler := NewRuntimeAssetsHandler(fixture.dist, fallback)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/assets/admin-ABCD1234.js", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("head status=%d body=%q cache=%q", response.Code, response.Body.String(), response.Header().Get("Cache-Control"))
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/../asset-manifest.json", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("invalid status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestRuntimeAssetsHandlerRejectsManifestMismatchAndChangedFile(t *testing.T) {
	fixture := runtimeAssetsFixture(t)
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Runtime-Asset-Fallback", "true")
		writer.WriteHeader(http.StatusNotFound)
	})
	handler := NewRuntimeAssetsHandler(fixture.dist, fallback)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/invalid-ABCD1234.js", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("X-Runtime-Asset-Fallback") != "true" || strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("invalid manifest hash status=%d fallback=%q cache=%q", response.Code, response.Header().Get("X-Runtime-Asset-Fallback"), response.Header().Get("Cache-Control"))
	}

	writeRuntimeAsset(t, fixture.dist, "assets/admin-ABCD1234.js", "changed after process start")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/admin-ABCD1234.js", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("X-Runtime-Asset-Fallback") != "true" || response.Header().Get("ETag") != "" || strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("changed file status=%d fallback=%q etag=%q cache=%q", response.Code, response.Header().Get("X-Runtime-Asset-Fallback"), response.Header().Get("ETag"), response.Header().Get("Cache-Control"))
	}
}

func TestRuntimeAssetsHandlerClearsImmutableHeadersForRangeAndPreconditionErrors(t *testing.T) {
	fixture := runtimeAssetsFixture(t)
	handler := NewRuntimeAssetsHandler(fixture.dist, http.NotFoundHandler())

	for _, testCase := range []struct {
		name       string
		headerName string
		header     string
		status     int
	}{
		{name: "unsatisfiable range", headerName: "Range", header: "bytes=999-1000", status: http.StatusRequestedRangeNotSatisfiable},
		{name: "failed precondition", headerName: "If-Match", header: `"another-release"`, status: http.StatusPreconditionFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/assets/admin-ABCD1234.js", nil)
			request.Header.Set(testCase.headerName, testCase.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Fatalf("status=%d", response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" {
				t.Fatalf("error leaked immutable headers cache=%q etag=%q", response.Header().Get("Cache-Control"), response.Header().Get("ETag"))
			}
		})
	}
}

type runtimeAssets struct {
	dist        string
	assetSHA256 string
}

func runtimeAssetsFixture(t *testing.T) runtimeAssets {
	t.Helper()
	dist := t.TempDir()
	content := "export const admin = true;"
	assetSHA256 := writeRuntimeAsset(t, dist, "assets/admin-ABCD1234.js", content)
	writeRuntimeAsset(t, dist, "assets/admin.js", "legacy")
	writeRuntimeAsset(t, dist, "assets/standard-components/material_picker.js", "legacy picker")
	writeRuntimeAsset(t, dist, "assets/invalid-ABCD1234.js", "invalid manifest integrity")
	manifest := `{"files":{"assets/admin-ABCD1234.js":{"sha256":"` + assetSHA256 + `"},"assets/invalid-ABCD1234.js":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"assets/admin.js":{"sha256":"` + assetSHA256 + `"},"assets/standard-components/material_picker.js":{"sha256":"` + assetSHA256 + `"}}}`
	writeRuntimeAsset(t, dist, "asset-manifest.json", manifest)
	return runtimeAssets{dist: dist, assetSHA256: assetSHA256}
}

func writeRuntimeAsset(t *testing.T, dist, relative, content string) string {
	t.Helper()
	file := filepath.Join(dist, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
