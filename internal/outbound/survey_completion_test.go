package outbound

import (
	"context"
	"crypto/hmac"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type surveyCompletionReaderStub struct{ payload surveyport.CompletionPayload }

func (s surveyCompletionReaderStub) ReadCompletionPayload(_ context.Context, source string) (surveyport.CompletionPayload, error) {
	if source != s.payload.SourceDigest {
		return surveyport.CompletionPayload{}, surveyport.ErrNotFound
	}
	return s.payload, nil
}

type surveyIdentityStub struct{}

func (surveyIdentityStub) VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error) {
	return "union-3", true, nil
}

type surveyCompletionResolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f surveyCompletionResolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func surveyCompletionTLSTarget(t *testing.T, server *httptest.Server, host string) (string, SurveyCompletionNetwork) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://" + host + ":" + parsed.Port()
	return endpoint, SurveyCompletionNetwork{
		Resolver: surveyCompletionResolverFunc(func(_ context.Context, _ string, requested string) ([]netip.Addr, error) {
			if requested != host {
				return nil, errors.New("unexpected test host")
			}
			return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
		}),
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
}

func TestSurveyCompletionProviderPostsSignedPayloadToWhitelistedTarget(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	day, frequency, expiresAtTS := int64(15), int64(3), int64(2147483647)
	payload := surveyport.CompletionPayload{QuestionnaireID: 1, SubmissionID: 2, CustomerID: 3, ExternalUserID: "union-3", ConfigurationReference: "trial-webhook", SourceDigest: string(effectport.Hash("source")), TargetDigest: string(effectport.Hash("target")), PayloadDigest: string(effectport.Hash("payload")), PolicyDigest: string(effectport.Hash("policy")), IdempotencyKey: "survey.completion:2", QuestionnaireTitle: "调研", SubmittedAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC), Answers: []surveyport.CompletionAnswer{{QuestionTitle: "目标", QuestionType: surveyport.QuestionSingleChoice, OptionTexts: []string{"增长"}}, {QuestionTitle: "手机", QuestionType: surveyport.QuestionMobile, TextValue: "13812345678"}}}
	payload.Policy.Day, payload.Policy.Frequency, payload.Policy.ExpiresAtTS = &day, &frequency, &expiresAtTS
	called := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodPost || r.Header.Get("X-AICRM-Idempotency-Key") != payload.IdempotencyKey {
			t.Fatalf("unexpected request method/key: %s/%q", r.Method, r.Header.Get("X-AICRM-Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		signature := strings.TrimPrefix(r.Header.Get("X-AICRM-Signature"), "sha256=")
		if !hmac.Equal([]byte(signature), []byte(surveyCompletionSignature(key, r.Header.Get("X-AICRM-Timestamp"), r.Header.Get("X-AICRM-Event-Id"), body))) {
			t.Fatal("completion signature did not authenticate canonical body")
		}
		if r.Header.Get("X-AICRM-Client-Id") != "survey-v3" || r.Header.Get("X-AICRM-Event-Id") != payload.IdempotencyKey || r.Header.Get("X-AICRM-Timestamp") != strconv.FormatInt(payload.SubmittedAt.Unix(), 10) {
			t.Fatal("missing donor-compatible headers")
		}
		if !strings.Contains(string(body), `"user_id":"union-3"`) || !strings.Contains(string(body), `"phone_number":"13812345678"`) || !strings.Contains(string(body), `"answer":"13812345678"`) || !strings.Contains(string(body), `"day":15`) || !strings.Contains(string(body), `"frequency":3`) || !strings.Contains(string(body), `"expires_at_ts":2147483647`) {
			t.Fatalf("unexpected provider payload %s", body)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	endpoint, network := surveyCompletionTLSTarget(t, server, "example.com")
	target := SurveyCompletionTarget{Reference: "trial-webhook", Endpoint: endpoint, SigningKey: key, ClientID: "survey-v3", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary", Day: &day, Frequency: &frequency, ExpiresAtTS: &expiresAtTS}
	payload.Policy.ConfigurationDigest = target.policyDigest()
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: true, Targets: []SurveyCompletionTarget{target}, Reader: surveyCompletionReaderStub{payload: payload}, Client: server.Client(), Network: network, Identities: surveyIdentityStub{}})
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return payload.SubmittedAt }
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSurveyCompletion, SourceRefDigest: effectport.Digest(payload.SourceDigest), TargetRefDigest: effectport.Digest(payload.TargetDigest), PayloadDigest: effectport.Digest(payload.PayloadDigest), PolicyVersionHash: effectport.Digest(payload.PolicyDigest)}
	result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || called != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, called, err)
	}
}

