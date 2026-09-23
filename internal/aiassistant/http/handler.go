// Package http exposes authenticated AI Assistant administration and signed intake.
package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

const maxBody = 4 << 20

type RequestSecurity interface {
	Authenticate(context.Context, *stdhttp.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *stdhttp.Request) (accessdomain.Principal, error)
}

type Application interface {
	CreatePlan(context.Context, aiassistantport.CreatePlanCommand) (aiassistantport.CreatePlanResult, error)
	CreatePlanFromIdentities(context.Context, aiassistantapp.IdentityPlanCommand) (aiassistantapp.IdentityPlanResult, error)
	ListPlans(context.Context, aiassistantport.PlanListQuery) (aiassistantport.PlanPage, error)
	GetPlan(context.Context, aiassistantport.PlanID) (aiassistantport.Plan, error)
	ListRecipients(context.Context, aiassistantport.RecipientPageQuery) (aiassistantport.RecipientPage, error)
	GetRecipient(context.Context, aiassistantport.PlanID, aiassistantport.RecipientID) (aiassistantport.Recipient, aiassistantport.ContentVersion, error)
	UpdateContent(context.Context, aiassistantport.UpdateContentCommand) (aiassistantport.ContentVersion, error)
	ReviewRecipient(context.Context, aiassistantport.ReviewRecipientCommand) (aiassistantport.Recipient, error)
	RejectPlan(context.Context, aiassistantport.RejectPlanCommand) (aiassistantport.Plan, error)
	PreviewApproval(context.Context, aiassistantport.PreviewApprovalCommand) (aiassistantport.ApprovalPreview, error)
	ApprovePlan(context.Context, aiassistantport.ApprovePlanCommand) (aiassistantport.Plan, error)
	ListEffects(context.Context, aiassistantport.PlanID) ([]aiassistantport.EffectBinding, error)
	ReconcileEffect(context.Context, aiassistantport.ReconcileEffectCommand) (aiassistantport.Recipient, error)
}

type IntegrationConfig struct {
	Enabled        bool
	Key            string
	Secret         string
	ActorID        int64
	MaxSkew        time.Duration
	WeComCorpID    string
	OpenPlatformID string
}

type Config struct {
	Application   Application
	Security      RequestSecurity
	Authorizer    accessport.AIAssistantAuthorizer
	Integration   IntegrationConfig
	DispatchReady bool
	Now           func() time.Time
}

type Handler struct {
	app           Application
	security      RequestSecurity
	authorizer    accessport.AIAssistantAuthorizer
	integration   IntegrationConfig
	dispatchReady bool
	now           func() time.Time
}

