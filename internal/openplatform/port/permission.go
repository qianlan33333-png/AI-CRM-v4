package port

import (
	"context"
	"encoding/json"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type PermissionQuery struct {
	OperationID string          `json:"operation_id"`
	Resource    json.RawMessage `json:"resource"`
}

// Go's struct decoder matches field names without case sensitivity. Checking
// exact contract keys first prevents alternate spellings from overriding an
// authorization field while appearing distinct to duplicate-member checks.
func ParsePermissionQuery(raw []byte) (PermissionQuery, error) {
	var query PermissionQuery
	if !ValidJSONObject(raw) {
		return query, errors.New("invalid permission query")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return query, errors.New("invalid permission query")
	}
	for name := range fields {
		if name != "operation_id" && name != "resource" {
			return query, errors.New("unknown permission query field")
		}
	}
	if json.Unmarshal(raw, &query) != nil || query.OperationID == "" {
		return query, errors.New("operation_id is required")
	}
	if resource, present := fields["resource"]; present && !ValidJSONObject(resource) {
		return query, errors.New("resource must be an object")
	}
	return query, nil
}

type PermissionDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// PermissionChecker must not execute a command or accept caller-declared ownership.
type PermissionChecker interface {
	CheckPermission(context.Context, accessdomain.MachinePrincipal, OperationID, json.RawMessage) (PermissionDecision, error)
}
