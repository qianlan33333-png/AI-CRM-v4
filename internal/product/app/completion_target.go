package app

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
)

// completionTarget is the Product-owned, persisted representation of the
// legacy after-payment target. URL Link source URLs stay on the server: the
// public checkout receives only a same-origin resolver route after Payment
// authorizes the paid order.
type completionTarget struct {
	Enabled     bool
	TargetType  string
	H5URL       string
	SourceURL   string
	ResponseKey string
	FallbackURL string
}

func completionTargetFromProjection(raw json.RawMessage) (completionTarget, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return completionTarget{}, nil
	}
	var body struct {
		Enabled      bool            `json:"enabled"`
		TargetType   string          `json:"target_type"`
		Type         string          `json:"type"`
		H5URL        string          `json:"h5_url"`
		URL          string          `json:"url"`
		SourceURL    string          `json:"source_url"`
		ResponseKey  string          `json:"response_url_key"`
		LegacyKey    string          `json:"response_key"`
		FallbackURL  string          `json:"fallback_url"`
		URLLink      json.RawMessage `json:"url_link"`
		OpenStrategy string          `json:"open_strategy"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return completionTarget{}, ErrInvalidProduct
	}
	if body.TargetType == "" {
		body.TargetType = body.Type
	}
	if body.H5URL == "" {
		body.H5URL = body.URL
	}
	if body.ResponseKey == "" {
		body.ResponseKey = body.LegacyKey
	}
	if len(body.URLLink) > 0 && !bytes.Equal(bytes.TrimSpace(body.URLLink), []byte("null")) {
		var link struct {
			Enabled     bool   `json:"enabled"`
			URL         string `json:"url"`
			SourceURL   string `json:"source_url"`
			ResponseKey string `json:"response_url_key"`
			LegacyKey   string `json:"response_key"`
		}
		if json.Unmarshal(body.URLLink, &link) != nil {
			return completionTarget{}, ErrInvalidProduct
		}
		if body.SourceURL == "" {
			body.SourceURL = link.SourceURL
			if body.SourceURL == "" {
				body.SourceURL = link.URL
			}
		}
		if body.ResponseKey == "" {
			body.ResponseKey = link.ResponseKey
			if body.ResponseKey == "" {
				body.ResponseKey = link.LegacyKey
			}
		}
		if body.Enabled && body.TargetType == "url_link" && !link.Enabled {
			return completionTarget{}, ErrInvalidProduct
		}
	}
	if body.TargetType != "" && body.TargetType != "h5" && body.TargetType != "url_link" {
		return completionTarget{}, ErrInvalidProduct
	}
	if body.OpenStrategy != "" && body.OpenStrategy != "h5_redirect" && body.OpenStrategy != "url_link" {
		return completionTarget{}, ErrInvalidProduct
	}
	if body.FallbackURL != "" && !validPaidPurchaseRedirect(body.FallbackURL) {
		return completionTarget{}, ErrInvalidProduct
	}
	if !body.Enabled {
		return completionTarget{TargetType: body.TargetType, H5URL: body.H5URL, SourceURL: body.SourceURL, ResponseKey: body.ResponseKey, FallbackURL: body.FallbackURL}, nil
	}
	if body.TargetType == "h5" {
		if !validPaidPurchaseRedirect(body.H5URL) {
			return completionTarget{}, ErrInvalidProduct
		}
		return completionTarget{Enabled: true, TargetType: body.TargetType, H5URL: body.H5URL, FallbackURL: body.FallbackURL}, nil
	}
	if body.TargetType != "url_link" || !validCompletionSourceURL(body.SourceURL) {
		return completionTarget{}, ErrInvalidProduct
	}
	if body.ResponseKey == "" {
		body.ResponseKey = "url_link"
	}
	if !validCompletionResponseKey(body.ResponseKey) {
		return completionTarget{}, ErrInvalidProduct
	}
	return completionTarget{Enabled: true, TargetType: body.TargetType, SourceURL: body.SourceURL, ResponseKey: body.ResponseKey, FallbackURL: body.FallbackURL}, nil
}

func validCompletionSourceURL(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 2048 || strings.ContainsAny(value, "\\\r\n\t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()) {
		return false
	}
	return true
}

func validCompletionResponseKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 64 {
			return false
		}
		for _, runeValue := range part {
			if !(runeValue >= 'a' && runeValue <= 'z' || runeValue >= 'A' && runeValue <= 'Z' || runeValue >= '0' && runeValue <= '9' || runeValue == '_' || runeValue == '-') {
				return false
			}
		}
	}
	return true
}

func completionURLLinkSnapshot(target completionTarget) json.RawMessage {
	if !target.Enabled || target.TargetType != "url_link" {
		return nil
	}
	raw, err := json.Marshal(struct {
		Enabled     bool   `json:"enabled"`
		Type        string `json:"type"`
		SourceURL   string `json:"source_url"`
		ResponseKey string `json:"response_key"`
		FallbackURL string `json:"fallback_url"`
	}{Enabled: true, Type: target.TargetType, SourceURL: target.SourceURL, ResponseKey: target.ResponseKey, FallbackURL: target.FallbackURL})
	if err != nil {
		return nil
	}
	return raw
}
