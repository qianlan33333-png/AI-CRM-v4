package http

import (
	"fmt"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	"net/http"
)

func (h *Handler) executionReceipts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if _, ok := h.authorize(w, r, accessport.AIAssistantReceiptRead, false); !ok {
		return
	}
	planID, ok := pathID(w, r, "plan_id")
	if !ok {
		return
	}
	limit, err := queryLimit(r)
	live := r.URL.Query().Get("live")
	if err != nil || (live != "" && live != "false" && live != "true") {
		h.writeError(w, aiapp.ErrInvalid)
		return
	}
	if h.receipts == nil {
		h.writeError(w, aiapp.ErrUnavailable)
		return
	}
	page, err := h.app.ListRecipients(r.Context(), ai.RecipientPageQuery{PlanID: ai.PlanID(planID), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.writeError(w, err)
		return
	}
	bindings, err := h.app.ListEffects(r.Context(), ai.PlanID(planID))
	if err != nil {
		h.writeError(w, err)
		return
	}
	byRecipient := make(map[ai.RecipientID]ai.EffectBinding, len(bindings))
	for _, binding := range bindings {
		byRecipient[binding.RecipientID] = binding
	}
	type item struct {
		RecipientID  ai.RecipientID      `json:"recipient_id"`
		CustomerID   int64               `json:"customer_id"`
		CustomerName string              `json:"customer_name"`
		StaffID      int64               `json:"staff_id"`
		StaffName    string              `json:"staff_display_name"`
		State        ai.ExecutionState   `json:"local_execution_state"`
		Effect       *ai.EffectBinding   `json:"effect"`
		Receipt      ai.ExecutionReceipt `json:"receipt"`
	}
	items := make([]item, 0, len(page.Items))
	for _, recipient := range page.Items {
		row := item{RecipientID: recipient.ID, CustomerID: int64(recipient.CustomerID), CustomerName: recipient.CustomerName, StaffID: recipient.StaffID, StaffName: recipient.StaffDisplayName, State: recipient.ExecutionState, Receipt: ai.ExecutionReceipt{CreationCodeEvidence: "not_recorded", QueryState: "not_requested"}}
		if binding, found := byRecipient[recipient.ID]; found {
			row.Effect = &binding
			// Use the frozen plan recipient/content binding, never user-supplied Provider IDs.
			ref := fmt.Sprintf("aiassistant:%d:%d:%d", planID, recipient.ID, recipient.ContentVersionID)
			row.Receipt, err = h.receipts.ReadExecutionReceipt(r.Context(), ref, live == "true")
			if err != nil {
				h.writeError(w, aiapp.ErrUnavailable)
				return
			}
		}
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plan_id": planID, "items": items, "next_cursor": page.NextCursor, "send_interface": "POST /cgi-bin/externalcontact/add_msg_template", "chat_type": "single", "query_interface": "POST /cgi-bin/externalcontact/get_groupmsg_send_result", "read_only": true})
}
