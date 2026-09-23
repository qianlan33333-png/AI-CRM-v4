package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestVerifyIntegrationAuthenticatesTheExactRequest(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	secret := strings.Repeat("s", 32)
	handler := &Handler{integration: IntegrationConfig{Enabled: true, Key: "automation", Secret: secret, ActorID: 7, MaxSkew: 5 * time.Minute}, now: func() time.Time { return now }}
	body := []byte(`{"name":"review"}`)
	request := signedIntegrationRequest(now, secret, body)
	got, timestamp, nonce, key, err := handler.verifyIntegration(request)
	if err != nil || string(got) != string(body) || !timestamp.Equal(now) || nonce != "1234567890abcdef" || key != "automation" {
		t.Fatalf("body=%q timestamp=%s nonce=%q key=%q err=%v", got, timestamp, nonce, key, err)
	}

	tampered := signedIntegrationRequest(now, secret, body)
	tampered.Body = io.NopCloser(strings.NewReader(`{"name":"changed"}`))
	if _, _, _, _, err = handler.verifyIntegration(tampered); err == nil {
		t.Fatal("tampered body passed signature verification")
	}

	stale := signedIntegrationRequest(now.Add(-6*time.Minute), secret, body)
	if _, _, _, _, err = handler.verifyIntegration(stale); err == nil {
		t.Fatal("stale request passed timestamp verification")
	}
}

func TestIntegrationTargetsTreatsSignedIdentityAsDeclaredAndFreezesLegacyShapes(t *testing.T) {
	handler := &Handler{integration: IntegrationConfig{WeComCorpID: "corp-1", OpenPlatformID: "platform-1"}}
	modern := integrationRequest{Name: "review", SourceKind: "automation", SourceDigest: "sha256:8c7f3cd6a42f9fbbf45464fd8677b143f32ac7f3a1df834f09c6f1f219b5e99e"}
	modern.Identities = append(modern.Identities, struct {
		Kind    string                         `json:"kind"`
		Scope   string                         `json:"scope"`
		Value   string                         `json:"value"`
		StaffID int64                          `json:"staff_id"`
		Content []aiassistantport.ContentBlock `json:"content"`
	}{Kind: "wecom_external_userid", Scope: "wecom-corp:corp-1", Value: "external-1", StaffID: 7, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}})
	targets, _, _, _, err := handler.integrationTargets(modern, "integration", "idem-key-1")
	if err != nil || len(targets) != 1 || targets[0].Reference.Assurance != identitydomain.AssuranceDeclared {
		t.Fatalf("modern targets=%+v err=%v", targets, err)
	}
	legacy := integrationRequest{ExternalUserID: "external-1", OwnerUserID: "staff-1", ContentText: "hello", ExternalEventID: "event-1"}
	targets, _, _, _, err = handler.integrationTargets(legacy, "integration", "idem-key-1")
	if err != nil || len(targets) != 1 || targets[0].Reference.Kind != identitydomain.KindWeComExternalUserID || targets[0].StaffWeComUserID != "staff-1" {
		t.Fatalf("legacy single targets=%+v err=%v", targets, err)
	}
	batch := integrationRequest{ContentPackage: map[string]json.RawMessage{"content_text": json.RawMessage(`"hello"`)}}
	batch.Recipients = append(batch.Recipients, struct {
		UnionID      string `json:"unionid"`
		OwnerUserID  string `json:"owner_userid"`
		SenderUserID string `json:"sender_userid"`
		CustomerName string `json:"customer_name"`
	}{UnionID: "union-1", OwnerUserID: "staff-1", CustomerName: "customer"})
	targets, _, _, _, err = handler.integrationTargets(batch, "integration", "idem-key-2")
	if err != nil || len(targets) != 1 || targets[0].Reference.Kind != identitydomain.KindUnionID || targets[0].Reference.Scope != "wechat-open-platform:platform-1" {
		t.Fatalf("legacy batch targets=%+v err=%v", targets, err)
	}
	legacy.Recipients = batch.Recipients
	if _, _, _, _, err = handler.integrationTargets(legacy, "integration", "idem-key-3"); err == nil {
		t.Fatal("mixed legacy single and batch shapes were accepted")
	}
}

func TestRoutesExposeOnlyTheFrozenLegacyIntakeAdapter(t *testing.T) {
	handler := &Handler{}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-assist/review-plans", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy route status=%d", response.Code)
	}
}

