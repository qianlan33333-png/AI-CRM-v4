package port

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// SchemaVersion identifies the stable V1 Operation Catalog contract. REST and
// MCP expose the same descriptors and invoke the same application service.
const SchemaVersion = "v1"

type OperationID string

const (
	OperationCoreProducts             OperationID = "audience.core_products.list"
	OperationCoreMembers              OperationID = "audience.members.list"
	OperationCoreMemberOperations     OperationID = "audience.member.operations.get"
	OperationCoreMemberHistory        OperationID = "audience.member.history.list"
	OperationCorePushRecord           OperationID = "audience.push.record"
	OperationCapabilitiesList         OperationID = "platform.capabilities.list"
	OperationCustomerResolve          OperationID = "customer.resolve"
	OperationCustomerContext          OperationID = "customer.context.get"
	OperationCustomerList             OperationID = "customer.list"
	OperationCustomerActivities       OperationID = "customer.activities.list"
	OperationAIReviewPlanCreate       OperationID = "ai.review_plan.create"
	OperationGet                      OperationID = "operation.get"
	OperationOrderList                OperationID = "order.list"
	OperationOrderGet                 OperationID = "order.get"
	OperationIdentityGet              OperationID = "identity.get"
	OperationQuestionnaireSubmissions OperationID = "questionnaire.submissions.list"
	OperationCustomerDetail           OperationID = "customer.detail.get"
	OperationRadarClicks              OperationID = "radar.clicks.list"
	OperationRadarLinks               OperationID = "radar.links.list"
	OperationChatRecords              OperationID = "chat.records.list"
)

type Capability string

const (
	CapabilityCoreProductRead          Capability = "audience.product.read"
	CapabilityCoreMemberRead           Capability = "audience.member.read"
	CapabilityCoreMemberOperationsRead Capability = "audience.member.operations.read"
	CapabilityCoreMemberHistoryRead    Capability = "audience.member.history.read"
	CapabilityCorePushWrite            Capability = "audience.push.write"
	CapabilityPlatformCapabilitiesRead Capability = "platform.capabilities.read"
	CapabilityCustomerResolve          Capability = "customer.resolve"
	CapabilityCustomerRead             Capability = "customer.read"
	CapabilityCustomerListRead         Capability = "customer.list.read"
	CapabilityCustomerActivityRead     Capability = "customer.activity.read"
	CapabilityAIReviewPlanCreate       Capability = "ai.review_plan.create"
	CapabilityWorkbenchPackageCreate   Capability = "ai.workbench.package.create"
	CapabilityOperationRead            Capability = "operation.read"
	CapabilityOrderRead                Capability = "order.read"
	CapabilityIdentityRead             Capability = "identity.read"
	CapabilityQuestionnaireRead        Capability = "questionnaire.read"
	CapabilityCustomerDetailRead       Capability = "customer.detail.read"
	CapabilityRadarClickRead           Capability = "radar.click.read"
	CapabilityRadarLinkRead            Capability = "radar.link.read"
	CapabilityChatRead                 Capability = "chat.read"
)

// Descriptor is the single catalog entry used by REST, MCP, administration,
// and contract tests. Availability is evaluated by the composed application;
// no transport independently invents a route policy.
type Descriptor struct {
	OperationID        OperationID `json:"operation_id"`
	RESTMethod         string      `json:"rest_method"`
	RESTPath           string      `json:"rest_path"`
	MCPTool            string      `json:"mcp_tool"`
	Capability         Capability  `json:"capability"`
	RequiredScope      string      `json:"required_scope"`
	SchemaVersion      string      `json:"schema_version"`
	ActivityTypes      []string    `json:"activity_types,omitempty"`
	ActivityItemFields []string    `json:"activity_item_fields,omitempty"`
}

