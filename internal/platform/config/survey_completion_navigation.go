package config

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const maxSurveyCompletionNavigationTargets = 100

var surveyCompletionNavigationReference = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// ParseSurveyCompletionNavigationTargets parses the deployment-owned public
// navigation allowlist. It intentionally accepts only opaque Survey references
// and public HTTPS URLs, never provider endpoints or request-derived URLs.
//
// The parser is exported so Composition can apply the same fail-closed rules
// to isolated Runtime fixtures, which do not pass through Load.
func ParseSurveyCompletionNavigationTargets(raw string) (map[string]string, error) {
	if raw == "" {
		return map[string]string{}, nil
	}
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errors.New("configuration must be compact JSON without surrounding whitespace")
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("configuration must be a JSON object")
	}
	targets := make(map[string]string)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		key, ok := keyToken.(string)
		if tokenErr != nil || !ok || !surveyCompletionNavigationReference.MatchString(key) {
			return nil, errors.New("invalid navigation reference")
		}
		if _, exists := targets[key]; exists {
			return nil, errors.New("duplicate navigation reference")
		}
		var target string
		if decodeErr := decoder.Decode(&target); decodeErr != nil || !validSurveyCompletionNavigationURL(target) {
			return nil, errors.New("invalid public completion URL")
		}
		targets[key] = target
		if len(targets) > maxSurveyCompletionNavigationTargets {
			return nil, errors.New("too many navigation targets")
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid navigation configuration")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid trailing navigation configuration")
	}
	if len(targets) == 0 {
		return nil, errors.New("navigation configuration must not be empty")
	}
	return targets, nil
}

func validSurveyCompletionNavigationURL(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return false
	}
	return true
}
