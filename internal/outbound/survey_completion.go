package outbound

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// SurveyCompletionTarget is composition-owned configuration. Reference is a
// whitelist key stored by Survey; endpoint and signing material never enter
// Survey, External Effects, or logs.
type SurveyCompletionTarget struct {
	Reference                   string
	Endpoint                    string
	SigningKey                  []byte
	ClientID                    string
	Version                     string
	IdentityKind                identitydomain.Kind
	IdentityScope               string
	Day, Frequency, ExpiresAtTS *int64
	PushType, Remark            string
	CustomParams                map[string]string
}

type SurveyCompletionProviderConfig struct {
	Enabled  bool
	Targets  []SurveyCompletionTarget
	Resolver SurveyCompletionTargetResolver
	Reader   surveyport.CompletionPayloadReader
	Client   *http.Client
	// Network is a test-composition seam. Production always validates the
	// resolved public address and never permits a loopback target.
	Network    SurveyCompletionNetwork
	Identities identityport.ExternalIdentityValueReader
}

type SurveyCompletionHostResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// SurveyCompletionNetwork supplies controlled TLS fixture routing only. It
// does not weaken endpoint parsing or resolved-IP validation.
type SurveyCompletionNetwork struct {
	Resolver    SurveyCompletionHostResolver
	DialContext func(context.Context, string, string) (net.Conn, error)
}

type SurveyCompletionProvider struct {
	enabled    bool
	targets    SurveyCompletionTargetResolver
	reader     surveyport.CompletionPayloadReader
	client     *http.Client
	now        func() time.Time
	identities identityport.ExternalIdentityValueReader
}

type SurveyCompletionTargetResolver interface {
	SurveyCompletionTarget(context.Context, string) (SurveyCompletionTarget, bool, error)
	SurveyCompletionTargetReferences(context.Context) ([]string, error)
}

type StaticSurveyCompletionTargets struct {
	targets map[string]SurveyCompletionTarget
}

func NewStaticSurveyCompletionTargets(values []SurveyCompletionTarget) (*StaticSurveyCompletionTargets, error) {
	targets := make(map[string]SurveyCompletionTarget, len(values))
	for _, target := range values {
		if !validSurveyCompletionTarget(target) {
			return nil, errors.New("invalid survey completion target")
		}
		if _, exists := targets[target.Reference]; exists {
			return nil, errors.New("duplicate survey completion target")
		}
		target.SigningKey = append([]byte(nil), target.SigningKey...)
		targets[target.Reference] = target
	}
	return &StaticSurveyCompletionTargets{targets: targets}, nil
}
func (s *StaticSurveyCompletionTargets) SurveyCompletionTarget(_ context.Context, reference string) (SurveyCompletionTarget, bool, error) {
	target, found := s.targets[reference]
	target.SigningKey = append([]byte(nil), target.SigningKey...)
	return target, found, nil
}
func (s *StaticSurveyCompletionTargets) SurveyCompletionTargetReferences(context.Context) ([]string, error) {
	references := make([]string, 0, len(s.targets))
	for reference := range s.targets {
		references = append(references, reference)
	}
	sort.Strings(references)
	return references, nil
}

func NewSurveyCompletionProvider(c SurveyCompletionProviderConfig) (*SurveyCompletionProvider, error) {
	if c.Reader == nil {
		return nil, errors.New("survey completion payload reader is required")
	}
	targets := c.Resolver
	if targets == nil {
		var err error
		targets, err = NewStaticSurveyCompletionTargets(c.Targets)
		if err != nil {
			return nil, err
		}
	}
	client := lockedSurveyCompletionHTTPClient(c.Client, c.Network)
	if c.Enabled && c.Identities == nil {
		return nil, errors.New("survey completion identity reader is required")
	}
	return &SurveyCompletionProvider{enabled: c.Enabled, targets: targets, reader: c.Reader, client: client, identities: c.Identities, now: time.Now}, nil
}