func NewHandler(config Config) (*Handler, error) {
	if config.Application == nil || config.Security == nil || config.Authorizer == nil {
		return nil, aiassistantapp.ErrUnavailable
	}
	if config.Integration.Enabled {
		if strings.TrimSpace(config.Integration.Key) != config.Integration.Key || config.Integration.Key == "" || len(config.Integration.Key) > 128 || len(config.Integration.Secret) < 32 || config.Integration.ActorID < 1 {
			return nil, aiassistantapp.ErrUnavailable
		}
		if config.Integration.MaxSkew == 0 {
			config.Integration.MaxSkew = 5 * time.Minute
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Handler{app: config.Application, security: config.Security, authorizer: config.Authorizer, integration: config.Integration, dispatchReady: config.DispatchReady, now: config.Now}, nil
}

func (h *Handler) Routes() stdhttp.Handler {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /api/admin/ai-assistant/plans", h.listPlans)
	mux.HandleFunc("POST /api/admin/ai-assistant/plans", h.createPlan)
	mux.HandleFunc("GET /api/admin/ai-assistant/plans/{plan_id}", h.getPlan)
	mux.HandleFunc("GET /api/admin/ai-assistant/plans/{plan_id}/recipients", h.listRecipients)
	mux.HandleFunc("GET /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}", h.getRecipient)
	mux.HandleFunc("PATCH /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}/content", h.updateContent)
	mux.HandleFunc("POST /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}/review", h.reviewRecipient)
	mux.HandleFunc("POST /api/admin/ai-assistant/plans/{plan_id}/reject", h.rejectPlan)
	mux.HandleFunc("POST /api/admin/ai-assistant/plans/{plan_id}/preview-approval", h.previewApproval)
	mux.HandleFunc("POST /api/admin/ai-assistant/plans/{plan_id}/approve", h.approvePlan)
	mux.HandleFunc("GET /api/admin/ai-assistant/plans/{plan_id}/effects", h.listEffects)
	mux.HandleFunc("POST /api/admin/ai-assistant/effects/{effect_id}/reconcile", h.reconcileEffect)
	mux.HandleFunc("POST /api/integrations/ai-assistant/review-plans", h.integrationPlan)
	// Frozen donor caller route. It shares the authenticated adapter and never
	// forks a second plan implementation.
	mux.HandleFunc("POST /api/admin/ai-assist/review-plans", h.integrationPlan)
	return mux
}

func (h *Handler) reconcileEffect(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantReconcile, true)
	if !ok {
		return
	}
	var input struct {
		Generation     int64             `json:"generation"`
		Fence          int64             `json:"fence"`
		EvidenceDigest effectport.Digest `json:"evidence_digest"`
		Reason         string            `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	recipient, err := h.app.ReconcileEffect(r.Context(), aiassistantport.ReconcileEffectCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, EffectID: r.PathValue("effect_id"), Generation: input.Generation, Fence: input.Fence, EvidenceDigest: input.EvidenceDigest, Reason: input.Reason})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "recipient": recipient})
}

func (h *Handler) previewApproval(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantApprove, true)
	if !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	preview, err := h.app.PreviewApproval(r.Context(), aiassistantport.PreviewApprovalCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, PlanID: aiassistantport.PlanID(planID), ExpectedVersion: input.ExpectedVersion})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "plan_id": preview.PlanID, "plan_version": preview.PlanVersion, "eligible_count": preview.EligibleCount, "preview_digest": preview.PreviewDigest})
}

func (h *Handler) approvePlan(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantApprove, true)
	if !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64             `json:"expected_version"`
		PreviewDigest   effectport.Digest `json:"preview_digest"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	plan, err := h.app.ApprovePlan(r.Context(), aiassistantport.ApprovePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, PlanID: aiassistantport.PlanID(planID), ExpectedVersion: input.ExpectedVersion, PreviewDigest: input.PreviewDigest})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "plan": plan, "replayed": false, "dispatch_ready": h.dispatchReady})
}

func (h *Handler) listEffects(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if _, ok := h.authorize(w, r, accessport.AIAssistantRead, false); !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	items, err := h.app.ListEffects(r.Context(), aiassistantport.PlanID(planID))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "items": items, "next_cursor": ""})
}

func (h *Handler) listPlans(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if _, ok := h.authorize(w, r, accessport.AIAssistantRead, false); !ok {
		return
	}
	limit, err := queryLimit(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	page, err := h.app.ListPlans(r.Context(), aiassistantport.PlanListQuery{Keyword: r.URL.Query().Get("keyword"), State: aiassistantport.PlanState(r.URL.Query().Get("status")), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "items": page.Items, "next_cursor": page.NextCursor})
}

func (h *Handler) createPlan(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantReview, true)
	if !ok {
		return
	}
	var input planCreateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	result, err := h.app.CreatePlan(r.Context(), aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, Name: input.Name, SourceKind: input.SourceKind, SourceDigest: input.SourceDigest, Recipients: input.Recipients, OccurredAt: h.now().UTC()})
	if err != nil {
		h.writeError(w, err)
		return
	}
	status := stdhttp.StatusCreated
	if result.Replayed {
		status = stdhttp.StatusOK
	}
	writeJSON(w, status, map[string]any{"ok": true, "plan": result.Plan, "replayed": result.Replayed, "dispatch_ready": h.dispatchReady})
}

