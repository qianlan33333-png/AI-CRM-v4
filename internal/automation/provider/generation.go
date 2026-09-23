// Package provider contains the composition-wired AI generation adapter. It
// owns no Automation tables and receives prompts only through the stable
// Automation dispatch reader after EER has committed an attempted effect.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

const generationArtifactKind = "automation.ai_agent_generate.text.v1"

type GenerationConfig struct {
	Enabled bool
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
	Client  *http.Client
}

func (c GenerationConfig) Policy() (automationport.GenerationModelPolicy, error) {
	if !c.Enabled {
		return automationport.GenerationModelPolicy{}, errors.New("AI generation is disabled")
	}
	if normalizeCompletionsURL(c.BaseURL) == "" || strings.TrimSpace(c.APIKey) != c.APIKey || c.APIKey == "" || strings.TrimSpace(c.Model) != c.Model || c.Model == "" || c.Timeout < time.Second || c.Timeout > 2*time.Minute {
		return automationport.GenerationModelPolicy{}, errors.New("AI generation configuration is incomplete")
	}
	return automationport.GenerationModelPolicy{Mode: "enabled", Endpoint: normalizeCompletionsURL(c.BaseURL), Model: c.Model, Temperature: 0.4}, nil
}

type GenerationProvider struct {
	ConfigReader func(context.Context) (GenerationConfig, error)
	owner        effectport.Owner
	kind         effectport.Kind
	config       GenerationConfig
	dispatch     automationport.GenerationDispatchReader
}

func NewGenerationProvider(config GenerationConfig, dispatch automationport.GenerationDispatchReader) (*GenerationProvider, error) {
	if dispatch == nil {
		return nil, errors.New("AI generation dispatch reader is required")
	}
	// Disabled is a valid deployed default. An enabled provider is checked at
	// construction so a malformed secret/endpoint cannot become an EER retry.
	if config.Enabled {
		if _, err := config.Policy(); err != nil {
			return nil, err
		}
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.Timeout}
	}
	return &GenerationProvider{owner: effectport.OwnerAutomation, kind: effectport.KindAIAgentGenerate, config: config, dispatch: dispatch}, nil
}

func (p *GenerationProvider) GenerationModelPolicy(ctx context.Context) (automationport.GenerationModelPolicy, error) {
	if p == nil {
		return automationport.GenerationModelPolicy{}, errors.New("AI generation provider unavailable")
	}
	config, err := p.currentConfig(ctx)
	if err != nil {
		return automationport.GenerationModelPolicy{}, err
	}
	return config.Policy()
}

func normalizeCompletionsURL(raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "/"))
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return ""
	}
	if strings.HasSuffix(raw, "/chat/completions") {
		return raw
	}
	return raw + "/chat/completions"
}

func generationUserPrompt(task string, values automationport.GenerationContext) string {
	return strings.TrimSpace(task) + "\n\n【问卷】\n" + values.Questionnaire + "\n\n【最近20条聊天】\n" + values.RecentChats + "\n\n【用户标签】\n" + values.Tags + "\n\n【激活信息】\n" + values.Activation
}

func validGeneratedText(value string, dispatch automationport.GenerationDispatch) bool {
	return automationport.ValidGeneratedText(value, dispatch.RolePrompt, dispatch.TaskPrompt)
}

func generationFailure(code string, attempted bool) effectport.AdapterResult {
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("automation.ai-agent-generate.failure", code), FailureCode: code, CallAttempted: attempted, RealExternalCallExecuted: attempted}
}

func (p *GenerationProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Owner != p.owner || envelope.Kind != p.kind || attempt.EffectID == "" {
		return generationFailure("generation_dispatch_invalid", false), nil
	}
	config, configErr := p.currentConfig(ctx)
	if configErr != nil {
		return generationFailure("generation_provider_config_unavailable", false), nil
	}
	if !config.Enabled {
		return generationFailure("generation_provider_disabled", false), nil
	}
	policy, err := config.Policy()
	if err != nil {
		return generationFailure("generation_provider_config_invalid", false), nil
	}
	dispatch, found, err := p.dispatch.GenerationDispatch(ctx, attempt.EffectID)
	if err != nil {
		// The model has not been called. A missing frozen input is a final
		// generation failure, rather than a retryable EER state that could later
		// mutate the same customer item after the run has been aggregated.
		return generationFailure("generation_dispatch_unavailable", false), nil
	}
	if !found || dispatch.EffectID != attempt.EffectID || !dispatch.Context.Valid() || !dispatch.ModelPolicy.Valid() || dispatch.ModelPolicy != policy || !effectport.ValidDigest(dispatch.PayloadDigest) || dispatch.PayloadDigest != envelope.PayloadDigest {
		return generationFailure("generation_dispatch_invalid", false), nil
	}
	body, err := json.Marshal(map[string]any{"model": policy.Model, "messages": []map[string]string{{"role": "system", "content": dispatch.RolePrompt}, {"role": "user", "content": generationUserPrompt(dispatch.TaskPrompt, dispatch.Context)}}, "temperature": 0.4})
	if err != nil {
		return generationFailure("generation_request_invalid", false), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeCompletionsURL(config.BaseURL), bytes.NewReader(body))
	if err != nil {
		return generationFailure("generation_request_invalid", false), nil
	}
	request.Header.Set("Authorization", "Bearer "+config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.config.Client.Do(request)
	if err != nil {
		// A timeout or disconnected request can have reached a billable model.
		// Do not retry with a new key; EER leaves it outcome_unknown.
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("automation.ai-agent-generate.outcome-unknown", attempt.EffectID, strconv.Itoa(int(attempt.Number))), FailureCode: "generation_call_unknown", CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("automation.ai-agent-generate.read-unknown", attempt.EffectID, strconv.Itoa(int(attempt.Number))), FailureCode: "generation_response_unknown", CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return generationFailure("generation_http_rejected", true), nil
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(responseBody, &decoded) != nil || len(decoded.Choices) == 0 || !validGeneratedText(decoded.Choices[0].Message.Content, dispatch) {
		return generationFailure("generation_response_invalid", true), nil
	}
	text := strings.TrimSpace(decoded.Choices[0].Message.Content)
	artifact := effectport.ResultArtifact{Kind: generationArtifactKind, Payload: []byte(text)}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("automation.ai-agent-generate.receipt", attempt.EffectID, string(artifact.Digest)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

var _ effectport.ProviderAdapter = (*GenerationProvider)(nil)
var _ automationport.GenerationModelPolicyReader = (*GenerationProvider)(nil)

func (p *GenerationProvider) currentConfig(ctx context.Context) (GenerationConfig, error) {
	if p.ConfigReader == nil {
		return p.config, nil
	}
	config, err := p.ConfigReader(ctx)
	if err != nil {
		return GenerationConfig{}, err
	}
	return config, nil
}

// NewAudienceRecommendationProvider reuses the approved model transport for
// a separate Segment effect; it does not create an Automation send or review.
func NewAudienceRecommendationProvider(config GenerationConfig, dispatch automationport.GenerationDispatchReader) (*GenerationProvider, error) {
	p, e := NewGenerationProvider(config, dispatch)
	if e != nil {
		return nil, e
	}
	p.owner = effectport.OwnerSegment
	p.kind = effectport.KindAIRecommend
	return p, nil
}
