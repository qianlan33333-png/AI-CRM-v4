package webshell

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAudienceSendHistoryPaginationBrowserContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_send_history.test.mjs")
	command.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "admin-audience-send-history: PASS") {
		t.Fatalf("history browser contract: %v\n%s", err, output)
	}
}
