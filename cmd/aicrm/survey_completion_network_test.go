package main

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
)

type surveyCompletionTestResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f surveyCompletionTestResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

// surveyCompletionTLSEndpoint maps a public-looking TLS endpoint to the local
// receiver only after the provider transport has accepted a documentation IP.
func surveyCompletionTLSEndpoint(t *testing.T, server *httptest.Server, host string) (string, outbound.SurveyCompletionNetwork) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return "https://" + host + ":" + parsed.Port(), outbound.SurveyCompletionNetwork{
		Resolver: surveyCompletionTestResolver(func(_ context.Context, _ string, requested string) ([]netip.Addr, error) {
			if requested != host {
				return nil, errors.New("unexpected survey completion test host")
			}
			return []netip.Addr{netip.MustParseAddr("203.0.113.11")}, nil
		}),
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
}
