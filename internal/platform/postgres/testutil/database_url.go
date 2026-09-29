// Package testutil contains helpers for PostgreSQL integration tests.
package testutil

import (
	"net/url"
	"strings"
)

// WithSearchPath returns databaseURL with the supplied PostgreSQL search_path
// set as a URL query parameter. Query encoding matters when the original URL
// has no query string or already contains connection options.
func WithSearchPath(databaseURL, schema string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", err
	}
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// IsLocalTestDatabaseURL matches the canonical preflight boundary for local
// PostgreSQL test databases.
func IsLocalTestDatabaseURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for name := range query {
		switch strings.ToLower(name) {
		case "database", "dbname", "host", "hostaddr", "service", "servicefile":
			// These libpq URI parameters can replace the URL path/authority or
			// select a different connection target. Keep the loopback and test
			// database checks bound to the actual endpoint used by the test.
			return false
		}
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	database := strings.Trim(parsed.Path, "/")
	return database == "aicrm_ci" || strings.HasPrefix(database, "aicrm_test_")
}
