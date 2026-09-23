package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

type CategoryView struct {
	Key              string         `json:"key"`
	Enabled          bool           `json:"enabled"`
	Settings         map[string]any `json:"settings"`
	SettingsRedacted bool           `json:"settings_redacted"`
	Version          int64          `json:"version"`
	UpdatedBy        string         `json:"updated_by"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type ReleaseView struct {
	ID                  int64          `json:"id"`
	State               string         `json:"state"`
	Changes             map[string]any `json:"changes"`
	ChangesRedacted     bool           `json:"changes_redacted"`
	Checksum            string         `json:"checksum"`
	BasedOnReleaseID    *int64         `json:"based_on_release_id,omitempty"`
	RollbackOfReleaseID *int64         `json:"rollback_of_release_id,omitempty"`
	CreatedBy           string         `json:"created_by"`
	PublishedBy         string         `json:"published_by,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	ValidatedAt         *time.Time     `json:"validated_at,omitempty"`
	PublishedAt         *time.Time     `json:"published_at,omitempty"`
}

var categorySettingValidators = map[string]func(any) (any, bool){
	"enabled": booleanControlPlaneValue,
}

var releaseChangeValidators = map[string]func(any) (any, bool){
	"wecom.corp_id":            corpIDControlPlaneValue,
	"wecom.agent_id":           positiveControlPlaneInteger,
	"outbound.rate_per_second": positiveControlPlaneInteger,
	"outbound.max_attempts":    positiveControlPlaneInteger,
	"wecom.webhook_ref":        secretReferenceControlPlaneValue,
}

func canonicalCategorySettings(value map[string]any) (json.RawMessage, error) {
	return canonicalControlPlaneObject(value, categorySettingValidators, true)
}

func canonicalReleaseChanges(value map[string]any) (json.RawMessage, error) {
	if corpID, present := value["wecom.corp_id"]; present {
		if _, valid := corpIDControlPlaneValue(corpID); !valid {
			return nil, ErrInvalidCommand
		}
	}
	return canonicalControlPlaneObject(value, releaseChangeValidators, false)
}

func validateStoredReleaseChanges(raw []byte) error {
	decoded, ok := decodeControlPlaneObject(raw)
	if !ok {
		return ErrInvalidCommand
	}
	_, err := canonicalReleaseChanges(decoded)
	return err
}

func canonicalControlPlaneObject(value map[string]any, validators map[string]func(any) (any, bool), allowEmpty bool) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil || !json.Valid(encoded) || len(encoded) > 64<<10 {
		return nil, ErrInvalidCommand
	}
	decoded, ok := decodeControlPlaneObject(encoded)
	if !ok {
		return nil, ErrInvalidCommand
	}
	if containsForbiddenControlPlaneKey(decoded) {
		return nil, ErrSecretMaterial
	}
	if len(decoded) == 0 && !allowEmpty {
		return nil, ErrInvalidCommand
	}
	canonical := make(map[string]any, len(decoded))
	for key, raw := range decoded {
		validate, known := validators[key]
		if !known {
			return nil, ErrInvalidCommand
		}
		projected, valid := validate(raw)
		if !valid {
			return nil, ErrInvalidCommand
		}
		canonical[key] = projected
	}
	encoded, err = json.Marshal(canonical)
	if err != nil || !json.Valid(encoded) {
		return nil, ErrInvalidCommand
	}
	return encoded, nil
}

func projectCategory(item adminopsport.Category) CategoryView {
	settings, redacted := projectControlPlaneObject(item.Settings, categorySettingValidators)
	return CategoryView{
		Key: item.Key, Enabled: item.Enabled, Settings: settings, SettingsRedacted: redacted,
		Version: item.Version, UpdatedBy: item.UpdatedBy, UpdatedAt: item.UpdatedAt,
	}
}

func projectRelease(item adminopsport.Release) ReleaseView {
	changes, redacted := projectControlPlaneObject(item.Changes, releaseChangeValidators)
	return ReleaseView{
		ID: item.ID, State: item.State, Changes: changes, ChangesRedacted: redacted, Checksum: item.Checksum,
		BasedOnReleaseID: cloneInt64(item.BasedOnReleaseID), RollbackOfReleaseID: cloneInt64(item.RollbackOfReleaseID),
		CreatedBy: item.CreatedBy, PublishedBy: item.PublishedBy, CreatedAt: item.CreatedAt,
		ValidatedAt: cloneTime(item.ValidatedAt), PublishedAt: cloneTime(item.PublishedAt),
	}
}

