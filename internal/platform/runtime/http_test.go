package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthAndReadinessHaveSeparateSemantics(t *testing.T) {
	ready := false
	handler, err := NewHandler(HandlerOptions{
		ReleaseSHA: "0123456789abcdef",
		Readiness: ReadinessFunc(func(context.Context) error {
			if !ready {
				return errors.New("dependency unavailable")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"alive"`) {
		t.Fatalf("health response=%d %s", health.Code, health.Body.String())
	}

	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable || !strings.Contains(notReady.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("not-ready response=%d %s", notReady.Code, notReady.Body.String())
	}

	ready = true
	readyResponse := httptest.NewRecorder()
	handler.ServeHTTP(readyResponse, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyResponse.Code != http.StatusOK || !strings.Contains(readyResponse.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response=%d %s", readyResponse.Code, readyResponse.Body.String())
	}
	if readyResponse.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control=%q", readyResponse.Header().Get("Cache-Control"))
	}
}

func TestHandlerRejectsIncompleteOptions(t *testing.T) {
	if _, err := NewHandler(HandlerOptions{}); err == nil {
		t.Fatal("expected invalid options error")
	}
}
