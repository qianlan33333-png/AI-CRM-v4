package main

import (
	"context"
	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

func (executor *openPlatformExecutor) CheckPermission(ctx context.Context, p accessdomain.MachinePrincipal, op openport.OperationID, resource json.RawMessage) (openport.PermissionDecision, error) {
	result := openport.PermissionDecision{Decision: "requires_resource_check", Reason: "owner_contract_requires_execution_context"}
	switch op {
	case openport.OperationCustomerContext, openport.OperationCustomerDetail, openport.OperationCustomerActivities, openport.OperationIdentityGet, openport.OperationChatRecords:
		var input struct {
			CustomerID int64 `json:"customer_id"`
		}
		if len(resource) == 0 {
			return result, nil
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(resource, &fields) != nil || len(fields) != 1 || fields["customer_id"] == nil {
			return openport.PermissionDecision{Decision: "denied", Reason: "invalid_resource"}, nil
		}
		if e := decodeV1JSON(resource, &input); e != nil || input.CustomerID < 1 {
			return openport.PermissionDecision{Decision: "denied", Reason: "invalid_resource"}, nil
		}
		if e := executor.ensureCustomerScope(ctx, p, customerdomain.CustomerID(input.CustomerID), nil); e != nil {
			return openport.PermissionDecision{Decision: "denied", Reason: "resource_not_authorized_or_unresolved"}, nil
		}
		return openport.PermissionDecision{Decision: "allowed", Reason: "current_action_and_customer_scope"}, nil
	case openport.OperationCapabilitiesList:
		return openport.PermissionDecision{Decision: "allowed", Reason: "current_action_grant"}, nil
	}
	return result, nil
}
