package crmcli

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strings"
)

// StrictJSON rejects duplicate members recursively before ordinary unmarshalling.
func StrictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var consume func() error
	consume = func() error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					name, ok := k.(string)
					if !ok || seen[name] {
						return fail("validation", "duplicate JSON member", 2)
					}
					seen[name] = true
					if e = consume(); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e := consume(); e != nil {
						return e
					}
				}
			default:
				return fail("validation", "invalid JSON", 2)
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := consume(); e != nil {
		return fail("validation", "invalid or duplicate JSON input", 2)
	}
	if _, e := d.Token(); e != io.EOF {
		return fail("validation", "trailing JSON input", 2)
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return fail("validation", "invalid JSON input", 2)
	}
	return nil
}

// resolveParameterSchema follows local references before choosing the wire
// parameter parser. Validation and serialization must use the same type.
func resolveParameterSchema(schema map[string]any, definitions map[string]any) (map[string]any, error) {
	seen := map[string]bool{}
	for {
		ref, ok := schema["$ref"].(string)
		if !ok {
			return schema, nil
		}
		if seen[ref] {
			return nil, fail("validation", "cyclic parameter schema reference", 2)
		}
		seen[ref] = true
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		next, ok := definitions[name].(map[string]any)
		if !ok {
			return nil, fail("validation", "unresolved schema reference", 2)
		}
		schema = next
	}
}

// Validate supplies local structural checks. Domain rules remain server-owned.
func Validate(value any, schema map[string]any, definitions map[string]any) error {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if s, ok := definitions[name].(map[string]any); ok {
			return Validate(value, s, definitions)
		}
		return fail("validation", "unresolved schema reference", 2)
	}
	if list, ok := schema["allOf"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(map[string]any); ok {
				if e := Validate(value, s, definitions); e != nil {
					return e
				}
			}
		}
	}
	if value == nil {
		if schema["nullable"] == true {
			return nil
		}
		if typ, ok := schema["type"].(string); ok && typ != "null" {
			return fail("validation", "null is not allowed by schema", 2)
		}
		return nil
	}
	if enums, ok := schema["enum"].([]any); ok {
		raw, _ := json.Marshal(value)
		match := false
		for _, v := range enums {
			b, _ := json.Marshal(v)
			if bytes.Equal(b, raw) {
				match = true
			}
		}
		if extra, ok := schema["x-aicrm-additional-capabilities"].([]any); ok {
			for _, v := range extra {
				b, _ := json.Marshal(v)
				if bytes.Equal(b, raw) {
					match = true
				}
			}
		}
		if !match {
			return fail("validation", "value is outside schema enum", 2)
		}
	}
	switch schema["type"] {
	case "object":
		m, ok := value.(map[string]any)
		if !ok {
			return fail("validation", "expected object", 2)
		}
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				if key, ok := r.(string); ok {
					if _, exists := m[key]; !exists {
						return fail("validation", "required body field missing: "+key, 2)
					}
				}
			}
		}
		for k, v := range m {
			p, exists := properties[k]
			if !exists && schema["additionalProperties"] == false {
				return fail("validation", "unknown body field; consult registered schema", 2)
			}
			if s, ok := p.(map[string]any); ok {
				if e := Validate(v, s, definitions); e != nil {
					return e
				}
			}
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return fail("validation", "expected array", 2)
		}
		if schema["uniqueItems"] == true {
			seen := map[string]bool{}
			for _, item := range a {
				b, _ := json.Marshal(item)
				key := string(b)
				if seen[key] {
					return fail("validation", "array requires unique items", 2)
				}
				seen[key] = true
			}
		}
		if s, ok := schema["items"].(map[string]any); ok {
			for _, v := range a {
				if e := Validate(v, s, definitions); e != nil {
					return e
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fail("validation", "expected string", 2)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fail("validation", "expected boolean", 2)
		}
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return fail("validation", "expected integer", 2)
		}
		if _, e := n.Int64(); e != nil {
			return fail("validation", "expected integer", 2)
		}
	case "number":
		if n, ok := value.(json.Number); !ok {
			return fail("validation", "expected number", 2)
		} else if number, err := n.Float64(); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fail("validation", "expected finite JSON number", 2)
		}
	}
	return nil
}
