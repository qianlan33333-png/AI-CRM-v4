package http

// Route is the frozen dd8d60d machine-route inventory. It is data rather than
// implicit prefix matching so a new admin route cannot accidentally become a
// machine endpoint.
type Route struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Capability string `json:"capability"`
}

// scopeFor keeps the frozen protocol vocabulary separate from the client
// capability grant. A read-scoped token cannot use a write grant that its
// parent client also happens to have.
func scopeFor(route Route) string {
	if route.Path == "/mcp" {
		if route.Method == "GET" {
			return "read"
		}
		return "write"
	}
	if route.Method == "GET" {
		return "read"
	}
	return "write"
}

var Inventory = []Route{
	{"GET", "/mcp", "mcp_read"},
	{"POST", "/mcp", "mcp_execute"},
	{"GET", "/api/identity/resolve", "identity_resolve"},
	{"GET", "/api/external/chat-records", "external_read"},
	{"GET", "/api/external/questionnaire-submissions", "external_read"},
	{"GET", "/api/external/radar-clicks", "external_read"},
	{"GET", "/api/external/radar-links", "external_read"},
	{"POST", "/api/external/ai-audience/spec/dry-run", "external_write"},
	{"POST", "/api/external/ai-audience/spec/apply", "external_write"},
	{"POST", "/api/external/ai-audience/spec/publish", "external_write"},
	{"POST", "/api/external/ai-audience/packages/{package_key}/archive", "external_write"},
	{"POST", "/api/external/ai-audience/templates/preview", "external_write"},
	{"POST", "/api/external/ai-audience/templates/apply", "external_write"},
	{"POST", "/api/external/ai-audience/simple/preview", "external_write"},
	{"POST", "/api/external/ai-audience/simple/apply", "external_write"},
	{"POST", "/api/external/ai-audience/simple/{package_key}/activate", "external_write"},
	{"POST", "/api/external/ai-audience/simple/{package_key}/archive", "external_write"},
	{"POST", "/api/external/ai-audience/e2e/run", "external_write"},
	{"POST", "/api/automation/group-ops/broadcast", "group_broadcast_execute"},
	{"GET", "/api/external/orders", "external_read"},
	{"GET", "/api/external/orders/{order_no}", "external_read"},
	{"GET", "/api/external/users/resolve", "external_read"},
	{"POST", "/api/ai-assist/external/campaigns", "campaign_draft_create"},
	{"GET", "/api/ai-assist/external/campaigns/{campaign_code}", "campaign_status_read"},
	{"POST", "/api/ai-assist/external/campaign-preparations", "campaign_preparation_create"},
	{"GET", "/api/ai-assist/external/campaign-preparations/{preparation_id}", "campaign_preparation_read"},
	{"POST", "/api/ai-assist/external/campaign-preparations/{preparation_id}/commit", "campaign_preparation_commit"},
	{"GET", "/api/ai/audience/schema-catalog", "external_read"},
	{"GET", "/api/ai/audience/packages", "external_read"},
	{"POST", "/api/ai/audience/packages", "external_write"},
	{"GET", "/api/ai/audience/packages/{package_id}", "external_read"},
	{"POST", "/api/ai/audience/packages/{package_id}/versions", "external_write"},
	{"POST", "/api/ai/audience/packages/{package_id}/preview", "external_write"},
	{"POST", "/api/ai/audience/packages/{package_id}/publish", "external_write"},
	{"POST", "/api/ai/audience/packages/{package_id}/pause", "external_write"},
	{"POST", "/api/ai/audience/packages/{package_id}/archive", "external_write"},
	{"POST", "/api/ai/audience/packages/{package_id}/refresh", "external_write"},
	{"POST", "/api/ai/audience/ticks/incremental", "external_write"},
	{"POST", "/api/ai/audience/ticks/daily", "external_write"},
	{"POST", "/api/ai/audience/source-dirty", "external_write"},
	{"GET", "/api/ai/audience/packages/{package_id}/outbound-subscriptions", "external_read"},
	{"POST", "/api/ai/audience/packages/{package_id}/outbound-subscriptions", "external_write"},
	{"PATCH", "/api/ai/audience/outbound-subscriptions/{subscription_id}", "external_write"},
	{"POST", "/api/ai/audience/outbound-subscriptions/{subscription_id}/pause", "external_write"},
	{"GET", "/api/ai/audience/packages/{package_id}/runs", "external_read"},
	{"GET", "/api/ai/audience/packages/{package_id}/members", "external_read"},
	{"GET", "/api/ai/audience/packages/{package_id}/events", "external_read"},
	{"GET", "/api/ai/audience/packages/{package_id}/external-effects", "external_read"},
	{"GET", "/api/ai/audience/health", "external_read"},
	{"POST", "/api/operation-cycles/reports", "operation_cycle_report_write"},
	{"POST", "/api/operation-cycles/runner/heartbeat", "operation_cycle_runner_heartbeat"},
	{"POST", "/api/operation-cycles/action-requests/claim", "operation_cycle_action_claim"},
	{"POST", "/api/operation-cycles/action-requests/{request_id}/events", "operation_cycle_action_event_write"},
	{"GET", "/api/operation-cycles/context-index", "operation_cycle_context_read"},
	{"GET", "/api/operation-cycles/strategies/{strategy_key}/context", "operation_cycle_context_read"},
	{"POST", "/api/operation-cycles/strategy-change-proposals", "operation_cycle_strategy_propose"},
}
