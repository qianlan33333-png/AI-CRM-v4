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

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const completionURLLinkMaxResponse = 64 << 10

// CompletionURLLinkResolver is a Product-owned Provider-read adapter. It
// resolves only a stored URL Link source after Payment authorizes a paid
// order, and rejects private/local destinations both before the request and
// in the returned destination. It does not write Provider or database state.
type CompletionURLLinkResolver struct {
	client *http.Client
}

func NewCompletionURLLinkResolver() *CompletionURLLinkResolver {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           completionURLLinkDialContext(dialer),
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	return &CompletionURLLinkResolver{client: &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (resolver *CompletionURLLinkResolver) Resolve(ctx context.Context, target completionTarget) (string, error) {
	if resolver == nil || resolver.client == nil || !target.Enabled || target.TargetType != "url_link" || !validCompletionSourceURL(target.SourceURL) || !validCompletionResponseKey(target.ResponseKey) {
		return "", ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.SourceURL, nil)
	if err != nil {
		return "", ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	response, err := resolver.client.Do(request)
	if err != nil {
		return completionURLLinkFallback(target), nil
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return completionURLLinkFallback(target), nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, completionURLLinkMaxResponse+1))
	if err != nil || len(body) > completionURLLinkMaxResponse {
		return completionURLLinkFallback(target), nil
	}
	var document map[string]any
	if json.Unmarshal(body, &document) != nil {
		return completionURLLinkFallback(target), nil
	}
	for _, key := range append([]string{target.ResponseKey}, "url_link", "url", "http", "link") {
		if destination, found := completionURLLinkValue(document, key); found && validCompletionDestinationURL(destination) {
			return destination, nil
		}
	}
	return completionURLLinkFallback(target), nil
}

func (s *PaidPurchaseActionService) SetCompletionURLLinkResolver(resolver *CompletionURLLinkResolver) error {
	if s == nil || resolver == nil || s.urlLinks != nil {
		return ErrUnavailable
	}
	s.urlLinks = resolver
	return nil
}

func (s *PaidPurchaseActionService) ResolvePaidPurchaseURLLink(ctx context.Context, action productport.PaidPurchaseAction) (string, error) {
	if s == nil || s.urlLinks == nil || !action.Enabled || action.Mode != productport.PaidPurchaseActionRedirect || len(action.CompletionTarget) == 0 {
		return "", productport.ErrProductReadUnavailable
	}
	target, err := completionTargetFromProjection(action.CompletionTarget)
	if err != nil {
		return "", productport.ErrProductReadUnavailable
	}
	destination, err := s.urlLinks.Resolve(ctx, target)
	if err != nil || destination == "" {
		return "", productport.ErrProductReadUnavailable
	}
	return destination, nil
}

func completionURLLinkFallback(target completionTarget) string {
	if validPaidPurchaseRedirect(target.FallbackURL) {
		return target.FallbackURL
	}
	return ""
}

func completionURLLinkValue(document map[string]any, key string) (string, bool) {
	if !validCompletionResponseKey(key) {
		return "", false
	}
	var value any = document
	for _, part := range strings.Split(key, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return "", false
		}
		value, ok = object[part]
		if !ok {
			return "", false
		}
	}
	text, ok := value.(string)
	return text, ok
}

func validCompletionDestinationURL(value string) bool {
	if !validCompletionSourceURL(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func completionURLLinkDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || port == "" {
			return nil, errors.New("invalid completion URL Link address")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("completion URL Link host unavailable")
		}
		for _, address := range addresses {
			if completionURLLinkDisallowedIP(address) {
				return nil, errors.New("completion URL Link host disallowed")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
}

func completionURLLinkDisallowedIP(value netip.Addr) bool {
	return value.IsLoopback() || value.IsPrivate() || value.IsLinkLocalUnicast() || value.IsLinkLocalMulticast() || value.IsMulticast() || value.IsUnspecified() || value.Is6() && value.Is4In6() && completionURLLinkDisallowedIP(value.Unmap())
}

var _ productport.PaidPurchaseURLLinkResolver = (*PaidPurchaseActionService)(nil)