func TestSurveyCompletionProviderDisabledDoesNotCallTarget(t *testing.T) {
	payload := surveyport.CompletionPayload{QuestionnaireID: 1, SubmissionID: 2, CustomerID: 3, ConfigurationReference: "target", SourceDigest: string(effectport.Hash("source")), TargetDigest: string(effectport.Hash("target")), PayloadDigest: string(effectport.Hash("payload")), PolicyDigest: string(effectport.Hash("policy")), IdempotencyKey: "survey.completion:2"}
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: false, Reader: surveyCompletionReaderStub{payload: payload}, Identities: surveyIdentityStub{}})
	if err != nil {
		t.Fatal(err)
	}
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSurveyCompletion, SourceRefDigest: effectport.Digest(payload.SourceDigest), TargetRefDigest: effectport.Digest(payload.TargetDigest), PayloadDigest: effectport.Digest(payload.PayloadDigest), PolicyVersionHash: effectport.Digest(payload.PolicyDigest)}
	result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSurveyCompletionProviderSyntheticTestUsesFrozenWebhookVector(t *testing.T) {
	const (
		eventID   = "survey.completion.test:questionnaire-test-0123456789abcdef0123456789abcdef"
		body      = `{"answers":[],"is_test":true,"phone_number":"NULL","questionnaire_title":"测试","submitted_at":"2026-09-05T01:02:03Z","test_run_id":"questionnaire-test-0123456789abcdef0123456789abcdef","user_id":"questionnaire_test"}`
		signature = "d9bc15c06d587453bdd08f0fabc2b605525afafe81c9f8e1905e60a0f7f7c8ee"
	)
	fixed := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	payload := surveyport.CompletionPayload{QuestionnaireID: 7, ConfigurationReference: "test-webhook", SourceDigest: string(effectport.Hash("synthetic-source")), TargetDigest: string(effectport.Hash("synthetic-target")), PayloadDigest: string(effectport.Hash("synthetic-payload")), PolicyDigest: string(effectport.Hash("synthetic-policy")), IdempotencyKey: eventID, QuestionnaireTitle: "测试", SubmittedAt: fixed, Answers: []surveyport.CompletionAnswer{}, ExternalUserID: "questionnaire_test", SyntheticTest: true, TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef"}
	key := []byte("01234567890123456789012345678901")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != body || r.Header.Get("X-AICRM-Timestamp") != "1788570123" || r.Header.Get("X-AICRM-Event-Id") != eventID || r.Header.Get("X-AICRM-Signature") != "sha256="+signature || r.Header.Get("X-AICRM-Client-Id") != "survey-v3-test" {
			t.Fatalf("fixed webhook vector mismatch body=%s headers=%v", got, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	endpoint, network := surveyCompletionTLSTarget(t, server, "example.com")
	target := SurveyCompletionTarget{Reference: "test-webhook", Endpoint: endpoint, SigningKey: key, ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
	payload.Policy.ConfigurationDigest = target.policyDigest()
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: true, Targets: []SurveyCompletionTarget{target}, Reader: surveyCompletionReaderStub{payload: payload}, Client: server.Client(), Network: network, Identities: surveyIdentityStub{}})
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return fixed }
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSurveyCompletion, SourceRefDigest: effectport.Digest(payload.SourceDigest), TargetRefDigest: effectport.Digest(payload.TargetDigest), PayloadDigest: effectport.Digest(payload.PayloadDigest), PolicyVersionHash: effectport.Digest(payload.PolicyDigest)}
	result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted {
		t.Fatalf("synthetic vector result=%+v err=%v", result, err)
	}
}

