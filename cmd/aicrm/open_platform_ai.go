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
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

const v1AIReviewPlanOperationPrefix = "ai_review_plan:"

type v1AIReviewPlanInput struct {
	Name         string                                  `json:"name"`
	SourceKind   string                                  `json:"source_kind"`
	SourceDigest effectport.Digest                       `json:"source_digest"`
	Recipients   []aiassistantport.RecipientCandidate    `json:"recipients"`
	Package      *aiassistantport.MachinePackageMetadata `json:"package,omitempty"`
	Members      []v1WorkbenchMember                     `json:"members,omitempty"`
	Content      []aiassistantport.ContentBlock          `json:"content,omitempty"`
}

type v1WorkbenchMember struct {
	UnionID     string `json:"union_id"`
	OwnerUserID string `json:"owner_userid"`
}

type v1WorkbenchDecision struct {
	Index  int    `json:"index"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
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
	if input.Package != nil || len(input.Members) > 0 || principal.HasCapability(string(openplatformport.CapabilityWorkbenchPackageCreate)) {
		if !principal.HasCapability(string(openplatformport.CapabilityWorkbenchPackageCreate)) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "workbench package grant is required")
		}
		return executor.v1CreateWorkbenchPackage(ctx, principal, input, idempotencyKey, requestID)
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

// The workbench package is only a frozen review candidate. Existing human
// approval of this source kind is disabled until authoritative send gates and
// the synthetic allowlist are composed.
func (executor *openPlatformExecutor) v1CreateWorkbenchPackage(ctx context.Context, principal accessdomain.MachinePrincipal, input v1AIReviewPlanInput, idempotencyKey, requestID string) (openplatformport.Result, error) {
	if executor.workbenchUnions == nil || executor.identity == nil || executor.contacts == nil || executor.contactStatuses == nil || executor.contactStaff == nil || executor.operationAudit == nil || executor.aiUOW == nil || len(executor.scopes.SurveyUnionScopes) != 1 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "workbench identity scope is unavailable")
	}
	if input.Package == nil || !input.Package.Valid() || input.Package.MemberCount != len(input.Members) || len(input.Members) < 1 || len(input.Members) > 10 || len(input.Recipients) != 0 || len(input.Content) == 0 || len(input.Content) > aiassistantport.MaxMessagesPerTarget || strings.TrimSpace(input.Name) == "" || !stringIn(principal.OwnerScope["corp_id"], principal.CorpID) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid workbench package")
	}
	// The dedicated machine Client must carry a bounded, explicit customer
	// allowlist. This is a configuration gate for synthetic P1 fixtures; it is
	// not evidence that a real customer permits automated messages.
	allowedCustomers := principal.OwnerScope["customer_id"]
	if len(allowedCustomers) < 1 || len(allowedCustomers) > 10 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "bounded package allowlist is required")
	}
	for _, value := range allowedCustomers {
		id, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || id < 1 || strconv.FormatInt(id, 10) != value {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "bounded package allowlist is required")
		}
	}
	// A shared frozen copy applies to each member; caller-provided customer and
	// staff IDs are never trusted. The AI owner validates material references.
	content := input.Content
	for _, block := range content {
		if block.ExcelCard != nil || !block.ValidInput() || block.LegacySourceSystem != "" || block.LegacyMaterialID != "" {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid workbench content")
		}
	}
	actor, err := aiassistantport.MachineActorFromClientID(principal.ClientID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorAuthentication, "machine subject is invalid")
	}
	var result aiassistantport.MachineCreatePlanResult
	decisions := make([]v1WorkbenchDecision, len(input.Members))
	err = executor.aiUOW.Within(ctx, func(tx context.Context) error {
		contacts, _, readErr := executor.contacts.MachineContactRows(tx, executor.scopes.WeComScope)
		if readErr != nil {
			return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact directory is unavailable")
		}
		byCustomer := make(map[int64]struct {
			owner, relation, identity string
			removed                   bool
		}, len(contacts))
		ownerIDs := make([]string, 0, len(contacts))
		customerIDs := make([]int64, 0, len(contacts))
		for _, row := range contacts {
			byCustomer[row.CustomerID] = struct {
				owner, relation, identity string
				removed                   bool
			}{row.OwnerUserID, row.BindingStatus, row.IdentityStatus, row.RemovedAt != nil}
			if row.OwnerUserID != "" {
				ownerIDs = append(ownerIDs, row.OwnerUserID)
			}
			customerIDs = append(customerIDs, row.CustomerID)
		}
		statuses, readErr := executor.contactStatuses.MachineContactStatuses(tx, customerIDs)
		if readErr != nil {
			return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "customer status is unavailable")
		}
		staff, readErr := executor.contactStaff.UsersByWeComUserIDs(tx, ownerIDs)
		if readErr != nil {
			return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "owner directory is unavailable")
		}
		staffByOwner := make(map[string]int64, len(staff))
		for _, user := range staff {
			if user.Active {
				staffByOwner[user.WeComUserID] = user.ID
			}
		}
		recipients := make([]aiassistantport.RecipientCandidate, 0, len(input.Members))
		seenUnion, seenCustomer := map[string]bool{}, map[customerdomain.CustomerID]bool{}
		rejected := false
		for index, member := range input.Members {
			decision := v1WorkbenchDecision{Index: index, Status: "rejected"}
			union := strings.TrimSpace(member.UnionID)
			if union == "" || union != member.UnionID || member.OwnerUserID == "" || member.OwnerUserID != strings.TrimSpace(member.OwnerUserID) || seenUnion[union] {
				decision.Reason = "invalid_or_duplicate_member"
			} else {
				seenUnion[union] = true
				resolved, resolveErr := executor.identity.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: executor.scopes.SurveyUnionScopes[0], Value: union, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.workbench"})
				if resolveErr != nil {
					return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "identity lookup is unavailable")
				}
				switch resolved.Status {
				case identityport.ResolveConflict:
					decision.Reason = "identity_conflict"
				case identityport.ResolveNotFound:
					decision.Reason = "unresolved"
				case identityport.ResolveFound:
					verified, verifyErr := executor.workbenchUnions.HasVerifiedScopedUnion(tx, resolved.CustomerID, executor.scopes.SurveyUnionScopes[0], union)
					if verifyErr != nil {
						return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "identity verification is unavailable")
					}
					row, present := byCustomer[int64(resolved.CustomerID)]
					status, statusPresent := statuses[int64(resolved.CustomerID)]
					staffID := staffByOwner[member.OwnerUserID]
					if !verified {
						decision.Reason = "identity_unverified_or_conflicted"
					} else if seenCustomer[resolved.CustomerID] {
						decision.Reason = "duplicate_customer"
					} else if !present || row.identity != "resolved" || row.relation != "bound" || row.removed {
						decision.Reason = "relationship_unavailable"
					} else if !statusPresent || status.State != string(customerdomain.StatusActive) {
						decision.Reason = "customer_inactive"
					} else if row.owner != member.OwnerUserID || staffID < 1 {
						decision.Reason = "owner_mismatch"
					} else if scopeErr := executor.ensureCustomerScope(tx, principal, resolved.CustomerID, nil); scopeErr != nil {
						decision.Reason = "out_of_scope"
					} else {
						seenCustomer[resolved.CustomerID] = true
						decision.Status = "accepted"
						recipients = append(recipients, aiassistantport.RecipientCandidate{CustomerID: resolved.CustomerID, StaffID: staffID, Content: content})
					}
				default:
					decision.Reason = "unresolved"
				}
			}
			if decision.Status != "accepted" {
				rejected = true
			}
			decisions[index] = decision
		}
		if rejected || len(recipients) != input.Package.MemberCount {
			return openplatformport.NewDetailedError(openplatformport.ErrorConflict, "workbench package contains rejected members", map[string]any{"created": false, "members": decisions})
		}
		command := aiassistantport.MachineCreatePlanCommand{Actor: actor, IdempotencyKey: idempotencyKey, Name: input.Name, SourceKind: "scrm_workbench", SourceDigest: effectport.Digest(input.Package.SourceFingerprint), Recipients: recipients, Package: input.Package, OccurredAt: time.Now().UTC()}
		result, readErr = executor.aiMachineIntake.CreateMachinePlanWithin(tx, command)
		if readErr != nil {
			return v1AIError(readErr)
		}
		outcome := "succeeded"
		if result.Replayed {
			outcome = "replayed"
		}
		return executor.operationAudit.RecordWithin(tx, principal, openplatformport.OperationAIReviewPlanCreate, requestID, outcome)
	})
	if err != nil {
		return openplatformport.Result{}, err
	}
	return openplatformport.Result{Data: map[string]any{"operation_id": v1AIReviewPlanOperationPrefix + strconv.FormatInt(int64(result.Plan.ID), 10), "review_state": result.Plan.ReviewState, "replayed": result.Replayed, "members": decisions, "automatic_send_allowed": false}}, nil
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
	items, err := executor.aiMachineReader.GetMachineRecipientResults(ctx, actor, id)
	if err != nil {
		return openplatformport.Result{}, v1AIError(err)
	}
	results := make([]map[string]any, 0, len(items))
	if executor.aiUOW == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation scope is unavailable")
	}
	err = executor.aiUOW.Within(ctx, func(tx context.Context) error {
		for _, item := range items {
			if scopeErr := executor.ensureCustomerScope(tx, principal, customerdomain.CustomerID(item.CustomerID), nil); scopeErr != nil {
				return openplatformport.NewError(openplatformport.ErrorPermission, "operation owner scope changed")
			}
			results = append(results, map[string]any{"recipient_id": item.RecipientID, "safe_user_ref": customerdomain.CanonicalOneIDLabel(customerdomain.CustomerID(item.CustomerID)), "review_state": item.ReviewState, "execution_state": item.ExecutionState, "provider_accepted": item.ProviderAccepted, "delivery_proven": item.DeliveryProven, "effect_id": item.EffectID, "updated_at": item.UpdatedAt.UTC()})
		}
		return nil
	})
	if err != nil {
		return openplatformport.Result{}, err
	}
	return openplatformport.Result{Data: map[string]any{
		"operation_id":          input.OperationID,
		"review_state":          status.ReviewState,
		"operation_state":       status.OperationState,
		"outcome_unknown_count": status.OutcomeUnknownCount,
		"created_at":            status.CreatedAt.UTC(),
		"updated_at":            status.UpdatedAt.UTC(),
		"recipients":            results,
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
