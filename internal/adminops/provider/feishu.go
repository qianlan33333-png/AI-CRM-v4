// Package provider owns the bounded Feishu operations-report adapter.
package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type ReportReader interface {
	LoadOpsReportPayload(context.Context, effectport.Envelope) (opsport.OpsReportPayload, error)
}
type FeishuConfig struct {
	Enabled                              bool
	TargetRef, WebhookURL, SigningSecret string
}
type Feishu struct {
	config FeishuConfig
	reader ReportReader
	client *http.Client
	now    func() time.Time
}

func NewFeishu(config FeishuConfig, reader ReportReader) (*Feishu, error) {
	if reader == nil {
		return nil, errors.New("ops report reader unavailable")
	}
	if config.Enabled && (!validEndpoint(config.WebhookURL) || config.TargetRef == "") {
		return nil, errors.New("ops notification configuration invalid")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = guardedDial(net.DefaultResolver, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Feishu{config: config, reader: reader, client: client, now: time.Now}, nil
}

func validEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || !allowedHost(u.Host) {
		return false
	}
	token := strings.TrimPrefix(u.Path, "/open-apis/bot/v2/hook/")
	if token == u.Path || len(token) < 16 || len(token) > 128 || u.RawPath != "" {
		return false
	}
	for _, r := range token {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
func allowedHost(host string) bool { return host == "open.feishu.cn" || host == "open.larksuite.com" }

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// This private sentinel is created only by our dial guard before a connection
// exists. Generic network errors remain unknown because a write may have left.
var errDNSBeforeConnect = errors.New("ops notification DNS unavailable before connection")

func guardedDial(r resolver, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || !allowedHost(host) {
			return nil, errors.New("ops notification destination rejected")
		}
		ips, err := r.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errDNSBeforeConnect
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("0.0.0.0/8").Contains(ip) {
				return nil, errors.New("ops notification destination rejected")
			}
		}
		return dial(ctx, network, net.JoinHostPort(ips[0].String(), "443"))
	}
}

func (p *Feishu) Execute(ctx context.Context, e effectport.Envelope, a effectport.Attempt) (effectport.AdapterResult, error) {
	result := func(state effectport.State, code string, attempted, executed bool) effectport.AdapterResult {
		return effectport.AdapterResult{Completion: state, FailureCode: code, CallAttempted: attempted, RealExternalCallExecuted: executed, ReceiptDigest: effectport.Hash("ops-feishu-receipt-v1", a.EffectID, strconv.Itoa(int(a.Number)), string(state), code)}
	}
	if e.Owner != effectport.OwnerAdminOps || e.Kind != effectport.KindFeishuOpsNotification || !e.Valid() {
		return result(effectport.StateFinalFailed, "invalid_envelope", false, false), nil
	}
	if !p.config.Enabled {
		return result(effectport.StateFinalFailed, "provider_disabled", false, false), nil
	}
	payload, err := p.reader.LoadOpsReportPayload(ctx, e)
	if errors.Is(err, opsport.ErrOpsReportPayloadExpired) {
		return result(effectport.StateFinalFailed, "payload_expired", false, false), nil
	}
	if err != nil {
		return result(effectport.StateRetryable, "payload_unavailable", false, false), errors.New("ops report payload unavailable")
	}
	source := effectport.Hash("ops-report-source-v1", payload.HourKey.UTC().Format(time.RFC3339))
	kind := payload.NotificationKind
	if kind == "" {
		kind = "hourly"
	}
	if kind == "critical" || kind == "recovery" {
		source = effectport.Hash("ops-event-source-v1", kind, payload.EventKey)
	} else if kind != "hourly" {
		return result(effectport.StateFinalFailed, "invalid_notification_kind", false, false), nil
	}
	policy := effectport.Hash("ops-report-policy-v1", kind+"-v1")
	if payload.TargetRef != p.config.TargetRef || payload.PayloadDigest != e.PayloadDigest || effectport.Hash("ops-report-payload-v1", string(payload.Content)) != e.PayloadDigest || effectport.Hash("ops-report-target-v1", payload.TargetRef) != e.TargetRefDigest || source != e.SourceRefDigest || policy != e.PolicyVersionHash || (payload.SourceDigest != "" && payload.SourceDigest != e.SourceRefDigest) || (payload.PolicyDigest != "" && payload.PolicyDigest != e.PolicyVersionHash) {
		return result(effectport.StateFinalFailed, "payload_mismatch", false, false), nil
	}

	var body map[string]json.RawMessage
	if len(payload.Content) > 24000 || json.Unmarshal(payload.Content, &body) != nil || len(body) == 0 {
		return result(effectport.StateFinalFailed, "invalid_content", false, false), nil
	}
	if p.config.SigningSecret != "" {
		timestamp := strconv.FormatInt(p.now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+p.config.SigningSecret))
		body["timestamp"], _ = json.Marshal(timestamp)
		body["sign"], _ = json.Marshal(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return result(effectport.StateFinalFailed, "invalid_content", false, false), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.WebhookURL, bytes.NewReader(raw))
	if err != nil {
		return result(effectport.StateFinalFailed, "invalid_target", false, false), nil
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, errDNSBeforeConnect) {
			return result(effectport.StateRetryable, "dns_unavailable_before_connect", false, false), nil
		}
		return result(effectport.StateUnknown, "transport_unknown", true, false), nil
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(responseBody) > 65536 {
		return result(effectport.StateUnknown, "response_unknown", true, false), nil
	}
	var receipt struct {
		Code       *int `json:"code"`
		LegacyCode *int `json:"StatusCode"`
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(responseBody, &receipt) != nil || receipt.Code == nil && receipt.LegacyCode == nil {
		return result(effectport.StateUnknown, "response_unknown", true, false), nil
	}
	if receipt.Code != nil && *receipt.Code != 0 || receipt.LegacyCode != nil && *receipt.LegacyCode != 0 {
		return result(effectport.StateFinalFailed, "provider_rejected", true, false), nil
	}
	// Provider acceptance is not proof that a group member actually saw it.
	return result(effectport.StateExecuted, "provider_accepted", true, true), nil
}

var _ effectport.ProviderAdapter = (*Feishu)(nil)
