package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
)

const (
	webhookMaxBody         = 32 << 10
	webhookPathPrefix      = "/api/integrations/ai-audience/"
	webhookPathSuffix      = "/membership-facts"
	webhookTimestampHeader = "X-AICRM-Timestamp"
	webhookEventIDHeader   = "X-AICRM-Event-Id"
	webhookSignatureHeader = "X-AICRM-Signature"
)

var packageKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,119}$`)

type WebhookApplication interface {
	Ingest(context.Context, segmentapp.VerifiedInboundFact) (segmentapp.InboundResult, error)
}
type WebhookHandler struct {
	service  WebhookApplication
	verifier segmentadapter.WebhookVerifier
	now      func() time.Time
}

func NewWebhookHandler(service WebhookApplication, secret string) (*WebhookHandler, error) {
	if service == nil {
		return nil, errors.New("audience webhook service is required")
	}
	verifier, err := segmentadapter.NewWebhookVerifier(secret)
	if err != nil {
		return nil, err
	}
	return &WebhookHandler{service: service, verifier: verifier, now: time.Now}, nil
}
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.verifier.Configured() {
		fail(w, 503, "capability_not_ready")
		return
	}
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	if !strings.HasPrefix(r.URL.Path, webhookPathPrefix) || !strings.HasSuffix(r.URL.Path, webhookPathSuffix) {
		fail(w, 404, "not_found")
		return
	}
	key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, webhookPathPrefix), webhookPathSuffix)
	if !packageKeyPattern.MatchString(key) {
		fail(w, 404, "not_found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBody))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			fail(w, 413, "request_too_large")
		} else {
			fail(w, 400, "invalid_request")
		}
		return
	}
	fact, err := h.verifier.Verify(key, r.Header.Get(webhookEventIDHeader), r.Header.Get(webhookTimestampHeader), r.Header.Get(webhookSignatureHeader), body, h.now())
	if errors.Is(err, segmentadapter.ErrInvalidWebhookProof) {
		fail(w, 401, "invalid_signature")
		return
	}
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	result, err := h.service.Ingest(r.Context(), fact)
	if err != nil {
		resultError(w, err)
		return
	}
	receipt := result.Receipt
	respond(w, http.StatusAccepted, map[string]any{"receipt_id": receipt.ID, "disposition": receipt.Disposition, "refresh_requested": receipt.RefreshRunID > 0, "replayed": result.Replayed})
}
