package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

func TestValidateSettingRegistry(t *testing.T) {
	maximumCorpID := `"` + strings.Repeat("c", 256) + `"`
	overlongCorpID := `"` + strings.Repeat("c", 257) + `"`

	tests := []struct {
		name    string
		key     configport.Key
		value   string
		want    string
		wantErr error
	}{
		{name: "corp ID accepts a string", key: configport.WeComCorpID, value: `"corp-1"`, want: `"corp-1"`},
		{name: "corp ID canonicalizes JSON outer whitespace", key: configport.WeComCorpID, value: " \n\t\"corp-1\"\r\n", want: `"corp-1"`},
		{name: "corp ID canonicalizes escaped JSON", key: configport.WeComCorpID, value: `"\u0063orp-1"`, want: `"corp-1"`},
		{name: "corp ID accepts exactly 256 bytes", key: configport.WeComCorpID, value: maximumCorpID, want: maximumCorpID},
		{name: "corp ID rejects empty string", key: configport.WeComCorpID, value: `""`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects leading decoded whitespace", key: configport.WeComCorpID, value: `" corp-1"`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects trailing decoded whitespace", key: configport.WeComCorpID, value: `"corp-1 "`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects overlong string", key: configport.WeComCorpID, value: overlongCorpID, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects number", key: configport.WeComCorpID, value: `1`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects boolean", key: configport.WeComCorpID, value: `true`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects null", key: configport.WeComCorpID, value: `null`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects object", key: configport.WeComCorpID, value: `{}`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects array", key: configport.WeComCorpID, value: `[]`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects malformed JSON", key: configport.WeComCorpID, value: `"corp-1`, wantErr: configport.ErrInvalidSetting},
		{name: "corp ID rejects trailing JSON value", key: configport.WeComCorpID, value: `"corp-1" null`, wantErr: configport.ErrInvalidSetting},

		{name: "agent ID accepts lower boundary", key: configport.WeComAgentID, value: `1`, want: `1`},
		{name: "agent ID accepts upper int64 boundary", key: configport.WeComAgentID, value: `9223372036854775807`, want: `9223372036854775807`},
		{name: "agent ID canonicalizes JSON outer whitespace", key: configport.WeComAgentID, value: " \n1\t", want: `1`},
		{name: "agent ID rejects zero", key: configport.WeComAgentID, value: `0`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects negative integer", key: configport.WeComAgentID, value: `-1`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects int64 overflow", key: configport.WeComAgentID, value: `9223372036854775808`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects fraction", key: configport.WeComAgentID, value: `1.0`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects string", key: configport.WeComAgentID, value: `"1"`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects boolean", key: configport.WeComAgentID, value: `true`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects null", key: configport.WeComAgentID, value: `null`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects object", key: configport.WeComAgentID, value: `{}`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects array", key: configport.WeComAgentID, value: `[]`, wantErr: configport.ErrInvalidSetting},
		{name: "agent ID rejects trailing JSON value", key: configport.WeComAgentID, value: `1 2`, wantErr: configport.ErrInvalidSetting},

		{name: "rate accepts lower boundary", key: configport.OutboundRatePerSecond, value: `1`, want: `1`},
		{name: "rate accepts upper boundary", key: configport.OutboundRatePerSecond, value: `50`, want: `50`},
		{name: "rate canonicalizes JSON outer whitespace", key: configport.OutboundRatePerSecond, value: " \n50\t", want: `50`},
		{name: "rate rejects below lower boundary", key: configport.OutboundRatePerSecond, value: `0`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects above upper boundary", key: configport.OutboundRatePerSecond, value: `51`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects fraction", key: configport.OutboundRatePerSecond, value: `1.0`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects string", key: configport.OutboundRatePerSecond, value: `"1"`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects boolean", key: configport.OutboundRatePerSecond, value: `true`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects null", key: configport.OutboundRatePerSecond, value: `null`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects object", key: configport.OutboundRatePerSecond, value: `{}`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects array", key: configport.OutboundRatePerSecond, value: `[]`, wantErr: configport.ErrInvalidSetting},
		{name: "rate rejects trailing JSON value", key: configport.OutboundRatePerSecond, value: `1 2`, wantErr: configport.ErrInvalidSetting},

		{name: "max attempts accepts lower boundary", key: configport.OutboundMaxAttempts, value: `1`, want: `1`},
		{name: "max attempts accepts upper boundary", key: configport.OutboundMaxAttempts, value: `10`, want: `10`},
		{name: "max attempts canonicalizes JSON outer whitespace", key: configport.OutboundMaxAttempts, value: " \n10\t", want: `10`},
		{name: "max attempts rejects below lower boundary", key: configport.OutboundMaxAttempts, value: `0`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects above upper boundary", key: configport.OutboundMaxAttempts, value: `11`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects fraction", key: configport.OutboundMaxAttempts, value: `1.0`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects string", key: configport.OutboundMaxAttempts, value: `"1"`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects boolean", key: configport.OutboundMaxAttempts, value: `true`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects null", key: configport.OutboundMaxAttempts, value: `null`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects object", key: configport.OutboundMaxAttempts, value: `{}`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects array", key: configport.OutboundMaxAttempts, value: `[]`, wantErr: configport.ErrInvalidSetting},
		{name: "max attempts rejects trailing JSON value", key: configport.OutboundMaxAttempts, value: `1 2`, wantErr: configport.ErrInvalidSetting},

		{name: "unknown key rejects write", key: "custom.key", value: `"unknown-write-input-must-not-appear"`, wantErr: configport.ErrUnknownSetting},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateSetting(test.key, json.RawMessage(test.value))
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ValidateSetting() error = %v, want %v", err, test.wantErr)
				}
				if got != nil {
					t.Fatalf("ValidateSetting() value = %s, want nil when validation fails", got)
				}
				assertErrorDoesNotContainInput(t, err, test.value)
				return
			}
			if err != nil || string(got) != test.want {
				t.Fatalf("ValidateSetting() = %s, %v; want %s, nil", got, err, test.want)
			}
		})
	}
}

func TestNonSecretKeysAreReadable(t *testing.T) {
	tests := []struct {
		name string
		key  configport.Key
	}{
		{name: "corp ID", key: configport.WeComCorpID},
		{name: "agent ID", key: configport.WeComAgentID},
		{name: "outbound rate", key: configport.OutboundRatePerSecond},
		{name: "outbound max attempts", key: configport.OutboundMaxAttempts},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateReadableSetting(test.key); err != nil {
				t.Fatalf("ValidateReadableSetting(%s) error = %v, want nil", test.key, err)
			}
		})
	}
}

func TestSecretKeysAlwaysFailClosed(t *testing.T) {
	const secretInput = "synthetic-secret-input-must-not-appear"
	secretJSON := `"` + secretInput + `"`
	tests := []struct {
		name string
		key  configport.Key
	}{
		{name: "database URL", key: configport.DatabaseURL},
		{name: "WeCom secret", key: configport.WeComSecret},
		{name: "WeCom callback token", key: configport.WeComCallbackToken},
		{name: "WeCom callback AES key", key: configport.WeComCallbackAESKey},
		{name: "AI API key", key: configport.AIAPIKey},
		{name: "auth JWT secret", key: configport.AuthJWTSecret},
		{name: "extension API key pepper", key: configport.ExtensionAPIKeyPepper},
		{name: "webhook master key", key: configport.WebhookMasterKey},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateSetting(test.key, json.RawMessage(secretJSON))
			if !errors.Is(err, configport.ErrSecretSetting) {
				t.Fatalf("ValidateSetting() error = %v, want ErrSecretSetting", err)
			}
			if got != nil {
				t.Fatalf("ValidateSetting() value = %s, want nil for secret key", got)
			}
			assertErrorDoesNotContainInput(t, err, secretJSON)

			err = ValidateReadableSetting(test.key)
			if !errors.Is(err, configport.ErrSecretSetting) {
				t.Fatalf("ValidateReadableSetting() error = %v, want ErrSecretSetting", err)
			}
			assertErrorDoesNotContainInput(t, err, secretJSON)
		})
	}
}

func TestUnknownKeyIsNeverReadable(t *testing.T) {
	if err := ValidateReadableSetting("custom.key"); !errors.Is(err, configport.ErrUnknownSetting) {
		t.Fatalf("ValidateReadableSetting() error = %v, want ErrUnknownSetting", err)
	}
}

func assertErrorDoesNotContainInput(t *testing.T, err error, value string) {
	t.Helper()
	if strings.Contains(err.Error(), value) {
		t.Fatalf("error %q contains raw input", err)
	}

	var decoded string
	if json.Unmarshal([]byte(value), &decoded) == nil && decoded != "" && strings.Contains(err.Error(), decoded) {
		t.Fatalf("error %q contains decoded input", err)
	}
}
