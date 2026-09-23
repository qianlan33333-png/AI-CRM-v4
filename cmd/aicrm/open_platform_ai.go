package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

const v1AIReviewPlanOperationPrefix = "ai_review_plan:"

type v1AIReviewPlanInput struct {
	Name         string                               `json:"name"`
	SourceKind   string                               `json:"source_kind"`
	SourceDigest effectport.Digest                    `json:"source_digest"`
	Recipients   []aiassistantport.RecipientCandidate `json:"recipients"`
}

type v1OperationInput struct {
	OperationID string `json:"operation_id"`
}

func (executor *openPlatformExecutor) v1CreateAIReviewPlan(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage, idempotencyKey, requestID string) (openplatformport.Result, error) {
	if executor == nil || executor.aiMachineIntake == nil || executor.aiUOW == nil || executor.operationAudit == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "AI review plans are unavailable")
	}
	var input v1AIReviewPlanInput
	if err := decodeV1JSON(raw, &input); err != nil || strings.TrimSpace(idempotencyKey) != idempotencyKey || idempotencyKey == "" {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid AI review plan request")
	}
	actor, err := aiassistantport.MachineActorFromClientID(principal.ClientID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorAuthentication, "machine subject is invalid")
	}
	if len(input.Recipients) == 0 || len(input.Recipients) > aiassistantport.MaxRecipients {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "review plan recipients are required")
	}
	for _, recipient := range input.Recipients {
		if err := executor.ensureCustomerScope(ctx, principal, customerdomain.CustomerID(recipient.CustomerID), nil); err != nil {
			return openplatformport.Result{}, v1CustomerScopeError(err)
		}
	}
	command := aiassistantport.MachineCreatePlanCommand{Actor: actor, IdempotencyKey: idempotencyKey, Name: input.Name, SourceKind: input.SourceKind, SourceDigest: input.SourceDigest, Recipients: input.Recipients, OccurredAt: time.Now().UTC()}
	var result aiassistantport.MachineCreatePlanResult
	err = executor.aiUOW.Within(ctx, func(tx context.Context) error {
		var createErr error
		result, createErr = executor.aiMachineIntake.CreateMachinePlanWithin(tx, command)
		if createErr != nil {
			return createErr
		}
		outcome := "succeeded"
		if result.Replayed {
			outcome = "replayed"
		}
		return executor.operationAudit.RecordWithin(tx, principal, openplatformport.OperationAIReviewPlanCreate, requestID, outcome)
	})
	if err != nil {
		return openplatformport.Result{}, v1AIError(err)
	}
	return openplatformport.Result{Data: map[string]any{
		"operation_id": v1AIReviewPlanOperationPrefix + strconv.FormatInt(int64(result.Plan.ID), 10),
		"review_state": result.Plan.ReviewState,
		"replayed":     result.Replayed,
	}}, nil
}

func (executor *openPlatformExecutor) v1OperationStatus(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.aiMachineReader == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation status is unavailable")
	}
	var input v1OperationInput
	if err := decodeV1JSON(raw, &input); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid operation identifier")
	}
	id, err := parseV1AIReviewPlanOperationID(input.OperationID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "operation was not found")
	}
	actor, err := aiassistantport.MachineActorFromClientID(principal.ClientID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorAuthentication, "machine subject is invalid")
	}
	status, err := executor.aiMachineReader.GetMachineOperationStatus(ctx, actor, id)
	if err != nil {
		return openplatformport.Result{}, v1AIError(err)
	}
	return openplatformport.Result{Data: map[string]any{
		"operation_id":          input.OperationID,
		"review_state":          status.ReviewState,
		"operation_state":       status.OperationState,
		"outcome_unknown_count": status.OutcomeUnknownCount,
		"created_at":            status.CreatedAt.UTC(),
		"updated_at":            status.UpdatedAt.UTC(),
	}}, nil
}

func parseV1AIReviewPlanOperationID(value string) (aiassistantport.PlanID, error) {
	if !strings.HasPrefix(value, v1AIReviewPlanOperationPrefix) {
		return 0, errors.New("operation kind is invalid")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, v1AIReviewPlanOperationPrefix), 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != strings.TrimPrefix(value, v1AIReviewPlanOperationPrefix) {
		return 0, errors.New("operation id is invalid")
	}
	return aiassistantport.PlanID(id), nil
}

func v1AIError(err error) error {
	// AI's stable Port intentionally exposes only errors, not its app package.
	// Invalid or conflicting machine commands are never treated as an accepted
	// operation; unavailable ownership/read failures remain retryable at the
	// protocol level without disclosing plan details.
	switch {
	case errors.Is(err, aiassistantport.ErrInvalidMachineActor):
		return openplatformport.NewError(openplatformport.ErrorAuthentication, "machine subject is invalid")
	case errors.Is(err, aiassistantapp.ErrInvalid):
		return openplatformport.NewError(openplatformport.ErrorValidation, "AI review plan input is invalid")
	case errors.Is(err, aiassistantapp.ErrConflict), errors.Is(err, aiassistantapp.ErrIdempotencyConflict):
		return openplatformport.NewError(openplatformport.ErrorConflict, "AI review plan conflicts with the idempotency receipt")
	case errors.Is(err, aiassistantapp.ErrNotFound):
		return openplatformport.NewError(openplatformport.ErrorNotFound, "operation was not found")
	default:
		return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "AI review plan is unavailable")
	}
}
