package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

const (
	AudienceWebhookClientID        = "aicrm-audience-direct-push"
	AudienceWebhookClientIDHeader  = "X-AICRM-Client-Id"
	AudienceWebhookTimestampHeader = "X-AICRM-Timestamp"
	AudienceWebhookEventIDHeader   = "X-AICRM-Event-Id"
	AudienceWebhookSignatureHeader = "X-AICRM-Signature"
	maxAudienceWebhookBodyBytes    = 2 << 20
)

var (
	ErrAudienceProtocolUnauthorized = errors.New("audience webhook authentication failed")
	ErrAudienceProtocolUnavailable  = errors.New("audience webhook authentication unavailable")
)

type AudienceProtocolClaim struct {
	EventIDDigest [32]byte
	PayloadDigest [32]byte
}

type AudienceProtocolAuthenticator interface {
	AuthenticateAudienceWebhook(context.Context, *http.Request, string, []byte) (AudienceProtocolClaim, error)
}

type DirectPushHandler struct {
	service   automationport.DirectPushService
	protocols AudienceProtocolAuthenticator
	now       func() time.Time
}

func NewDirectPushHandler(service automationport.DirectPushService, protocols AudienceProtocolAuthenticator) (*DirectPushHandler, error) {
	if service == nil || protocols == nil {
		return nil, errors.New("audience direct push HTTP dependencies are required")
	}
	return &DirectPushHandler{service: service, protocols: protocols, now: time.Now}, nil
}

func (h *DirectPushHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		method(writer, "POST")
		return
	}
	reference, status, ok := directPushRoute(request.URL.Path)
	if !ok {
		errorJSON(writer, http.StatusNotFound, "audience_webhook_not_found")
		return
	}
	body, err := readDirectPushBody(writer, request)
	if err != nil {
		errorJSON(writer, http.StatusBadRequest, "invalid_request_body")
		return
	}
	claim, err := h.protocols.AuthenticateAudienceWebhook(request.Context(), request, reference, body)
	if err != nil {
		if errors.Is(err, ErrAudienceProtocolUnavailable) {
			errorJSON(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		errorJSON(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	if status {
		h.status(writer, request, reference, body)
		return
	}
	h.accept(writer, request, reference, body, claim)
}

func (h *DirectPushHandler) accept(writer http.ResponseWriter, request *http.Request, reference string, body []byte, claim AudienceProtocolClaim) {
	var input struct {
		Items []automationport.DirectPushInput `json:"items"`
	}
	if decodeDirectPushJSON(body, &input) != nil || len(input.Items) < 1 || len(input.Items) > automationport.MaxDirectPushItems {
		errorJSON(writer, http.StatusBadRequest, "invalid_audience_push")
		return
	}
	result, err := h.service.AcceptDirectPush(request.Context(), automationport.DirectPushCommand{
		WebhookReference: reference,
		IdempotencyKey:   "audience-webhook",
		EventIDDigest:    claim.EventIDDigest,
		PayloadDigest:    claim.PayloadDigest,
		Items:            input.Items,
		AcceptedAt:       h.now().UTC(),
	})
	if err != nil {
		directPushError(writer, err)
		return
	}
	status := http.StatusAccepted
	if result.AcceptedCount == 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(writer, status, result)
}

func (h *DirectPushHandler) status(writer http.ResponseWriter, request *http.Request, reference string, body []byte) {
	var input struct {
		PushIDs []string `json:"push_ids"`
	}
	if decodeDirectPushJSON(body, &input) != nil || len(input.PushIDs) < 1 || len(input.PushIDs) > automationport.MaxDirectPushItems {
		errorJSON(writer, http.StatusBadRequest, "invalid_status_query")
		return
	}
	items, err := h.service.DirectPushStatuses(request.Context(), reference, input.PushIDs)
	if err != nil {
		directPushError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func directPushRoute(path string) (string, bool, bool) {
	const prefix = "/api/automation/audience/webhooks/"
	if !strings.HasPrefix(path, prefix) {
		return "", false, false
	}
	tail := strings.TrimPrefix(path, prefix)
	status := strings.HasSuffix(tail, "/status")
	if status {
		tail = strings.TrimSuffix(tail, "/status")
	}
	if tail == "" || strings.Contains(tail, "/") || !validAudienceOpaque(tail) {
		return "", false, false
	}
	return tail, status, true
}

func validAudienceOpaque(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func readDirectPushBody(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	reader := http.MaxBytesReader(writer, request.Body, maxAudienceWebhookBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil || len(body) == 0 {
		return nil, errors.New("invalid request body")
	}
	return body, nil
}

func decodeDirectPushJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func directPushError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automationapp.ErrDirectPushInvalid):
		errorJSON(writer, http.StatusBadRequest, "invalid_audience_push")
	case errors.Is(err, automationapp.ErrDirectPushConflict):
		errorJSON(writer, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, automationapp.ErrDirectPushNotFound):
		errorJSON(writer, http.StatusNotFound, "audience_push_not_found")
	case errors.Is(err, automationapp.ErrDirectPushUnavailable):
		errorJSON(writer, http.StatusServiceUnavailable, "service_unavailable")
	default:
		errorJSON(writer, http.StatusServiceUnavailable, "service_unavailable")
	}
}

func AudiencePayloadDigest(reference string, body []byte) [32]byte {
	payload := make([]byte, 0, len(reference)+1+len(body))
	payload = append(payload, reference...)
	payload = append(payload, '\n')
	payload = append(payload, body...)
	return sha256.Sum256(payload)
}

var _ http.Handler = (*DirectPushHandler)(nil)