func (h *Handler) getPlan(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if _, ok := h.authorize(w, r, accessport.AIAssistantRead, false); !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	plan, err := h.app.GetPlan(r.Context(), aiassistantport.PlanID(planID))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "plan": plan, "replayed": false, "dispatch_ready": h.dispatchReady})
}

func (h *Handler) listRecipients(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if _, ok := h.authorize(w, r, accessport.AIAssistantRead, false); !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	limit, err := queryLimit(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	page, err := h.app.ListRecipients(r.Context(), aiassistantport.RecipientPageQuery{PlanID: aiassistantport.PlanID(planID), State: aiassistantport.ReviewState(r.URL.Query().Get("status")), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "items": page.Items, "next_cursor": page.NextCursor})
}

func (h *Handler) getRecipient(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if _, ok := h.authorize(w, r, accessport.AIAssistantRead, false); !ok {
		return
	}
	planID, recipientID, ok := twoPathIDs(w, r)
	if !ok {
		return
	}
	recipient, content, err := h.app.GetRecipient(r.Context(), aiassistantport.PlanID(planID), aiassistantport.RecipientID(recipientID))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "recipient": recipient, "content": content})
}

func (h *Handler) updateContent(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantReview, true)
	if !ok {
		return
	}
	planID, recipientID, ok := twoPathIDs(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64                          `json:"expected_version"`
		Blocks          []aiassistantport.ContentBlock `json:"blocks"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	content, err := h.app.UpdateContent(r.Context(), aiassistantport.UpdateContentCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, PlanID: aiassistantport.PlanID(planID), RecipientID: aiassistantport.RecipientID(recipientID), ExpectedVersion: input.ExpectedVersion, Blocks: input.Blocks})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, content)
}

func (h *Handler) reviewRecipient(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantReview, true)
	if !ok {
		return
	}
	planID, recipientID, ok := twoPathIDs(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64                       `json:"expected_version"`
		Decision        aiassistantport.ReviewState `json:"decision"`
		Reason          string                      `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	recipient, err := h.app.ReviewRecipient(r.Context(), aiassistantport.ReviewRecipientCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, PlanID: aiassistantport.PlanID(planID), RecipientID: aiassistantport.RecipientID(recipientID), ExpectedVersion: input.ExpectedVersion, Decision: input.Decision, Reason: input.Reason})
	if err != nil {
		h.writeError(w, err)
		return
	}
	_, content, err := h.app.GetRecipient(r.Context(), aiassistantport.PlanID(planID), recipient.ID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "recipient": recipient, "content": content})
}

func (h *Handler) rejectPlan(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	actor, ok := h.authorize(w, r, accessport.AIAssistantApprove, true)
	if !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	plan, err := h.app.RejectPlan(r.Context(), aiassistantport.RejectPlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, PlanID: aiassistantport.PlanID(planID), ExpectedVersion: input.ExpectedVersion, Reason: input.Reason})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, stdhttp.StatusOK, map[string]any{"ok": true, "plan": plan, "replayed": false, "dispatch_ready": h.dispatchReady})
}

type integrationRequest struct {
	Name         string            `json:"name"`
	SourceKind   string            `json:"source_kind"`
	SourceDigest effectport.Digest `json:"source_digest"`
	Identities   []struct {
		Kind    string                         `json:"kind"`
		Scope   string                         `json:"scope"`
		Value   string                         `json:"value"`
		StaffID int64                          `json:"staff_id"`
		Content []aiassistantport.ContentBlock `json:"content"`
	} `json:"identities"`
	// Frozen donor compatibility: these fields are only translated at this
	// authenticated edge. Raw IDs never cross into the domain aggregate.
	ExternalUserID       string                     `json:"external_userid"`
	TargetExternalUserID string                     `json:"target_external_userid"`
	OwnerUserID          string                     `json:"owner_userid"`
	SenderUserID         string                     `json:"sender_userid"`
	ContentText          string                     `json:"content_text"`
	Message              string                     `json:"message"`
	ContentPackage       map[string]json.RawMessage `json:"content_package"`
	ExternalEventID      string                     `json:"external_event_id"`
	IdempotencyKey       string                     `json:"idempotency_key"`
	Operator             string                     `json:"operator"`
	PackageKey           string                     `json:"package_key"`
	DisplayName          string                     `json:"display_name"`
	Recipients           []struct {
		UnionID      string `json:"unionid"`
		OwnerUserID  string `json:"owner_userid"`
		SenderUserID string `json:"sender_userid"`
		CustomerName string `json:"customer_name"`
	} `json:"recipients"`
}

