package sidebar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// AIWarehouseReader is a versioned consumption boundary. Its adapter must use
// the context center's standard warehouse path, not a browser-supplied identity
// or a second CRM database connection. Read must not trigger ingestion/model jobs.
type AIWarehouseReader interface {
	ReadAIOperations(context.Context, Principal, customerdomain.CustomerID) (AIWarehouseSnapshot, error)
}

// AIViewerVerifier reuses the current HttpOnly employee session. Possessing an
// older signed customer token is insufficient after an employee/session change.
type AIViewerVerifier interface {
	VerifyAIViewer(context.Context, *http.Request) (Principal, error)
}

// AISuggestionGenerator must enforce durable idempotency for IdempotencyKey in
// the existing Agent. No automatic retries or new CRM queue are introduced.
type AISuggestionGenerator interface {
	GenerateSidebarSuggestion(context.Context, AISuggestionInput) (string, error)
}

type AIMessage struct {
	ID         string    `json:"id"`
	Role       string    `json:"role"` // customer or sales; only this employee's private conversation
	Type       string    `json:"type"`
	Text       string    `json:"text"`
	OccurredAt time.Time `json:"occurred_at"`
}

type AIWarehouseSnapshot struct {
	CustomerID          customerdomain.CustomerID `json:"customer_id"`
	CorpID              string                    `json:"-"`
	EmployeeID          string                    `json:"-"`
	SourceVersion       string                    `json:"source_version"`
	Summary             string                    `json:"summary"`
	SummaryUpdatedAt    *time.Time                `json:"summary_updated_at"`
	SyncedAt            *time.Time                `json:"synced_at"`
	Messages            []AIMessage               `json:"-"`
	LastCustomerMessage *AIMessage                `json:"last_customer_message"`
}

type AISuggestionInput struct {
	Principal      Principal
	Snapshot       AIWarehouseSnapshot
	IdempotencyKey string
}

type aiSnapshotClaim struct {
	Version    int                       `json:"v"`
	CustomerID customerdomain.CustomerID `json:"c"`
	Binding    string                    `json:"b"`
	Digest     string                    `json:"d"`
	ExpiresAt  int64                     `json:"e"`
}

const aiSnapshotTTL = 5 * time.Minute
const aiReadTimeout = 8 * time.Second
const aiGenerateTimeout = 45 * time.Second

