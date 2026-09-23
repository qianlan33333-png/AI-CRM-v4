package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type EndpointProbe struct {
	origin, release string
	client          *http.Client
}

// NewEndpointProbe is local to the API listener, without browser cookies or
// mutation endpoints. It follows no redirects and never reads response bodies
// other than the fixed public health contract.
func NewEndpointProbe(listen, release string) (*EndpointProbe, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("endpoint probe requires local API listener")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &EndpointProbe{origin: "http://" + net.JoinHostPort(host, port), release: release, client: &http.Client{Transport: tr, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *EndpointProbe) Counts(ctx context.Context, _ time.Time) (map[string]int64, error) {
	out := map[string]int64{"expected_endpoints": 4}
	for _, path := range []string{"/healthz", "/readyz", "/api/admin/ops-inspections", "/admin/ops"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.origin+path, nil)
		if err != nil {
			return nil, err
		}
		start := time.Now()
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, errors.New("local endpoint unavailable")
		}
		out["observed_endpoints"]++
		out["total_duration_ms"] += time.Since(start).Milliseconds()
		if path == "/healthz" || path == "/readyz" {
			var value struct {
				Status  string `json:"status"`
				Release string `json:"release_sha"`
			}
			if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&value) != nil || value.Release == "" {
				out["failed_endpoints"]++
			} else {
				if value.Release != p.release {
					out["version_mismatch"]++
				}
				if value.Status != "alive" && value.Status != "ready" {
					out["failed_endpoints"]++
				}
			}
		} else {
			protected := resp.StatusCode == 401 || resp.StatusCode == 403
			if path == "/admin/ops" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
				target, e := url.Parse(resp.Header.Get("Location"))
				protected = e == nil && target.Host == "" && (target.Path == "/login" || strings.HasPrefix(target.Path, "/admin/login"))
			}
			if !protected {
				out["auth_boundary_failed"]++
			}
		}
		_ = resp.Body.Close()
	}
	return out, nil
}
