package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type settingDefinition struct {
	secret   bool
	validate func(json.RawMessage) (json.RawMessage, bool)
}

var settingRegistry = map[configport.Key]settingDefinition{
	configport.WeComCorpID:                  {validate: validateNonEmptyString},
	configport.WeComAgentID:                 {validate: validatePositiveInteger},
	configport.OutboundRatePerSecond:        {validate: validateIntegerRange(1, 50)},
	configport.OutboundMaxAttempts:          {validate: validateIntegerRange(1, 10)},
	configport.AdminAppSettingsEnabled:      {validate: validateBoolean},
	configport.AdminPushCapabilitiesEnabled: {validate: validateBoolean},
	configport.AdminReleasesEnabled:         {validate: validateBoolean},
	configport.AdminDiagnosticsEnabled:      {validate: validateBoolean},
	configport.DatabaseURL:                  {secret: true},
	configport.WeComSecret:                  {secret: true},
	configport.WeComCallbackToken:           {secret: true},
	configport.WeComCallbackAESKey:          {secret: true},
	configport.AIAPIKey:                     {secret: true},
	configport.AuthJWTSecret:                {secret: true},
	configport.ExtensionAPIKeyPepper:        {secret: true},
	configport.WebhookMasterKey:             {secret: true},
}

func ValidateSetting(key configport.Key, value json.RawMessage) (json.RawMessage, error) {
	definition, ok := settingRegistry[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
	if definition.secret {
		return nil, fmt.Errorf("%w: %s", configport.ErrSecretSetting, key)
	}
	canonical, valid := definition.validate(value)
	if !valid {
		return nil, fmt.Errorf("%w: %s", configport.ErrInvalidSetting, key)
	}
	return canonical, nil
}

func ValidateReadableSetting(key configport.Key) error {
	definition, ok := settingRegistry[key]
	if !ok {
		return fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
	if definition.secret {
		return fmt.Errorf("%w: %s", configport.ErrSecretSetting, key)
	}
	return nil
}

func validateNonEmptyString(value json.RawMessage) (json.RawMessage, bool) {
	var decoded string
	if !decodeOne(value, &decoded) || strings.TrimSpace(decoded) != decoded || decoded == "" || len(decoded) > 256 {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func validatePositiveInteger(value json.RawMessage) (json.RawMessage, bool) {
	return validateIntegerRange(1, 1<<63-1)(value)
}

func validateBoolean(value json.RawMessage) (json.RawMessage, bool) {
	var decoded bool
	if !decodeOne(value, &decoded) {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func validateIntegerRange(minimum, maximum int64) func(json.RawMessage) (json.RawMessage, bool) {
	return func(value json.RawMessage) (json.RawMessage, bool) {
		var decoded int64
		if !decodeOne(value, &decoded) || decoded < minimum || decoded > maximum {
			return nil, false
		}
		canonical, _ := json.Marshal(decoded)
		return canonical, true
	}
}

func decodeOne(value json.RawMessage, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}
