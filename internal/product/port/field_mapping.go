package port

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrFieldMappingInvalid = errors.New("invalid field mapping")
var ErrFieldMappingVariableUnavailable = errors.New("field mapping variable unavailable")

type FieldMapping struct {
	Version int                 `json:"version"`
	Fields  []FieldMappingField `json:"fields"`
}
type FieldMappingField struct {
	Key       string          `json:"key"`
	Source    string          `json:"source"`
	ValueType string          `json:"value_type"`
	Value     json.RawMessage `json:"value,omitempty"`
	Variable  string          `json:"variable,omitempty"`
}

func validMappingKey(key string) bool {
	return key != "" && len(key) <= 128 && utf8.ValidString(key) && strings.TrimSpace(key) == key && !strings.ContainsFunc(key, unicode.IsControl) && key != "__proto__" && key != "constructor" && key != "prototype"
}

// DecodeFieldMapping rejects ambiguous JSON before a map decoder can silently
// collapse duplicate keys. All numeric values remain exact JSON numbers.
func DecodeFieldMapping(raw json.RawMessage) (*FieldMapping, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if _, err := decodeMappingJSON(raw); err != nil {
		return nil, err
	}
	var mapping FieldMapping
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&mapping) != nil {
		return nil, ErrFieldMappingInvalid
	}
	if err := ValidateFieldMapping(&mapping); err != nil {
		return nil, err
	}
	return &mapping, nil
}
func ValidateFieldMapping(mapping *FieldMapping) error {
	if mapping == nil || mapping.Version != 1 || len(mapping.Fields) < 1 || len(mapping.Fields) > 64 {
		return ErrFieldMappingInvalid
	}
	seen := map[string]bool{}
	for _, field := range mapping.Fields {
		if !validMappingKey(field.Key) || seen[field.Key] {
			return ErrFieldMappingInvalid
		}
		seen[field.Key] = true
		switch field.Source {
		case "fixed":
			if field.Variable != "" || len(field.Value) == 0 {
				return ErrFieldMappingInvalid
			}
			value, err := decodeMappingJSON(field.Value)
			if err != nil || !mappingValueType(value, field.ValueType) {
				return ErrFieldMappingInvalid
			}
		case "variable":
			expected := map[string]string{"order.mobile": "string", "payer.nickname": "string", "order.paid_amount_minor": "number"}[field.Variable]
			if expected == "" || field.ValueType != expected || len(field.Value) != 0 {
				return ErrFieldMappingInvalid
			}
		default:
			return ErrFieldMappingInvalid
		}
	}
	raw, err := json.Marshal(mapping)
	if err != nil || len(raw) > 32768 {
		return ErrFieldMappingInvalid
	}
	return nil
}
func mappingValueType(value any, kind string) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "json":
		switch value.(type) {
		case map[string]any, []any:
			return true
		}
		return false
	default:
		return false
	}
}

// CompileFieldMapping is pure and shared by synthetic preview and Outbound.
// It never resolves identities or performs IO. Nil means legacy, and callers
// must preserve their existing legacy path rather than invoking this compiler.
func CompileFieldMapping(mapping *FieldMapping, variables map[string]json.RawMessage) (json.RawMessage, error) {
	if err := ValidateFieldMapping(mapping); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, field := range mapping.Fields {
		raw := field.Value
		if field.Source == "variable" {
			var ok bool
			raw, ok = variables[field.Variable]
			if !ok {
				return nil, ErrFieldMappingVariableUnavailable
			}
			value, err := decodeMappingJSON(raw)
			if err != nil || (value != nil && !mappingValueType(value, field.ValueType)) {
				return nil, ErrFieldMappingVariableUnavailable
			}
		}
		out[field.Key] = append(json.RawMessage(nil), raw...)
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 32768 {
		return nil, ErrFieldMappingInvalid
	}
	return raw, nil
}
func decodeMappingJSON(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > 32768 || !utf8.Valid(raw) {
		return nil, ErrFieldMappingInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := mappingJSONValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, ErrFieldMappingInvalid
	}
	return value, nil
}
func mappingJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, ErrFieldMappingInvalid
	}
	token, err := d.Token()
	if err != nil {
		return nil, ErrFieldMappingInvalid
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			t, e := d.Token()
			key, ok := t.(string)
			if e != nil || !ok || !validMappingKey(key) {
				return nil, ErrFieldMappingInvalid
			}
			if _, exists := out[key]; exists {
				return nil, ErrFieldMappingInvalid
			}
			value, e := mappingJSONValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			out[key] = value
		}
		if t, e := d.Token(); e != nil || t != json.Delim('}') {
			return nil, ErrFieldMappingInvalid
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			value, e := mappingJSONValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			out = append(out, value)
		}
		if t, e := d.Token(); e != nil || t != json.Delim(']') {
			return nil, ErrFieldMappingInvalid
		}
		return out, nil
	default:
		return nil, ErrFieldMappingInvalid
	}
}

// SyntheticFieldMappingVariables returns isolated samples; never customer data.
func SyntheticFieldMappingVariables() map[string]json.RawMessage {
	return map[string]json.RawMessage{"order.mobile": json.RawMessage(`"+8613800000000"`), "payer.nickname": json.RawMessage(`"测试用户"`), "order.paid_amount_minor": json.RawMessage(`990`)}
}

// EqualFieldMappings compares decimal values exactly across PostgreSQL JSONB's
// normalization (for example 1e3 and 1000), without floating-point conversion.
func EqualFieldMappings(a, b *FieldMapping) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ae := json.Marshal(a)
	br, be := json.Marshal(b)
	if ae != nil || be != nil {
		return false
	}
	av, ae := decodeMappingJSON(ar)
	bv, be := decodeMappingJSON(br)
	return ae == nil && be == nil && equalMappingJSON(av, bv)
}
func equalMappingJSON(a, b any) bool {
	switch v := a.(type) {
	case json.Number:
		w, ok := b.(json.Number)
		return ok && canonicalMappingNumber(string(v)) == canonicalMappingNumber(string(w))
	case map[string]any:
		w, ok := b.(map[string]any)
		if !ok || len(v) != len(w) {
			return false
		}
		for k, x := range v {
			y, ok := w[k]
			if !ok || !equalMappingJSON(x, y) {
				return false
			}
		}
		return true
	case []any:
		w, ok := b.([]any)
		if !ok || len(v) != len(w) {
			return false
		}
		for i, x := range v {
			if !equalMappingJSON(x, w[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
func canonicalMappingNumber(raw string) string {
	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	exponent := new(big.Int)
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		exponent.SetString(strings.TrimPrefix(raw[i+1:], "+"), 10)
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(raw)-i-1)))
		raw = raw[:i] + raw[i+1:]
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return "0"
	}
	trimmed := strings.TrimRight(raw, "0")
	exponent.Add(exponent, big.NewInt(int64(len(raw)-len(trimmed))))
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + trimmed + "e" + exponent.String()
}
