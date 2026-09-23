package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type generationDispatchStub struct {
	dispatch automationport.GenerationDispatch
	found    bool
	err      error
}

func (s generationDispatchStub) GenerationDispatch(context.Context, string) (automationport.GenerationDispatch, bool, error) {
	return s.dispatch, s.found, s.err
}

func testDispatch() automationport.GenerationDispatch {
	return automationport.GenerationDispatch{ItemID: 1, RunID: 2, EffectID: "eer_1", AgentCode: "agent", RolePrompt: "你是顾问", TaskPrompt: "写一句问候", Context: automationport.GenerationContext{Questionnaire: "已填写", RecentChats: "最近聊天", Tags: "新用户", Activation: "已激活"}, ModelPolicy: automationport.GenerationModelPolicy{Mode: "enabled", Endpoint: "https://model.example.test/chat/completions", Model: "unit-model", Temperature: 0.4}, PayloadDigest: effectport.Hash("payload"), AcceptedAt: time.Now().UTC()}
}

func testEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerAutomation, Kind: effectport.KindAIAgentGenerate, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy")}
}

func TestGenerationProviderUsesNormalizedOpenAICompatibleRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request path=%q method=%s auth=%q", r.URL.Path, r.Method, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		for _, value := range []string{`"model":"unit-model"`, `"temperature":0.4`, "你是顾问", "写一句问候", "已填写", "最近聊天", "新用户", "已激活"} {
			if !strings.Contains(string(body), value) {
				t.Fatalf("missing %q body=%s", value, body)
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"你好，欢迎回来"}}]}`))
	}))
	defer server.Close()
	dispatch := testDispatch()
	dispatch.ModelPolicy.Endpoint = server.URL + "/chat/completions"
	provider, err := NewGenerationProvider(GenerationConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", Model: "unit-model", Timeout: time.Second}, generationDispatchStub{dispatch: dispatch, found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || !result.Artifact.Valid() || string(result.Artifact.Payload) != "你好，欢迎回来" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGenerationProviderFailsClosedBeforeDisabledCall(t *testing.T) {
	provider, err := NewGenerationProvider(GenerationConfig{}, generationDispatchStub{dispatch: testDispatch(), found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || result.FailureCode != "generation_provider_disabled" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGenerationProviderMakesTimeoutOutcomeUnknown(t *testing.T) {
	provider, err := NewGenerationProvider(GenerationConfig{Enabled: true, BaseURL: "https://model.example.test", APIKey: "secret", Model: "unit-model", Timeout: time.Second, Client: &http.Client{Transport: failingTransport{}}}, generationDispatchStub{dispatch: testDispatch(), found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.RealExternalCallExecuted || result.FailureCode != "generation_call_unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("disconnected")
}

func TestGenerationProviderRejectsPromptReflection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// This is the frozen prompt structure, not customer-facing copy.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"【问卷】\n已填写\n【最近20条聊天】\n最近聊天"}}]}`))
	}))
	defer server.Close()
	dispatch := testDispatch()
	dispatch.ModelPolicy.Endpoint = server.URL + "/chat/completions"
	provider, err := NewGenerationProvider(GenerationConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", Model: "unit-model", Timeout: time.Second}, generationDispatchStub{dispatch: dispatch, found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || !result.CallAttempted || result.FailureCode != "generation_response_invalid" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGenerationProviderRejectsChangedEndpointAndPayloadWithoutCallingModel(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	provider, err := NewGenerationProvider(GenerationConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", Model: "unit-model", Timeout: time.Second}, generationDispatchStub{dispatch: testDispatch(), found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.FailureCode != "generation_dispatch_invalid" || result.CallAttempted || called {
		t.Fatalf("changed endpoint result=%+v err=%v called=%t", result, err, called)
	}
	dispatch := testDispatch()
	dispatch.ModelPolicy.Endpoint = server.URL + "/chat/completions"
	dispatch.PayloadDigest = effectport.Hash("different-payload")
	provider, err = NewGenerationProvider(GenerationConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", Model: "unit-model", Timeout: time.Second}, generationDispatchStub{dispatch: dispatch, found: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err = provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.FailureCode != "generation_dispatch_invalid" || result.CallAttempted || called {
		t.Fatalf("payload mismatch result=%+v err=%v called=%t", result, err, called)
	}
}

func TestGenerationProviderReadsStoredSelectionAndRejectsFrozenPolicyDrift(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer configured-key" {
			t.Error("configured key not used")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"新回复"}}]}`))
	}))
	defer server.Close()
	dispatch := testDispatch()
	dispatch.ModelPolicy.Endpoint = server.URL + "/chat/completions"
	provider, e := NewGenerationProvider(GenerationConfig{Timeout: time.Second}, generationDispatchStub{dispatch: dispatch, found: true})
	if e != nil {
		t.Fatal(e)
	}
	selected := GenerationConfig{Enabled: true, BaseURL: server.URL, Model: "unit-model", APIKey: "configured-key", Timeout: time.Second}
	provider.ConfigReader = func(context.Context) (GenerationConfig, error) { return selected, nil }
	result, e := provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 1})
	if e != nil || result.Completion != effectport.StateExecuted || calls != 1 {
		t.Fatalf("stored selection result=%+v err=%v", result, e)
	}
	selected.Model = "changed-model"
	result, e = provider.Execute(context.Background(), testEnvelope(), effectport.Attempt{EffectID: "eer_1", Number: 2})
	if e != nil || result.CallAttempted || calls != 1 {
		t.Fatal("frozen task silently switched models")
	}
}

func TestAudienceRecommendationUsesStoredModelAndSeparateEffect(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer configured-key" {
			t.Error("stored credential not used")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"product_id\":1,\"reason\":\"匹配\",\"evidence\":\"问卷\"}"}}]}`))
	}))
	defer server.Close()
	dispatch := testDispatch()
	dispatch.ModelPolicy.Endpoint = server.URL + "/chat/completions"
	p, err := NewAudienceRecommendationProvider(GenerationConfig{Timeout: time.Second}, generationDispatchStub{dispatch: dispatch, found: true})
	if err != nil {
		t.Fatal(err)
	}
	selected := GenerationConfig{Enabled: true, BaseURL: server.URL, Model: "unit-model", APIKey: "configured-key", Timeout: time.Second}
	p.ConfigReader = func(context.Context) (GenerationConfig, error) { return selected, nil }
	policy, err := p.GenerationModelPolicy(context.Background())
	if err != nil || policy != dispatch.ModelPolicy {
		t.Fatal("stored policy unavailable", err)
	}
	envelope := testEnvelope()
	attempt := effectport.Attempt{EffectID: "eer_1", Number: 1}
	result, err := p.Execute(context.Background(), envelope, attempt)
	if err != nil || result.CallAttempted || calls != 0 {
		t.Fatal("accepted Automation effect")
	}
	envelope.Owner, envelope.Kind = effectport.OwnerSegment, effectport.KindAIRecommend
	result, err = p.Execute(context.Background(), envelope, attempt)
	if err != nil || result.Completion != effectport.StateExecuted || calls != 1 {
		t.Fatalf("recommendation=%+v err=%v", result, err)
	}
	selected.Model = "changed-model"
	result, err = p.Execute(context.Background(), envelope, attempt)
	if err != nil || result.CallAttempted || calls != 1 {
		t.Fatal("frozen recommendation silently switched model")
	}
}
