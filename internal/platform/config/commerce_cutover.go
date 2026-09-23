package config

import "os"

// LoadCommerceCutoverSourceURL loads only the offline read-only capture source.
// It is deliberately separate from application runtime database configuration.
func LoadCommerceCutoverSourceURL() string {
	return os.Getenv("AICRM_COMMERCE_SOURCE_URL")
}
