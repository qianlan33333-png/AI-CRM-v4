package testconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestFixtureDatabaseRegistersBeforeCallerCanCreate(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_TEST_PREP_DIR", directory)
	first, err := NewDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("fixture database names collide")
	}
	for _, name := range []string{first, second} {
		if !regexp.MustCompile(`^aicrm_test_clone_[0-9a-f]{16}_acceptance_test$`).MatchString(name) {
			t.Fatal("unsafe name")
		}
		file := filepath.Join(directory, "database-"+name+".json")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]string
		if err = json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record["database"] != name || record["purpose"] != "fixture" {
			t.Fatal("invalid inventory")
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("inventory is not private")
		}
	}
}

func TestFixtureDatabaseRejectsUnsafePreparationBeforeReturningName(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_TEST_PREP_DIR", directory)
	if name, err := NewDatabaseName(); err == nil || name != "" {
		t.Fatal("accepted nonprivate preparation")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AICRM_TEST_PREP_DIR", link)
	if name, err := NewDatabaseName(); err == nil || name != "" {
		t.Fatal("accepted symlink preparation")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe preparation was written")
	}
	t.Setenv("AICRM_TEST_PREP_DIR", "")
	if name, err := NewDatabaseName(); err != nil || name == "" {
		t.Fatal("direct tests require an attempt directory")
	}
}
