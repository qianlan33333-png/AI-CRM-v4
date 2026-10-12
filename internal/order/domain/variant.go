package domain

// VariantSnapshot is solely the order line's immutable selection. Money remains
// in the existing order and checkout facts; catalog edits never rewrite this.
type VariantAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type VariantSnapshot struct {
	ID          int64              `json:"id"`
	Version     int64              `json:"version"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Attributes  []VariantAttribute `json:"attributes"`
}

func (v *VariantSnapshot) Valid() bool {
	if v == nil {
		return true
	}
	if v.ID < 1 || v.Version < 1 || !validText(v.Name, 200) || len(v.Description) > 10000 {
		return false
	}
	seen := map[string]bool{}
	for _, a := range v.Attributes {
		if !validText(a.Key, 200) || !validText(a.Value, 10000) || seen[a.Key] {
			return false
		}
		seen[a.Key] = true
	}
	return true
}
func (v *VariantSnapshot) Clone() *VariantSnapshot {
	if v == nil {
		return nil
	}
	copy := *v
	copy.Attributes = append([]VariantAttribute{}, v.Attributes...)
	return &copy
}
func (item ItemSnapshot) DisplayName() string {
	if item.Variant == nil {
		return item.ProductName
	}
	return item.ProductName + " · " + item.Variant.Name
}
