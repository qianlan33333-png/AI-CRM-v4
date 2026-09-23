package paymenthttp

import (
	"context"
	"net/http"
	"strings"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// ProfitSharingReceiverRecoveryApplication is intentionally optional so the
// ordinary checkout surface does not gain a money-operation command.  The
// composed Payment application owns all proof, receiver CAS, receipt, audit,
// and EER acceptance checks.
type ProfitSharingReceiverRecoveryApplication interface {
	RecoverProfitSharingReceiver(context.Context, paymentport.ProfitSharingReceiverRecoveryCommand) (paymentport.ReceiverReadiness, error)
}

func (handler *Handler) recoverProfitSharingReceiver(writer http.ResponseWriter, request *http.Request, reference string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	// A historical receiver recovery is a payment-configuration exception: it
	// may accept a fresh Provider intent after independently proving that every
	// prior attempt stopped locally. Keep that exceptional command with the
	// super administrator rather than the ordinary daily payment-write role.
	if err != nil || !paymentSuperAdminWriteRole(principal) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	application, ok := handler.app.(ProfitSharingReceiverRecoveryApplication)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var body struct {
		EvidenceReference string `json:"evidence_reference"`
	}
	command := paymentport.ProfitSharingReceiverRecoveryCommand{
		ReceiverReference: reference,
		ActorAdminUserID:  principal.InternalID,
		IdempotencyKey:    strings.TrimSpace(request.Header.Get("Idempotency-Key")),
	}
	if command.IdempotencyKey == "" || !decodeJSON(writer, request, &body) {
		if command.IdempotencyKey == "" {
			writeError(writer, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	command.EvidenceReference = body.EvidenceReference
	if !command.Valid() {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	value, err := application.RecoverProfitSharingReceiver(request.Context(), command)
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"reference":     value.Reference,
		"state":         value.State,
		"ready":         value.Ready,
		"outcome_known": value.OutcomeKnown,
		"updated_at":    value.UpdatedAt,
	})
}
