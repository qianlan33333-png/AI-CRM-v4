package port

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// PublicCompletionTargetResolver is composition-owned. Its input is only the
// Survey-owned opaque navigation reference; an implementation must enforce the
// allowlist and return a public HTTPS destination. It must never expose or
// reuse an outbound completion endpoint, its credentials, or its parameters.
type PublicCompletionTargetResolver interface {
	ResolvePublicCompletionTarget(context.Context, string) (url string, found bool, err error)
}

type CompletionEndpointManager interface {
	ReadSurveyCompletionEndpointWithin(context.Context, ID, string) (string, error)
	SaveSurveyCompletionEndpointWithin(context.Context, ID, string, string, json.RawMessage) (string, error)
}

// ValidPublicCompletionURL is the final public-browser boundary for completion
// actions. A same-origin absolute-path reference is safe to return without a
// server-side dial. Absolute values must be public HTTPS destinations; their
// DNS is deliberately not resolved here because the browser, rather than the
// server, performs that navigation. Server-executed URL Link fetches enforce
// the corresponding resolved-address policy at their transport boundary.
func ValidPublicCompletionURL(raw string) bool {
	if raw == "" || len(raw) > 2048 || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\\\r\n\t") {
		return false
	}
	if strings.HasPrefix(raw, "/") {
		return !strings.HasPrefix(raw, "//")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return ValidPublicCompletionHost(parsed.Hostname())
}

// SafePublicCompletionURL remains the compatibility name for callers that
// validate the same browser-visible completion-action boundary.
func SafePublicCompletionURL(raw string) bool {
	return ValidPublicCompletionURL(raw)
}

var publicCompletionIDNA = idna.New(idna.MapForLookup(), idna.Transitional(false))

// ValidPublicCompletionHost is the shared final host boundary for browser
// completion actions and Survey completion endpoint configuration. Besides
// canonical IP literals, browsers accept UTS46 compatibility mappings and
// historic IPv4 forms such as 127.1, 0177.0.0.1 and 0x7f000001. Normalize
// those forms before applying the public-address policy, so they cannot bypass
// it through full-width characters or WHATWG inet_aton parsing.
func ValidPublicCompletionHost(raw string) bool {
	host := strings.TrimSuffix(strings.ToLower(raw), ".")
	// Preserve the existing literal IPv6 and canonical IPv4 policy without
	// passing colons through the IDNA lookup profile.
	if ip, err := netip.ParseAddr(host); err == nil {
		return !DisallowedPublicIP(ip)
	}
	var err error
	host, err = publicCompletionIDNA.ToASCII(host)
	if err != nil {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return !DisallowedPublicIP(ip)
	}
	if ip, ok := normalizeLegacyIPv4(host); ok {
		return !DisallowedPublicIP(ip)
	}
	// A colon can only occur in an IP literal here. Do not turn malformed or
	// zone-qualified IPv6 into a DNS hostname merely because netip rejected it.
	return !strings.Contains(host, ":")
}

// normalizeLegacyIPv4 implements the numeric host forms browsers still accept
// for URLs: one to four components, each decimal, octal (leading 0), or hex
// (leading 0x). It intentionally returns false for DNS names and malformed
// numeric inputs, which URL parsers reject instead of navigating to an IP.
func normalizeLegacyIPv4(host string) (netip.Addr, bool) {
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	var values [4]uint64
	for index, part := range parts {
		value, ok := parseLegacyIPv4Part(part)
		if !ok || index < len(parts)-1 && value > 255 {
			return netip.Addr{}, false
		}
		values[index] = value
	}
	lastLimit := uint64(1) << (8 * (5 - len(parts)))
	if values[len(parts)-1] >= lastLimit {
		return netip.Addr{}, false
	}
	value := values[len(parts)-1]
	for index := 0; index < len(parts)-1; index++ {
		value += values[index] << (8 * (3 - index))
	}
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}), true
}

func parseLegacyIPv4Part(raw string) (uint64, bool) {
	if raw == "" {
		return 0, false
	}
	base, digits := 10, raw
	if len(raw) > 1 && raw[0] == '0' {
		base, digits = 8, raw[1:]
		if len(raw) > 2 && raw[1] == 'x' {
			base, digits = 16, raw[2:]
		}
	}
	if digits == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(digits, base, 32)
	return value, err == nil
}

// DisallowedPublicIP is shared by completion configuration validation and the
// server-side URL Link transport. It deliberately rejects every non-public
// literal, including IPv4-in-IPv6 forms, before a configured URL can be
// returned to a browser or reached by the server.
func DisallowedPublicIP(value netip.Addr) bool {
	return value.IsLoopback() || value.IsPrivate() || value.IsLinkLocalUnicast() || value.IsLinkLocalMulticast() || value.IsMulticast() || value.IsUnspecified() || value.Is6() && value.Is4In6() && DisallowedPublicIP(value.Unmap())
}
