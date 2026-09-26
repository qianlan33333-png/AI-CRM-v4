// Package testconfig supplies configuration only to synthetic test fixtures.
package testconfig

import "os"

// PreparationDirectory identifies the private directory owned by one check
// attempt. Runtime roles do not import this package.
func PreparationDirectory() string {
	return os.Getenv("AICRM_TEST_PREP_DIR")
}
