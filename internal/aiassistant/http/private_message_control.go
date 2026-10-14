package http

import (
	"context"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"net/http"
)

type privateControlApplication interface {
	RequestPrivateMessageControl(context.Context, ai.PrivateMessageControlCommand) (outbound.PrivateMessageControl, error)
	GetPrivateMessageControl(context.Context, ai.PlanID, ai.RecipientID, string, string) (outbound.PrivateMessageControl, error)
}

func (h *Handler) privateMessageControl(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authorize(w, r, access.AIAssistantStop, true)
	if !ok {
		return
	}
	plan, recipient, ok := twoPathIDs(w, r)
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
		h.writeError(w, err)
		return
	}
	controller, ok := h.app.(privateControlApplication)
	if !ok {
		h.writeError(w, app.ErrUnavailable)
		return
	}
	action := "stop"
	if r.PathValue("action") == "reconcile" {
		action = "reconcile"
	}
	result, err := controller.RequestPrivateMessageControl(r.Context(), ai.PrivateMessageControlCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: key, PlanID: ai.PlanID(plan), RecipientID: ai.RecipientID(recipient), ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, Action: action})
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "control": result, "send_triggered": false})
}
func (h *Handler) privateMessageControlResult(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r, access.AIAssistantReceiptRead, false); !ok {
		return
	}
	plan, recipient, ok := twoPathIDs(w, r)
	if !ok {
		return
	}
	controller, ok := h.app.(privateControlApplication)
	if !ok {
		h.writeError(w, app.ErrUnavailable)
		return
	}
	action := r.URL.Query().Get("action")
	if action == "" {
		action = "stop"
	}
	result, err := controller.GetPrivateMessageControl(r.Context(), ai.PlanID(plan), ai.RecipientID(recipient), action, r.URL.Query().Get("effect_id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plan_id": plan, "recipient_id": recipient, "control": result, "read_only": true})
}