// OperationCatalog is deliberately data, rather than path-prefix dispatch, so
// an endpoint cannot accidentally become available to a machine client.
func OperationCatalog() []Descriptor {
	return []Descriptor{
		{OperationID: OperationCapabilitiesList, RESTMethod: "GET", RESTPath: "/open/v1/capabilities", MCPTool: "list_capabilities", Capability: CapabilityPlatformCapabilitiesRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerResolve, RESTMethod: "POST", RESTPath: "/open/v1/customers:resolve", MCPTool: "resolve_customer", Capability: CapabilityCustomerResolve, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerContext, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}", MCPTool: "get_customer_context", Capability: CapabilityCustomerRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerList, RESTMethod: "GET", RESTPath: "/open/v1/customers", MCPTool: "list_customers", Capability: CapabilityCustomerListRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerActivities, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}/activities", MCPTool: "list_customer_activities", Capability: CapabilityCustomerActivityRead, RequiredScope: "read", SchemaVersion: SchemaVersion, ActivityTypes: []string{"message", "survey", "radar", "order"}, ActivityItemFields: []string{"activity_id", "type", "occurred_at", "source", "payload"}},
		{OperationID: OperationAIReviewPlanCreate, RESTMethod: "POST", RESTPath: "/open/v1/ai/review-plans", MCPTool: "create_ai_review_plan", Capability: CapabilityAIReviewPlanCreate, RequiredScope: "write", SchemaVersion: SchemaVersion},
		{OperationID: OperationGet, RESTMethod: "GET", RESTPath: "/open/v1/operations/{operation_id}", MCPTool: "get_operation_status", Capability: CapabilityOperationRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationOrderList, RESTMethod: "GET", RESTPath: "/open/v1/orders", MCPTool: "list_orders", Capability: CapabilityOrderRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationOrderGet, RESTMethod: "GET", RESTPath: "/open/v1/orders/{order_id}", MCPTool: "get_order", Capability: CapabilityOrderRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationIdentityGet, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}/identities", MCPTool: "get_customer_identities", Capability: CapabilityIdentityRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationQuestionnaireSubmissions, RESTMethod: "GET", RESTPath: "/open/v1/questionnaire-submissions", MCPTool: "list_questionnaire_submissions", Capability: CapabilityQuestionnaireRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerDetail, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}/detail", MCPTool: "get_customer_detail", Capability: CapabilityCustomerDetailRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationRadarClicks, RESTMethod: "GET", RESTPath: "/open/v1/radar/clicks", MCPTool: "list_radar_clicks", Capability: CapabilityRadarClickRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationRadarLinks, RESTMethod: "GET", RESTPath: "/open/v1/radar/links", MCPTool: "list_radar_links", Capability: CapabilityRadarLinkRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationChatRecords, RESTMethod: "GET", RESTPath: "/open/v1/chat-records", MCPTool: "list_chat_records", Capability: CapabilityChatRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCoreProducts, RESTMethod: "GET", RESTPath: "/open/v1/audience/core-products", MCPTool: "list_audience_core_products", Capability: CapabilityCoreProductRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCoreMembers, RESTMethod: "GET", RESTPath: "/open/v1/audience/packages/{package_id}/members", MCPTool: "list_audience_members", Capability: CapabilityCoreMemberRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCoreMemberOperations, RESTMethod: "GET", RESTPath: "/open/v1/audience/packages/{package_id}/members/{customer_id}/operations", MCPTool: "get_audience_member_operations", Capability: CapabilityCoreMemberOperationsRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCoreMemberHistory, RESTMethod: "GET", RESTPath: "/open/v1/audience/packages/{package_id}/members/{customer_id}/history", MCPTool: "list_audience_member_history", Capability: CapabilityCoreMemberHistoryRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCorePushRecord, RESTMethod: "POST", RESTPath: "/open/v1/audience/push-records", MCPTool: "record_audience_push", Capability: CapabilityCorePushWrite, RequiredScope: "write", SchemaVersion: SchemaVersion},
	}
}