func TestSurveyCompletionProviderDoesNotForwardBodyOnRedirect(t *testing.T) {
	payload := syntheticRedirectPayload()
	redirected := 0
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected++ }))
	defer destination.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/different-target", http.StatusFound)
	}))
	defer origin.Close()
	endpoint, network := surveyCompletionTLSTarget(t, origin, "example.com")
	target := SurveyCompletionTarget{Reference: "redirect-test", Endpoint: endpoint, SigningKey: []byte(strings.Repeat("r", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
	payload.ConfigurationReference, payload.Policy.ConfigurationDigest = target.Reference, target.policyDigest()
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: true, Targets: []SurveyCompletionTarget{target}, Reader: surveyCompletionReaderStub{payload: payload}, Client: origin.Client(), Network: network, Identities: surveyIdentityStub{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), completionEnvelope(payload), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || !result.CallAttempted || redirected != 0 {
		t.Fatalf("redirect result=%+v err=%v destination_calls=%d", result, err, redirected)
	}
}

func TestSurveyCompletionProviderRejectsTargetConfigDriftBeforeCallingNewEndpoint(t *testing.T) {
	payload := syntheticRedirectPayload()
	original := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("old target must not be called after provider config mutation")
	}))
	defer original.Close()
	newCalls := 0
	changed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { newCalls++ }))
	defer changed.Close()
	originalEndpoint, _ := surveyCompletionTLSTarget(t, original, "example.com")
	changedEndpoint, _ := surveyCompletionTLSTarget(t, changed, "example.com")
	frozen := SurveyCompletionTarget{Reference: "frozen-target", Endpoint: originalEndpoint, SigningKey: []byte(strings.Repeat("f", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
	payload.ConfigurationReference, payload.Policy.ConfigurationDigest = frozen.Reference, frozen.policyDigest()
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: true, Targets: []SurveyCompletionTarget{frozen}, Reader: surveyCompletionReaderStub{payload: payload}, Identities: surveyIdentityStub{}})
	if err != nil {
		t.Fatal(err)
	}
	provider.targets.(*StaticSurveyCompletionTargets).targets[frozen.Reference] = SurveyCompletionTarget{Reference: frozen.Reference, Endpoint: changedEndpoint, SigningKey: frozen.SigningKey, ClientID: frozen.ClientID, Version: "v2", IdentityKind: frozen.IdentityKind, IdentityScope: frozen.IdentityScope}
	result, err := provider.Execute(context.Background(), completionEnvelope(payload), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || newCalls != 0 {
		t.Fatalf("drift result=%+v err=%v new_calls=%d", result, err, newCalls)
	}
}

func syntheticRedirectPayload() surveyport.CompletionPayload {
	return surveyport.CompletionPayload{QuestionnaireID: 7, SourceDigest: string(effectport.Hash("redirect-source")), TargetDigest: string(effectport.Hash("redirect-target")), PayloadDigest: string(effectport.Hash("redirect-payload")), PolicyDigest: string(effectport.Hash("redirect-policy")), IdempotencyKey: "survey.completion.test:questionnaire-test-abcdefabcdefabcdefabcdefabcdefab", QuestionnaireTitle: "测试", SubmittedAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC), Answers: []surveyport.CompletionAnswer{}, ExternalUserID: "questionnaire_test", SyntheticTest: true, TestRunID: "questionnaire-test-abcdefabcdefabcdefabcdefabcdefab"}
}

func completionEnvelope(payload surveyport.CompletionPayload) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSurveyCompletion, SourceRefDigest: effectport.Digest(payload.SourceDigest), TargetRefDigest: effectport.Digest(payload.TargetDigest), PayloadDigest: effectport.Digest(payload.PayloadDigest), PolicyVersionHash: effectport.Digest(payload.PolicyDigest)}
}

type completionProjectionCall struct {
	state                   string
	callAttempted, realCall bool
	resultReceived          *bool
	attempt                 int32
}
type completionProjectionStub struct{ calls []completionProjectionCall }

func (s *completionProjectionStub) CompleteCompletionEffect(_ context.Context, _ string, state string, callAttempted, realCall bool, resultReceived *bool, _ string, attempt int32, _ time.Time) error {
	s.calls = append(s.calls, completionProjectionCall{state: state, callAttempted: callAttempted, realCall: realCall, resultReceived: resultReceived, attempt: attempt})
	return nil
}

func TestSurveyCompletionProviderTimeoutIsOutcomeUnknownAfterCall(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	payload := syntheticRedirectPayload()
	endpoint, network := surveyCompletionTLSTarget(t, server, "example.com")
	target := SurveyCompletionTarget{Reference: "timeout-target", Endpoint: endpoint, SigningKey: []byte(strings.Repeat("t", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
	payload.ConfigurationReference, payload.Policy.ConfigurationDigest = target.Reference, target.policyDigest()
	client := server.Client()
	client.Timeout = 20 * time.Millisecond
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{Enabled: true, Targets: []SurveyCompletionTarget{target}, Reader: surveyCompletionReaderStub{payload: payload}, Identities: surveyIdentityStub{}, Client: client, Network: network})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), completionEnvelope(payload), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err == nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("timeout result=%+v err=%v", result, err)
	}
}

