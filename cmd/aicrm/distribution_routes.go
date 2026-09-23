package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// distributionUnavailableHandler deliberately exposes no fallback business
// state when Payment's scoped identity/provider configuration is absent.
type distributionUnavailableHandler struct{}

func (distributionUnavailableHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"distribution_unavailable"}`))
}

// mountDistribution keeps public distributor sessions separate from employee
// Access routes. Admin routes are still authorized by Distribution's own
// AdminHandler using the normal employee session and CSRF boundary.
func mountDistribution(next, public, admin http.Handler) http.Handler {
	if public == nil {
		public = distributionUnavailableHandler{}
	}
	if admin == nil {
		admin = distributionUnavailableHandler{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/d/") || strings.HasPrefix(r.URL.Path, "/api/v1/distribution/"):
			public.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/admin/distribution/"):
			admin.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// distributionRuntimeAssets exposes only the manifest closure of the two
// public Distribution documents.  Admin donor assets continue through Access
// authentication; a distributor page cannot load a broad private /assets/
// tree merely because its own hashed runtime is public.
func distributionRuntimeAssets(next http.Handler, dist string) http.Handler {
	type file struct {
		Imports []struct {
			Path string `json:"path"`
		} `json:"imports"`
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
		Files   map[string]file   `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return next
	}
	allowed := map[string]struct{}{}
	var include func(string)
	include = func(name string) {
		if _, ok := allowed[name]; ok {
			return
		}
		value, ok := manifest.Files[name]
		if !ok {
			return
		}
		allowed[name] = struct{}{}
		for _, dependency := range value.Imports {
			include(dependency.Path)
		}
	}
	for _, entry := range []string{"distributionCenter", "distributionAdmin", "distributionStyles", "sharedDetailDrawerStyles", "referralCenter", "referralStyles"} {
		include(manifest.Entries[entry])
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if _, ok := allowed[name]; ok {
				http.ServeFile(w, r, filepath.Join(dist, filepath.FromSlash(name)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
