package provider

import (
	"context"
	"errors"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fixtureReader struct{ payload opsport.OpsReportPayload }

func (r fixtureReader) LoadOpsReportPayload(context.Context, effectport.Envelope) (opsport.OpsReportPayload, error) {
	return r.payload, nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func notificationFixture(t *testing.T) (*Feishu, effectport.Envelope) {
	t.Helper()
	payload := opsport.OpsReportPayload{HourKey: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC), TargetRef: "test-group", Content: []byte(`{"msg_type":"text","content":{"text":"CRM巡查"}}`)}
	payload.PayloadDigest = effectport.Hash("ops-report-payload-v1", string(payload.Content))
	e := effectport.Envelope{Owner: effectport.OwnerAdminOps, Kind: effectport.KindFeishuOpsNotification, SourceRefDigest: effectport.Hash("ops-report-source-v1", payload.HourKey.Format(time.RFC3339)), TargetRefDigest: effectport.Hash("ops-report-target-v1", payload.TargetRef), PayloadDigest: payload.PayloadDigest, PolicyVersionHash: effectport.Hash("ops-report-policy-v1", "hourly-v1")}
	p, err := NewFeishu(FeishuConfig{Enabled: true, TargetRef: payload.TargetRef, WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/00000000-0000-0000-0000-000000000000"}, fixtureReader{payload})
	if err != nil {
		t.Fatal(err)
	}
	return p, e
}
func TestFeishuDistinguishesAcceptanceRejectionAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		status         int
		state          effectport.State
		transportError bool
	}{
		{"accepted", `{"code":0}`, 200, effectport.StateExecuted, false},
		{"legacy_accepted", `{"StatusCode":0}`, 200, effectport.StateExecuted, false},
		{"rejected", `{"code":19021,"msg":"private details"}`, 200, effectport.StateFinalFailed, false},
		{"empty", `{}`, 200, effectport.StateUnknown, false},
		{"contradictory", `{"code":0,"StatusCode":1}`, 200, effectport.StateFinalFailed, false},
		{"redirect", `{"code":0}`, 302, effectport.StateUnknown, false},
		{"server_error", `{"code":0}`, 500, effectport.StateUnknown, false},
		{"timeout", ``, 0, effectport.StateUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := notificationFixture(t)
			calls := 0
			p.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.transportError {
					return nil, errors.New("private-url-and-token")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			got, err := p.Execute(context.Background(), e, effectport.Attempt{EffectID: "eer_1", Number: 1})
			if err != nil || got.Completion != tc.state || calls != 1 || !got.CallAttempted || got.RealExternalCallExecuted != (tc.state == effectport.StateExecuted) {
				t.Fatalf("result=%+v err=%v calls=%d", got, err, calls)
			}
			if strings.Contains(got.FailureCode, "private") || got.SafeToRetryRejected {
				t.Fatal("unsafe receipt/retry")
			}
		})
	}
}
func TestFeishuImmutableTargetAndDisabledNeverCall(t *testing.T) {
	for _, mutate := range []func(*Feishu, *effectport.Envelope){func(p *Feishu, e *effectport.Envelope) { p.config.Enabled = false }, func(p *Feishu, e *effectport.Envelope) { p.config.TargetRef = "another-group" }, func(p *Feishu, e *effectport.Envelope) { e.PayloadDigest = effectport.Hash("altered") }} {
		p, e := notificationFixture(t)
		mutate(p, &e)
		p.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("forbidden provider call"); return nil, nil })}
		got, err := p.Execute(context.Background(), e, effectport.Attempt{EffectID: "eer_1", Number: 1})
		if err != nil || got.CallAttempted || got.Completion != effectport.StateFinalFailed {
			t.Fatal("invalid intent not rejected")
		}
	}
}

type fixedResolver []netip.Addr

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}
func TestFeishuPinsPublicDNSAndRejectsPrivateResolution(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:192.168.1.1"} {
		dial := guardedDial(fixedResolver{netip.MustParseAddr(ip)}, func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("private destination dialed")
			return nil, nil
		})
		if _, err := dial(context.Background(), "tcp", "open.feishu.cn:443"); err == nil {
			t.Fatal("private address accepted")
		}
	}
	destination := ""
	dial := guardedDial(fixedResolver{netip.MustParseAddr("1.2.3.4")}, func(_ context.Context, _ string, a string) (net.Conn, error) {
		destination = a
		return nil, errors.New("fixture")
	})
	_, _ = dial(context.Background(), "tcp", "open.feishu.cn:443")
	if destination != "1.2.3.4:443" {
		t.Fatal("DNS was not pinned")
	}
	for _, u := range []string{"http://open.feishu.cn/open-apis/bot/v2/hook/0000000000000000", "https://evil.test/open-apis/bot/v2/hook/0000000000000000", "https://open.feishu.cn:443/open-apis/bot/v2/hook/0000000000000000", "https://open.feishu.cn/open-apis/bot/v2/hook/0000000000000000?x=y"} {
		if validEndpoint(u) {
			t.Fatal("unapproved webhook endpoint")
		}
	}
}

func TestFeishuOwnDNSFailureProvesNoCall(t *testing.T) {
	p, e := notificationFixture(t)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = guardedDial(fixedResolver(nil), func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("DNS failure must not connect")
		return nil, nil
	})
	p.client = &http.Client{Transport: transport}
	got, err := p.Execute(context.Background(), e, effectport.Attempt{EffectID: "eer_1", Number: 1})
	if err != nil || got.Completion != effectport.StateRetryable || got.CallAttempted || got.RealExternalCallExecuted || got.FailureCode != "dns_unavailable_before_connect" {
		t.Fatalf("DNS fact: %+v %v", got, err)
	}
}
