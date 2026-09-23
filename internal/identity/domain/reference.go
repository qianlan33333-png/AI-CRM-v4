// Package domain contains pure OneID values and validation rules.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

type Kind string
type Assurance string

const (
	KindWeComExternalUserID Kind = "wecom_external_userid"
	KindUnionID             Kind = "unionid"
	KindMPOpenID            Kind = "mp_openid"
	KindOAOpenID            Kind = "oa_openid"
	// Alipay identities deliberately preserve the provider field contract. An
	// OAuth user id, OAuth open id, buyer id and buyer open id are not
	// interchangeable and must never be inferred from one another.
	KindAlipayUserID       Kind = "alipay_user_id" // legacy spelling; kept scoped.
	KindAlipayOAuthUserID  Kind = "alipay_oauth_user_id"
	KindAlipayOAuthOpenID  Kind = "alipay_oauth_open_id"
	KindAlipayBuyerID      Kind = "alipay_buyer_id"
	KindAlipayBuyerOpenID  Kind = "alipay_buyer_open_id"
	KindFirstPartyMemberID Kind = "first_party_member_id"
	KindPhone              Kind = "phone"
	KindExtension          Kind = "ext"

	AssuranceVerified Assurance = "verified"
	AssuranceDeclared Assurance = "declared"
)

var (
	ErrInvalidReference = errors.New("invalid identity reference")
	phoneE164           = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
	phoneCN11           = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
)

type Reference struct {
	Kind      Kind
	Scope     string
	Value     string
	Assurance Assurance
	Source    string
}

type NormalizedReference struct {
	Kind              Kind
	Scope             string
	NormalizedValue   string
	Assurance         Assurance
	Source            string
	NormalizerVersion int16
}

const NormalizerVersion int16 = 1

func ValidateKind(kind Kind) error {
	if !validKind(kind) {
		return ErrInvalidReference
	}
	return nil
}

func ValidateNamespace(kind Kind, scope string) error {
	if !validKind(kind) || !validScope(kind, scope) {
		return ErrInvalidReference
	}
	return nil
}

func Normalize(reference Reference) (NormalizedReference, error) {
	// Reject control characters before trimming, otherwise a trailing newline
	// could disappear and be accepted as a canonical identity.
	if containsControl(reference.Scope) || containsControl(reference.Value) || containsControl(reference.Source) {
		return NormalizedReference{}, ErrInvalidReference
	}

	scope := strings.TrimSpace(reference.Scope)
	value := strings.TrimSpace(reference.Value)
	source := strings.TrimSpace(reference.Source)
	if !validKind(reference.Kind) || !validScope(reference.Kind, scope) || value == "" || source == "" ||
		len(value) > 1024 || len(source) > 128 || strings.IndexFunc(source, unicode.IsSpace) >= 0 ||
		!validAssurance(reference.Assurance) {
		return NormalizedReference{}, ErrInvalidReference
	}
	if reference.Kind == KindPhone {
		if scope == "phone:e164" {
			value = compactPhone(value)
		}
		if scope == "phone:cn11" && value != reference.Value {
			return NormalizedReference{}, ErrInvalidReference
		}
		if scope == "phone:e164" && !phoneE164.MatchString(value) || scope == "phone:cn11" && !phoneCN11.MatchString(value) {
			return NormalizedReference{}, ErrInvalidReference
		}
	}
	return NormalizedReference{
		Kind:              reference.Kind,
		Scope:             scope,
		NormalizedValue:   value,
		Assurance:         reference.Assurance,
		Source:            source,
		NormalizerVersion: NormalizerVersion,
	}, nil
}

func validKind(kind Kind) bool {
	switch kind {
	case KindWeComExternalUserID, KindUnionID, KindMPOpenID, KindOAOpenID,
		KindAlipayUserID, KindAlipayOAuthUserID, KindAlipayOAuthOpenID,
		KindAlipayBuyerID, KindAlipayBuyerOpenID, KindFirstPartyMemberID,
		KindPhone, KindExtension:
		return true
	default:
		return false
	}
}

func validAssurance(assurance Assurance) bool {
	return assurance == AssuranceVerified || assurance == AssuranceDeclared
}

func validScope(kind Kind, scope string) bool {
	if scope == "" || len(scope) > 256 || strings.IndexFunc(scope, unicode.IsSpace) >= 0 {
		return false
	}
	switch kind {
	case KindWeComExternalUserID:
		return hasNamespace(scope, "wecom-corp:")
	case KindUnionID:
		return hasNamespace(scope, "wechat-open-platform:")
	case KindMPOpenID, KindOAOpenID:
		return hasNamespace(scope, "wechat-app:")
	case KindAlipayUserID, KindAlipayOAuthUserID, KindAlipayOAuthOpenID,
		KindAlipayBuyerID, KindAlipayBuyerOpenID:
		return hasNamespace(scope, "alipay-app:")
	case KindFirstPartyMemberID:
		return hasNamespace(scope, "first-party:")
	case KindPhone:
		return scope == "phone:e164" || scope == "phone:cn11"
	case KindExtension:
		return hasNamespace(scope, "ext:")
	default:
		return false
	}
}

func hasNamespace(scope, prefix string) bool {
	return strings.HasPrefix(scope, prefix) && len(scope) > len(prefix)
}

func compactPhone(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		switch {
		case character == '+' || character >= '0' && character <= '9':
			builder.WriteRune(character)
		case unicode.IsSpace(character) || character == '-' || character == '(' || character == ')' || character == '.':
		default:
			return ""
		}
	}
	return builder.String()
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