func TestSignedLegacyHTTPIntakeAcceptsFrozenMetadataWithoutTrustingIt(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	secret := strings.Repeat("s", 32)
	app := &capturingIntegrationApplication{}
	handler := &Handler{app: app, integration: IntegrationConfig{Enabled: true, Key: "automation", Secret: secret, ActorID: 7, MaxSkew: 5 * time.Minute, WeComCorpID: "corp-1", OpenPlatformID: "platform-1"}, now: func() time.Time { return now }}
	body := []byte(`{"external_userid":"external-1","owner_userid":"staff-1","content_package":{"content_text":"hello"},"external_event_id":"event-1","idempotency_key":"donor-key-1","operator":"untrusted-operator","package_key":"legacy-package-1"}`)
	request := signedIntegrationRequest(now, secret, body)
	request.URL.Path = "/api/admin/ai-assist/review-plans"
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if app.command.Actor != (aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: 7}) || app.command.IdempotencyKey == "integration-replay-1" || !strings.HasPrefix(app.command.IdempotencyKey, "legacy-event-sha256:") || len(app.command.Targets) != 1 {
		t.Fatalf("command=%+v", app.command)
	}
	target := app.command.Targets[0]
	if target.Reference.Assurance != identitydomain.AssuranceDeclared || target.Reference.Value != "external-1" || target.StaffWeComUserID != "staff-1" || len(target.Content) != 1 || target.Content[0].Text != "hello" {
		t.Fatalf("target=%+v", target)
	}
}

func TestLegacyContentPackageUsesOnlyVerifiedMediaMappings(t *testing.T) {
	input := integrationRequest{ContentPackage: map[string]json.RawMessage{
		"content_text":             json.RawMessage(`"hello"`),
		"image_library_ids":        json.RawMessage(`["11",12]`),
		"miniprogram_library_ids":  json.RawMessage(`[21]`),
		"attachment_library_ids":   json.RawMessage(`[31]`),
		"group_invite_library_ids": json.RawMessage(`[41]`),
		"dynamic_miniprogram_card": json.RawMessage(`{"appid":"wx1","pagepath":"pages/index"}`),
	}}
	blocks, err := legacyTextContent(input)
	if err != nil || len(blocks) != 7 || blocks[0].Kind != aiassistantport.ContentText || blocks[1].LegacyMaterialID != "11" || blocks[1].MaterialID != 0 || blocks[1].LegacySourceSystem != "ai-crm-v2" || blocks[4].Kind != aiassistantport.ContentAttachment || !strings.HasPrefix(blocks[6].LegacyMaterialID, "dynamic_miniprogram:") {
		t.Fatalf("blocks=%+v err=%v", blocks, err)
	}
	if blocks[1].Valid() || !blocks[1].ValidInput() {
		t.Fatalf("legacy block must be input-only: %+v", blocks[1])
	}
	if _, err = legacyTextContent(integrationRequest{ContentPackage: map[string]json.RawMessage{"image_library_ids": json.RawMessage(`[1,2,3,4]`)}}); err == nil {
		t.Fatal("over-limit donor package was accepted")
	}
}

func TestLegacyEventBusinessKeySurvivesSignedHeaderRotation(t *testing.T) {
	input := integrationRequest{ExternalEventID: "event-42", IdempotencyKey: "donor-request-key"}
	first, err := integrationBusinessKey(input, "integration", "header-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := integrationBusinessKey(input, "integration", "header-two")
	if err != nil || first != second || !strings.HasPrefix(first, "legacy-event-sha256:") {
		t.Fatalf("first=%q second=%q err=%v", first, second, err)
	}
}

// Embedding the application interface keeps this edge test focused on the
// signed decoder and avoids replacing the application's production behavior.
type capturingIntegrationApplication struct {
	Application
	command aiassistantapp.IdentityPlanCommand
}

func (a *capturingIntegrationApplication) CreatePlanFromIdentities(_ context.Context, command aiassistantapp.IdentityPlanCommand) (aiassistantapp.IdentityPlanResult, error) {
	a.command = command
	return aiassistantapp.IdentityPlanResult{Unverified: 1, Dispositions: []aiassistantapp.IdentityTargetDisposition{{Ordinal: 0, Status: "unverified"}}}, nil
}

func signedIntegrationRequest(at time.Time, secret string, body []byte) *http.Request {
	req := httptest.NewRequest("POST", "/api/integrations/ai-assistant/review-plans", strings.NewReader(string(body)))
	timestamp := strconv.FormatInt(at.Unix(), 10)
	nonce, idempotency := "1234567890abcdef", "integration-replay-1"
	digest := sha256.Sum256(body)
	message := timestamp + "\n" + nonce + "\n" + idempotency + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	req.Header.Set("X-AICRM-Integration-Key", "automation")
	req.Header.Set("X-AICRM-Nonce", nonce)
	req.Header.Set("X-AICRM-Timestamp", timestamp)
	req.Header.Set("X-AICRM-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Idempotency-Key", idempotency)
	return req
}
