package testutil

import (
	"net/url"
	"testing"
)

func TestWithSearchPathPreservesAndAddsQueryParameters(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{name: "no existing query", dsn: "postgres://user:secret@localhost/aicrm_test_fixture"},
		{name: "existing query", dsn: "postgres://user:secret@localhost/aicrm_test_fixture?sslmode=disable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WithSearchPath(tt.dsn, "fixture_schema")
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			if query.Get("search_path") != "fixture_schema" {
				t.Fatalf("search_path=%q", query.Get("search_path"))
			}
			if tt.name == "existing query" && query.Get("sslmode") != "disable" {
				t.Fatalf("sslmode=%q", query.Get("sslmode"))
			}
			if parsed.Path != "/aicrm_test_fixture" {
				t.Fatalf("path=%q", parsed.Path)
			}
		})
	}
}

func TestWithSearchPathRejectsInvalidURL(t *testing.T) {
	if _, err := WithSearchPath("postgres://[broken", "fixture_schema"); err == nil {
		t.Fatal("invalid database URL was accepted")
	}
}

func TestIsLocalTestDatabaseURLMatchesCanonicalPreflightBoundary(t *testing.T) {
	accepted := []string{
		"postgres://user:pass@localhost/aicrm_test_one",
		"postgresql://user:pass@127.0.0.1/aicrm_test_payment_9bbbf28?sslmode=disable",
		"postgres://user:pass@[::1]/aicrm_ci",
	}
	for _, value := range accepted {
		if !IsLocalTestDatabaseURL(value) {
			t.Errorf("IsLocalTestDatabaseURL(%q)=false, want true", value)
		}
	}
	rejected := []string{
		"postgres://user:pass@db.example/aicrm_test_one",
		"postgres://user:pass@localhost/aicrm_production",
		"https://localhost/aicrm_test_one",
		"postgres://user:pass@[broken/aicrm_test_one",
		"postgres://user:pass@localhost:not-a-port/aicrm_test_one",
	}
	for _, value := range rejected {
		if IsLocalTestDatabaseURL(value) {
			t.Errorf("IsLocalTestDatabaseURL(%q)=true, want false", value)
		}
	}
}
