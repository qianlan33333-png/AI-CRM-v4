package app

import "encoding/json"

// ProductContactCollectionLevel applies the existing canonical projection
// contract, including require_mobile compatibility, for both product kinds.
func ProductContactCollectionLevel(raw json.RawMessage) (string, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return "", ErrUnavailable
	}
	var projection struct {
		Level string `json:"contact_collection_level"`
	}
	if json.Unmarshal(canonical, &projection) != nil {
		return "", ErrUnavailable
	}
	return projection.Level, nil
}
