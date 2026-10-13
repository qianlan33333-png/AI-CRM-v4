package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

type runtimeConfigCheckResult struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
}

func runInstalledRuntimeConfigCheck(t *testing.T, binary, role string, extra map[string]string, args ...string) (runtimeConfigCheckResult, string, int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	environmentValues := map[string]string{
		"PATH":                            "/usr/bin:/bin",
		"AICRM_DATABASE_URL":              "postgres://synthetic:secret@127.0.0.1:1/aicrm?connect_timeout=1",
		"AICRM_RELEASE_SHA":               "0123456789abcdef0123456789abcdef01234567",
		"AICRM_ROLE":                      role,
		"AICRM_OUTBOUND_PROVIDER_ENABLED": "false",
	}
	for key, value := range extra {
		environmentValues[key] = value
	}
	keys := make([]string, 0, len(environmentValues))
	for key := range environmentValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = make([]string, 0, len(keys))
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+environmentValues[key])
	}
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("installed runtime configuration check did not exit promptly")
	}
	status := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else {
			t.Fatalf("installed runtime configuration check could not be executed")
		}
	}
	var result runtimeConfigCheckResult
	if decodeErr := json.Unmarshal(output, &result); decodeErr != nil {
		t.Fatalf("installed runtime configuration check did not return its JSON contract")
	}
	return result, string(output), status
}

func loopbackDatabaseSentinel(t *testing.T) (string, <-chan bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("could not create loopback database sentinel")
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan bool, 1)
	go func() {
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			_ = tcpListener.SetDeadline(time.Now().Add(300 * time.Millisecond))
		}
		connection, acceptErr := listener.Accept()
		accepted <- acceptErr == nil
		if connection != nil {
			_ = connection.Close()
		}
	}()
	databaseURL := fmt.Sprintf("postgres://synthetic:secret@%s/aicrm?connect_timeout=1", listener.Addr().String())
	return databaseURL, accepted
}

func TestInstalledRuntimeConfigCheck(t *testing.T) {
	binary := os.Getenv("AICRM_INSTALLED_PRODUCT_BINARY")
	if binary == "" {
		t.Skip("AICRM_INSTALLED_PRODUCT_BINARY is required for installed-artifact validation")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatal("AICRM_INSTALLED_PRODUCT_BINARY must name an executable regular file")
	}

	for _, role := range []string{"api", "worker", "effects-worker"} {
		databaseURL, connectionAttempt := loopbackDatabaseSentinel(t)
		result, stderr, status := runInstalledRuntimeConfigCheck(t, binary, role, map[string]string{
			"AICRM_DATABASE_URL": databaseURL,
		}, "--check-runtime-config")
		if status != 0 || !result.OK || result.Code != "runtime_config_valid" || strings.Contains(stderr, "synthetic") {
			t.Fatalf("valid installed runtime configuration was rejected for role %s", role)
		}
		select {
		case connected := <-connectionAttempt:
			if connected {
				t.Fatalf("runtime config check opened a database connection for role %s", role)
			}
		case <-time.After(time.Second):
			t.Fatal("loopback database sentinel did not finish")
		}
	}

	const secret = "runtime-check-private-sentinel"
	invalid, output, status := runInstalledRuntimeConfigCheck(t, binary, "api", map[string]string{
		"AICRM_WECOM_ENABLED": "true",
		"AICRM_WECOM_SECRET":  secret,
	}, "--check-runtime-config")
	if status != 1 || invalid.OK || invalid.Code != "runtime_config_invalid" || strings.Contains(output, secret) {
		t.Fatal("invalid configuration did not return a fixed, secret-free error")
	}

	arguments, output, status := runInstalledRuntimeConfigCheck(t, binary, "api", map[string]string{
		"AICRM_WECOM_SECRET": secret,
	}, "--check-runtime-config", "--unknown")
	if status != 2 || arguments.OK || arguments.Code != "invalid_arguments" || strings.Contains(output, secret) {
		t.Fatal("unsupported arguments were not refused with a fixed, secret-free error")
	}
}