func projectControlPlaneObject(raw []byte, validators map[string]func(any) (any, bool)) (map[string]any, bool) {
	decoded, ok := decodeControlPlaneObject(raw)
	if !ok {
		return map[string]any{}, len(bytes.TrimSpace(raw)) != 0 && string(bytes.TrimSpace(raw)) != "{}"
	}
	result := make(map[string]any, len(decoded))
	redacted := false
	for key, value := range decoded {
		validate, known := validators[key]
		if !known || containsForbiddenControlPlaneKey(map[string]any{key: value}) {
			redacted = true
			continue
		}
		if key == "wecom.webhook_ref" && value == "masked" {
			result[key] = "masked"
			redacted = true
			continue
		}
		projected, valid := validate(value)
		if !valid {
			redacted = true
			continue
		}
		if projected == nil {
			result[key] = nil
			continue
		}
		if key == "wecom.webhook_ref" {
			result[key] = "masked"
			redacted = true
			continue
		}
		result[key] = projected
	}
	return result, redacted
}

func decodeCategoryResult(raw json.RawMessage, err error) (CategoryView, error) {
	if err != nil {
		return CategoryView{}, classify(err)
	}
	var current CategoryView
	if json.Unmarshal(raw, &current) == nil && current.Key != "" && current.Settings != nil {
		encoded, _ := json.Marshal(current.Settings)
		var redacted bool
		current.Settings, redacted = projectControlPlaneObject(encoded, categorySettingValidators)
		current.SettingsRedacted = current.SettingsRedacted || redacted
		return current, nil
	}
	var legacy adminopsport.Category
	if json.Unmarshal(raw, &legacy) != nil || legacy.Key == "" {
		return CategoryView{}, ErrUnavailable
	}
	return projectCategory(legacy), nil
}

func decodeReleaseResult(raw json.RawMessage, err error) (ReleaseView, error) {
	if err != nil {
		return ReleaseView{}, classify(err)
	}
	var current ReleaseView
	if json.Unmarshal(raw, &current) == nil && current.ID > 0 && current.Changes != nil {
		encoded, _ := json.Marshal(current.Changes)
		var redacted bool
		current.Changes, redacted = projectControlPlaneObject(encoded, releaseChangeValidators)
		current.ChangesRedacted = current.ChangesRedacted || redacted
		return current, nil
	}
	var legacy adminopsport.Release
	if json.Unmarshal(raw, &legacy) != nil || legacy.ID < 1 {
		return ReleaseView{}, ErrUnavailable
	}
	return projectRelease(legacy), nil
}

func decodeControlPlaneObject(raw []byte) (map[string]any, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil || result == nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return result, true
}

func containsForbiddenControlPlaneKey(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			normalized := normalizeControlPlaneKey(key)
			for _, forbidden := range []string{"apikey", "clientsecret", "accesstoken", "refreshtoken", "authorization", "cookie", "privatekey", "credential", "password", "secret", "webhookurl"} {
				if strings.Contains(normalized, forbidden) {
					return true
				}
			}
			if containsForbiddenControlPlaneKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range item {
			if containsForbiddenControlPlaneKey(nested) {
				return true
			}
		}
	}
	return false
}

func normalizeControlPlaneKey(value string) string {
	return strings.Map(func(item rune) rune {
		if unicode.IsLetter(item) || unicode.IsDigit(item) {
			return unicode.ToLower(item)
		}
		return -1
	}, value)
}

func booleanControlPlaneValue(value any) (any, bool) {
	result, ok := value.(bool)
	return result, ok
}

func corpIDControlPlaneValue(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	result, ok := value.(string)
	if !ok || result == "" || !utf8.ValidString(result) || strings.TrimSpace(result) != result || len(result) > 256 {
		return nil, false
	}
	for _, item := range result {
		if unicode.IsControl(item) {
			return nil, false
		}
	}
	lower := strings.ToLower(result)
	if lower == "bearer" || strings.HasPrefix(lower, "bearer ") {
		return nil, false
	}
	return result, true
}

func positiveControlPlaneInteger(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	switch item := value.(type) {
	case json.Number:
		return canonicalPositiveIntegerText(string(item))
	case float64:
		if math.IsNaN(item) || math.IsInf(item, 0) || item <= 0 || item != math.Trunc(item) || item >= math.Exp2(63) {
			return nil, false
		}
		return int64(item), true
	case string:
		if item != strings.TrimSpace(item) {
			return nil, false
		}
		return canonicalPositiveIntegerText(item)
	default:
		return nil, false
	}
}

func canonicalPositiveIntegerText(value string) (any, bool) {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || !json.Valid([]byte(value)) {
		return nil, false
	}
	var number big.Rat
	if _, ok := number.SetString(value); !ok || !number.IsInt() || number.Sign() <= 0 || !number.Num().IsInt64() {
		return nil, false
	}
	return number.Num().Int64(), true
}

func secretReferenceControlPlaneValue(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	result, ok := value.(string)
	return result, ok && validSecretRef(result)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
