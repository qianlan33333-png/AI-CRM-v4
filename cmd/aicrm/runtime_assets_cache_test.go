package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
)

func TestAdminRuntimeAssetsRequireSessionBeforeImmutableHeaders(t *testing.T) {
	dist := t.TempDir()
	content := []byte("export const admin = true;")
	asset := filepath.Join(dist, "assets", "admin-ABCD1234.js")
	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	manifest := `{"files":{"assets/admin-ABCD1234.js":{"sha256":"` + hex.EncodeToString(sum[:]) + `"}}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	authentication := &fakeAccessAuthentication{err: errors.New("session required")}
	handler := requireAdminSession(authentication, webshell.NewRuntimeAssetsHandler(dist, http.NotFoundHandler()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/admin-ABCD1234.js", nil))

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fassets%2Fadmin-ABCD1234.js" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if response.Header().Get("Cache-Control") != "" || response.Header().Get("ETag") != "" {
		t.Fatalf("unauthenticated asset leaked cache headers cache=%q etag=%q", response.Header().Get("Cache-Control"), response.Header().Get("ETag"))
	}
}
