package testconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// NewDatabaseName registers a synthetic fixture database before CREATE. The
// controller can recover it after SIGKILL using its existing owned-DB inventory.
// Direct tests without a managed preparation directory retain their own cleanup.
func NewDatabaseName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := "aicrm_test_clone_" + hex.EncodeToString(random[:]) + "_acceptance_test"
	directory := PreparationDirectory()
	if directory == "" {
		return name, nil
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !filepath.IsAbs(directory) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !ok || int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf("fixture database requires a private check-owned preparation directory")
	}
	file, err := os.CreateTemp(directory, ".database-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	data, _ := json.Marshal(struct {
		Database string `json:"database"`
		Purpose  string `json:"purpose"`
	}{name, "fixture"})
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(file.Name(), filepath.Join(directory, "database-"+name+".json")); err != nil {
		return "", err
	}
	return name, nil
}