func (p *SurveyCompletionProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	base := effectport.Hash("survey.completion.provider.v1", string(envelope.Fingerprint()))
	if p == nil || p.reader == nil || envelope.Kind != effectport.KindSurveyCompletion || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "invalid")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-disabled")}, nil
	}
	payload, err := p.reader.ReadCompletionPayload(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		// The dispatch payload is read from Survey's PostgreSQL transactionally
		// recorded receipt. A transient read outage must not turn an already
		// accepted completion into a permanent provider failure.
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "payload-unavailable")}, errors.New("survey completion payload unavailable")
	}
	if !matchesSurveyCompletionEnvelope(payload, envelope) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-unavailable")}, nil
	}
	target, found, targetErr := p.targets.SurveyCompletionTarget(ctx, payload.ConfigurationReference)
	if targetErr != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, errors.New("survey completion target unavailable")
	}
	if !found || target.policyDigest() != payload.Policy.ConfigurationDigest {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, nil
	}
	if payload.ExternalUserID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "identity-missing")}, nil
	}
	body, err := marshalSurveyCompletion(payload, payload.ExternalUserID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-invalid")}, nil
	}
	timestamp := strconv.FormatInt(p.now().UTC().Unix(), 10)
	eventID := payload.IdempotencyKey
	signature := surveyCompletionSignature(target.SigningKey, timestamp, eventID, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Endpoint, bytes.NewReader(body))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "request-invalid")}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AICRM-Client-Id", target.ClientID)
	req.Header.Set("X-AICRM-Timestamp", timestamp)
	req.Header.Set("X-AICRM-Event-Id", eventID)
	req.Header.Set("X-AICRM-Signature", "sha256="+signature)
	req.Header.Set("X-AICRM-Idempotency-Key", payload.IdempotencyKey)
	response, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, errSurveyCompletionTargetDisallowed) {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "target-disallowed")}, nil
		}
		if errors.Is(err, errSurveyCompletionTargetResolutionUnavailable) {
			return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, errors.New("survey completion target unavailable")
		}
		// Once Do is entered, request delivery is ambiguous. Reuse this exact
		// key through EER reconciliation; never mint a second Provider write.
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "request-unknown"), CallAttempted: true, RealExternalCallExecuted: true}, errors.New("survey completion request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 500 {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "provider-retryable", strconv.Itoa(response.StatusCode)), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected", strconv.Itoa(response.StatusCode)), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", response.Status, string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

var (
	errSurveyCompletionTargetDisallowed            = errors.New("survey completion target disallowed")
	errSurveyCompletionTargetResolutionUnavailable = errors.New("survey completion target resolution unavailable")
)

func lockedSurveyCompletionHTTPClient(source *http.Client, network SurveyCompletionNetwork) *http.Client {
	if source == nil {
		source = &http.Client{Timeout: 10 * time.Second}
	}
	client := *source
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if configured, ok := source.Transport.(*http.Transport); ok && configured != nil {
		transport = configured.Clone()
	}
	resolver := network.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dial := network.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	transport.Proxy = nil
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.DialContext = guardedSurveyCompletionDialer(resolver, dial)
	client.Transport = transport
	// Completion bodies and their HMAC are authorized for exactly one
	// configured target. Never follow a provider redirect to a different URL.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func guardedSurveyCompletionDialer(resolver SurveyCompletionHostResolver, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !validSurveyCompletionPort(port) || !validSurveyCompletionHost(host) {
			return nil, errSurveyCompletionTargetDisallowed
		}
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, errSurveyCompletionTargetResolutionUnavailable
		}
		for _, value := range addresses {
			if surveyport.DisallowedPublicIP(value) {
				return nil, errSurveyCompletionTargetDisallowed
			}
		}
		return dial(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
}

func validSurveyCompletionTarget(target SurveyCompletionTarget) bool {
	if target.Reference == "" || strings.TrimSpace(target.Reference) != target.Reference || len(target.Reference) > 128 || target.Version == "" || len(target.Version) > 128 || target.ClientID == "" || len(target.ClientID) > 256 || len(target.SigningKey) < 32 || identitydomain.ValidateNamespace(target.IdentityKind, target.IdentityScope) != nil {
		return false
	}
	return validSurveyCompletionEndpoint(target.Endpoint)
}

func validSurveyCompletionEndpoint(raw string) bool {
	if raw == "" || len(raw) > 4096 || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\\\\\r\n\t") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || !validSurveyCompletionHost(parsed.Hostname()) {
		return false
	}
	return parsed.Port() == "" || validSurveyCompletionPort(parsed.Port())
}

func validSurveyCompletionHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return !surveyport.DisallowedPublicIP(ip)
	}
	return surveyport.ValidPublicCompletionHost(host)
}

