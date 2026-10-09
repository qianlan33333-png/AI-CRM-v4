package http

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var publicBrowserPreparation struct {
	sync.Once
	err error
}

// These selected Go tests run before the frontend lane has prepared npm.
// Reuse the lockfile-bound dependency snapshot without building Host assets.
func preparePublicBrowserDependencies(t *testing.T) {
	t.Helper()
	publicBrowserPreparation.Do(func() {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			publicBrowserPreparation.err = fmt.Errorf("cannot locate browser test source")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "python3", "scripts/ci/check_preparation.py", "npm")
		command.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "AICRM_DATABASE_URL=") {
				command.Env = append(command.Env, value)
			}
		}
		if output, err := command.CombinedOutput(); err != nil {
			publicBrowserPreparation.err = fmt.Errorf("prepare public browser dependencies: %w output=%s", err, strings.TrimSpace(string(output)))
		}
	})
	if publicBrowserPreparation.err != nil {
		t.Fatal(publicBrowserPreparation.err)
	}
}