func (h *Handler) integrationPlan(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !h.integration.Enabled {
		h.writeError(w, aiassistantapp.ErrUnavailable)
		return
	}
	body, timestamp, nonce, key, err := h.verifyIntegration(r)
	if err != nil {
		writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"ok": false, "error": "integration_authentication_failed"})
		return
	}
	var input integrationRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	businessKey, keyErr := integrationBusinessKey(input, key, r.Header.Get("Idempotency-Key"))
	if keyErr != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	targets, name, sourceKind, sourceDigest, adaptErr := h.integrationTargets(input, key, businessKey)
	if adaptErr != nil {
		h.writeError(w, aiassistantapp.ErrInvalid)
		return
	}
	result, err := h.app.CreatePlanFromIdentities(r.Context(), aiassistantapp.IdentityPlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: h.integration.ActorID}, IdempotencyKey: businessKey, Name: name, SourceKind: sourceKind, SourceDigest: sourceDigest, Targets: targets, OccurredAt: timestamp, IntegrationKey: key, Nonce: nonce, ExpiresAt: timestamp.Add(h.integration.MaxSkew)})
	if err != nil {
		h.writeError(w, err)
		return
	}
	var plan any
	if result.Plan.ID > 0 {
		plan = result.Plan
	}
	writeJSON(w, stdhttp.StatusAccepted, map[string]any{"ok": true, "plan": plan, "replayed": result.Replayed, "dispatch_ready": h.dispatchReady, "resolution": map[string]int{"found": result.Found, "not_found": result.NotFound, "conflict": result.Conflicted, "unverified": result.Unverified, "ineligible": result.Ineligible, "invalid": result.Invalid, "material_unmapped": result.MaterialUnmapped}, "dispositions": result.Dispositions})
}

// integrationBusinessKey keeps the authenticated request key for nonce
// protection, but makes the frozen donor's event key the plan idempotency
// scope. A retry can therefore use a new signed nonce/header without minting
// a second review plan for the same donor event.
func integrationBusinessKey(input integrationRequest, integrationKey, headerKey string) (string, error) {
	if len(input.Identities) > 0 {
		return headerKey, nil
	}
	eventID := strings.TrimSpace(input.ExternalEventID)
	donorKey := strings.TrimSpace(input.IdempotencyKey)
	if eventID == "" {
		eventID = donorKey
	}
	if eventID == "" {
		return headerKey, nil
	}
	if len(eventID) > 512 || strings.ContainsAny(eventID, "\r\n\x00") || len(donorKey) > 512 || strings.ContainsAny(donorKey, "\r\n\x00") {
		return "", errors.New("invalid legacy event key")
	}
	return "legacy-event-" + string(effectport.Hash("aiassistant.legacy-event", integrationKey, eventID)), nil
}