func validSurveyCompletionPort(raw string) bool {
	value, err := strconv.Atoi(raw)
	return err == nil && value >= 1 && value <= 65535
}

func (target SurveyCompletionTarget) policyDigest() string {
	meta, _ := json.Marshal(struct {
		Version                     string              `json:"version"`
		Kind                        identitydomain.Kind `json:"kind"`
		Scope                       string              `json:"scope"`
		Day, Frequency, ExpiresAtTS *int64
		PushType, Remark            string
		CustomParams                map[string]string
	}{target.Version, target.IdentityKind, target.IdentityScope, target.Day, target.Frequency, target.ExpiresAtTS, target.PushType, target.Remark, target.CustomParams})
	return string(effectport.Hash("survey.completion.config.v1", target.Reference, target.Endpoint, string(meta)))
}

func (p *SurveyCompletionProvider) CompletionPolicy(ctx context.Context, reference string) (surveyport.CompletionPolicy, bool, error) {
	if p == nil {
		return surveyport.CompletionPolicy{}, false, nil
	}
	target, found, err := p.targets.SurveyCompletionTarget(ctx, reference)
	if err != nil {
		return surveyport.CompletionPolicy{}, false, err
	}
	if !found {
		return surveyport.CompletionPolicy{}, false, nil
	}
	return surveyport.CompletionPolicy{ConfigurationReference: target.Reference, ConfigurationVersion: target.Version, ConfigurationDigest: target.policyDigest(), IdentityKind: target.IdentityKind, IdentityScope: target.IdentityScope, Day: target.Day, Frequency: target.Frequency, ExpiresAtTS: target.ExpiresAtTS, PushType: target.PushType, Remark: target.Remark, CustomParams: target.CustomParams}, true, nil
}

func (p *SurveyCompletionProvider) CompletionTargetReferences(ctx context.Context) ([]string, error) {
	if p == nil {
		return []string{}, nil
	}
	return p.targets.SurveyCompletionTargetReferences(ctx)
}

func (p *SurveyCompletionProvider) SnapshotCompletionIdentity(ctx context.Context, customerID int64, policy surveyport.CompletionPolicy) (string, bool, error) {
	if p == nil || p.identities == nil || customerID < 1 || policy.IdentityKind == "" || policy.IdentityScope == "" {
		return "", false, nil
	}
	return p.identities.VerifiedExternalIdentityValue(ctx, customerdomain.CustomerID(customerID), policy.IdentityKind, policy.IdentityScope)
}

func matchesSurveyCompletionEnvelope(payload surveyport.CompletionPayload, envelope effectport.Envelope) bool {
	isProtectedSubmission := !payload.SyntheticTest && payload.SubmissionID > 0 && payload.CustomerID > 0
	isSyntheticTest := payload.SyntheticTest && payload.SubmissionID == 0 && payload.CustomerID == 0 && payload.TestRunID != "" && payload.ExternalUserID == "questionnaire_test" && len(payload.Answers) == 0
	return payload.QuestionnaireID > 0 && (isProtectedSubmission || isSyntheticTest) && payload.ConfigurationReference != "" &&
		payload.SourceDigest == string(envelope.SourceRefDigest) && payload.TargetDigest == string(envelope.TargetRefDigest) &&
		payload.PayloadDigest == string(envelope.PayloadDigest) && payload.PolicyDigest == string(envelope.PolicyVersionHash) && payload.IdempotencyKey != ""
}