func DescriptorForOperation(operation OperationID) (Descriptor, bool) {
	for _, descriptor := range OperationCatalog() {
		if descriptor.OperationID == operation {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func DescriptorForMCPTool(tool string) (Descriptor, bool) {
	for _, descriptor := range OperationCatalog() {
		if descriptor.MCPTool == tool {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// Allows requires both the narrowed token scope and the current client grant.
// A token minted before a capability change remains safe because Access reloads
// the client and the application checks the effective principal every call.
func (descriptor Descriptor) Allows(principal accessdomain.MachinePrincipal) bool {
	return principal.HasScope(descriptor.RequiredScope) && principal.HasCapability(string(descriptor.Capability))
}

func AvailableDescriptors(principal accessdomain.MachinePrincipal, available map[OperationID]bool) []Descriptor {
	result := make([]Descriptor, 0, len(available))
	for _, descriptor := range OperationCatalog() {
		if available[descriptor.OperationID] && descriptor.Allows(principal) {
			copy := descriptor
			copy.ActivityTypes = append([]string(nil), descriptor.ActivityTypes...)
			copy.ActivityItemFields = append([]string(nil), descriptor.ActivityItemFields...)
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].OperationID < result[right].OperationID })
	return result
}

// ActivityCursorContract freezes the opaque-cursor safeguards for the aggregate
// activity stream. The application signs this binding and keeps one position
// per Owner Port; it must reject a cursor when the customer, selected types or
// effective grant differ, and must never advance it after an Owner failure.
type ActivityCursorContract struct {
	BindsCustomer      bool
	BindsTypes         bool
	BindsGrant         bool
	PerTypeCursor      bool
	NoAdvanceOnFailure bool
}

func CustomerActivityCursorContract() ActivityCursorContract {
	return ActivityCursorContract{BindsCustomer: true, BindsTypes: true, BindsGrant: true, PerTypeCursor: true, NoAdvanceOnFailure: true}
}

// Invocation is transport-normalized. REST path/query values must be encoded
// into Input using the same V1 DTO fields accepted by the corresponding MCP
// tool, so the application sees one semantic request shape.
type Invocation struct {
	Operation      OperationID
	Principal      accessdomain.MachinePrincipal
	RequestID      string
	IdempotencyKey string
	Path           map[string]string
	Input          json.RawMessage
}

type Result struct {
	Data any
}

type ErrorCode string

const (
	ErrorAuthentication        ErrorCode = "authentication"
	ErrorPermission            ErrorCode = "permission"
	ErrorValidation            ErrorCode = "validation"
	ErrorNotFound              ErrorCode = "not_found"
	ErrorIdentityPending       ErrorCode = "identity_pending"
	ErrorIdentityConflict      ErrorCode = "identity_conflict"
	ErrorRateLimited           ErrorCode = "rate_limited"
	ErrorDependencyUnavailable ErrorCode = "dependency_unavailable"
	ErrorOutcomeUnknown        ErrorCode = "outcome_unknown"
	ErrorConflict              ErrorCode = "conflict"
)

type OperationError struct {
	Code    ErrorCode
	Message string
	Details any
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func NewError(code ErrorCode, message string) error {
	return &OperationError{Code: code, Message: message}
}

func NewDetailedError(code ErrorCode, message string, details any) error {
	return &OperationError{Code: code, Message: message, Details: details}
}

func ErrorDetailsOf(err error) any {
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil {
		return operationError.Details
	}
	return nil
}

func ErrorCodeOf(err error) ErrorCode {
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil {
		return operationError.Code
	}
	return ErrorDependencyUnavailable
}

// OperationService is the V1 application boundary. HTTP and MCP must invoke
// this exact service; it owns capability, scope, identity, idempotency and
// owner-Port semantics rather than leaving those decisions to either transport.
type OperationService interface {
	Available(context.Context, accessdomain.MachinePrincipal) ([]Descriptor, error)
	Invoke(context.Context, Invocation) (Result, error)
}

// ValidJSONObject accepts exactly one JSON object and rejects duplicate member
// names at every nesting level. The same check is used before REST and MCP
// hand an input to an operation, so a later duplicate cannot produce a
// different semantic request or idempotency digest by transport.
func ValidJSONObject(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, true); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func scanJSONValue(decoder *json.Decoder, requireObject bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if requireObject {
			return errors.New("JSON input must be an object")
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("JSON object key is invalid")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON member")
			}
			seen[name] = struct{}{}
			if err = scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		if requireObject {
			return errors.New("JSON input must be an object")
		}
		for decoder.More() {
			if err := scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}
