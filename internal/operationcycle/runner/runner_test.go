package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
)

type remoteStub struct {
	mu                  sync.Mutex
	heartbeat           []Heartbeat
	action              Action
	claims              int
	renew               []string
	events              []ActionEvent
	err                 error
	heartbeatErr        error
	heartbeatErrAfter   int
	eventErr            error
	emptyAfterCompleted bool
}

func (s *remoteStub) Heartbeat(_ context.Context, value Heartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeat = append(s.heartbeat, value)
	if s.heartbeatErr != nil && len(s.heartbeat) > s.heartbeatErrAfter {
		return s.heartbeatErr
	}
	return s.err
}
func (s *remoteStub) Claim(context.Context, string) (Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	return s.action, s.err
}
func (s *remoteStub) Renew(_ context.Context, id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renew = append(s.renew, id+":"+token)
	return s.err
}
func (s *remoteStub) Event(_ context.Context, event ActionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	if s.emptyAfterCompleted && event.EventType == "completed" && s.eventErr == nil {
		s.action.Claimed = false
	}
	return s.eventErr
}
func (s *remoteStub) renewCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.renew) }
func (s *remoteStub) eventCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.events) }
func (s *remoteStub) heartbeatSnapshot() []Heartbeat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Heartbeat(nil), s.heartbeat...)
}
func (s *remoteStub) eventSnapshot() []ActionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ActionEvent(nil), s.events...)
}

type executorStub struct {
	available      error
	startErr       error
	threads, turns int
}

func (s *executorStub) Available(context.Context) error { return s.available }
func (s *executorStub) StartThread(context.Context, string) (string, error) {
	s.threads++
	if s.startErr != nil {
		return "", s.startErr
	}
	return "thread-1", nil
}
func (s *executorStub) StartTurn(context.Context, string, string) (string, error) {
	s.turns++
	return "turn-1", nil
}

type slowAfterFirstCompatibilityExecutor struct {
	executorStub
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (s *slowAfterFirstCompatibilityExecutor) Available(ctx context.Context) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		return nil
	}
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-ctx.Done()
	return ctx.Err()
}

func executableAction() Action {
	return Action{
		Claimed: true, RequestID: "ocact_0123456789012345678901234567", StrategyKey: "weekly.review",
		RunKey: "weekly.review.001", ActionKey: "start_review", ActionTitle: "review", StrategyVersion: 1, LeaseToken: "lease",
		Objective: "complete the frozen review", CodexPrompt: "review only the frozen context", RequiredLocalBindings: []string{"weekly.review"},
		ContextSummary: map[string]any{"run_key": "weekly.review.001", "summary": "frozen"},
		ExecutionHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContextHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
}

func TestRunOnceRecoversStoredThreadAndTurnWithoutDuplicateCodexWork(t *testing.T) {
	action := executableAction()
	action.Recovered = true
	action.LeaseToken = "lease-recovered"
	action.ThreadID = "thread-existing"
	action.TurnID = "turn-existing"
	remote := &remoteStub{action: action}
	executor := &executorStub{}
	runner, err := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "codex_app_server_jsonrpc_v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local/workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	action, err = runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if action.ThreadID != "thread-existing" || action.TurnID != "turn-existing" || executor.threads != 0 || executor.turns != 0 {
		t.Fatalf("recovery action=%#v starts=%d/%d", action, executor.threads, executor.turns)
	}
	if !reflect.DeepEqual(remote.renew, []string{action.RequestID + ":lease-recovered"}) || len(remote.events) != 0 {
		t.Fatalf("renew/events=%v/%#v", remote.renew, remote.events)
	}
}
func TestRunOnceBindsMissingThreadAndTurnExactlyOnce(t *testing.T) {
	action := executableAction()
	action.LeaseToken = "lease-new"
	remote := &remoteStub{action: action}
	executor := &executorStub{}
	runner, _ := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "codex_app_server_jsonrpc_v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local/workspace"}})
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.threads != 1 || executor.turns != 1 || len(remote.events) != 2 || remote.events[0].EventType != "thread_bound" || remote.events[1].EventType != "turn_started" {
		t.Fatalf("starts=%d/%d events=%#v", executor.threads, executor.turns, remote.events)
	}
}
func TestRunOnceOnlyReportsUnavailableWhenSocketExecutorIsUnavailable(t *testing.T) {
	remote := &remoteStub{}
	executor := &executorStub{available: errors.New("socket unavailable")}
	runner, _ := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "codex_app_server_jsonrpc_v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local/workspace"}})
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(remote.heartbeat) != 1 || remote.heartbeat[0].CompatibilityStatus != "unavailable" || len(remote.renew) != 0 {
		t.Fatalf("heartbeat=%#v renew=%#v", remote.heartbeat, remote.renew)
	}
}

