package runner

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
)

const requestTimeout = 15 * time.Second

type HTTPClient struct {
	base   *url.URL
	token  string
	client *http.Client
}

func NewHTTPClient(baseURL, serviceToken string, client *http.Client) (*HTTPClient, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || strings.TrimSpace(serviceToken) != serviceToken || len(serviceToken) < 32 {
		return nil, errors.New("operation runner HTTP configuration is invalid")
	}
	base.Path = ""
	if client == nil {
		client = &http.Client{}
	}
	configured := *client
	configured.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if configured.Timeout <= 0 {
		configured.Timeout = requestTimeout
	}
	return &HTTPClient{base: base, token: serviceToken, client: &configured}, nil
}
func (c *HTTPClient) Heartbeat(ctx context.Context, heartbeat Heartbeat) error {
	return c.post(ctx, "/api/operation-cycles/runner/heartbeat", map[string]any{"schema_version": "operation_cycle_runner_heartbeat.v1", "runner_id": heartbeat.RunnerID, "connector_version": heartbeat.ConnectorVersion, "codex_version": heartbeat.CodexVersion, "app_server_protocol": heartbeat.AppServerProtocol, "compatibility_status": heartbeat.CompatibilityStatus, "binding_keys": heartbeat.BindingKeys}, "", nil)
}
func (c *HTTPClient) Claim(ctx context.Context, runnerID string) (Action, error) {
	var value map[string]any
	err := c.post(ctx, "/api/operation-cycles/action-requests/claim", map[string]any{"schema_version": "operation_cycle_action_claim.v1", "runner_id": runnerID, "wait_seconds": 0}, "", &value)
	if err != nil {
		return Action{}, err
	}
	execution, executionOK := objectValue(value, "execution")
	contextSummary, contextOK := objectValue(value, "context_summary")
	action := Action{Claimed: boolValue(value, "claimed"), Recovered: boolValue(value, "recovered"), RequestID: stringValue(value, "request_id"), StrategyKey: stringValue(value, "strategy_key"), RunKey: stringValue(value, "run_key"), ActionKey: stringValue(value, "action_key"), ActionTitle: stringValue(value, "action_title"), StrategyVersion: intValue(value, "strategy_version"), Status: stringValue(value, "status"), LeaseToken: stringValue(value, "lease_token"), ThreadID: stringValue(value, "thread_id"), TurnID: stringValue(value, "turn_id"), BlockedCode: stringValue(value, "blocked_code"), ExecutionHash: stringValue(value, "execution_hash"), ContextHash: stringValue(value, "context_hash")}
	if action.Claimed && (action.RequestID == "" || action.LeaseToken == "") {
		return Action{}, errors.New("operation runner received an invalid flat claim")
	}
	if !action.Claimed || action.BlockedCode != "" {
		return action, nil
	}
	if !executionOK || !contextOK {
		return Action{}, errors.New("operation runner claim is missing its immutable execution snapshot")
	}
	if !matchesCanonicalDigest(execution, action.ExecutionHash) || !matchesCanonicalDigest(contextSummary, action.ContextHash) {
		return Action{}, errors.New("operation runner claim snapshot digest is invalid")
	}
	action.Objective = stringValue(execution, "objective")
	action.CodexPrompt = stringValue(execution, "codex_prompt")
	action.RequiredLocalBindings = stringSliceValue(execution, "required_local_bindings")
	action.ContextSummary = contextSummary
	return action, nil
}
func (c *HTTPClient) Renew(ctx context.Context, requestID, leaseToken string) error {
	return c.post(ctx, "/api/operation-cycles/action-requests/"+url.PathEscape(requestID)+"/lease-renewals", map[string]any{"schema_version": "operation_cycle_action_lease_renewal.v1", "lease_token": leaseToken}, "", nil)
}
func (c *HTTPClient) Event(ctx context.Context, event ActionEvent) error {
	payload := map[string]any{"schema_version": "operation_cycle_action_event.v1", "event_type": event.EventType, "lease_token": event.LeaseToken}
	if event.ThreadID != "" {
		payload["thread_id"] = event.ThreadID
	}
	if event.TurnID != "" {
		payload["turn_id"] = event.TurnID
	}
	if event.Result != nil {
		payload["result"] = event.Result
	}
	if event.FailureCode != "" {
		payload["failure_code"] = event.FailureCode
	}
	return c.post(ctx, "/api/operation-cycles/action-requests/"+url.PathEscape(event.RequestID)+"/events", payload, event.EventID, nil)
}
func (c *HTTPClient) post(ctx context.Context, path string, payload any, idempotency string, target *map[string]any) error {
	if c == nil {
		return errors.New("operation runner HTTP client is nil")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := c.base.ResolveReference(&url.URL{Path: path})
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return errors.New("operation runner request is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return errors.New("operation runner request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return err
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return errors.New("operation runner response is not JSON")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || body["ok"] == false {
		return errors.New("operation runner request rejected")
	}
	if target != nil {
		*target = body
	}
	return nil
}

// matchesCanonicalDigest verifies the receipt-frozen material before it can
// become a local task prompt. The digest is not merely a display trace.
func matchesCanonicalDigest(value map[string]any, encoded string) bool {
	expected, err := hex.DecodeString(encoded)
	if err != nil || len(expected) != 32 {
		return false
	}
	actual, err := operationapp.Digest(value)
	return err == nil && subtle.ConstantTimeCompare(expected, actual[:]) == 1
}

func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return strings.TrimSpace(result)
}
func boolValue(value map[string]any, key string) bool { result, _ := value[key].(bool); return result }
func intValue(value map[string]any, key string) int {
	switch v := value[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func objectValue(value map[string]any, key string) (map[string]any, bool) {
	result, ok := value[key].(map[string]any)
	return result, ok
}

func stringSliceValue(value map[string]any, key string) []string {
	raw, ok := value[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) != text || text == "" {
			return nil
		}
		result = append(result, text)
	}
	return result
}
