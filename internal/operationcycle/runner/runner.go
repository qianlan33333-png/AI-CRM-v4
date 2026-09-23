// Package runner is the explicit, local OperationCycle-to-Codex adapter. It
// has no scheduler: an installed local supervisor chooses when to call RunOnce.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	connectorVersion               = "operation-cycle-go-runner/1.0"
	maximumRenewalInterval         = 25 * time.Second
	activeHeartbeatBudget          = 10 * time.Second
	activeCompatibilityProbeBudget = 5 * time.Second
)

var ErrOutcomeUnknown = errors.New("operation action start outcome is unknown; manual verification is required")

type Action struct {
	Claimed               bool
	Recovered             bool
	RequestID             string
	StrategyKey           string
	RunKey                string
	ActionKey             string
	ActionTitle           string
	StrategyVersion       int
	Status                string
	LeaseToken            string
	ThreadID              string
	TurnID                string
	BlockedCode           string
	Objective             string
	CodexPrompt           string
	RequiredLocalBindings []string
	ContextSummary        map[string]any
	ExecutionHash         string
	ContextHash           string
}

type Remote interface {
	Heartbeat(context.Context, Heartbeat) error
	Claim(context.Context, string) (Action, error)
	Renew(context.Context, string, string) error
	Event(context.Context, ActionEvent) error
}

type Heartbeat struct {
	RunnerID            string
	ConnectorVersion    string
	CodexVersion        string
	AppServerProtocol   string
	CompatibilityStatus string
	BindingKeys         []string
}

type ActionEvent struct {
	RequestID   string
	EventID     string
	EventType   string
	LeaseToken  string
	ThreadID    string
	TurnID      string
	Result      map[string]any
	FailureCode string
}

type TaskExecutor interface {
	Available(context.Context) error
	StartThread(context.Context, string) (string, error)
	StartTurn(context.Context, string, string) (string, error)
}

type Config struct {
	RunnerID          string
	CodexVersion      string
	AppServerProtocol string
	ControlSocket     string
	Bindings          map[string]string
}

type Runner struct {
	remote   Remote
	executor TaskExecutor
	config   Config
	mu       sync.Mutex
	actions  map[string]Action
}

func New(remote Remote, executor TaskExecutor, config Config) (*Runner, error) {
	if remote == nil || executor == nil || !validKey(config.RunnerID) || !validKey(config.CodexVersion) || !validKey(config.AppServerProtocol) || !filepath.IsAbs(config.ControlSocket) || strings.TrimSpace(config.ControlSocket) != config.ControlSocket || len(config.Bindings) == 0 {
		return nil, errors.New("operation runner dependencies are invalid")
	}
	for key, directory := range config.Bindings {
		if !validKey(key) || strings.TrimSpace(directory) == "" {
			return nil, errors.New("operation runner binding is invalid")
		}
	}
	return &Runner{remote: remote, executor: executor, config: config, actions: make(map[string]Action)}, nil
}

// RunOnce safely resumes a claimed action. It only starts missing bindings: a
// recovered thread/turn never causes a second Codex task or turn.
func (r *Runner) RunOnce(ctx context.Context) (Action, error) {
	if r == nil {
		return Action{}, errors.New("operation runner is nil")
	}
	ready, err := r.heartbeat(ctx)
	if err != nil {
		return Action{}, err
	}
	if !ready {
		return Action{}, nil
	}
	action, err := r.remote.Claim(ctx, r.config.RunnerID)
	if err != nil || !action.Claimed {
		return action, err
	}
	if err = r.remote.Renew(ctx, action.RequestID, action.LeaseToken); err != nil {
		return action, err
	}
	// A recovered action has crossed a process or report-acknowledgement
	// boundary. Missing bindings are not evidence that Codex did not create
	// them, so fail closed for operator verification instead of starting again.
	if action.Recovered && (action.ThreadID == "" || action.TurnID == "") {
		return action, r.unknownStart(ctx, action)
	}
	if action.BlockedCode != "" {
		return action, r.blocked(ctx, action, action.BlockedCode)
	}
	directory, ok := r.workspace(action)
	if !ok {
		return action, r.blocked(ctx, action, "binding_unavailable")
	}
	if !validExecution(action) {
		return action, r.blocked(ctx, action, "invalid_execution_snapshot")
	}
	if action.ThreadID == "" {
		action.ThreadID, err = r.executor.StartThread(ctx, directory)
		if err != nil {
			return action, r.unknownStart(ctx, action)
		}
		if err = r.remote.Event(ctx, ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":thread:" + action.ThreadID, EventType: "thread_bound", LeaseToken: action.LeaseToken, ThreadID: action.ThreadID}); err != nil {
			return action, r.unknownStart(ctx, action)
		}
	}
	if action.TurnID == "" {
		action.TurnID, err = r.executor.StartTurn(ctx, action.ThreadID, r.prompt(action))
		if err != nil {
			return action, r.unknownStart(ctx, action)
		}
		if err = r.remote.Event(ctx, ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":turn:" + action.TurnID, EventType: "turn_started", LeaseToken: action.LeaseToken, ThreadID: action.ThreadID, TurnID: action.TurnID}); err != nil {
			return action, r.unknownStart(ctx, action)
		}
	}
	r.remember(action)
	return action, nil
}