func (h *Handler) integrationTargets(input integrationRequest, key, idempotencyKey string) ([]aiassistantapp.IdentityTarget, string, string, effectport.Digest, error) {
	if len(input.Identities) > 0 {
		if input.ExternalUserID != "" || input.TargetExternalUserID != "" || len(input.Recipients) > 0 {
			return nil, "", "", "", errors.New("mixed intake contracts")
		}
		targets := make([]aiassistantapp.IdentityTarget, 0, len(input.Identities))
		for _, item := range input.Identities {
			kind, ok := identityKind(item.Kind)
			if !ok {
				return nil, "", "", "", errors.New("invalid identity kind")
			}
			targets = append(targets, aiassistantapp.IdentityTarget{Reference: identitydomain.Reference{Kind: kind, Scope: item.Scope, Value: item.Value, Assurance: identitydomain.AssuranceDeclared, Source: "aiassistant.integration." + key}, StaffID: item.StaffID, Content: item.Content})
		}
		return targets, input.Name, input.SourceKind, input.SourceDigest, nil
	}
	content, err := legacyTextContent(input)
	if err != nil {
		return nil, "", "", "", err
	}
	name, kind := strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.SourceKind)
	if name == "" {
		name = strings.TrimSpace(input.Name)
	}
	if name == "" {
		name = "AI assistant review"
	}
	if kind == "" {
		kind = "legacy_ai_assist_review"
	}
	digest := input.SourceDigest
	if !effectport.ValidDigest(digest) {
		frozen, marshalErr := json.Marshal(content)
		if marshalErr != nil {
			return nil, "", "", "", marshalErr
		}
		digest = effectport.Hash("aiassistant.legacy-review", input.ExternalEventID, idempotencyKey, name, string(frozen))
	}
	owner := strings.TrimSpace(input.OwnerUserID)
	if owner == "" {
		owner = strings.TrimSpace(input.SenderUserID)
	}
	external := strings.TrimSpace(input.ExternalUserID)
	legacyTarget := strings.TrimSpace(input.TargetExternalUserID)
	if external != "" && legacyTarget != "" && external != legacyTarget {
		return nil, "", "", "", errors.New("ambiguous legacy target")
	}
	if external == "" {
		external = legacyTarget
	}
	if external != "" {
		if len(input.Recipients) > 0 {
			return nil, "", "", "", errors.New("mixed legacy recipient shapes")
		}
		if h.integration.WeComCorpID == "" || owner == "" {
			return nil, "", "", "", errors.New("legacy single configuration")
		}
		return []aiassistantapp.IdentityTarget{{Reference: identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + h.integration.WeComCorpID, Value: external, Assurance: identitydomain.AssuranceDeclared, Source: "aiassistant.integration." + key}, StaffWeComUserID: owner, Content: content}}, name, kind, digest, nil
	}
	if len(input.Recipients) == 0 || h.integration.OpenPlatformID == "" {
		return nil, "", "", "", errors.New("legacy batch configuration")
	}
	targets := make([]aiassistantapp.IdentityTarget, 0, len(input.Recipients))
	for _, item := range input.Recipients {
		staff := strings.TrimSpace(item.OwnerUserID)
		if staff == "" {
			staff = strings.TrimSpace(item.SenderUserID)
		}
		targets = append(targets, aiassistantapp.IdentityTarget{Reference: identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:" + h.integration.OpenPlatformID, Value: item.UnionID, Assurance: identitydomain.AssuranceDeclared, Source: "aiassistant.integration." + key}, StaffWeComUserID: staff, Content: content})
	}
	return targets, name, kind, digest, nil
}

func legacyTextContent(input integrationRequest) ([]aiassistantport.ContentBlock, error) {
	text := strings.TrimSpace(input.ContentText)
	if text == "" {
		text = strings.TrimSpace(input.Message)
	}
	blocks := make([]aiassistantport.ContentBlock, 0, 10)
	if input.ContentPackage != nil {
		if raw := input.ContentPackage["content_text"]; len(raw) > 0 && text == "" {
			if err := json.Unmarshal(raw, &text); err != nil {
				return nil, errors.New("invalid legacy content text")
			}
			text = strings.TrimSpace(text)
		}
		allowed := map[string]bool{"content_text": true, "image_library_ids": true, "miniprogram_library_ids": true, "attachment_library_ids": true, "group_invite_library_ids": true, "dynamic_miniprogram_card": true}
		for key := range input.ContentPackage {
			if !allowed[key] {
				return nil, errors.New("unsupported legacy content package")
			}
		}
		for _, field := range []struct {
			name, kind string
			content    aiassistantport.ContentKind
			maximum    int
		}{
			{"image_library_ids", "image", aiassistantport.ContentImage, 3},
			{"miniprogram_library_ids", "miniprogram", aiassistantport.ContentMiniProgram, 1},
			{"attachment_library_ids", "attachment", aiassistantport.ContentAttachment, 9},
			{"group_invite_library_ids", "group_invite", aiassistantport.ContentLink, 1},
		} {
			ids, err := legacyPackageIDs(input.ContentPackage[field.name], field.name, field.maximum)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				blocks = append(blocks, aiassistantport.ContentBlock{Kind: field.content, MaterialKind: field.kind, LegacySourceSystem: "ai-crm-v2", LegacyMaterialID: id})
			}
		}
		if raw := input.ContentPackage["dynamic_miniprogram_card"]; meaningfulJSON(raw) {
			canonical, err := canonicalJSONObject(raw)
			if err != nil {
				return nil, errors.New("invalid legacy dynamic mini program")
			}
			sum := sha256.Sum256(canonical)
			blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentMiniProgram, MaterialKind: "miniprogram", LegacySourceSystem: "ai-crm-v2", LegacyMaterialID: "dynamic_miniprogram:" + hex.EncodeToString(sum[:])})
		}
	}
	if len([]rune(text)) > 4000 {
		return nil, errors.New("legacy content text too long")
	}
	if text != "" {
		blocks = append([]aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: text}}, blocks...)
	}
	if len(blocks) == 0 || len(blocks) > 9+1 {
		return nil, errors.New("legacy content required")
	}
	return blocks, nil
}

