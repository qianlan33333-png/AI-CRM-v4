package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeFreezesScopedIdentityNamespaces(t *testing.T) {
	tests := []struct {
		name      string
		reference Reference
		wantValue string
		valid     bool
	}{
		{
			name: "verified WeCom external contact",
			reference: Reference{Kind: KindWeComExternalUserID, Scope: "wecom-corp:corp-1", Value: "wm_42",
				Assurance: AssuranceVerified, Source: "wecom.callback"},
			wantValue: "wm_42", valid: true,
		},
		{
			name: "app-scoped mini-program openid",
			reference: Reference{Kind: KindMPOpenID, Scope: "wechat-app:wx123", Value: "openid-1",
				Assurance: AssuranceVerified, Source: "wechat.oauth"},
			wantValue: "openid-1", valid: true,
		},
		{
			name: "alipay OAuth and buyer fields remain distinct scoped kinds",
			reference: Reference{Kind: KindAlipayOAuthUserID, Scope: "alipay-app:app-a:production", Value: "ali-user-1",
				Assurance: AssuranceVerified, Source: "alipay.callback"},
			wantValue: "ali-user-1", valid: true,
		},
		{
			name: "phone is compacted to E164",
			reference: Reference{Kind: KindPhone, Scope: "phone:e164", Value: "+86 138-0013-8000",
				Assurance: AssuranceDeclared, Source: "admin"},
			wantValue: "+8613800138000", valid: true,
		},
		{
			name: "mainland phone is already strict canonical form",
			reference: Reference{Kind: KindPhone, Scope: "phone:cn11", Value: "13800138000",
				Assurance: AssuranceDeclared, Source: "survey"},
			wantValue: "13800138000", valid: true,
		},
		{
			name: "mainland phone rejects surrounding whitespace",
			reference: Reference{Kind: KindPhone, Scope: "phone:cn11", Value: " 13800138000 ",
				Assurance: AssuranceDeclared, Source: "survey"},
			valid: false,
		},
		{
			name: "openid cannot use corp scope",
			reference: Reference{Kind: KindMPOpenID, Scope: "wecom-corp:corp-1", Value: "openid-1",
				Assurance: AssuranceVerified, Source: "wechat.oauth"},
			valid: false,
		},
		{
			name: "empty namespace value is invalid",
			reference: Reference{Kind: KindUnionID, Scope: "wechat-open-platform:", Value: "union-1",
				Assurance: AssuranceVerified, Source: "wechat.oauth"},
			valid: false,
		},
		{
			name: "internal control character is invalid",
			reference: Reference{Kind: KindExtension, Scope: "ext:partner", Value: "bad\nvalue",
				Assurance: AssuranceDeclared, Source: "partner"},
			valid: false,
		},
		{
			name: "trailing control character is invalid before trimming",
			reference: Reference{Kind: KindUnionID, Scope: "wechat-open-platform:primary", Value: "union-1\n",
				Assurance: AssuranceVerified, Source: "wechat.oauth"},
			valid: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize(test.reference)
			if !test.valid {
				if !errors.Is(err, ErrInvalidReference) {
					t.Fatalf("Normalize() error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.NormalizedValue != test.wantValue || got.Scope != test.reference.Scope ||
				got.NormalizerVersion != NormalizerVersion {
				t.Fatalf("Normalize()=%+v", got)
			}
		})
	}
}

func TestProviderVerifiedInputCannotCarryCallerSelectedAssurance(t *testing.T) {
	inputType := reflect.TypeOf(ProviderVerifiedIdentityInput{})
	if _, found := inputType.FieldByName("Assurance"); found {
		t.Fatal("provider verified input must not expose a caller-selected assurance field")
	}
}

func TestNormalizeRejectsMissingSourceAndUntrustedAssurance(t *testing.T) {
	base := Reference{Kind: KindWeComExternalUserID, Scope: "wecom-corp:corp-1", Value: "wm_42", Assurance: AssuranceVerified, Source: "wecom"}
	base.Source = ""
	if _, err := Normalize(base); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("missing source error=%v", err)
	}
	base.Source = "wecom"
	base.Assurance = "trusted-by-browser"
	if _, err := Normalize(base); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("invalid assurance error=%v", err)
	}
}

func TestValidateIdentityNamespaceForDurableCommands(t *testing.T) {
	if err := ValidateKind(Kind("invented")); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("invalid kind error=%v", err)
	}
	if err := ValidateNamespace(KindMPOpenID, "wecom-corp:main"); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("wrong namespace error=%v", err)
	}
	if err := ValidateNamespace(KindMPOpenID, "wechat-app:main"); err != nil {
		t.Fatalf("valid namespace error=%v", err)
	}
}