// Serve is the explicit connector runtime entrypoint. It keeps one active
// local action fenced and consumes its Unix-socket terminal receipt. It is
// started by a service manager; this package does not register a CRM job or
// start itself from the web process.
func (r *Runner) Serve(ctx context.Context, controlSocket string, renewalInterval time.Duration) error {
	if controlSocket != r.config.ControlSocket {
		return errors.New("operation runner control socket does not match prompt configuration")
	}
	if renewalInterval <= 0 || renewalInterval > maximumRenewalInterval {
		return errors.New("operation runner renewal interval is invalid")
	}
	runCtx, cancel := context.WithCancel(ctx)
	controlReady := make(chan error, 1)
	controlDone := make(chan error, 1)
	waited := false
	go func() { controlDone <- r.serveControl(runCtx, controlSocket, controlReady) }()
	defer func() {
		cancel()
		if !waited {
			<-controlDone
		}
	}()
	select {
	case err := <-controlReady:
		if err != nil {
			waited = true
			<-controlDone
			return err
		}
	case err := <-controlDone:
		waited = true
		return err
	case <-runCtx.Done():
		return nil
	}
	var active Action
	for {
		if active.RequestID == "" {
			claimed, err := r.RunOnce(runCtx)
			if err != nil {
				return err
			}
			if claimed.Claimed {
				active = claimed
			}
		} else if _, stillActive := r.action(active.RequestID); !stillActive {
			active = Action{}
		} else {
			// Renew first so a slow compatibility probe cannot consume the
			// current 60-second fence. The bounded heartbeat then keeps the
			// runner visible to Start's 45-second offline guard. Heartbeat
			// transport failure is non-terminal for an already-fenced action:
			// its local control socket must remain able to record the result.
			if err := r.Renew(runCtx, active); err != nil {
				return err
			}
			heartbeatCtx, heartbeatCancel := context.WithTimeout(runCtx, activeHeartbeatBudget)
			_, _ = r.heartbeatWithProbeTimeout(heartbeatCtx, activeCompatibilityProbeBudget)
			heartbeatCancel()
		}
		timer := time.NewTimer(renewalInterval)
		select {
		case <-runCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case err := <-controlDone:
			if !timer.Stop() {
				<-timer.C
			}
			waited = true
			return err
		case <-timer.C:
		}
	}
}

// heartbeat performs the same read-only compatibility check before both a
// claim and each active-action renewal. An unavailable executor is recorded as
// such, which prevents a later Start while keeping the current fenced action
// available to its local terminal-result control socket.
func (r *Runner) heartbeat(ctx context.Context) (bool, error) {
	return r.heartbeatWithProbeTimeout(ctx, 0)
}

func (r *Runner) heartbeatWithProbeTimeout(ctx context.Context, probeTimeout time.Duration) (bool, error) {
	status := "ready"
	probeCtx := ctx
	cancel := func() {}
	if probeTimeout > 0 {
		probeCtx, cancel = context.WithTimeout(ctx, probeTimeout)
	}
	if err := r.executor.Available(probeCtx); err != nil {
		status = "unavailable"
	}
	cancel()
	if err := r.remote.Heartbeat(ctx, Heartbeat{RunnerID: r.config.RunnerID, ConnectorVersion: connectorVersion, CodexVersion: r.config.CodexVersion, AppServerProtocol: r.config.AppServerProtocol, CompatibilityStatus: status, BindingKeys: sortedKeys(r.config.Bindings)}); err != nil {
		return false, err
	}
	return status == "ready", nil
}

func (r *Runner) remember(action Action) {
	if !action.Claimed || action.RequestID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions[action.RequestID] = action
}
func (r *Runner) action(requestID string) (Action, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	action, ok := r.actions[requestID]
	return action, ok
}
func (r *Runner) forget(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.actions, requestID)
}