func marshalSurveyCompletion(payload surveyport.CompletionPayload, userID string) ([]byte, error) {
	type answer struct {
		Title  string `json:"title"`
		Answer any    `json:"answer"`
	}
	out := struct {
		UserID             string   `json:"user_id"`
		QuestionnaireTitle string   `json:"questionnaire_title"`
		SubmittedAt        string   `json:"submitted_at"`
		PhoneNumber        string   `json:"phone_number"`
		Answers            []answer `json:"answers"`
	}{UserID: userID, QuestionnaireTitle: payload.QuestionnaireTitle, SubmittedAt: payload.SubmittedAt.UTC().Format(time.RFC3339), PhoneNumber: "NULL", Answers: make([]answer, 0, len(payload.Answers))}
	for _, item := range payload.Answers {
		var value any
		switch item.QuestionType {
		case surveyport.QuestionSingleChoice:
			if len(item.OptionTexts) > 0 {
				value = item.OptionTexts[0]
			} else {
				value = ""
			}
		case surveyport.QuestionMultiChoice:
			value = item.OptionTexts
		case surveyport.QuestionTextarea, surveyport.QuestionMobile:
			value = item.TextValue
			if item.QuestionType == surveyport.QuestionMobile && item.TextValue != "" {
				out.PhoneNumber = item.TextValue
			}
		default:
			return nil, errors.New("unsupported survey answer type")
		}
		out.Answers = append(out.Answers, answer{Title: item.QuestionTitle, Answer: value})
	}
	base, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var full map[string]any
	if err = json.Unmarshal(base, &full); err != nil {
		return nil, err
	}
	if payload.Policy.Day != nil {
		full["day"] = *payload.Policy.Day
	}
	if payload.Policy.Frequency != nil {
		full["frequency"] = *payload.Policy.Frequency
	}
	if payload.Policy.ExpiresAtTS != nil {
		full["expires_at_ts"] = *payload.Policy.ExpiresAtTS
	}
	if payload.Policy.PushType != "" {
		full["type"] = payload.Policy.PushType
	}
	if payload.Policy.Remark != "" {
		full["remark"] = payload.Policy.Remark
	}
	if len(payload.AssessmentResult) > 2 {
		var result any
		if json.Unmarshal(payload.AssessmentResult, &result) == nil {
			if object, ok := result.(map[string]any); ok && len(object) > 0 {
				full["assessment_result_snapshot"] = object
			}
		}
	}
	for key, value := range payload.Policy.CustomParams {
		if !reservedSurveyPayloadField(key) && (!payload.SyntheticTest || safeSyntheticSurveyPayloadField(key)) {
			full[key] = value
		}
	}
	if payload.SyntheticTest {
		full["is_test"] = true
		full["test_run_id"] = payload.TestRunID
	}
	return json.Marshal(full)
}

func surveyCompletionSignature(key []byte, timestamp, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(eventID))
	mac.Write([]byte{'\n'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SurveyCompletionSink projects terminal effect states into the Survey-owned
// receipt in the same EER completion transaction.
type SurveyCompletionSink struct {
	projector surveyport.CompletionEffectProjector
}

func NewSurveyCompletionSink(projector surveyport.CompletionEffectProjector) (*SurveyCompletionSink, error) {
	if projector == nil {
		return nil, errors.New("survey completion projector is required")
	}
	return &SurveyCompletionSink{projector: projector}, nil
}

func (s *SurveyCompletionSink) CompleteEffect(ctx context.Context, effectID string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.projector == nil || effectID == "" || envelope.Kind != effectport.KindSurveyCompletion || !effectport.ValidDigest(result.ReceiptDigest) || attempt.Number < 1 {
		return errors.New("invalid survey completion result")
	}
	// EER provides the call facts separately. A returned provider response is
	// only known when the adapter entered the call and reached a non-unknown
	// terminal result; reconciliation is local evidence and remains unknown.
	var resultReceived *bool
	if result.Completion != effectport.StateReconciled {
		known := result.CallAttempted && result.Completion != effectport.StateUnknown
		resultReceived = &known
	}
	return s.projector.CompleteCompletionEffect(ctx, effectID, string(result.Completion), result.CallAttempted, result.RealExternalCallExecuted, resultReceived, string(result.ReceiptDigest), attempt.Number, time.Now().UTC())
}

var _ effectport.ProviderAdapter = (*SurveyCompletionProvider)(nil)
var _ effectport.CompletionSink = (*SurveyCompletionSink)(nil)
var _ surveyport.CompletionPolicyResolver = (*SurveyCompletionProvider)(nil)
var _ surveyport.CompletionTargetCatalog = (*SurveyCompletionProvider)(nil)
var _ surveyport.CompletionIdentitySnapshotter = (*SurveyCompletionProvider)(nil)

func reservedSurveyPayloadField(key string) bool {
	switch key {
	case "user_id", "questionnaire_title", "submitted_at", "answers", "phone_number", "type", "expires_at_ts", "day", "frequency", "remark", "assessment_result_snapshot":
		return true
	}
	return key == "" || len(key) > 128
}

func safeSyntheticSurveyPayloadField(key string) bool {
	normalized := strings.ToLower(key)
	for _, fragment := range []string{"phone", "mobile", "openid", "unionid", "external_user", "respondent", "identity", "customer"} {
		if strings.Contains(normalized, fragment) {
			return false
		}
	}
	return true
}
