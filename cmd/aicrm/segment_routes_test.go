package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMountSegmentAPIOwnsOnlyCanonicalPrefix(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(210) })
	audience := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(211) })
	handler, err := mountSegmentAPI(base, audience)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/api/admin/ai-audience/packages": 211,
		"/api/admin/customers":            210,
		"/admin/automation-conversion":    210,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, want)
		}
	}
}

func TestMountAutomationRuntimeAPIOwnsCanonicalRuntimePaths(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(210) })
	runtime := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(212) })
	handler, err := mountAutomationRuntimeAPI(base, runtime)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/api/admin/automations":                                212,
		"/api/admin/automations/42":                             212,
		"/api/admin/automation-runs":                            212,
		"/api/admin/automation-runs/42/reconcile":               212,
		"/api/admin/ai-audience/packages/42/broadcast-previews": 212,
		"/api/admin/ai-audience/packages/42/runs":               212,
		"/api/admin/ai-audience/packages/42":                    210,
		"/api/admin/customers":                                  210,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, want)
		}
	}
}
