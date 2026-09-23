package segment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSegmentRuntimeSQLDoesNotNameOtherDomainTables(t *testing.T) {
	forbidden := []string{"customers", "customer_identities", "automation_agents", "external_effects", "access_users", "group_ops_"}
	err := filepath.Walk("store", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return err
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(contents))
		for _, name := range forbidden {
			if strings.Contains(lower, name) {
				t.Fatalf("segment runtime SQL names foreign table %q in %s", name, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
