package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ownerScopeKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var (
	ErrMachineClientDisabled  = errors.New("machine client disabled")
	ErrMachineClientExpired   = errors.New("machine client expired")
	ErrMachineCredential      = errors.New("invalid machine credentials")
	ErrMachineAudience        = errors.New("machine audience denied")
	ErrMachineScope           = errors.New("machine scope denied")
	ErrMachineSourceIP        = errors.New("machine source IP denied")
	ErrMachineReissueRequired = errors.New("machine client requires reissue")
	ErrMachineIssuerUnready   = errors.New("machine token issuer is not configured")
	ErrMachineClientActive    = errors.New("machine client must be disabled before update")
	ErrMachineActivation      = errors.New("machine client activation requires secret self-check")
)

// MachineClient is Access-owned authentication state. It deliberately has no
// user roles: a machine principal receives only these explicit grants.
type MachineClient struct {
	ID              int64
	ClientID        string
	DisplayName     string
	Purpose         string
	SecretHash      string
	CredentialHint  string
	Audiences       []string
	Scopes          []string
	Capabilities    []string
	AllowedCIDRs    []string
	CorpID          string
	OwnerScope      OwnerScope
	TokenTTLSeconds int
	ExpiresAt       *time.Time
	Enabled         bool
	ReissueRequired bool
	AuthVersion     int64
	LastUsedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MachinePrincipal struct {
	ClientID     string
	ClientRecord int64
	Audience     string
	Scopes       []string
	Capabilities []string
	CorpID       string
	OwnerScope   OwnerScope
	AuthVersion  int64
	DirectKey    bool
}

// OwnerScope is the frozen auth-platform resource constraint. Empty means
// unrestricted for that client; a non-empty scope requires every named
// resource to be present and to match. It is deliberately a value object,
// not a new RBAC or tenancy system.
type OwnerScope map[string][]string

func NormalizeOwnerScope(raw []byte) (OwnerScope, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return OwnerScope{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var source map[string]any
	if err := decoder.Decode(&source); err != nil || len(source) > 20 {
		return nil, ErrInvalidInput
	}
	result := make(OwnerScope, len(source))
	for key, rawValue := range source {
		if !ownerScopeKey.MatchString(key) {
			return nil, ErrInvalidInput
		}
		values, err := ownerScopeValues(rawValue)
		if err != nil || len(values) == 0 || len(values) > 100 {
			return nil, ErrInvalidInput
		}
		result[key] = values
	}
	return result, nil
}

func (scope OwnerScope) JSON() []byte {
	if len(scope) == 0 {
		return []byte(`{}`)
	}
	payload, err := json.Marshal(scope)
	if err != nil {
		return []byte(`{}`)
	}
	return payload
}

func (scope OwnerScope) Allows(resources map[string]string) bool {
	for key, allowed := range scope {
		actual, ok := resources[key]
		if !ok {
			return false
		}
		matched := false
		for _, candidate := range allowed {
			if candidate == actual {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func ownerScopeValues(raw any) ([]string, error) {
	items := []any{raw}
	if list, ok := raw.([]any); ok {
		items = list
	}
	seen := make(map[string]struct{}, len(items))
	values := make([]string, 0, len(items))
	for _, item := range items {
		var value string
		switch typed := item.(type) {
		case string:
			value = strings.TrimSpace(typed)
		case json.Number:
			value = typed.String()
		default:
			return nil, ErrInvalidInput
		}
		if value == "" || len(value) > 256 {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values, nil
}

func (principal MachinePrincipal) HasCapability(capability string) bool {
	for _, candidate := range principal.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// HasScope reports whether the bearer token itself carries the requested
// protocol scope. Capabilities describe the client's current grant; scopes
// describe the deliberately narrower token minted for one request session.
// Both checks are required before an operation may run.
func (principal MachinePrincipal) HasScope(scope string) bool {
	for _, candidate := range principal.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

// MachineAudit contains only identifiers and bounded safe facts. Credentials,
// JWTs, external identifiers, and source address values never enter it.
type MachineAudit struct {
	MachineClientID int64
	ActorAdminID    *int64
	Action          string
	Outcome         string
	Details         []byte
	CreatedAt       time.Time
}

func NormalizeMachineClientID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 120 {
		return "", ErrInvalidInput
	}
	if !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= '0' && value[0] <= '9')) {
		return "", ErrInvalidInput
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", ErrInvalidInput
	}
	return value, nil
}

func NormalizeMachineStrings(values []string, allowed map[string]struct{}) ([]string, error) {
	if len(values) == 0 || len(values) > len(allowed) {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if _, ok := allowed[value]; !ok {
			return nil, ErrInvalidInput
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, ErrInvalidInput
	}
	sort.Strings(result)
	return result, nil
}

func NormalizeCIDRs(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, ErrInvalidInput
		}
		value = prefix.Masked().String()
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func MachineSourceAllowed(cidrs []string, source netip.Addr) bool {
	if !source.IsValid() {
		return false
	}
	if len(cidrs) == 0 {
		return true
	}
	for _, raw := range cidrs {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil && prefix.Contains(source) {
			return true
		}
	}
	return false
}
