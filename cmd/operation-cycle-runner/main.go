// Command operation-cycle-runner is the explicit local connector process for
// OperationCycle. It is installed and supervised outside the CRM web process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/runner"
)

type config struct {
	crmURL          string
	serviceTokenEnv string
	runnerID        string
	codexBinary     string
	codexSocket     string
	codexVersion    string
	controlSocket   string
	renewalInterval time.Duration
	bindings        map[string]string
}

func main() {
	if err := run(os.Args[1:], os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args, environ []string, stderr io.Writer) error {
	configuration, err := parseConfig(args)
	if err != nil {
		return err
	}
	token, ok := lookupEnv(environ, configuration.serviceTokenEnv)
	if !ok || strings.TrimSpace(token) != token || len(token) < 32 {
		return errors.New("operation runner service token is unavailable")
	}
	remote, err := runner.NewHTTPClient(configuration.crmURL, token, nil)
	if err != nil {
		return err
	}
	executor := runner.AppServerProxy{Binary: configuration.codexBinary, SocketPath: configuration.codexSocket, ExpectedVersion: configuration.codexVersion}
	local, err := runner.New(remote, executor, runner.Config{RunnerID: configuration.runnerID, CodexVersion: configuration.codexVersion, AppServerProtocol: "codex_app_server_jsonrpc_v2", ControlSocket: configuration.controlSocket, Bindings: configuration.bindings})
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintln(stderr, "operation-cycle-runner started")
	return local.Serve(ctx, configuration.controlSocket, configuration.renewalInterval)
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("operation-cycle-runner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var rawBindings bindingFlags
	result := config{}
	flags.StringVar(&result.crmURL, "crm-url", "", "HTTPS CRM base URL")
	flags.StringVar(&result.serviceTokenEnv, "service-token-env", "AICRM_OPERATION_RUNNER_SERVICE_TOKEN", "environment variable holding the service token")
	flags.StringVar(&result.runnerID, "runner-id", "", "registered local runner id")
	flags.StringVar(&result.codexBinary, "codex-binary", "", "absolute managed Codex binary")
	flags.StringVar(&result.codexSocket, "codex-socket", "", "absolute managed Codex app-server socket")
	flags.StringVar(&result.codexVersion, "codex-version", "", "exact Codex version")
	flags.StringVar(&result.controlSocket, "control-socket", "", "absolute local completion socket")
	flags.DurationVar(&result.renewalInterval, "renewal-interval", 25*time.Second, "active lease renewal interval, at most 25 seconds")
	flags.Var(&rawBindings, "binding", "required local binding as key=/absolute/directory; repeatable")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return config{}, errors.New("invalid operation runner arguments")
	}
	result.bindings = map[string]string(rawBindings)
	if !validConfig(result) {
		return config{}, errors.New("invalid operation runner configuration")
	}
	return result, nil
}

type bindingFlags map[string]string

func (values *bindingFlags) String() string { return "" }
func (values *bindingFlags) Set(value string) error {
	key, directory, ok := strings.Cut(value, "=")
	if !ok || !validKey(key) || !filepath.IsAbs(directory) || strings.TrimSpace(directory) != directory || directory == "/" {
		return errors.New("invalid operation runner binding")
	}
	if *values == nil {
		*values = bindingFlags{}
	}
	if _, exists := (*values)[key]; exists {
		return errors.New("duplicate operation runner binding")
	}
	(*values)[key] = directory
	return nil
}

func validConfig(value config) bool {
	return strings.HasPrefix(value.crmURL, "https://") && validKey(value.serviceTokenEnv) && validKey(value.runnerID) && filepath.IsAbs(value.codexBinary) && filepath.IsAbs(value.codexSocket) && validKey(value.codexVersion) && filepath.IsAbs(value.controlSocket) && value.controlSocket != "/" && value.renewalInterval > 0 && value.renewalInterval <= 25*time.Second && len(value.bindings) > 0
}
func validKey(value string) bool {
	return strings.TrimSpace(value) == value && len(value) > 0 && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t")
}
func lookupEnv(environ []string, key string) (string, bool) {
	for _, item := range environ {
		name, value, found := strings.Cut(item, "=")
		if found && name == key {
			return value, true
		}
	}
	return "", false
}