func legacyPackageIDs(raw json.RawMessage, field string, maximum int) ([]string, error) {
	if !meaningfulJSON(raw) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values []any
	if err := decoder.Decode(&values); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid legacy " + field)
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		var rawID string
		switch item := value.(type) {
		case json.Number:
			rawID = string(item)
		case string:
			rawID = item
		default:
			return nil, errors.New("invalid legacy " + field)
		}
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id < 1 || rawID != strconv.FormatInt(id, 10) {
			return nil, errors.New("invalid legacy " + field)
		}
		canonical := strconv.FormatInt(id, 10)
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			ids = append(ids, canonical)
		}
	}
	if len(ids) > maximum {
		return nil, errors.New("too many legacy " + field)
	}
	return ids, nil
}

func meaningfulJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "[]" && value != "{}"
}

func canonicalJSONObject(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || len(value) == 0 || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid object")
	}
	return json.Marshal(value)
}

func (h *Handler) verifyIntegration(r *stdhttp.Request) ([]byte, time.Time, string, string, error) {
	key := strings.TrimSpace(r.Header.Get("X-AICRM-Integration-Key"))
	nonce := strings.TrimSpace(r.Header.Get("X-AICRM-Nonce"))
	rawTimestamp := strings.TrimSpace(r.Header.Get("X-AICRM-Timestamp"))
	signature := strings.TrimSpace(r.Header.Get("X-AICRM-Signature"))
	idempotency, err := idempotencyKey(r)
	if err != nil || key != h.integration.Key || len(nonce) < 16 || len(nonce) > 128 || len(signature) != 64 {
		return nil, time.Time{}, "", "", errors.New("invalid integration authentication")
	}
	seconds, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return nil, time.Time{}, "", "", err
	}
	timestamp := time.Unix(seconds, 0).UTC()
	now := h.now().UTC()
	if timestamp.Before(now.Add(-h.integration.MaxSkew)) || timestamp.After(now.Add(h.integration.MaxSkew)) {
		return nil, time.Time{}, "", "", errors.New("integration timestamp outside window")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, time.Time{}, "", "", err
	}
	if len(body) > maxBody {
		return nil, time.Time{}, "", "", errors.New("integration request body too large")
	}
	bodyDigest := sha256.Sum256(body)
	message := rawTimestamp + "\n" + nonce + "\n" + idempotency + "\n" + hex.EncodeToString(bodyDigest[:])
	mac := hmac.New(sha256.New, []byte(h.integration.Secret))
	_, _ = mac.Write([]byte(message))
	provided, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, time.Time{}, "", "", errors.New("invalid integration signature")
	}
	return body, timestamp, nonce, key, nil
}

