// Package app owns Open Platform read orchestration and safe wire projections.
package app

import (
	"context"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"strconv"
	"strings"
)

type OperationStatus struct {
	reader aiassistantport.MachineTransactionalReader
	uow    platformport.UnitOfWork
	scope  port.OperationCustomerScopeAuthorizer
}

func NewOperationStatus(reader aiassistantport.MachineTransactionalReader, uow platformport.UnitOfWork, scope port.OperationCustomerScopeAuthorizer) (*OperationStatus, error) {
	if reader == nil || uow == nil || scope == nil {
		return nil, port.NewError(port.ErrorDependencyUnavailable, "operation status is unavailable")
	}
	return &OperationStatus{reader: reader, uow: uow, scope: scope}, nil
}

func (service *OperationStatus) Read(ctx context.Context, principal accessdomain.MachinePrincipal, operationID string) (port.Result, error) {
	id, err := parseOperationID(operationID)
	if err != nil {
		return port.Result{}, port.NewError(port.ErrorNotFound, "operation was not found")
	}
	actor, err := aiassistantport.MachineActorFromClientID(principal.ClientID)
	if err != nil {
		return port.Result{}, port.NewError(port.ErrorAuthentication, "machine subject is invalid")
	}
	var status aiassistantport.MachineOperationStatus
	results := make([]map[string]any, 0)
	err = service.uow.Within(ctx, func(tx context.Context) error {
		aggregate, readErr := service.reader.GetMachineOperationWithin(tx, actor, id)
		if readErr != nil {
			return MachineAIError(readErr)
		}
		status = aggregate.Status
		for _, item := range aggregate.Recipients {
			if scopeErr := service.scope.AuthorizeOperationCustomer(tx, principal, customerdomain.CustomerID(item.CustomerID)); scopeErr != nil {
				return port.NewError(port.ErrorPermission, "operation owner scope changed")
			}
			results = append(results, map[string]any{"recipient_id": item.RecipientID, "safe_user_ref": customerdomain.CanonicalOneIDLabel(customerdomain.CustomerID(item.CustomerID)), "review_state": item.ReviewState, "execution_state": item.ExecutionState, "provider_accepted": item.ProviderAccepted, "delivery_proven": item.DeliveryProven, "effect_id": item.EffectID, "updated_at": item.UpdatedAt.UTC()})
		}
		return nil
	})
	if err != nil {
		return port.Result{}, err
	}
	return port.Result{Data: map[string]any{"operation_id": operationID, "review_state": status.ReviewState, "operation_state": status.OperationState, "outcome_unknown_count": status.OutcomeUnknownCount, "created_at": status.CreatedAt.UTC(), "updated_at": status.UpdatedAt.UTC(), "recipients": results}}, nil
}

func parseOperationID(value string) (aiassistantport.PlanID, error) {
	if !strings.HasPrefix(value, port.AIReviewPlanOperationPrefix) {
		return 0, errors.New("operation kind is invalid")
	}
	suffix := strings.TrimPrefix(value, port.AIReviewPlanOperationPrefix)
	id, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != suffix {
		return 0, errors.New("operation id is invalid")
	}
	return aiassistantport.PlanID(id), nil
}

// MachineAIError translates stable Owner errors without importing AI app/store.
func MachineAIError(err error) error {
	var operationError *port.OperationError
	if errors.As(err, &operationError) {
		return err
	}
	switch {
	case errors.Is(err, aiassistantport.ErrInvalidMachineActor):
		return port.NewError(port.ErrorAuthentication, "machine subject is invalid")
	case errors.Is(err, aiassistantport.ErrInvalid):
		return port.NewError(port.ErrorValidation, "AI review plan input is invalid")
	case errors.Is(err, aiassistantport.ErrConflict), errors.Is(err, aiassistantport.ErrIdempotencyConflict):
		return port.NewError(port.ErrorConflict, "AI review plan conflicts with the idempotency receipt")
	case errors.Is(err, aiassistantport.ErrNotFound):
		return port.NewError(port.ErrorNotFound, "operation was not found")
	default:
		return port.NewError(port.ErrorDependencyUnavailable, "AI review plan is unavailable")
	}
}
