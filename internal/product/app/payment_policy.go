package app

import "encoding/json"

// ProductAlipayDisabled keeps missing historical configuration enabled. Invalid
// explicit values are rejected instead of turning a disabled policy back on.
func ProductAlipayDisabled(raw json.RawMessage) (bool, error) {
	var projection map[string]json.RawMessage
	if json.Unmarshal(raw, &projection) != nil {
		return false, ErrInvalidProduct
	}
	value, present := projection["alipay_enabled"]
	if !present {
		return false, nil
	}
	var enabled bool
	if string(value) == "null" || json.Unmarshal(value, &enabled) != nil {
		return false, ErrInvalidProduct
	}
	return !enabled, nil
}

func hasAlipayPolicy(raw json.RawMessage) bool {
	var projection map[string]json.RawMessage
	_ = json.Unmarshal(raw, &projection)
	_, present := projection["alipay_enabled"]
	return present
}

// Omission on an update preserves the locked row's policy for older clients.
func preserveAlipayPolicy(next, current json.RawMessage) json.RawMessage {
	var projection map[string]json.RawMessage
	if json.Unmarshal(next, &projection) != nil {
		return next
	}
	disabled, err := ProductAlipayDisabled(current)
	if err != nil {
		return next
	}
	projection["alipay_enabled"] = mustJSON(!disabled)
	return mustJSON(projection)
}
