package webshell

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAudienceActivationHostBrowser(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_activation_host.test.mjs")
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("activation host: %v\n%s", err, out)
	}
}
