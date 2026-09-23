package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type surveyCompletionTarget struct {
	Enabled                                   bool
	TargetType, H5URL, SourceURL, ResponseKey string
}

func parseSurveyCompletionTarget(raw json.RawMessage) (surveyCompletionTarget, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return surveyCompletionTarget{}, nil
	}
	var body struct {
		Enabled     bool   `json:"enabled"`
		TargetType  string `json:"target_type"`
		H5URL       string `json:"h5_url"`
		SourceURL   string `json:"source_url"`
		ResponseKey string `json:"response_url_key"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return surveyCompletionTarget{}, surveyport.ErrInvalid
	}
	target := surveyCompletionTarget{TargetType: strings.TrimSpace(body.TargetType), H5URL: strings.TrimSpace(body.H5URL), SourceURL: strings.TrimSpace(body.SourceURL), ResponseKey: strings.TrimSpace(body.ResponseKey)}
	if !body.Enabled {
		return target, nil
	}
	target.Enabled = true
	if target.TargetType == "h5" && safeStoredSurveyRedirect(target.H5URL) {
		return target, nil
	}
	if target.TargetType == "url_link" && safeSurveyProviderURL(target.SourceURL) {
		if target.ResponseKey == "" {
			target.ResponseKey = "url_link"
		}
		if safeSurveyResponseKey(target.ResponseKey) {
			return target, nil
		}
	}
	return surveyCompletionTarget{}, surveyport.ErrInvalid
}
func safeStoredSurveyRedirect(raw string) bool {
	return surveyport.SafePublicCompletionURL(raw)
}
func safeSurveyProviderURL(raw string) bool {
	if !safeStoredSurveyRedirect(raw) || strings.HasPrefix(raw, "/") {
		return false
	}
	parsed, _ := url.Parse(raw)
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil {
		return !blockedSurveyIP(ip)
	}
	return !strings.EqualFold(strings.TrimSuffix(parsed.Hostname(), "."), "localhost")
}
func safeSurveyResponseKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 64 {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return false
			}
		}
	}
	return true
}
func (s *SubmissionService) resolveStoredCompletionTarget(ctx context.Context, raw json.RawMessage) (string, bool) {
	target, err := parseSurveyCompletionTarget(raw)
	if err != nil || !target.Enabled {
		return "", false
	}
	if target.TargetType == "h5" {
		return target.H5URL, true
	}
	destination, err := resolveSurveyURLLink(ctx, target)
	return destination, err == nil && destination != ""
}
func resolveSurveyURLLink(ctx context.Context, target surveyCompletionTarget) (string, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid URL Link address")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("URL Link host unavailable")
		}
		for _, address := range addresses {
			if blockedSurveyIP(address) {
				return nil, errors.New("URL Link host disallowed")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.SourceURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("URL Link rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return "", errors.New("URL Link response invalid")
	}
	var document map[string]any
	if json.Unmarshal(body, &document) != nil {
		return "", errors.New("URL Link response invalid")
	}
	var value any = document
	for _, part := range strings.Split(target.ResponseKey, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return "", errors.New("URL Link field missing")
		}
		value, ok = object[part]
		if !ok {
			return "", errors.New("URL Link field missing")
		}
	}
	destination, ok := value.(string)
	if !ok || !safeSurveyProviderURL(destination) {
		return "", errors.New("URL Link destination invalid")
	}
	return destination, nil
}
func blockedSurveyIP(value netip.Addr) bool {
	return value.IsLoopback() || value.IsPrivate() || value.IsLinkLocalUnicast() || value.IsLinkLocalMulticast() || value.IsMulticast() || value.IsUnspecified() || value.Is6() && value.Is4In6() && blockedSurveyIP(value.Unmap())
}