func TestHeartbeatPublishesUnavailableAfterBoundedSlowProbe(t *testing.T) {
	remote := &remoteStub{}
	executor := &slowAfterFirstCompatibilityExecutor{calls: 1, started: make(chan struct{})}
	local, err := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ready, err := local.heartbeatWithProbeTimeout(context.Background(), 15*time.Millisecond)
	if err != nil || ready {
		t.Fatalf("bounded compatibility heartbeat ready=%v err=%v", ready, err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("compatibility probe exceeded its budget: %s", elapsed)
	}
	heartbeats := remote.heartbeatSnapshot()
	if len(heartbeats) != 1 || heartbeats[0].CompatibilityStatus != "unavailable" {
		t.Fatalf("slow compatibility was not published as unavailable: %#v", heartbeats)
	}
}
func TestAppServerProxyFailsClosedForUnavailableSocket(t *testing.T) {
	proxy := AppServerProxy{Binary: "/Applications/ChatGPT.app/Contents/Resources/codex", SocketPath: t.TempDir() + "/missing.sock", ExpectedVersion: "codex 1"}
	if err := proxy.Available(context.Background()); err == nil {
		t.Fatal("expected unavailable socket")
	}
}
func TestHTTPClientUsesFlatImmediateServiceTokenProtocol(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	token := "abcdefghijklmnopqrstuvwxyz0123456789"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/api/operation-cycles/action-requests/claim":
			if body["wait_seconds"] != float64(0) {
				t.Errorf("wait=%#v", body["wait_seconds"])
			}
			execution := map[string]any{"objective": "complete the frozen review", "codex_prompt": "review only the frozen context", "required_local_bindings": []any{"weekly.review"}}
			contextSummary := map[string]any{"run_key": "weekly.review.001"}
			executionDigest, digestErr := operationapp.Digest(execution)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			contextDigest, digestErr := operationapp.Digest(contextSummary)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"claimed": true, "recovered": true, "request_id": "ocact_0123456789012345678901234567", "strategy_key": "weekly.review", "run_key": "weekly.review.001", "action_key": "review", "action_title": "review", "strategy_version": 1, "status": "turn_started", "lease_token": "lease-recovered", "thread_id": "thread-existing", "turn_id": "turn-existing", "execution": execution, "context_summary": contextSummary, "execution_hash": hex.EncodeToString(executionDigest[:]), "context_hash": hex.EncodeToString(contextDigest[:])})
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, token, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Heartbeat(context.Background(), Heartbeat{RunnerID: "designated-mac", ConnectorVersion: "v1", CodexVersion: "codex 1", AppServerProtocol: "v2", CompatibilityStatus: "ready", BindingKeys: []string{"weekly.review"}}); err != nil {
		t.Fatal(err)
	}
	action, err := client.Claim(context.Background(), "designated-mac")
	if err != nil || !action.Claimed || action.ThreadID != "thread-existing" || action.TurnID != "turn-existing" {
		t.Fatalf("claim=%#v err=%v", action, err)
	}
	if err = client.Renew(context.Background(), action.RequestID, action.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = client.Event(context.Background(), ActionEvent{RequestID: action.RequestID, EventID: "event-1", EventType: "completed", LeaseToken: action.LeaseToken, Result: map[string]any{"outcome": "outcome_unknown"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/operation-cycles/runner/heartbeat", "/api/operation-cycles/action-requests/claim", "/api/operation-cycles/action-requests/ocact_0123456789012345678901234567/lease-renewals", "/api/operation-cycles/action-requests/ocact_0123456789012345678901234567/events"}) {
		t.Fatalf("paths=%#v", paths)
	}
	if _, err = NewHTTPClient("http://127.0.0.1", token, server.Client()); err == nil {
		t.Fatal("non-HTTPS endpoint accepted")
	}
}

func TestHTTPClientRejectsClaimWithMismatchedFrozenDigest(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz0123456789"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"claimed": true, "request_id": "ocact_0123456789012345678901234567", "lease_token": "lease", "execution": map[string]any{"objective": "frozen", "codex_prompt": "frozen", "required_local_bindings": []any{"weekly.review"}}, "context_summary": map[string]any{"run_key": "weekly.review.001"}, "execution_hash": strings.Repeat("0", 64), "context_hash": strings.Repeat("0", 64)})
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, token, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Claim(context.Background(), "designated-mac"); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("mismatched snapshot digest accepted or leaked token: %v", err)
	}
}

func TestAppServerProxyRequiresExactConfiguredVersion(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "codex")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'codex fixed'
else
  IFS= read -r _
  printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
  IFS= read -r _
fi
`), 0700); err != nil {
		t.Fatal(err)
	}
	name, err := os.CreateTemp("/tmp", "aicrm-oc-socket-")
	if err != nil {
		t.Fatal(err)
	}
	socket := name.Name()
	if err = name.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	proxy := AppServerProxy{Binary: binary, SocketPath: socket, ExpectedVersion: "codex other"}
	if err = proxy.Available(context.Background()); err == nil {
		t.Fatal("incompatible fixed version accepted")
	}
	proxy.ExpectedVersion = "codex fixed"
	if err = proxy.Available(context.Background()); err != nil {
		t.Fatalf("fixed local version/socket rejected: %v", err)
	}
}
func TestRunOnceReturnsCodexCreateAndEventReportFailures(t *testing.T) {
	base := executableAction()
	config := Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}}
	remote := &remoteStub{action: base}
	createFailure := &executorStub{startErr: errors.New("Codex create failed")}
	runner, _ := New(remote, createFailure, config)
	if _, err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("Codex create failure was hidden")
	}
	reportFailure := &remoteStub{action: base, eventErr: errors.New("event report failed")}
	runner, _ = New(reportFailure, &executorStub{}, config)
	if _, err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("event report failure was hidden")
	}
}

func TestRecoveredIncompleteBindingStopsAsOutcomeUnknownWithoutStartingCodex(t *testing.T) {
	for _, action := range []Action{
		func() Action { action := executableAction(); action.Recovered = true; return action }(),
		func() Action {
			action := executableAction()
			action.Recovered = true
			action.ThreadID = "thread-known"
			return action
		}(),
	} {
		remote := &remoteStub{action: action}
		executor := &executorStub{}
		runner, _ := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}})
		if _, err := runner.RunOnce(context.Background()); !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("action=%#v err=%v", action, err)
		}
		if executor.threads != 0 || executor.turns != 0 || len(remote.events) != 1 {
			t.Fatalf("action=%#v starts=%d/%d events=%#v", action, executor.threads, executor.turns, remote.events)
		}
		event := remote.events[0]
		if event.EventType != "failed" || event.FailureCode != "start_outcome_unknown" || event.Result["outcome"] != "outcome_unknown" || event.Result["operator_action"] != "manual_verification_required" {
			t.Fatalf("unknown event=%#v", event)
		}
	}
}
func TestStartOrBindingReportFailureCannotCauseRecoveredRestart(t *testing.T) {
	base := executableAction()
	config := Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}}
	for _, first := range []struct {
		name     string
		remote   *remoteStub
		executor *executorStub
	}{
		{name: "start returned unknown", remote: &remoteStub{action: base}, executor: &executorStub{startErr: errors.New("start connection lost")}},
		{name: "thread report acknowledgement lost", remote: &remoteStub{action: base, eventErr: errors.New("report connection lost")}, executor: &executorStub{}},
	} {
		t.Run(first.name, func(t *testing.T) {
			runner, _ := New(first.remote, first.executor, config)
			if _, err := runner.RunOnce(context.Background()); err == nil {
				t.Fatal("initial uncertainty was hidden")
			}
			// The later Claim is the only safe recovery representation after the
			// process dies or an event acknowledgement is lost.
			first.remote.action = executableAction()
			first.remote.action.Recovered = true
			first.remote.action.LeaseToken = "renewed"
			first.remote.eventErr = nil
			if _, err := runner.RunOnce(context.Background()); !errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("recovery err=%v", err)
			}
			if first.executor.threads > 1 || first.executor.turns != 0 {
				t.Fatalf("unexpected restart starts=%d/%d", first.executor.threads, first.executor.turns)
			}
		})
	}
}

func TestControlSocketSubmitsOnlyKnownSanitizedTerminalAction(t *testing.T) {
	directory := t.TempDir()
	name, err := os.CreateTemp("/tmp", "aicrm-operation-control-")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := name.Name()
	if err = name.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	remote := &remoteStub{}
	runner, err := New(remote, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": directory}})
	if err != nil {
		t.Fatal(err)
	}
	runner.remember(Action{Claimed: true, RequestID: "ocact_0123456789012345678901234567", LeaseToken: "lease", StrategyKey: "weekly.review", ThreadID: "thread", TurnID: "turn"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.ServeControl(ctx, socketPath) }()
	var connection net.Conn
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		connection, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("dial control socket: %v", err)
	}
	if err = json.NewEncoder(connection).Encode(map[string]any{"request_id": "ocact_0123456789012345678901234567", "event_type": "completed", "result": map[string]any{"outcome": "outcome_unknown"}}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err = json.NewDecoder(connection).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if reply["ok"] != true || len(remote.events) != 1 || remote.events[0].EventType != "completed" || remote.events[0].Result["outcome"] != "outcome_unknown" {
		t.Fatalf("reply=%#v events=%#v", reply, remote.events)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control server did not stop")
	}
	if _, err = os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control socket remained: %v", err)
	}
}

func TestAppServerProxyRejectsStaleSocketInitializeFailureAndTimeout(t *testing.T) {
	newSocket := func(t *testing.T) (string, net.Listener) {
		t.Helper()
		file, err := os.CreateTemp("/tmp", "aicrm-oc-ready-")
		if err != nil {
			t.Fatal(err)
		}
		path := file.Name()
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		return path, listener
	}
	binary := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "codex")
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	version := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'codex fixed'; else "
	t.Run("stale socket", func(t *testing.T) {
		socket, listener := newSocket(t)
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(socket)
		proxy := AppServerProxy{Binary: binary(t, version+"exit 0; fi\n"), SocketPath: socket, ExpectedVersion: "codex fixed", Timeout: 100 * time.Millisecond}
		if err := proxy.Available(context.Background()); err == nil {
			t.Fatal("stale socket accepted")
		}
	})
	t.Run("initialize rejected", func(t *testing.T) {
		socket, listener := newSocket(t)
		defer listener.Close()
		defer os.Remove(socket)
		script := version + "IFS= read -r _; printf '{\\\"jsonrpc\\\":\\\"2.0\\\",\\\"id\\\":1,\\\"error\\\":{\\\"code\\\":-1}}\\n'; fi\n"
		proxy := AppServerProxy{Binary: binary(t, script), SocketPath: socket, ExpectedVersion: "codex fixed", Timeout: time.Second}
		if err := proxy.Available(context.Background()); err == nil {
			t.Fatal("initialize error accepted")
		}
	})
	t.Run("initialize timeout", func(t *testing.T) {
		socket, listener := newSocket(t)
		defer listener.Close()
		defer os.Remove(socket)
		script := version + "IFS= read -r _; sleep 2; fi\n"
		proxy := AppServerProxy{Binary: binary(t, script), SocketPath: socket, ExpectedVersion: "codex fixed", Timeout: 50 * time.Millisecond}
		if err := proxy.Available(context.Background()); err == nil {
			t.Fatal("initialize timeout accepted")
		}
	})
}

func TestRunOnceBlocksMissingExecutionSnapshotWithoutStartingCodex(t *testing.T) {
	action := executableAction()
	action.BlockedCode = "missing_execution_snapshot"
	remote := &remoteStub{action: action}
	executor := &executorStub{}
	runner, err := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.RunOnce(context.Background()); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("err=%v", err)
	}
	if executor.threads != 0 || executor.turns != 0 || len(remote.events) != 1 || remote.events[0].FailureCode != "missing_execution_snapshot" || remote.events[0].Result["operator_action"] != "manual_verification_required" {
		t.Fatalf("starts=%d/%d events=%#v", executor.threads, executor.turns, remote.events)
	}
}

func TestPromptUsesFrozenObjectiveContextBindingsAndActualCompletionCommand(t *testing.T) {
	action := executableAction()
	runner, err := New(&remoteStub{}, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local/frozen-workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	value := runner.prompt(action)
	for _, expected := range []string{"complete the frozen review", "review only the frozen context", `"summary": "frozen"`, "/local/frozen-workspace", "aicrm-operation-cycle-result --socket '/tmp/aicrm-operation-runner.sock' --request-id '" + action.RequestID + "'", action.ExecutionHash, action.ContextHash} {
		if !strings.Contains(value, expected) {
			t.Fatalf("prompt omitted %q: %s", expected, value)
		}
	}
}

func TestPromptShellQuotesControlSocketAndRequestID(t *testing.T) {
	action := executableAction()
	action.RequestID = "ocact_unsafe $() ' request"
	local, err := New(&remoteStub{}, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/review $()'socket", Bindings: map[string]string{"weekly.review": "/local/frozen-workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	prompt := local.prompt(action)
	want := "--socket '/tmp/review $()'\"'\"'socket' --request-id 'ocact_unsafe $() '" + "\"'\"'" + " request'"
	if !strings.Contains(prompt, want) {
		t.Fatalf("unsafe completion command was not POSIX-quoted: %s", prompt)
	}
}

func TestServeRenewsAnActiveActionAndRunsTheLocalControlConsumer(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "aicrm-operation-runner-")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(socket)
	action := executableAction()
	remote := &remoteStub{action: action, heartbeatErr: errors.New("heartbeat transport timeout"), heartbeatErrAfter: 1}
	runner, err := New(remote, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: socket, Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Serve(ctx, socket, 15*time.Millisecond) }()
	deadline := time.Now().Add(time.Second)
	for remote.renewCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if remote.renewCount() < 2 {
		cancel()
		t.Fatalf("serve did not renew active action after heartbeat transport failure")
	}
	heartbeats := remote.heartbeatSnapshot()
	if len(heartbeats) < 2 {
		cancel()
		t.Fatalf("long-running action stopped heartbeating while its lease was renewed: %#v", heartbeats)
	}
	for _, heartbeat := range heartbeats {
		if heartbeat.CompatibilityStatus != "ready" {
			cancel()
			t.Fatalf("long-running action published unexpected compatibility: %#v", heartbeats)
		}
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
}

func TestServeRenewsBeforeASlowCompatibilityProbe(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "aicrm-operation-runner-slow-probe-")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(socket)
	remote := &remoteStub{action: executableAction()}
	executor := &slowAfterFirstCompatibilityExecutor{started: make(chan struct{})}
	local, err := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: socket, Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- local.Serve(ctx, socket, 15*time.Millisecond) }()
	select {
	case <-executor.started:
		if remote.renewCount() == 0 {
			cancel()
			t.Fatal("slow compatibility probe started before the active lease was renewed")
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("active compatibility probe did not start")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}
}

func TestServeCompletesThroughItsBoundControlSocket(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "aicrm-operation-serve-complete-")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(socket)
	remote := &remoteStub{action: executableAction(), emptyAfterCompleted: true}
	local, err := New(remote, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: socket, Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- local.Serve(ctx, socket, 15*time.Millisecond) }()
	for deadline := time.Now().Add(time.Second); remote.eventCount() < 2 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if remote.eventCount() < 2 {
		cancel()
		t.Fatal("Serve did not start and bind the claimed action")
	}
	connection, err := net.Dial("unix", socket)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = json.NewEncoder(connection).Encode(map[string]any{"request_id": executableAction().RequestID, "event_type": "completed", "result": map[string]any{"outcome": "reviewed"}}); err != nil {
		_ = connection.Close()
		cancel()
		t.Fatal(err)
	}
	if err = connection.(*net.UnixConn).CloseWrite(); err != nil {
		_ = connection.Close()
		cancel()
		t.Fatal(err)
	}
	var reply map[string]any
	if err = json.NewDecoder(connection).Decode(&reply); err != nil || reply["ok"] != true {
		_ = connection.Close()
		cancel()
		t.Fatalf("completion reply=%#v err=%v", reply, err)
	}
	_ = connection.Close()
	for deadline := time.Now().Add(time.Second); remote.eventCount() < 3 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if events := remote.eventSnapshot(); len(events) != 3 || events[2].EventType != "completed" || events[2].Result["outcome"] != "reviewed" {
		cancel()
		t.Fatalf("Serve did not record terminal receipt: %#v", events)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not join after terminal completion")
	}
}

func TestServeRequiresBoundControlSocketBeforeClaimingOrStarting(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "aicrm-operation-control-live-")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	_ = file.Close()
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	remote := &remoteStub{action: executableAction()}
	executor := &executorStub{}
	local, err := New(remote, executor, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: socket, Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = local.Serve(context.Background(), socket, 15*time.Millisecond); err == nil {
		t.Fatal("active control socket accepted")
	}
	if remote.claims != 0 || executor.threads != 0 || executor.turns != 0 {
		t.Fatalf("claim/start occurred before control readiness: claims=%d starts=%d/%d", remote.claims, executor.threads, executor.turns)
	}
}

func TestControlSocketOnlyRecoversPrivateStaleSocketAndCancellationClosesReaders(t *testing.T) {
	newSocket := func(t *testing.T) string {
		t.Helper()
		file, err := os.CreateTemp("/tmp", "aicrm-operation-control-stale-")
		if err != nil {
			t.Fatal(err)
		}
		path := file.Name()
		_ = file.Close()
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	local, err := New(&remoteStub{}, &executorStub{}, Config{RunnerID: "designated-mac", CodexVersion: "codex 1", AppServerProtocol: "v2", ControlSocket: "/tmp/aicrm-operation-runner.sock", Bindings: map[string]string{"weekly.review": "/local"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("non socket remains untouched", func(t *testing.T) {
		path := newSocket(t)
		if err := os.WriteFile(path, []byte("not a socket"), 0600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		if err := local.ServeControl(context.Background(), path); err == nil {
			t.Fatal("ordinary file accepted")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("ordinary file was removed: %v", err)
		}
	})
	t.Run("private stale socket is replaced and cancellation closes idle reader", func(t *testing.T) {
		path := newSocket(t)
		stale, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err = stale.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- local.ServeControl(ctx, path) }()
		var connection net.Conn
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			connection, err = net.Dial("unix", path)
			if err == nil {
				break
			}
		}
		if err != nil {
			cancel()
			t.Fatalf("stale socket was not recovered: %v", err)
		}
		cancel()
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		buffer := make([]byte, 1)
		if _, readErr := connection.Read(buffer); readErr == nil {
			t.Fatal("idle control reader remained open after shutdown")
		}
		_ = connection.Close()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Fatal(serveErr)
			}
		case <-time.After(time.Second):
			t.Fatal("control socket did not stop")
		}
	})
}

func TestHTTPClientRejectsUnsafeOriginsRedirectsAndTimeoutsWithoutLeakingURL(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, base := range []string{"http://crm.example.test", "https://user@crm.example.test", "https://crm.example.test/?credential=secret", "https://crm.example.test/#fragment", "https://crm.example.test/api"} {
		if _, err := NewHTTPClient(base, token, nil); err == nil {
			t.Fatalf("unsafe base accepted: %q", base)
		}
	}
	redirectTargetCalls := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargetCalls++ }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"?secret=hidden", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewHTTPClient(redirect.URL, token, redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Heartbeat(context.Background(), Heartbeat{RunnerID: "runner", ConnectorVersion: "v", CodexVersion: "v", AppServerProtocol: "v", CompatibilityStatus: "ready"}); err == nil || redirectTargetCalls != 0 {
		t.Fatalf("redirect followed or accepted: err=%v calls=%d", err, redirectTargetCalls)
	}
	releaseSlow := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-releaseSlow }))
	defer func() { close(releaseSlow); slow.Close() }()
	slowClient, err := NewHTTPClient(slow.URL, token, slow.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = slowClient.Heartbeat(ctx, Heartbeat{RunnerID: "runner", ConnectorVersion: "v", CodexVersion: "v", AppServerProtocol: "v", CompatibilityStatus: "ready"})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("timeout not bounded: err=%v elapsed=%s", err, time.Since(started))
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "?") {
		t.Fatalf("unsafe request error: %q", err)
	}
}

func TestAppServerInitializeSendsInitializedNotificationAfterResponse(t *testing.T) {
	var wire bytes.Buffer
	client := &jsonRPCClient{writer: &wire, reader: bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))}
	proxy := AppServerProxy{}
	if _, err := proxy.initialize(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(wire.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("handshake messages=%q", wire.String())
	}
	var initialize, initialized map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &initialized); err != nil {
		t.Fatal(err)
	}
	if initialize["method"] != "initialize" || initialized["method"] != "initialized" {
		t.Fatalf("handshake order=%#v / %#v", initialize, initialized)
	}
	if _, hasID := initialized["id"]; hasID {
		t.Fatalf("initialized must be a JSON-RPC notification: %#v", initialized)
	}
}