// Renew keeps a long-running, already-bound Codex task fenced. A caller may
// invoke it on the Action returned by RunOnce; this package intentionally does
// not create a hidden ticker or scheduler.
func (r *Runner) blocked(ctx context.Context, action Action, code string) error {
	event := ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":blocked:" + code, EventType: "failed", LeaseToken: action.LeaseToken, FailureCode: code, Result: map[string]any{"outcome": "outcome_unknown", "operator_action": "manual_verification_required"}}
	if err := r.remote.Event(ctx, event); err != nil {
		return fmt.Errorf("record blocked action: %w", err)
	}
	return ErrOutcomeUnknown
}

func (r *Runner) unknownStart(ctx context.Context, action Action) error {
	event := ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":start-outcome-unknown", EventType: "failed", LeaseToken: action.LeaseToken, FailureCode: "start_outcome_unknown", Result: map[string]any{"outcome": "outcome_unknown", "operator_action": "manual_verification_required"}}
	if err := r.remote.Event(ctx, event); err != nil {
		return fmt.Errorf("%w: record stop fact: %v", ErrOutcomeUnknown, err)
	}
	return ErrOutcomeUnknown
}

func (r *Runner) Renew(ctx context.Context, action Action) error {
	if r == nil || !action.Claimed || action.RequestID == "" || action.LeaseToken == "" {
		return errors.New("operation lease renewal is invalid")
	}
	return r.remote.Renew(ctx, action.RequestID, action.LeaseToken)
}

func (r *Runner) Complete(ctx context.Context, action Action, result map[string]any) error {
	if r == nil || !action.Claimed || action.RequestID == "" || action.LeaseToken == "" || result == nil {
		return errors.New("operation completion is invalid")
	}
	err := r.remote.Event(ctx, ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":completed", EventType: "completed", LeaseToken: action.LeaseToken, Result: result})
	if err == nil {
		r.forget(action.RequestID)
	}
	return err
}

func (r *Runner) workspace(action Action) (string, bool) {
	if len(action.RequiredLocalBindings) == 0 {
		return "", false
	}
	for _, key := range action.RequiredLocalBindings {
		if _, ok := r.config.Bindings[key]; !ok {
			return "", false
		}
	}
	return r.config.Bindings[action.RequiredLocalBindings[0]], true
}

func validExecution(action Action) bool {
	return strings.TrimSpace(action.Objective) == action.Objective && action.Objective != "" &&
		strings.TrimSpace(action.CodexPrompt) == action.CodexPrompt && action.CodexPrompt != "" &&
		len(action.RequiredLocalBindings) > 0 && action.ContextSummary != nil &&
		len(action.ExecutionHash) == 64 && len(action.ContextHash) == 64
}

func (r *Runner) prompt(action Action) string {
	contextJSON, err := json.MarshalIndent(action.ContextSummary, "", "  ")
	if err != nil {
		contextJSON = []byte(`{"unavailable":true}`)
	}
	bindingLines := make([]string, 0, len(action.RequiredLocalBindings))
	for _, key := range action.RequiredLocalBindings {
		bindingLines = append(bindingLines, "- "+key+": "+r.config.Bindings[key])
	}
	completionCommand := "aicrm-operation-cycle-result --socket " + shellQuote(r.config.ControlSocket) + " --request-id " + shellQuote(action.RequestID) + " --result-file " + shellQuote("/absolute/path/to/safe-result.json")
	return fmt.Sprintf("# CRM operation action: %s\n\nRequest: %s\nStrategy: %s (v%d)\nRun: %s\nExecution SHA-256: %s\nContext SHA-256: %s\n\n## Objective\n\n%s\n\n## Approved task instructions\n\n%s\n\n## Frozen CRM context\n\n```json\n%s\n```\n\n## Local bindings\n\n%s\n\n## Completion\n\nAfter an operator has reviewed a sanitized aggregate JSON result, submit it through the local runner:\n\n%s\n\nDo not put customer identifiers, credentials, local paths, source files, or raw conversations in the result. Do not send messages or make external changes.", action.ActionTitle, action.RequestID, action.StrategyKey, action.StrategyVersion, action.RunKey, action.ExecutionHash, action.ContextHash, action.Objective, action.CodexPrompt, contextJSON, strings.Join(bindingLines, "\n"), completionCommand)
}

// shellQuote produces one POSIX shell word without permitting path or request
// text to become syntax in the operator-visible completion command.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validKey(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t")
}
func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
