package main

import (
	"context"
	diagnostics "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicSurveyClientDiagnosticsUsesMountedRoute(t *testing.T) {
	mux := http.NewServeMux()
	mountSurveyAPIs(mux, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }))
	var got diagnostics.Observation
	handler := diagnostics.MiddlewareWithClientErrorFilter(mux, strings.Repeat("a", 40), func(_ context.Context, o diagnostics.Observation) error { got = o; return nil }, observePublicSurveyClientError)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/public/questionnaires/fixture-private-slug/submissions", nil))
	if got.Code != "http_client_error_400" || got.RouteTemplate != "/api/public/questionnaires/" {
		t.Fatalf("mounted public failure was not recorded safely: %+v", got)
	}
}
