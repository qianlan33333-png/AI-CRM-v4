package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointProbeSeparatesAuthBoundaryVersionAndAvailability(t *testing.T) {
	leaked := false
	release := "fixture"
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("probe mutated or supplied credentials")
		}
		switch r.URL.Path {
		case "/healthz", "/readyz":
			if failed {
				w.WriteHeader(503)
				return
			}
			fmt.Fprintf(w, `{"status":"ready","release_sha":%q}`, release)
		case "/admin/ops":
			if !leaked {
				http.Redirect(w, r, "/login?next=%2Fadmin%2Fops", 303)
			}
		default:
			if !leaked {
				w.WriteHeader(403)
			}
		}
	}))
	defer server.Close()
	p, err := NewEndpointProbe(strings.TrimPrefix(server.URL, "http://"), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	counts, err := p.Counts(context.Background(), time.Now())
	if err != nil || counts["observed_endpoints"] != 4 || counts["auth_boundary_failed"] != 0 || counts["failed_endpoints"] != 0 {
		t.Fatalf("healthy=%v %v", counts, err)
	}
	leaked = true
	release = "stale"
	counts, err = p.Counts(context.Background(), time.Now())
	if err != nil || counts["auth_boundary_failed"] != 2 || counts["version_mismatch"] != 2 {
		t.Fatalf("masked issue=%v %v", counts, err)
	}
	failed = true
	counts, err = p.Counts(context.Background(), time.Now())
	if err != nil || counts["failed_endpoints"] != 2 {
		t.Fatalf("false healthy readiness=%v %v", counts, err)
	}
	if _, err = NewEndpointProbe("169.254.169.254:80", "fixture"); err == nil {
		t.Fatal("remote target accepted")
	}
}
