package main

import (
	"fmt"
	"io"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// checkRuntimeConfig runs the same environment loader used by normal startup,
// then exits before composition, database access, listeners, or provider setup.
func checkRuntimeConfig(output io.Writer) int {
	cfg, err := platformconfig.Load()
	if err == nil && cfg.Role != platformconfig.RoleAPI && cfg.Role != platformconfig.RoleWorker && cfg.Role != platformconfig.RoleEffectsWorker {
		err = fmt.Errorf("runtime role is not supported by this check")
	}
	if err != nil {
		writeRuntimeConfigCheckResult(output, false, "runtime_config_invalid")
		return 1
	}
	writeRuntimeConfigCheckResult(output, true, "runtime_config_valid")
	return 0
}

func writeRuntimeConfigCheckResult(output io.Writer, ok bool, code string) {
	if ok {
		_, _ = fmt.Fprintf(output, "{\"ok\":true,\"code\":\"%s\"}\n", code)
		return
	}
	_, _ = fmt.Fprintf(output, "{\"ok\":false,\"code\":\"%s\"}\n", code)
}