func TestSurveyCompletionProviderRejectsNonPublicTargetsBeforeExecution(t *testing.T) {
	for _, endpoint := range []string{
		"http://public.example.test/complete",
		"https://localhost/complete",
		"https://receiver.localhost/complete",
		"https://receiver.local/complete",
		"https://127.0.0.1/complete",
		"https://10.0.0.8/complete",
		"https://169.254.169.254/complete",
		"https://224.0.0.1/complete",
		"https://0.0.0.0/complete",
		"https://[::1]/complete",
		"https://[fc00::1]/complete",
		"https://user:secret@public.example.test/complete",
		"https://2130706433/complete",
		"https://127.1/complete",
		"https://127.1.1/complete",
		"https://0177.0.0.1/complete",
		"https://0x7f000001/complete",
		"https://127.0x0.1/complete",
		"https://0300.0250.0001.0001/complete",
		"https://１２７.０.０.１/complete",
		"https://127。0。0。1/complete",
		"https://０x７ｆ０００００１/complete",
		"https://１２７.０x０.１/complete",
	} {
		t.Run(endpoint, func(t *testing.T) {
			target := SurveyCompletionTarget{Reference: "unsafe-target", Endpoint: endpoint, SigningKey: []byte(strings.Repeat("s", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
			if validSurveyCompletionTarget(target) {
				t.Fatalf("unsafe completion target accepted: %q", endpoint)
			}
		})
	}
}

func TestSurveyCompletionProviderRejectsDNSRebindingBeforeDial(t *testing.T) {
	payload := syntheticRedirectPayload()
	target := SurveyCompletionTarget{Reference: "rebind-target", Endpoint: "https://receiver.example.test/complete", SigningKey: []byte(strings.Repeat("d", 32)), ClientID: "survey-v3-test", Version: "v1", IdentityKind: identitydomain.KindUnionID, IdentityScope: "wechat-open-platform:primary"}
	payload.ConfigurationReference, payload.Policy.ConfigurationDigest = target.Reference, target.policyDigest()
	dialed := false
	provider, err := NewSurveyCompletionProvider(SurveyCompletionProviderConfig{
		Enabled: true, Targets: []SurveyCompletionTarget{target}, Reader: surveyCompletionReaderStub{payload: payload}, Identities: surveyIdentityStub{},
		Network: SurveyCompletionNetwork{
			Resolver: surveyCompletionResolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("203.0.113.17"), netip.MustParseAddr("127.0.0.1")}, nil
			}),
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("must not dial")
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), completionEnvelope(payload), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted || dialed {
		t.Fatalf("rebind result=%+v err=%v dialed=%t", result, err, dialed)
	}
}

func TestSurveyCompletionSinkRecordsCallFactsWithoutInferringProviderResult(t *testing.T) {
	for _, tc := range []struct {
		name         string
		result       effectport.AdapterResult
		wantReceived *bool
	}{
		{"timeout", effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("timeout"), CallAttempted: true, RealExternalCallExecuted: true}, boolPointerForSurvey(false)},
		{"pre_call_rejected", effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("pre-call")}, boolPointerForSurvey(false)},
		{"reconciled", effectport.AdapterResult{Completion: effectport.StateReconciled, ReceiptDigest: effectport.Hash("reconciled"), CallAttempted: true, RealExternalCallExecuted: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projector := &completionProjectionStub{}
			sink, err := NewSurveyCompletionSink(projector)
			if err != nil {
				t.Fatal(err)
			}
			err = sink.CompleteEffect(context.Background(), "eer_7", effectport.Envelope{Kind: effectport.KindSurveyCompletion}, effectport.Attempt{Number: 1}, tc.result)
			if err != nil || len(projector.calls) != 1 {
				t.Fatalf("calls=%+v err=%v", projector.calls, err)
			}
			got := projector.calls[0]
			if got.callAttempted != tc.result.CallAttempted || got.realCall != tc.result.RealExternalCallExecuted || got.attempt != 1 || (got.resultReceived == nil) != (tc.wantReceived == nil) || got.resultReceived != nil && *got.resultReceived != *tc.wantReceived {
				t.Fatalf("projection=%+v want=%+v", got, tc.wantReceived)
			}
		})
	}
}

func boolPointerForSurvey(value bool) *bool { return &value }