func (h *Handler) authorize(w stdhttp.ResponseWriter, r *stdhttp.Request, action accessport.AIAssistantAction, csrf bool) (accessdomain.Principal, bool) {
	var actor accessdomain.Principal
	var err error
	if csrf {
		actor, err = h.security.AuthorizeCSRF(r.Context(), r)
	} else {
		actor, err = h.security.Authenticate(r.Context(), r)
	}
	if err == nil {
		err = h.authorizer.AuthorizeAIAssistant(r.Context(), actor, action)
	}
	if err != nil {
		if errors.Is(err, accessdomain.ErrAuthentication) {
			writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"ok": false, "error": "authentication_required"})
		} else {
			writeJSON(w, stdhttp.StatusForbidden, map[string]any{"ok": false, "error": "permission_denied"})
		}
		return accessdomain.Principal{}, false
	}
	return actor, true
}

func (h *Handler) writeError(w stdhttp.ResponseWriter, err error) {
	status, code := stdhttp.StatusServiceUnavailable, "ai_assistant_unavailable"
	switch {
	case errors.Is(err, aiassistantapp.ErrInvalid):
		status, code = stdhttp.StatusBadRequest, "invalid_request"
	case errors.Is(err, aiassistantapp.ErrNotFound):
		status, code = stdhttp.StatusNotFound, "not_found"
	case errors.Is(err, aiassistantapp.ErrConflict):
		status, code = stdhttp.StatusConflict, "version_or_idempotency_conflict"
	case errors.Is(err, aiassistantapp.ErrMaterialDrift):
		status, code = stdhttp.StatusConflict, "material_drift"
	case errors.Is(err, aiassistantapp.ErrNoRecipients):
		status, code = stdhttp.StatusUnprocessableEntity, "no_resolvable_recipients"
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}

type planCreateRequest struct {
	Name         string                               `json:"name"`
	SourceKind   string                               `json:"source_kind"`
	SourceDigest effectport.Digest                    `json:"source_digest"`
	Recipients   []aiassistantport.RecipientCandidate `json:"recipients"`
}

func decodeJSON(w stdhttp.ResponseWriter, r *stdhttp.Request, target any) bool {
	decoder := json.NewDecoder(stdhttp.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
		return false
	}
	return true
}

func pathID(w stdhttp.ResponseWriter, r *stdhttp.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
		return 0, false
	}
	return id, true
}

func twoPathIDs(w stdhttp.ResponseWriter, r *stdhttp.Request) (int64, int64, bool) {
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return 0, 0, false
	}
	recipientID, ok := pathID(w, r, "recipient_id")
	return planID, recipientID, ok
}

func queryLimit(r *stdhttp.Request) (int, error) {
	if r.URL.Query().Get("limit") == "" {
		return aiassistantapp.MaximumPageSize, nil
	}
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 || value > aiassistantapp.MaximumPageSize {
		return 0, aiassistantapp.ErrInvalid
	}
	return value, nil
}

func idempotencyKey(r *stdhttp.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", aiassistantapp.ErrInvalid
	}
	key := strings.TrimSpace(values[0])
	if len(key) < 8 || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return "", aiassistantapp.ErrInvalid
	}
	return key, nil
}

func identityKind(value string) (identitydomain.Kind, bool) {
	switch identitydomain.Kind(value) {
	case identitydomain.KindWeComExternalUserID, identitydomain.KindUnionID, identitydomain.KindMPOpenID, identitydomain.KindOAOpenID:
		return identitydomain.Kind(value), true
	default:
		return "", false
	}
}

func writeJSON(w stdhttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