func aiBinding(principal Principal) string {
	b, _ := json.Marshal(principal)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func aiDigest(snapshot AIWarehouseSnapshot) string {
	// Messages are private to the backend, but every role/body participates in
	// the version check, including late arriving employee replies.
	b, _ := json.Marshal(struct {
		Snapshot AIWarehouseSnapshot
		Messages []AIMessage
	}{snapshot, snapshot.Messages})
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func (h *Handler) readAI(ctx context.Context, principal Principal, customerID customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, aiReadTimeout)
	defer cancel()
	s, err := h.config.AIWarehouse.ReadAIOperations(ctx, principal, customerID)
	if err != nil {
		return AIWarehouseSnapshot{}, err
	}
	if s.CustomerID != customerID || s.CorpID != principal.CorpID || s.EmployeeID != principal.EmployeeID || strings.TrimSpace(s.SourceVersion) == "" || len(s.Messages) > 40 || len(s.Summary) > 32000 {
		return AIWarehouseSnapshot{}, errors.New("warehouse scope or contract mismatch")
	}
	seen := map[string]bool{}
	var last *AIMessage
	for i := range s.Messages {
		m := &s.Messages[i]
		if m.ID == "" || seen[m.ID] || (m.Role != "customer" && m.Role != "sales") || m.OccurredAt.IsZero() || len(m.Text) > 16000 {
			return AIWarehouseSnapshot{}, errors.New("warehouse dialogue mismatch")
		}
		seen[m.ID] = true
		if m.Role == "customer" && (last == nil || m.OccurredAt.After(last.OccurredAt) || (m.OccurredAt.Equal(last.OccurredAt) && m.ID > last.ID)) {
			last = m
		}
	}
	if (last == nil) != (s.LastCustomerMessage == nil) || (last != nil && *last != *s.LastCustomerMessage) {
		return AIWarehouseSnapshot{}, errors.New("warehouse last message mismatch")
	}
	// Freeze caller-owned slices/pointers before a potentially long model call.
	s.Messages = append([]AIMessage(nil), s.Messages...)
	if s.LastCustomerMessage != nil {
		m := *s.LastCustomerMessage
		s.LastCustomerMessage = &m
	}
	if s.SummaryUpdatedAt != nil {
		at := *s.SummaryUpdatedAt
		s.SummaryUpdatedAt = &at
	}
	if s.SyncedAt != nil {
		at := *s.SyncedAt
		s.SyncedAt = &at
	}
	return s, nil
}

func (h *Handler) authorizeAI(w http.ResponseWriter, r *http.Request) (Principal, customerdomain.CustomerID, bool) {
	p, id, ok := h.authorize(w, r)
	if !ok {
		return p, id, false
	}
	if h.config.AICurrentViewer != nil {
		current, err := h.config.AICurrentViewer.VerifyAIViewer(r.Context(), r)
		if err != nil || current != p {
			writeError(w, 401, "invalid_context")
			return Principal{}, 0, false
		}
	}
	return p, id, true
}

func (h *Handler) signAISnapshot(c aiSnapshotClaim) string {
	b, _ := json.Marshal(c)
	encoded := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, h.config.CursorSigningKey)
	_, _ = mac.Write([]byte("sidebar-ai-v1:" + encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) verifyAISnapshot(token string, p Principal, id customerdomain.CustomerID) (aiSnapshotClaim, bool) {
	var c aiSnapshotClaim
	if len(token) > 2048 {
		return c, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	if c.Version != 1 || c.CustomerID != id || c.Binding != aiBinding(p) || c.ExpiresAt <= h.now().Unix() || c.ExpiresAt > h.now().Add(aiSnapshotTTL).Unix() || len(c.Digest) != 64 {
		return c, false
	}
	return c, hmac.Equal([]byte(token), []byte(h.signAISnapshot(c)))
}

func (h *Handler) aiOperations(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.authorizeAI(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "invalid_request")
		return
	}
	if h.config.AIWarehouse == nil {
		h.writeJSON(w, 200, map[string]any{"state": "unconfigured", "generation_ready": false, "read_at": h.now(), "source": "context_center_warehouse", "synced_at": nil})
		return
	}
	s, err := h.readAI(r.Context(), p, id)
	if err != nil {
		writeError(w, 503, "warehouse_unavailable")
		return
	}
	expiry := h.now().Add(aiSnapshotTTL)
	claim := aiSnapshotClaim{Version: 1, CustomerID: id, Binding: aiBinding(p), Digest: aiDigest(s), ExpiresAt: expiry.Unix()}
	h.writeJSON(w, 200, map[string]any{"state": "ready", "source": "context_center_warehouse", "data": s, "read_at": h.now(), "snapshot_token": h.signAISnapshot(claim), "expires_at": expiry, "generation_ready": h.config.AISuggestions != nil && strings.TrimSpace(s.Summary) != "" && s.LastCustomerMessage != nil && s.LastCustomerMessage.Type == "text" && strings.TrimSpace(s.LastCustomerMessage.Text) != ""})
}

func (h *Handler) aiSuggestion(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.authorizeAI(w, r)
	if !ok {
		return
	}
	var body struct {
		SnapshotToken string `json:"snapshot_token"`
		Confirmed     bool   `json:"confirmed"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	if !body.Confirmed {
		writeError(w, 400, "confirmation_required")
		return
	}
	claim, valid := h.verifyAISnapshot(body.SnapshotToken, p, id)
	if !valid {
		writeError(w, 409, "snapshot_expired")
		return
	}
	if h.config.AIWarehouse == nil || h.config.AISuggestions == nil {
		writeError(w, 503, "generation_unavailable")
		return
	}
	s, err := h.readAI(r.Context(), p, id)
	if err != nil {
		writeError(w, 503, "warehouse_unavailable")
		return
	}
	if aiDigest(s) != claim.Digest {
		writeError(w, 409, "snapshot_changed")
		return
	}
	if strings.TrimSpace(s.Summary) == "" || s.LastCustomerMessage == nil || s.LastCustomerMessage.Type != "text" || strings.TrimSpace(s.LastCustomerMessage.Text) == "" {
		writeError(w, 409, "context_incomplete")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aiGenerateTimeout)
	defer cancel()
	// Stable for this content and employee even after refreshing the token.
	key := h.signAISnapshot(aiSnapshotClaim{Version: 1, CustomerID: id, Binding: claim.Binding, Digest: claim.Digest})
	result, err := h.config.AISuggestions.GenerateSidebarSuggestion(ctx, AISuggestionInput{Principal: p, Snapshot: s, IdempotencyKey: key})
	if err != nil || ctx.Err() != nil {
		writeError(w, 504, "generation_outcome_unknown")
		return
	}
	if strings.TrimSpace(result) == "" || len(result) > 32000 {
		writeError(w, 502, "generation_invalid_result")
		return
	}
	current, err := h.readAI(r.Context(), p, id)
	if err != nil {
		writeError(w, 503, "warehouse_unavailable")
		return
	}
	if aiDigest(current) != claim.Digest || claim.ExpiresAt <= h.now().Unix() {
		writeError(w, 409, "snapshot_changed")
		return
	}
	if _, _, ok := h.authorizeAI(w, r); !ok {
		return
	}
	h.writeJSON(w, 200, map[string]any{"state": "ready", "snapshot_token": body.SnapshotToken, "suggestion": result, "auto_sent": false})
}
